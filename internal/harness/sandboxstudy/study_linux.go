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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const maxLineBytes = workerOutputLimit

var errCancelBeforeRelease = errors.New("cancel before operation release")

// Observation separates what a fixture observed from expected authority A and
// the narrower observation that would be consistent with authority B.
type Observation struct {
	Case          string `json:"case"`
	Profile       string `json:"candidate_profile,omitempty"`
	Attempt       string `json:"attempted_operation"`
	ActualEffect  string `json:"actual_host_effect"`
	ExpectedA     string `json:"expected_A"`
	ObservedB     string `json:"observed_B"`
	Status        string `json:"status"`
	WorkerOutcome string `json:"worker_outcome,omitempty"`
	Limitation    string `json:"limitation,omitempty"`
}

type workerResult struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type scenario struct {
	name             string
	action           string
	path             string
	setup            func(string, string) (func() (string, error), error)
	actorSetup       func(string, string, fixtureActor) (func() (string, error), error)
	afterCreateActor string
	protected        string
}

type fixtureActor func(action, scope, outside string) error

// RunNativeSuite runs disposable same-user fixtures against the requested
// system bubblewrap binary. It never retries outside the sandbox.
func RunNativeSuite(ctx context.Context, bwrapPath, workerPath string) ([]Observation, error) {
	backend, err := filepath.Abs(bwrapPath)
	if err != nil {
		return nil, err
	}
	worker, err := filepath.Abs(workerPath)
	if err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp("", "komposyn-issue215-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)

	cases := scenarios()
	observations := make([]Observation, 0, len(cases))
	for _, test := range cases {
		caseRoot := filepath.Join(root, test.name)
		scope := filepath.Join(caseRoot, "scope")
		outside := filepath.Join(caseRoot, "outside")
		if err := os.MkdirAll(scope, 0o700); err != nil {
			return observations, err
		}
		if err := os.Mkdir(outside, 0o700); err != nil {
			return observations, err
		}
		afterReady, err := test.setup(scope, outside)
		if test.actorSetup != nil {
			afterReady, err = test.actorSetup(scope, outside, func(action, actorScope, actorOutside string) error {
				return runFixtureActor(worker, action, actorScope, actorOutside)
			})
		}
		if err != nil {
			return observations, fmt.Errorf("fixture %s: %w", test.name, err)
		}
		var result Observation
		var runErr error
		if test.afterCreateActor != "" {
			actor := func() error { return runFixtureActor(worker, test.afterCreateActor, scope, outside) }
			result, runErr = runGatedWithCreateCheckpoint(ctx, backend, worker, scope, test.protected, test.action, test.path, afterReady, actor)
		} else {
			result, runErr = runGated(ctx, backend, worker, scope, test.protected, test.action, test.path, afterReady)
		}
		if runErr != nil {
			profileName := "default-no-proc"
			if test.action == "create-one-otmpfile" {
				profileName = "otmpfile-private-pid-proc"
			}
			observations = append(observations, Observation{Case: test.name, Profile: profileName, Attempt: test.action + " " + test.path,
				ExpectedA: "no effect outside the approved host pathname; refuse changed target/context",
				Status:    "unavailable", WorkerOutcome: "not_executed", Limitation: sanitize(runErr.Error())})
			return observations, runErr
		}
		result.Case = test.name
		if result.Status == "unknown" || result.Status == "unavailable" {
			if test.name == "create-one-unknown-outcome" {
				result.ActualEffect = verifyUnknownCreate(scope, result)
			}
			observations = append(observations, result)
			continue
		}
		result.ActualEffect, result.Status = verifyObserved(test.name, scope, result)
		if test.name == "private-scratch" {
			result.ActualEffect, result.Status = verifyScratch(scope, result)
		}
		observations = append(observations, result)
	}
	observations = append(observations,
		Observation{Case: "mount-boundary", Attempt: "cross an independently mounted child filesystem", ExpectedA: "reject a mount crossing", Status: "unavailable", Limitation: "no native mount fixture was created; the surrounding launcher denies namespace setup"},
		Observation{Case: "aggregate-resource-limits", Attempt: "verify pids/memory/CPU subtree enforcement", ExpectedA: "effective aggregate controls before any exhaustion test", Status: "unavailable", Limitation: "study does not create or write cgroups; visible controllers are not proof of delegation"},
		Observation{Case: "parent-death-and-descendant-cleanup", Attempt: "kill the coordinator while descendants are active", ExpectedA: "all worker descendants terminated and joined", Status: "unavailable", Limitation: "not run because no delegated cgroup kill boundary is established"},
		Observation{Case: "shell-experiment", Attempt: "execute a shell command", ExpectedA: "separate shell-specific isolation evidence", Status: "unavailable", Limitation: "not implemented; typed worker findings do not establish shell safety"},
		Observation{Case: "revocation-after-operation-release", Attempt: "revoke immediately after release", ExpectedA: "request worker termination and classify possible effects partial/unknown", Status: "unavailable", Limitation: "synchronization ordering is exercised by deterministic Go gate tests only; no native live mutation test"},
	)
	return observations, nil
}

func scenarios() []scenario {
	return []scenario{
		{name: "create-one-parent-relocation", action: "create-one", path: "parent/new.txt", setup: setupNoop, actorSetup: setupCreateParentRelocation},
		{name: "create-one-destination-collision", action: "create-one", path: "parent/new.txt", setup: setupNoop, actorSetup: setupCreateCollision},
		{name: "create-one-destination-symlink-substitution", action: "create-one", path: "parent/new.txt", setup: setupNoop, actorSetup: setupCreateSymlinkSubstitution},
		{name: "create-one-partial-outcome", action: "create-one-partial", path: "parent/partial.txt", setup: setupNoop, actorSetup: setupCreatePartial},
		{name: "create-one-unknown-outcome", action: "create-one-unknown", path: "parent/unknown.txt", setup: setupNoop, actorSetup: setupCreateUnknown},
		{name: "create-one-late-hardlink", action: "create-one-late-link", path: "parent/new.txt", setup: setupNoop, actorSetup: setupCreateLateAlias, afterCreateActor: "link-created-alias"},
		{name: "create-one-otmpfile-publication", action: "create-one-otmpfile", path: "parent/new.txt", setup: setupNoop, actorSetup: setupCreatePartial},
		{name: "selected-root-relocation", action: "create-parent", path: "root-effect", setup: setupRootRelocation},
		{name: "selected-root-relocation-read", action: "read-open", path: "secret.txt", setup: setupRootRelocationRead},
		{name: "parent-relocation-before-create", action: "create-parent", path: "parent/new.txt", setup: setupParentRelocation},
		{name: "parent-relocation-before-delete", action: "delete-parent", path: "parent/target.txt", setup: setupParentDelete},
		{name: "opened-target-relocation-before-write", action: "write-open", path: "target.txt", setup: setupTargetRelocation},
		{name: "existing-hardlink-alias", action: "write-open", path: "target.txt", setup: setupExistingHardlink},
		{name: "late-hardlink-alias", action: "write-open", path: "target.txt", setup: setupLateHardlink},
		{name: "deletion-target-substitution", action: "delete-parent", path: "target.txt", setup: setupDeleteSubstitution},
		{name: "recursive-child-relocation", action: "recursive-child", path: "child", setup: setupRecursiveMove},
		{name: "rename-crosses-unmounted-scope", action: "rename-out", path: "source.txt", setup: setupRenameOut},
		{name: "symlink-to-unmounted-host-path", action: "read-path", path: "link.txt", setup: setupSymlinkEscape},
		{name: "nested-protected-path", action: "read-path", path: "protected/secret.txt", setup: setupProtectedAlias, protected: "protected"},
		{name: "protected-path-and-alias", action: "read-path", path: "protected-alias.txt", setup: setupProtectedAlias, protected: "protected"},
		{name: "network-host-path-and-runtime-probes", action: "probe", path: strconv.Itoa(os.Getpid()), setup: setupNoop},
		{name: "private-scratch", action: "scratch", path: "scratch.bin", setup: setupNoop},
		{name: "bounded-output", action: "flood", path: "fixture", setup: setupNoop},
		{name: "partial-write-outcome", action: "partial-create", path: "partial.txt", setup: setupNoop},
		{name: "cancel-before-release", action: "hold", path: "fixture", setup: setupCancel},
	}
}

func runFixtureActor(executable, action, scope, outside string) error {
	if !filepath.IsAbs(executable) {
		return errors.New("actor executable path is not absolute")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "--sandbox-study-actor", action, scope, outside)
	command.Env = []string{}
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("same-user actor %s failed: %s: %w", action, sanitize(string(output)), err)
	}
	return nil
}

// ActorMain handles the study command's fixed same-user fixture mutations.
// It accepts no arbitrary paths or operation text from a model/provider.
func ActorMain(args []string) int {
	if len(args) != 3 {
		return 2
	}
	action, scope, outside := args[0], args[1], args[2]
	if validateActorFixturePaths(scope, outside) != nil {
		return 2
	}
	if err := applyFixtureActor(action, scope, outside); err != nil {
		return 1
	}
	return 0
}

func applyFixtureActor(action, scope, outside string) error {
	switch action {
	case "move-parent":
		parent := filepath.Join(scope, "parent")
		if err := os.Rename(parent, filepath.Join(outside, "moved-parent")); err != nil {
			return err
		}
		if err := os.Mkdir(parent, 0o700); err != nil {
			return err
		}
	case "insert-collision":
		if err := os.WriteFile(filepath.Join(scope, "parent", "new.txt"), []byte("actor collision fixture\n"), 0o600); err != nil {
			return err
		}
	case "insert-symlink":
		if err := os.Symlink(filepath.Join(outside, "synthetic-target"), filepath.Join(scope, "parent", "new.txt")); err != nil {
			return err
		}
	case "link-created-alias":
		if err := os.Link(filepath.Join(scope, "parent", "new.txt"), filepath.Join(outside, "protected", "alias.txt")); err != nil {
			return err
		}
	case "noop":
	default:
		return errors.New("unknown fixture actor action")
	}
	return nil
}

func validateActorFixturePaths(scope, outside string) error {
	tempRoot, err := filepath.Abs(os.TempDir())
	if err != nil {
		return err
	}
	scope, err = filepath.Abs(scope)
	if err != nil {
		return err
	}
	outside, err = filepath.Abs(outside)
	if err != nil {
		return err
	}
	caseRoot := filepath.Dir(scope)
	root := filepath.Dir(caseRoot)
	if filepath.Base(scope) != "scope" || filepath.Base(outside) != "outside" || outside != filepath.Join(caseRoot, "outside") || filepath.Dir(root) != tempRoot || !strings.HasPrefix(filepath.Base(root), "komposyn-issue215-") {
		return errors.New("actor paths are outside the disposable study fixture")
	}
	for _, path := range []string{root, caseRoot, scope, outside} {
		info, statErr := os.Lstat(path)
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
			return errors.New("actor fixture directory is missing or not private")
		}
	}
	return nil
}

func runGated(ctx context.Context, backend, worker, scope, protected, action, target string, afterReady func() (string, error)) (Observation, error) {
	return runGatedWithCreateCheckpoint(ctx, backend, worker, scope, protected, action, target, afterReady, nil)
}

func runGatedWithCreateCheckpoint(ctx context.Context, backend, worker, scope, protected, action, target string, afterReady func() (string, error), afterCreate func() error) (Observation, error) {
	profile := Profile
	profileName := "default-no-proc"
	if action == "create-one-otmpfile" {
		profile = ProfileWithPrivateProc
		profileName = "otmpfile-private-pid-proc"
	}
	args, seccompFile, err := profile(backend, worker, scope, protected)
	if err != nil {
		return Observation{}, err
	}
	defer func() { _ = seccompFile.Close(); _ = os.Remove(seccompFile.Name()) }()
	args = append(args, action, target)
	commandCtx, cancel := context.WithTimeout(ctx, time.Duration(workerRuntimeLimit))
	defer cancel()
	cmd := exec.CommandContext(commandCtx, backend, args...)
	// The sole inherited environment value is synthetic and exists only to
	// verify that --clearenv removes it before the worker starts.
	cmd.Env = []string{"KOMPOSYN_STUDY_SECRET=fixture-secret"}
	cmd.ExtraFiles = []*os.File{seccompFile}
	cmd.WaitDelay = time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Observation{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Observation{}, err
	}
	var stderr boundedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return Observation{}, fmt.Errorf("candidate launch refused: %w", err)
	}
	gate := newOperationGate(1, true) // The current runtime already consumes once grants before ToolEnforcer.Execute.
	childID := fmt.Sprintf("pid-%d", cmd.Process.Pid)
	if err := gate.RegisterChild(childID); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return Observation{}, err
	}
	reader := bufio.NewReaderSize(stdout, maxLineBytes)
	ready, err := readLineBounded(reader)
	if err != nil || strings.TrimSpace(ready) != "READY" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if err == nil {
			err = fmt.Errorf("worker startup acknowledgement was %q", sanitize(ready))
		}
		return Observation{}, fmt.Errorf("candidate startup failed: %w: %s", err, stderr.String())
	}
	if err := gate.AcknowledgeStartup(childID); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return Observation{}, err
	}
	actualEffect, effectErr := afterReady()
	if effectErr != nil {
		_ = gate.Cancel()
		_, _ = io.WriteString(stdin, "CANCEL\n")
		_ = stdin.Close()
		waitErr := cmd.Wait()
		if errors.Is(effectErr, errCancelBeforeRelease) && waitErr != nil {
			return Observation{Attempt: action + " " + target, ActualEffect: "startup acknowledged; cancellation before release; no operation was permitted", ExpectedA: "no fixture effect",
				ObservedB: "not applicable", Status: "measured", WorkerOutcome: "not_executed", Limitation: "parent cancellation was requested before release"}, nil
		}
		return Observation{}, effectErr
	}
	if err := gate.Release(childID); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return Observation{}, err
	}
	if err := releaseWorker(stdin, afterCreate != nil); err != nil {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return Observation{}, err
	}
	started, startedErr := readLineBounded(reader)
	if startedErr != nil || strings.TrimSpace(started) != "STARTED" {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return unknownAfterRelease(action, target, "worker did not acknowledge operation start: "+sanitize(started)), nil
	}
	if afterCreate != nil {
		checkpoint, checkpointErr := readLineBounded(reader)
		if checkpointErr != nil || strings.TrimSpace(checkpoint) != "CREATED" {
			_ = stdin.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return unknownAfterRelease(action, target, "create checkpoint missing: "+sanitize(checkpoint)), nil
		}
		if err := afterCreate(); err != nil {
			_ = stdin.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return unknownAfterRelease(action, target, "same-user actor failed after create: "+sanitize(err.Error())), nil
		}
		if err := continueWorker(stdin); err != nil {
			_ = stdin.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return unknownAfterRelease(action, target, "cannot release post-create checkpoint: "+sanitize(err.Error())), nil
		}
	}
	line, readErr := readLineBounded(reader)
	var killErr error
	if readErr != nil {
		killErr = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		if action == "flood" && errors.Is(readErr, errOutputLimit) {
			if classifyOutputBound(action, readErr, cmd.ProcessState != nil) == "measured_pass" {
				return Observation{Attempt: action + " " + target, ActualEffect: "oversized protocol line rejected; worker command was reaped", ExpectedA: "bounded output without unbounded host memory growth", ObservedB: "not applicable", Status: "measured_pass", WorkerOutcome: "unknown", Limitation: "fixed 128 KiB fixture output only; process-tree cleanup is not established"}, nil
			}
			return Observation{Attempt: action + " " + target, ActualEffect: "oversized line observed but worker reaping was not confirmed", ExpectedA: "bound output and confirm worker cleanup", ObservedB: "not applicable", Status: "unavailable", WorkerOutcome: "unknown", Limitation: sanitize(fmt.Sprintf("kill=%v wait=%v", killErr, waitErr))}, nil
		}
		return unknownAfterRelease(action, target, fmt.Sprintf("worker result unavailable: %v: %s", readErr, stderr.String())), nil
	}
	result, err := parseWorkerResult(line)
	if err != nil {
		return unknownAfterRelease(action, target, "invalid worker fixture result: "+err.Error()), nil
	}
	if waitErr != nil {
		if commandCtx.Err() != nil {
			return unknownAfterRelease(action, target, "bounded execution deadline elapsed"), nil
		}
		return unknownAfterRelease(action, target, fmt.Sprintf("worker exit: %v: %s", waitErr, stderr.String())), nil
	}
	expected := "deny the effect if the approved host pathname, object binding, or protected identity changed"
	observed := "an already exposed object/directory descriptor can remain usable after host rename; successful effects are consistent with mounted-tree/object authority"
	limitation := "one disposable same-user fixture; does not prove general backend safety or satisfy A"
	if strings.HasPrefix(action, "create-one") {
		expected = "create only at the approved absent leaf beneath the admitted parent object; refuse collisions and classify partial/unknown outcomes without replay"
		observed = "the typed worker retains a parent directory descriptor and performs one single-leaf create/publication operation"
		limitation = "synthetic one-file candidate only; protected-owner lifecycle and general backend safety remain unproven"
	}
	return Observation{
		Case:          action + " " + target,
		Profile:       profileName,
		Attempt:       action + " " + target,
		ActualEffect:  actualEffect + "; worker=" + result.Status + " " + result.Detail,
		ExpectedA:     expected,
		ObservedB:     observed,
		Status:        "measured",
		WorkerOutcome: result.Status,
		Limitation:    limitation,
	}, nil
}

func releaseWorker(stdin io.WriteCloser, awaitCreateCheckpoint bool) error {
	if _, err := io.WriteString(stdin, "RELEASE\n"); err != nil {
		return err
	}
	if awaitCreateCheckpoint {
		return nil
	}
	return stdin.Close()
}

func continueWorker(stdin io.WriteCloser) error {
	if _, err := io.WriteString(stdin, "CONTINUE\n"); err != nil {
		return err
	}
	return stdin.Close()
}

func parseWorkerResult(line string) (workerResult, error) {
	var result workerResult
	err := json.Unmarshal([]byte(line), &result)
	return result, err
}

func unknownAfterRelease(action, target, reason string) Observation {
	return Observation{Attempt: action + " " + target,
		ActualEffect: "worker failed after the operation gate was released; an effect may have occurred",
		ExpectedA:    "classify as partial/unknown unless no-effect evidence exists",
		ObservedB:    "not applicable", Status: "unknown", WorkerOutcome: "unknown",
		Limitation: sanitize(reason)}
}

func readLineBounded(reader *bufio.Reader) (string, error) {
	var out bytes.Buffer
	for out.Len() <= maxLineBytes {
		fragment, err := reader.ReadSlice('\n')
		if out.Len()+len(fragment) > maxLineBytes {
			return "", fmt.Errorf("worker output line exceeded 64 KiB: %w", errOutputLimit)
		}
		_, _ = out.Write(fragment)
		if err == nil {
			return strings.TrimSuffix(out.String(), "\n"), nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return "", err
		}
	}
	return "", errors.New("worker output exceeded 64 KiB")
}

var errOutputLimit = errors.New("worker output limit exceeded")

func classifyOutputBound(action string, readErr error, reaped bool) string {
	if action == "flood" && errors.Is(readErr, errOutputLimit) && reaped {
		return "measured_pass"
	}
	if action == "flood" {
		return "unavailable"
	}
	return "not_applicable"
}

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.limit == 0 {
		b.limit = workerOutputLimit
	}
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buffer.Write(p[:remaining])
		return len(p), nil
	}
	return b.buffer.Write(p)
}

func (b *boundedBuffer) String() string { return sanitize(b.buffer.String()) }

func sanitize(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return ' '
		}
		return r
	}, value)
	if len(value) > 512 {
		value = value[:512]
	}
	return value
}

func verifyObserved(name, scope string, result Observation) (string, string) {
	outside := filepath.Join(filepath.Dir(scope), "outside")
	read := func(path string) (string, bool) {
		data, err := os.ReadFile(path)
		if err != nil {
			return "absent (" + sanitize(err.Error()) + ")", false
		}
		return fmt.Sprintf("present (%d fixture bytes)", len(data)), true
	}
	status := result.WorkerOutcome
	switch name {
	case "selected-root-relocation":
		effect, present := read(filepath.Join(outside, "moved-root", "root-effect"))
		return "moved root effect=" + effect + "; worker=" + status, classifyEffect(present)
	case "selected-root-relocation-read":
		_, present := read(filepath.Join(outside, "moved-root", "secret.txt"))
		return fmt.Sprintf("read through opened file after root move=%t; worker=%s", status == "succeeded" && present, status), classifyEffect(status == "succeeded")
	case "parent-relocation-before-create":
		effect, present := read(filepath.Join(outside, "moved-parent", "new.txt"))
		return "moved parent effect=" + effect + "; worker=" + status, classifyEffect(present)
	case "parent-relocation-before-delete":
		_, movedPresent := read(filepath.Join(outside, "moved-parent", "target.txt"))
		_, replacementPresent := read(filepath.Join(scope, "parent", "target.txt"))
		return fmt.Sprintf("opened moved-parent target removed=%t; replacement at authorized pathname remains=%t; worker=%s", !movedPresent, replacementPresent, status), classifyEffect(!movedPresent)
	case "opened-target-relocation-before-write":
		data, err := os.ReadFile(filepath.Join(outside, "moved-target"))
		changed := err == nil && string(data) != "before"
		return fmt.Sprintf("moved target changed=%t; worker=%s", changed, status), classifyEffect(changed)
	case "existing-hardlink-alias", "late-hardlink-alias":
		data, err := os.ReadFile(filepath.Join(outside, "alias.txt"))
		changed := err == nil && string(data) != "before"
		return fmt.Sprintf("outside alias changed=%t; worker=%s", changed, status), classifyEffect(changed)
	case "deletion-target-substitution":
		_, err := os.Lstat(filepath.Join(scope, "target.txt"))
		displaced, _ := read(filepath.Join(outside, "displaced.txt"))
		removed := errors.Is(err, os.ErrNotExist)
		return fmt.Sprintf("replacement link removed=%t; original displaced entry=%s; worker=%s", removed, displaced, status), classifyEffect(removed)
	case "recursive-child-relocation":
		_, present := read(filepath.Join(outside, "moved-child", "item.txt"))
		return fmt.Sprintf("moved child file removed=%t; worker=%s", !present, status), classifyEffect(!present)
	case "rename-crosses-unmounted-scope":
		_, present := read(filepath.Join(scope, "source.txt"))
		return fmt.Sprintf("source remains=%t; destination is unmounted; worker=%s", present, status), classifyDenied(status == "failed")
	case "symlink-to-unmounted-host-path":
		secret, present := read(filepath.Join(outside, "secret.txt"))
		return "worker attempted read through symlink; host fixture=" + secret + "; worker=" + status, classifyDenied(status == "failed" && present)
	case "protected-path-and-alias":
		_, aliasPresent := read(filepath.Join(scope, "protected-alias.txt"))
		return fmt.Sprintf("direct path masked; alias exists=%t; worker=%s", aliasPresent, status), classifyDenied(status == "failed")
	case "nested-protected-path":
		return "masked nested path direct read; worker=" + status, classifyDenied(status == "failed")
	case "network-host-path-and-runtime-probes":
		return "sandbox capability probe=" + result.ActualEffect, classifyProbe(result.ActualEffect)
	case "bounded-output":
		if result.Status == "measured_pass" && strings.Contains(result.ActualEffect, "oversized protocol line rejected") {
			return result.ActualEffect, "measured_pass"
		}
		if result.Status == "observed_violation" {
			return result.ActualEffect, "observed_violation"
		}
		return result.ActualEffect, "unavailable"
	case "partial-write-outcome":
		data, err := os.ReadFile(filepath.Join(scope, "partial.txt"))
		if result.WorkerOutcome == "partial" && err == nil && string(data) == "prefix" {
			return "injected post-effect failure left a known partial fixture; worker outcome=partial", "measured_pass"
		}
		return "partial outcome fixture mismatch", "observed_violation"
	case "create-one-parent-relocation":
		moved := filepath.Join(filepath.Dir(scope), "outside", "moved-parent", "new.txt")
		data, err := os.ReadFile(moved)
		_, replacementErr := os.ReadFile(filepath.Join(scope, "parent", "new.txt"))
		if result.WorkerOutcome == "succeeded" && err == nil && string(data) == "synthetic create fixture\n" && errors.Is(replacementErr, os.ErrNotExist) {
			return "same approved parent object was moved by an independent actor; its retained descriptor created the synthetic file; replacement directory stayed untouched", "measured_pass"
		}
		return "relocated-parent creation did not match expected descriptor-bound effect", "observed_violation"
	case "create-one-destination-collision":
		data, err := os.ReadFile(filepath.Join(scope, "parent", "new.txt"))
		if result.WorkerOutcome == "failed" && err == nil && string(data) == "actor collision fixture\n" {
			return "exclusive create refused the independently inserted destination and preserved actor fixture", "measured_pass"
		}
		return "collision did not fail without preserving the actor fixture", "observed_violation"
	case "create-one-destination-symlink-substitution":
		info, err := os.Lstat(filepath.Join(scope, "parent", "new.txt"))
		if result.WorkerOutcome == "failed" && err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "exclusive create refused the independently substituted symlink without following it", "measured_pass"
		}
		return "symlink substitution was not refused", "observed_violation"
	case "create-one-partial-outcome":
		data, err := os.ReadFile(filepath.Join(scope, "parent", "partial.txt"))
		if result.WorkerOutcome == "partial" && err == nil && string(data) == "synthetic prefix" {
			return "worker reported partial after creation and a synthetic prefix; file remains incomplete", "measured_pass"
		}
		return "partial creation was misclassified or fixture differed", "observed_violation"
	case "create-one-late-hardlink":
		alias, err := os.ReadFile(filepath.Join(filepath.Dir(scope), "outside", "protected", "alias.txt"))
		if result.WorkerOutcome == "succeeded" && err == nil && string(alias) == "synthetic create fixture\n" {
			return "independent actor linked the newly created inode into a synthetic protected directory before payload write; later write exposed the payload through that alias", "observed_violation"
		}
		return "late-alias fixture did not observe the expected post-create exposure", "unavailable"
	case "create-one-otmpfile-publication":
		if result.WorkerOutcome == "unavailable" {
			return "unnamed staging publication unavailable under the current worker profile: " + result.ActualEffect, "unavailable"
		}
		data, err := os.ReadFile(filepath.Join(scope, "parent", "new.txt"))
		if result.WorkerOutcome == "succeeded" && err == nil && string(data) == "synthetic create fixture\n" {
			return "unnamed inode was fully written before no-replace publication", "measured"
		}
		return "unnamed publication did not complete", "unavailable"
	case "create-one-unknown-outcome":
		return verifyUnknownCreate(scope, result), "unknown"
	case "cancel-before-release":
		return result.ActualEffect, "measured_pass"
	default:
		return result.ActualEffect, "measured"
	}
}

func verifyScratch(scope string, result Observation) (string, string) {
	_, err := os.Lstat(filepath.Join(scope, "scratch.bin"))
	if err == nil {
		return "host-visible scratch fixture exists", "observed_violation"
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "could not confirm host scratch fixture absence: " + sanitize(err.Error()), "unavailable"
	}
	if result.WorkerOutcome != "succeeded" {
		return "worker scratch write did not succeed; no host fixture was observed", "unavailable"
	}
	return "worker scratch write succeeded in its private /tmp; no host fixture file appeared", "measured_pass"
}

func verifyUnknownCreate(scope string, result Observation) string {
	data, err := os.ReadFile(filepath.Join(scope, "parent", "unknown.txt"))
	if err == nil && string(data) == "synthetic create fixture\n" {
		return "synthetic file effect is present after the result was lost; the harness did not retry, so the outcome remains unknown and replay remains forbidden"
	}
	return "result was lost and the synthetic effect could not be determined; outcome remains unknown and replay remains forbidden"
}

func classifyEffect(effect bool) string {
	if effect {
		return "observed_violation"
	}
	return "observed_prevented"
}

func classifyDenied(denied bool) string {
	if denied {
		return "observed_prevented"
	}
	return "observed_violation"
}

func classifyProbe(detail string) string {
	for _, violation := range []string{"environment_leak", "fd3_open", "socket_allowed", "shell_lookup_available", "host_pid_visible", "host_root_mounted", "proc_mounted", "home_mounted", "home_dir_missing", "home_read_error", "home_config_present", "no_new_privs=0", "no_new_privs=unavailable", "seccomp_mode=0"} {
		if strings.Contains(detail, violation) {
			return "observed_violation"
		}
	}
	if !strings.Contains(detail, "no_new_privs=1") || !strings.Contains(detail, "seccomp_mode=2") || !strings.Contains(detail, "fd3_closed") || !strings.Contains(detail, "socket_denied") || !strings.Contains(detail, "host_root_not_mounted") || !strings.Contains(detail, "proc_not_mounted") || !strings.Contains(detail, "home_not_mounted") || !strings.Contains(detail, "home_config_not_present") || !strings.Contains(detail, "environment_clear") || !strings.Contains(detail, "shell_lookup_unavailable") || !strings.Contains(detail, "host_pid_unreachable") {
		return "unavailable"
	}
	return "measured_pass"
}

// WorkerMain is called by the study command's private worker mode. It is a
// typed fixture worker and never invokes a shell or accepts arbitrary commands.
func WorkerMain(args []string, stdin io.Reader, stdout io.Writer) int {
	if len(args) != 2 {
		return 2
	}
	action, relative := args[0], args[1]
	if !filepath.IsLocal(relative) || relative == "." {
		return 2
	}
	var target *os.File
	var parent *os.File
	deleteEntryValidated := false
	var err error
	switch action {
	case "write-open", "read-open":
		mode := os.O_RDONLY
		if action == "write-open" {
			mode = os.O_WRONLY | os.O_APPEND
		}
		target, err = os.OpenFile(filepath.Join("/scope", relative), mode, 0)
	case "create-parent", "delete-parent":
		parentPath := filepath.Dir(filepath.Join("/scope", relative))
		parent, err = os.Open(parentPath)
		if err == nil && action == "delete-parent" {
			var identity unix.Stat_t
			err = unix.Fstatat(int(parent.Fd()), filepath.Base(relative), &identity, unix.AT_SYMLINK_NOFOLLOW)
			deleteEntryValidated = err == nil
		}
	case "recursive-child":
		parent, err = os.Open(filepath.Join("/scope", relative))
	case "read-path":
		// Resolution intentionally happens only after release for link/mask probes.
	case "create-one", "create-one-partial", "create-one-unknown", "create-one-late-link", "create-one-otmpfile":
		parentPath := filepath.Dir(relative)
		rootFD, openErr := unix.Open("/scope", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if openErr != nil {
			err = openErr
		} else if parentPath == "." {
			parent = os.NewFile(uintptr(rootFD), "scope")
		} else {
			how := &unix.OpenHow{Flags: unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV}
			var parentFD int
			parentFD, err = unix.Openat2(rootFD, parentPath, how)
			_ = unix.Close(rootFD)
			if err == nil {
				parent = os.NewFile(uintptr(parentFD), parentPath)
			}
		}
		if err == nil && (filepath.Base(relative) == "." || strings.Contains(filepath.Base(relative), string(filepath.Separator))) {
			err = errors.New("create target must be a single leaf")
		}
	case "probe", "scratch", "flood", "hold", "rename-out", "partial-create":
	default:
		return 2
	}
	if err != nil {
		result := workerResult{Status: "setup_refused", Detail: sanitize(err.Error())}
		_ = json.NewEncoder(stdout).Encode(result)
		return 0
	}
	_, _ = io.WriteString(stdout, "READY\n")
	line, err := bufio.NewReader(io.LimitReader(stdin, 64)).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "RELEASE" {
		if target != nil {
			_ = target.Close()
		}
		if parent != nil {
			_ = parent.Close()
		}
		return 3
	}
	_, _ = io.WriteString(stdout, "STARTED\n")
	result := workerResult{Status: "succeeded"}
	switch action {
	case "write-open":
		var written int
		written, err = target.Write([]byte("changed"))
		if err == nil && written != len("changed") {
			err = io.ErrShortWrite
		}
		if written > 0 && err != nil {
			result.Status = "partial"
		}
	case "read-open":
		data, readErr := io.ReadAll(io.LimitReader(target, 1024))
		err = readErr
		result.Detail = fmt.Sprintf("read %d fixture bytes", len(data))
	case "create-parent":
		name := filepath.Base(relative)
		var fd int
		fd, err = unix.Openat(int(parent.Fd()), name, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC, 0o600)
		if err == nil {
			file := os.NewFile(uintptr(fd), name)
			var written int
			written, err = file.Write([]byte("created"))
			if err == nil && written != len("created") {
				err = io.ErrShortWrite
			}
			if err != nil {
				result.Status = "partial"
			}
			_ = file.Close()
		}
	case "create-one", "create-one-partial", "create-one-unknown", "create-one-late-link":
		name := filepath.Base(relative)
		fd, openErr := unix.Openat(int(parent.Fd()), name, unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0o600)
		err = openErr
		if err == nil {
			file := os.NewFile(uintptr(fd), name)
			payload := []byte("synthetic create fixture\n")
			if action == "create-one-late-link" {
				_, _ = io.WriteString(stdout, "CREATED\n")
				command, commandErr := bufio.NewReader(io.LimitReader(stdin, 64)).ReadString('\n')
				if commandErr != nil || strings.TrimSpace(command) != "CONTINUE" {
					_ = file.Close()
					result.Status = "unknown"
					return 4
				}
			}
			if action == "create-one-partial" {
				payload = []byte("synthetic prefix")
			}
			written, writeErr := file.Write(payload)
			if action == "create-one-unknown" {
				_ = file.Close()
				_ = parent.Close()
				return 4 // effect occurred but result publication is intentionally lost
			}
			if action == "create-one-partial" {
				result.Status = "partial"
				result.Detail = "injected post-create incomplete-content outcome"
				err = errors.New("synthetic partial write")
			} else if writeErr != nil || written != len(payload) {
				err = errors.Join(writeErr, io.ErrShortWrite)
				if written > 0 {
					result.Status = "partial"
				}
			}
			if closeErr := file.Close(); closeErr != nil && err == nil {
				err = closeErr
				if written > 0 {
					result.Status = "partial"
				}
			}
		}
	case "create-one-otmpfile":
		payload := []byte("synthetic create fixture\n")
		fd, tmpErr := unix.Openat(int(parent.Fd()), ".", unix.O_TMPFILE|unix.O_WRONLY|unix.O_CLOEXEC, 0o600)
		if tmpErr != nil {
			result.Status = "unavailable"
			err = tmpErr
			break
		}
		file := os.NewFile(uintptr(fd), "unnamed-create-fixture")
		written, writeErr := file.Write(payload)
		if writeErr != nil || written != len(payload) {
			result.Status = "unavailable"
			err = errors.Join(writeErr, io.ErrShortWrite)
			_ = file.Close()
			break
		}
		procPath := fmt.Sprintf("/proc/self/fd/%d", fd)
		err = unix.Linkat(unix.AT_FDCWD, procPath, int(parent.Fd()), filepath.Base(relative), unix.AT_SYMLINK_FOLLOW)
		if err != nil {
			result.Status = "unavailable"
		}
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = closeErr
			result.Status = "unknown"
		}
	case "delete-parent":
		name := filepath.Base(relative)
		err = unix.Unlinkat(int(parent.Fd()), name, 0)
		if err == nil && deleteEntryValidated {
			result.Detail = "entry identity checked before operation gate"
		}
	case "recursive-child":
		entries, readErr := parent.ReadDir(-1)
		err = readErr
		deleted := 0
		for _, entry := range entries {
			if err != nil {
				break
			}
			if entry.IsDir() {
				err = unix.Unlinkat(int(parent.Fd()), entry.Name(), unix.AT_REMOVEDIR)
			} else {
				err = unix.Unlinkat(int(parent.Fd()), entry.Name(), 0)
			}
			if err == nil {
				deleted++
			}
		}
		if deleted > 0 && err != nil {
			result.Status = "partial"
		}
	case "rename-out":
		err = os.Rename(filepath.Join("/scope", relative), filepath.Join("/outside", "renamed.txt"))
	case "read-path":
		var data []byte
		data, err = os.ReadFile(filepath.Join("/scope", relative))
		result.Detail = fmt.Sprintf("read %d fixture bytes", len(data))
	case "probe":
		result.Detail = sandboxProbe(relative)
	case "scratch":
		result.Detail = "private scratch write"
		err = os.WriteFile(filepath.Join("/tmp", relative), []byte("scratch fixture"), 0o600)
	case "flood":
		_, err = io.WriteString(stdout, strings.Repeat("x", 128*1024))
	case "partial-create":
		var file *os.File
		file, err = os.OpenFile(filepath.Join("/scope", relative), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, err = file.Write([]byte("prefix"))
			result.Status = "partial"
			result.Detail = "injected fixture failure after creating and writing the prefix"
			_ = file.Close()
		}
	case "hold":
		result.Detail = "cancelled before effect"
	}
	if target != nil {
		_ = target.Close()
	}
	if parent != nil {
		_ = parent.Close()
	}
	if err != nil && result.Status != "partial" && result.Status != "unavailable" && result.Status != "unknown" {
		result.Status = "failed"
		result.Detail = sanitize(err.Error())
	} else if err != nil {
		result.Detail = sanitize(err.Error())
	}
	_ = json.NewEncoder(stdout).Encode(result)
	return 0
}

func sandboxProbe(hostPIDText string) string {
	var details []string
	if os.Getenv("KOMPOSYN_STUDY_SECRET") == "fixture-secret" {
		details = append(details, "environment_leak")
	} else {
		details = append(details, "environment_clear")
	}
	if _, err := os.Stat("/etc/passwd"); err != nil {
		details = append(details, "host_root_not_mounted")
	} else {
		details = append(details, "host_root_mounted")
	}
	if _, err := os.Stat("/proc/self"); err != nil {
		details = append(details, "proc_not_mounted")
	} else {
		details = append(details, "proc_mounted")
	}
	if _, err := os.Stat("/home"); err != nil {
		details = append(details, "home_not_mounted")
	} else {
		details = append(details, "home_mounted")
	}
	details = append(details, probeHome(os.Getenv("HOME")))
	if _, err := unix.FcntlInt(uintptr(3), unix.F_GETFD, 0); err != nil {
		details = append(details, "fd3_closed")
	} else {
		details = append(details, "fd3_open")
	}
	for _, family := range []int{unix.AF_INET, unix.AF_UNIX} {
		fd, err := unix.Socket(family, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
		if err == nil {
			_ = unix.Close(fd)
			details = append(details, "socket_allowed")
		} else {
			details = append(details, "socket_denied")
		}
	}
	if _, err := exec.LookPath("sh"); err != nil {
		details = append(details, "shell_lookup_unavailable")
	} else {
		details = append(details, "shell_lookup_available")
	}
	noNewPrivs, _, noNewPrivsErr := unix.Syscall(unix.SYS_PRCTL, unix.PR_GET_NO_NEW_PRIVS, 0, 0)
	if noNewPrivsErr == 0 {
		details = append(details, fmt.Sprintf("no_new_privs=%d", noNewPrivs))
	} else {
		details = append(details, "no_new_privs=unavailable")
	}
	seccomp, _, seccompErr := unix.Syscall(unix.SYS_PRCTL, unix.PR_GET_SECCOMP, 0, 0)
	if seccompErr == 0 {
		details = append(details, fmt.Sprintf("seccomp_mode=%d", seccomp))
	}
	if hostPID, err := strconv.Atoi(hostPIDText); err == nil {
		if err := unix.Kill(hostPID, 0); err != nil {
			details = append(details, "host_pid_unreachable")
		} else {
			details = append(details, "host_pid_visible")
		}
	}
	return strings.Join(details, ",")
}

func probeHome(path string) string {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return "home_dir_missing"
	}
	if err != nil {
		return "home_read_error"
	}
	if len(entries) == 0 {
		return "home_config_not_present"
	}
	return "home_config_present"
}
