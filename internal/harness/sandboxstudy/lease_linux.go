// Copyright 2026 Ayesh Almeida
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build linux

package sandboxstudy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const leaseStudyTimeout = 3 * time.Second

// LeaseObservation records direct host-kernel observations for a synthetic
// fixture. These rows are separate from Bubblewrap enforcement evidence.
type LeaseObservation struct {
	Case        string `json:"case"`
	Status      string `json:"status"`
	Attempt     string `json:"attempt"`
	Observation string `json:"observation"`
	Limitation  string `json:"limitation,omitempty"`
}

// RunLeaseStudy probes file-lease semantics with fixed operations on a fresh
// private fixture. It is research-only and does not use the runtime/tool path.
func RunLeaseStudy(ctx context.Context, executable string) ([]LeaseObservation, error) {
	root, err := os.MkdirTemp("", "komposyn-issue320-lease-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, err
	}
	rows := make([]LeaseObservation, 0, 4)
	appendUnavailable := func(name, attempt string, err error) {
		rows = append(rows, LeaseObservation{Case: name, Status: "unavailable", Attempt: attempt, Observation: "not executed", Limitation: errorText(err)})
	}

	// A separate actor holds an ordinary writable descriptor. A write lease
	// should refuse while that descriptor exists, then become acquirable once
	// the actor closes it.
	fdPath := filepath.Join(root, "fd.txt")
	if err := os.WriteFile(fdPath, []byte("revision-0"), 0o600); err != nil {
		return rows, err
	}
	actor, err := startLeaseActor(executable, "hold-fd", root)
	if err != nil {
		appendUnavailable("preexisting-writable-fd", "independent actor holds writable descriptor while coordinator requests F_WRLCK", err)
	} else {
		defer actor.stop()
		if err := actor.wait("ready"); err != nil {
			appendUnavailable("preexisting-writable-fd", "wait for actor FD checkpoint", err)
		} else {
			fd, openErr := os.OpenFile(fdPath, os.O_RDWR, 0)
			if openErr != nil {
				appendUnavailable("preexisting-writable-fd", "open synthetic fixture", openErr)
			} else {
				leaseErr := setFileLease(fd, unix.F_WRLCK)
				if leaseErr != nil && (errors.Is(leaseErr, syscall.EAGAIN) || errors.Is(leaseErr, syscall.EACCES)) {
					rows = append(rows, LeaseObservation{Case: "preexisting-writable-fd", Status: "measured_pass", Attempt: "acquire write lease with independent writable descriptor already open", Observation: "lease refused with " + leaseErr.Error() + "; no content change attempted"})
				} else if leaseErr != nil {
					appendUnavailable("preexisting-writable-fd", "acquire F_WRLCK", leaseErr)
				} else {
					_ = setFileLease(fd, unix.F_UNLCK)
					rows = append(rows, LeaseObservation{Case: "preexisting-writable-fd", Status: "observed_violation", Attempt: "acquire write lease with independent writable descriptor already open", Observation: "F_WRLCK unexpectedly succeeded"})
				}
				_ = os.WriteFile(filepath.Join(root, "release"), nil, 0o600)
				if err := actor.wait("closed"); err != nil {
					appendUnavailable("preexisting-writable-fd-close", "wait for actor to close its descriptor", err)
				} else if leaseErr == nil {
					// The unexpected lease was already unlocked above.
				} else if setFileLease(fd, unix.F_WRLCK) == nil {
					_ = setFileLease(fd, unix.F_UNLCK)
					rows = append(rows, LeaseObservation{Case: "preexisting-writable-fd-close", Status: "measured_pass", Attempt: "retry F_WRLCK after actor closes its descriptor", Observation: "lease acquired after the conflicting descriptor closed"})
				} else {
					rows = append(rows, LeaseObservation{Case: "preexisting-writable-fd-close", Status: "inconclusive", Attempt: "retry F_WRLCK after actor closes its descriptor", Observation: "lease still refused; filesystem or lease state may prevent acquisition"})
				}
				_ = fd.Close()
			}
		}
	}

	// An independent actor creates a MAP_SHARED writable mapping and closes
	// its descriptor before the lease is acquired. The actor then writes
	// through that retained mapping while the coordinator holds F_WRLCK.
	mapPath := filepath.Join(root, "mapping.txt")
	if err := os.WriteFile(mapPath, []byte("revision-0"), 0o600); err != nil {
		return rows, err
	}
	actor, err = startLeaseActor(executable, "hold-map", root)
	if err != nil {
		appendUnavailable("retained-writable-mapping", "prepare shared mapping and close actor descriptor", err)
	} else {
		defer actor.stop()
		if err := actor.wait("ready"); err != nil {
			appendUnavailable("retained-writable-mapping", "wait for mapping checkpoint", err)
		} else if fd, openErr := os.OpenFile(mapPath, os.O_RDWR, 0); openErr != nil {
			appendUnavailable("retained-writable-mapping", "open synthetic fixture", openErr)
		} else {
			leaseErr := setFileLease(fd, unix.F_WRLCK)
			if leaseErr != nil {
				if errors.Is(leaseErr, syscall.EAGAIN) || errors.Is(leaseErr, syscall.EACCES) {
					rows = append(rows, LeaseObservation{Case: "retained-writable-mapping-blocks-lease", Status: "measured_pass", Attempt: "acquire F_WRLCK while independent actor retains MAP_SHARED writable mapping after closing its descriptor", Observation: "lease refused with " + leaseErr.Error() + "; this mapping profile cannot enter the leased write path"})
					rows = append(rows, LeaseObservation{Case: "mapping-write-under-lease", Status: "unavailable", Attempt: "change bytes through the retained mapping while F_WRLCK is held", Observation: "not executed because the retained writable mapping caused F_WRLCK acquisition to fail"})
					_ = os.WriteFile(filepath.Join(root, "mutate"), nil, 0o600)
					if mutateErr := actor.wait("mutated"); mutateErr == nil {
						contents, readErr := os.ReadFile(mapPath)
						if readErr == nil && strings.HasPrefix(string(contents), "X") {
							rows = append(rows, LeaseObservation{Case: "mapping-write-without-lease", Status: "measured_pass", Attempt: "actor writes through retained MAP_SHARED mapping after write-lease refusal", Observation: "content changed after lease acquisition was refused; the mapping is a real unleased revision-change path"})
						} else {
							appendUnavailable("mapping-write-without-lease", "verify synthetic content after mapping mutation", readErr)
						}
					} else {
						appendUnavailable("mapping-write-without-lease", "wait for mapping mutation after lease refusal", mutateErr)
					}
				} else {
					appendUnavailable("retained-writable-mapping", "acquire F_WRLCK while mapping is retained without its fd", leaseErr)
				}
			} else {
				_ = os.WriteFile(filepath.Join(root, "mutate"), nil, 0o600)
				waitErr := actor.wait("mutated")
				var observed [1]byte
				_, readErr := unix.Pread(int(fd.Fd()), observed[:], 0)
				if readErr != nil {
					appendUnavailable("retained-writable-mapping", "read through lease-holder descriptor without opening a second file description", readErr)
				} else if observed[0] == 'X' {
					rows = append(rows, LeaseObservation{Case: "retained-writable-mapping", Status: "observed_violation", Attempt: "actor writes MAP_SHARED bytes while F_WRLCK is held", Observation: "content changed under the active write lease; descriptor-free mapping was not excluded"})
				} else if waitErr != nil && strings.Contains(waitErr.Error(), "timed out") {
					state, stateErr := getFileLease(fd)
					if stateErr == nil && state == unix.F_WRLCK {
						_ = setFileLease(fd, unix.F_UNLCK)
						_ = os.WriteFile(filepath.Join(root, "release"), nil, 0o600)
						if resumeErr := actor.wait("mutated"); resumeErr == nil {
							var after [1]byte
							if _, afterErr := unix.Pread(int(fd.Fd()), after[:], 0); afterErr == nil && after[0] == 'X' {
								rows = append(rows, LeaseObservation{Case: "retained-writable-mapping", Status: "measured_pass", Attempt: "actor writes through pre-existing MAP_SHARED mapping while F_WRLCK is held, then lease is released", Observation: "mapping write remained pending and content stayed unchanged for the bounded lease interval; it completed after lease release on this ext4 profile"})
							} else {
								appendUnavailable("retained-writable-mapping", "verify mapping write after lease release", afterErr)
							}
						} else {
							appendUnavailable("retained-writable-mapping", "actor did not resume after lease release", resumeErr)
						}
					} else {
						if stateErr == nil {
							stateErr = fmt.Errorf("lease state changed to %d before mapping write observation", state)
						}
						appendUnavailable("retained-writable-mapping", "confirm lease remained held while mapping write waited", stateErr)
					}
				} else if waitErr != nil {
					appendUnavailable("retained-writable-mapping", "wait for write through retained mapping", waitErr)
				} else {
					rows = append(rows, LeaseObservation{Case: "retained-writable-mapping", Status: "inconclusive", Attempt: "actor writes MAP_SHARED bytes while F_WRLCK is held", Observation: "mapping checkpoint completed but expected byte change was not visible through the retained descriptor"})
				}
				_ = setFileLease(fd, unix.F_UNLCK)
			}
			_ = os.WriteFile(filepath.Join(root, "release"), nil, 0o600)
			_ = actor.wait("closed")
			_ = fd.Close()
		}
	}

	// A late hard link is created while the write lease is held. Opening the
	// alias nonblocking should initiate a lease break and fail with EWOULDBLOCK.
	linkPath := filepath.Join(root, "link-target.txt")
	if err := os.WriteFile(linkPath, []byte("revision-0"), 0o600); err != nil {
		return rows, err
	}
	leaseFD, err := os.OpenFile(linkPath, os.O_RDWR, 0)
	if err != nil {
		appendUnavailable("late-hardlink-and-open-break", "open synthetic fixture", err)
	} else {
		leaseErr := setFileLease(leaseFD, unix.F_WRLCK)
		if leaseErr != nil {
			appendUnavailable("late-hardlink-and-open-break", "acquire F_WRLCK", leaseErr)
		} else {
			actor, actorErr := startLeaseActor(executable, "late-link", root)
			if actorErr != nil {
				appendUnavailable("late-hardlink-and-open-break", "start independent late-link actor", actorErr)
			} else {
				defer actor.stop()
				if waitErr := actor.wait("linked"); waitErr != nil {
					appendUnavailable("late-hardlink-alias", "wait for actor hard-link checkpoint", waitErr)
				} else if state, stateErr := getFileLease(leaseFD); stateErr != nil {
					appendUnavailable("late-hardlink-alias", "inspect lease after link creation", stateErr)
				} else if state == unix.F_WRLCK {
					rows = append(rows, LeaseObservation{Case: "late-hardlink-alias", Status: "observed_violation", Attempt: "create hard-link alias while F_WRLCK is held", Observation: "link creation completed while lease remained F_WRLCK; hard-link rejection cannot rely on the lease"})
				} else {
					rows = append(rows, LeaseObservation{Case: "late-hardlink-alias", Status: "inconclusive", Attempt: "create hard-link alias while F_WRLCK is held", Observation: fmt.Sprintf("lease state after link was %d", state)})
				}
				_ = os.WriteFile(filepath.Join(root, "open"), nil, 0o600)
				if waitErr := actor.wait("open-result"); waitErr != nil {
					appendUnavailable("late-hardlink-open-break", "wait for nonblocking open through alias", waitErr)
				} else if state, stateErr := getFileLease(leaseFD); stateErr != nil {
					appendUnavailable("late-hardlink-open-break", "inspect lease after open attempt", stateErr)
				} else if state == unix.F_UNLCK {
					errno, readErr := os.ReadFile(filepath.Join(root, "open-errno"))
					if readErr == nil && strings.TrimSpace(string(errno)) == "EWOULDBLOCK" {
						rows = append(rows, LeaseObservation{Case: "late-hardlink-open-break", Status: "measured_pass", Attempt: "open alias with O_NONBLOCK while write lease is held", Observation: "actor got EWOULDBLOCK and lease entered break state; coordinator removed lease before timeout"})
					} else {
						rows = append(rows, LeaseObservation{Case: "late-hardlink-open-break", Status: "inconclusive", Attempt: "open alias with O_NONBLOCK while write lease is held", Observation: "lease entered break state but actor errno was " + strings.TrimSpace(string(errno)), Limitation: errorText(readErr)})
					}
				} else {
					rows = append(rows, LeaseObservation{Case: "late-hardlink-open-break", Status: "inconclusive", Attempt: "open alias with O_NONBLOCK while write lease is held", Observation: fmt.Sprintf("lease state after open attempt was %d", state)})
				}
				_ = os.WriteFile(filepath.Join(root, "release"), nil, 0o600)
				actor.stop()
			}
			_ = setFileLease(leaseFD, unix.F_UNLCK)
		}
		_ = leaseFD.Close()
	}

	rows = append(rows, LeaseObservation{Case: "in-place-partial-or-unknown", Status: "not_executed", Attempt: "interrupt or fail an in-place write after an arbitrary prefix", Observation: "not run; lease semantics provide no transactional write or rollback, so a known prefix effect is partial and a lost completion after release is unknown; neither may be replayed automatically"})
	if err := ctx.Err(); err != nil {
		return rows, err
	}
	return rows, nil
}

type leaseActor struct {
	cmd  *exec.Cmd
	root string
	done chan struct{}
	err  error
}

func startLeaseActor(executable, action, root string) (*leaseActor, error) {
	if !filepath.IsAbs(executable) || !validLeaseRoot(root) {
		return nil, errors.New("lease study paths failed fixture validation")
	}
	for _, marker := range []string{"ready", "release", "mutate", "mutated", "linked", "open", "open-result", "open-errno", "closed", "actor-error"} {
		if err := os.Remove(filepath.Join(root, marker)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("clear actor checkpoint: %w", err)
		}
	}
	cmd := exec.Command(executable, "--sandbox-study-lease-actor", action, root)
	cmd.Env = []string{}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	actor := &leaseActor{cmd: cmd, root: root, done: done}
	go func() {
		actor.err = cmd.Wait()
		close(done)
	}()
	return actor, nil
}

func (a *leaseActor) wait(marker string) error {
	deadline := time.Now().Add(leaseStudyTimeout)
	for time.Now().Before(deadline) {
		if failure, err := os.ReadFile(filepath.Join(a.root, "actor-error")); err == nil {
			return fmt.Errorf("lease actor failed: %s", sanitize(string(failure)))
		}
		if _, err := os.Stat(filepath.Join(a.root, marker)); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		select {
		case <-a.done:
			if _, err := os.Stat(filepath.Join(a.root, marker)); err == nil {
				return nil
			}
			if a.err == nil {
				return fmt.Errorf("lease actor exited before checkpoint %s", marker)
			}
			return fmt.Errorf("lease actor exited before checkpoint %s: %s", marker, sanitize(a.err.Error()))
		case <-time.After(5 * time.Millisecond):
		}
	}
	return fmt.Errorf("timed out waiting for lease actor checkpoint %s", marker)
}

func (a *leaseActor) stop() {
	if a == nil || a.cmd == nil || a.cmd.Process == nil {
		return
	}
	_ = a.cmd.Process.Kill()
	<-a.done
	a.cmd = nil
}

// LeaseActorMain is the command's fixed same-user actor mode. It accepts no
// paths other than files in its private, generated fixture directory.
func LeaseActorMain(args []string) int {
	if len(args) != 2 || !validLeaseRoot(args[1]) {
		return 2
	}
	action, root := args[0], args[1]
	var err error
	switch action {
	case "hold-fd":
		var fd *os.File
		fd, err = os.OpenFile(filepath.Join(root, "fd.txt"), os.O_RDWR, 0)
		if err == nil {
			err = leaseActorHold(root, "ready", "release")
			_ = fd.Close()
			if err == nil {
				_ = os.WriteFile(filepath.Join(root, "closed"), nil, 0o600)
			}
		}
	case "hold-map":
		var file *os.File
		file, err = os.OpenFile(filepath.Join(root, "mapping.txt"), os.O_RDWR, 0)
		if err == nil {
			var mapped []byte
			mapped, err = unix.Mmap(int(file.Fd()), 0, os.Getpagesize(), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
			_ = file.Close()
			if err == nil {
				err = leaseActorHold(root, "ready", "mutate")
				if err == nil {
					mapped[0] = 'X'
					_ = os.WriteFile(filepath.Join(root, "mutated"), nil, 0o600)
				}
				if err == nil {
					err = leaseActorHold(root, "mutated", "release")
				}
				_ = unix.Munmap(mapped)
				if err == nil {
					_ = os.WriteFile(filepath.Join(root, "closed"), nil, 0o600)
				}
			}
		}
	case "late-link":
		err = os.Link(filepath.Join(root, "link-target.txt"), filepath.Join(root, "alias.txt"))
		if err == nil {
			_ = os.WriteFile(filepath.Join(root, "linked"), nil, 0o600)
			err = leaseActorHold(root, "linked", "open")
		}
		if err == nil {
			fd, openErr := unix.Open(filepath.Join(root, "alias.txt"), unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
			if fd >= 0 {
				_ = unix.Close(fd)
			}
			_ = os.WriteFile(filepath.Join(root, "open-errno"), []byte(errnoName(openErr)), 0o600)
			_ = os.WriteFile(filepath.Join(root, "open-result"), nil, 0o600)
			err = leaseActorHold(root, "open-result", "release")
		}
	default:
		return 2
	}
	if err != nil {
		_ = os.WriteFile(filepath.Join(root, "actor-error"), []byte(sanitize(err.Error())), 0o600)
		return 1
	}
	return 0
}

func leaseActorHold(root, ready, release string) error {
	if err := os.WriteFile(filepath.Join(root, ready), nil, 0o600); err != nil {
		return err
	}
	deadline := time.Now().Add(leaseStudyTimeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(root, release)); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
	return errors.New("lease actor checkpoint timed out")
}

func validLeaseRoot(root string) bool {
	abs, err := filepath.Abs(root)
	if err != nil || abs != root || filepath.Dir(abs) != os.TempDir() || !strings.HasPrefix(filepath.Base(abs), "komposyn-issue320-lease-") {
		return false
	}
	info, err := os.Lstat(abs)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 && info.Mode().Perm()&0o077 == 0
}

func errnoName(err error) string {
	if err == nil {
		return "nil"
	}
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return "EWOULDBLOCK"
	}
	return err.Error()
}

func errorText(err error) string {
	if err == nil {
		return "outcome did not match the expected observation"
	}
	return sanitize(err.Error())
}

func setFileLease(file *os.File, lease int) error {
	_, err := unix.FcntlInt(file.Fd(), unix.F_SETLEASE, lease)
	return err
}

func getFileLease(file *os.File) (int, error) {
	return unix.FcntlInt(file.Fd(), unix.F_GETLEASE, 0)
}
