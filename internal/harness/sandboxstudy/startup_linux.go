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
	"context"
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

const startupOutputProbe = "SYNTHETIC_APPROVAL_DISPLAY_CHANNEL\n"

// StartupExposureReport is a bounded synthetic probe of a descriptor opened
// through procfs before a process protects itself. It does not model the chat
// approval chain or qualify a host profile.
type StartupExposureReport struct {
	InitialDumpable    int    `json:"initial_dumpable,omitempty"`
	ProtectionDumpable int    `json:"protection_dumpable,omitempty"`
	FixtureFilesystem  string `json:"fixture_filesystem,omitempty"`
	FixtureMountID     uint64 `json:"fixture_mount_id,omitempty"`
	PreProtectionOpen  string `json:"pre_protection_proc_fd_open"`
	RetainedWrite      string `json:"write_through_retained_descriptor"`
	PostProtectionOpen string `json:"post_protection_proc_fd_open"`
	ObservedOutput     string `json:"observed_synthetic_output,omitempty"`
	WorkerReaped       bool   `json:"target_reaped"`
	ActorReaped        bool   `json:"actor_reaped"`
	Status             string `json:"status"`
	Limitation         string `json:"limitation,omitempty"`
}

// RunStartupExposureStudy uses the sandbox-study executable itself as a
// synthetic target and same-UID actor. The actor opens the target's inherited
// stdout before PR_SET_DUMPABLE(0), retains it, and attempts a write after the
// transition. No Bubblewrap or host setting is changed.
func RunStartupExposureStudy(ctx context.Context, executable string) (StartupExposureReport, error) {
	var report StartupExposureReport
	if !filepath.IsAbs(executable) {
		return report, errors.New("study executable path must be absolute")
	}
	root, err := os.MkdirTemp("", "komposyn-issue319-startup-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(root)
	report.FixtureFilesystem, report.FixtureMountID = fixtureFilesystem(root)

	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	targetRead, targetWrite, err := os.Pipe()
	if err != nil {
		return report, err
	}
	target := exec.CommandContext(probeCtx, executable, "--sandbox-study-gate-a-target")
	target.Env = []string{}
	target.Stdout = targetWrite
	targetControl, err := target.StdinPipe()
	if err != nil {
		_ = targetRead.Close()
		_ = targetWrite.Close()
		return report, err
	}
	if err := target.Start(); err != nil {
		_ = targetRead.Close()
		_ = targetWrite.Close()
		_ = targetControl.Close()
		return report, fmt.Errorf("start synthetic target: %w", err)
	}
	_ = targetWrite.Close()
	var actor *exec.Cmd
	defer func() {
		if targetControl != nil {
			_ = targetControl.Close()
		}
		stopProcess(actor)
		stopProcess(target)
		_ = targetRead.Close()
	}()
	targetReader := bufio.NewReader(io.LimitReader(targetRead, 4096))
	targetLine, err := readBoundedLine(targetReader)
	if err != nil {
		return report, fmt.Errorf("read synthetic target readiness: %w", err)
	}
	var initial int
	if _, err := fmt.Sscanf(targetLine, "READY %d", &initial); err != nil {
		return report, fmt.Errorf("invalid synthetic target readiness %q", targetLine)
	}
	report.InitialDumpable = initial
	if initial != 1 {
		report.PreProtectionOpen = "unavailable: target did not start dumpable=1"
		report.RetainedWrite = "not_run"
		report.PostProtectionOpen = "not_run"
		report.Status = "unavailable"
		report.Limitation = "the host's exec-time dumpability policy prevented the positive control"
		_ = targetControl.Close()
		targetControl = nil
		_ = target.Wait()
		report.WorkerReaped = target.ProcessState != nil
		report.ActorReaped = true
		return report, nil
	}

	actorDir := filepath.Join(root, "actor-before")
	if err := os.Mkdir(actorDir, 0o700); err != nil {
		return report, err
	}
	actor = exec.CommandContext(probeCtx, executable, "--sandbox-study-gate-a-actor", "hold", strconv.Itoa(target.Process.Pid), actorDir)
	actor.Env = []string{}
	if err := actor.Start(); err != nil {
		return report, fmt.Errorf("start pre-protection actor: %w", err)
	}
	openResult, err := waitStartupActorResult(probeCtx, filepath.Join(actorDir, "result"))
	if err != nil || !strings.HasPrefix(openResult, "opened") {
		report.PreProtectionOpen = "unavailable: " + sanitize(openResult)
		report.RetainedWrite = "not_run"
		report.PostProtectionOpen = "not_run"
		report.Status = "unavailable"
		report.Limitation = "the positive proc-fd control did not establish a retained descriptor"
		_ = actor.Wait()
		_ = targetControl.Close()
		targetControl = nil
		stopProcess(target)
		report.WorkerReaped = target.ProcessState != nil
		report.ActorReaped = actor.ProcessState != nil
		return report, nil
	}
	report.PreProtectionOpen = "opened while target reported dumpable=1"

	if _, err := io.WriteString(targetControl, "PROTECT\n"); err != nil {
		return report, fmt.Errorf("request target protection: %w", err)
	}
	protectedLine, err := readBoundedLine(targetReader)
	if err != nil {
		return report, fmt.Errorf("read target protection checkpoint: %w", err)
	}
	var protected int
	if _, err := fmt.Sscanf(protectedLine, "PROTECTED %d", &protected); err != nil || protected != 0 {
		return report, fmt.Errorf("target did not confirm dumpable=0: %q", protectedLine)
	}
	report.ProtectionDumpable = protected

	postDir := filepath.Join(root, "actor-after")
	if err := os.Mkdir(postDir, 0o700); err != nil {
		return report, err
	}
	postActor := exec.CommandContext(probeCtx, executable, "--sandbox-study-gate-a-actor", "probe", strconv.Itoa(target.Process.Pid), postDir)
	postActor.Env = []string{}
	if err := postActor.Run(); err != nil {
		return report, fmt.Errorf("run post-protection actor: %w", err)
	}
	postResult, err := waitStartupActorResult(probeCtx, filepath.Join(postDir, "result"))
	if err != nil {
		return report, err
	}
	if strings.HasPrefix(postResult, "opened") {
		report.PostProtectionOpen = "opened"
	} else {
		report.PostProtectionOpen = "refused: " + sanitize(postResult)
	}

	if err := os.WriteFile(filepath.Join(actorDir, "write"), []byte("1"), 0o600); err != nil {
		return report, err
	}
	actorErr := actor.Wait()
	actorResult, resultErr := waitStartupActorResult(probeCtx, filepath.Join(actorDir, "done"))
	if resultErr != nil || actorErr != nil || !strings.HasPrefix(actorResult, "wrote") {
		report.RetainedWrite = "unavailable: " + sanitize(actorResult)
		report.Status = "inconclusive"
	} else {
		report.RetainedWrite = "write succeeded through the pre-protection descriptor after target dumpable=0"
	}
	report.ActorReaped = actor.ProcessState != nil

	_, _ = io.WriteString(targetControl, "EXIT\n")
	_ = targetControl.Close()
	targetControl = nil
	workerErr := target.Wait()
	report.WorkerReaped = target.ProcessState != nil
	tail, tailErr := io.ReadAll(targetReader)
	if tailErr == nil {
		report.ObservedOutput = string(tail)
	}
	_ = targetRead.Close()
	if workerErr != nil {
		return report, fmt.Errorf("wait for synthetic target: %w", workerErr)
	}
	if report.RetainedWrite != "unavailable: " && report.RetainedWrite != "" && actorResult != "" && strings.Contains(report.ObservedOutput, startupOutputProbe) {
		report.Status = "observed_violation"
	} else if report.RetainedWrite != "unavailable: " && report.RetainedWrite != "" {
		report.Status = "inconclusive"
		report.Limitation = "the write syscall returned but the target output channel did not contain the exact synthetic marker"
	}
	if report.Status == "" {
		report.Status = "inconclusive"
		report.Limitation = "the actor's post-protection write outcome was not established"
	}
	return report, nil
}

// StartupTargetMain is the fixed synthetic target used by the Gate A probe.
func StartupTargetMain() int {
	dumpable, err := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0)
	if err != nil {
		return 2
	}
	if _, err := fmt.Fprintf(os.Stdout, "READY %d\n", dumpable); err != nil {
		return 2
	}
	commands := bufio.NewScanner(os.Stdin)
	if !commands.Scan() || commands.Text() != "PROTECT" {
		return 2
	}
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return 2
	}
	dumpable, err = unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0)
	if err != nil || dumpable != 0 {
		return 2
	}
	if _, err := fmt.Fprintln(os.Stdout, "PROTECTED 0"); err != nil {
		return 2
	}
	if !commands.Scan() || commands.Text() != "EXIT" {
		return 2
	}
	return 0
}

// StartupActorMain is the fixed same-user actor used by the Gate A probe.
func StartupActorMain(args []string) int {
	if len(args) != 3 {
		return 2
	}
	pid, err := strconv.Atoi(args[1])
	if err != nil || pid < 1 {
		return 2
	}
	dir := args[2]
	fd, openErr := unix.Open(fmt.Sprintf("/proc/%d/fd/1", pid), unix.O_WRONLY|unix.O_CLOEXEC, 0)
	if openErr != nil {
		_ = writeStartupResult(filepath.Join(dir, "result"), openErr.Error())
		return 0
	}
	defer unix.Close(fd)
	if args[0] == "probe" {
		_ = writeStartupResult(filepath.Join(dir, "result"), "opened")
		return 0
	}
	if args[0] != "hold" {
		return 2
	}
	if err := writeStartupResult(filepath.Join(dir, "result"), "opened"); err != nil {
		return 2
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "write")); err == nil {
			n, writeErr := unix.Write(fd, []byte(startupOutputProbe))
			if writeErr != nil || n != len(startupOutputProbe) {
				_ = writeStartupResult(filepath.Join(dir, "done"), "write-failed")
				return 1
			}
			_ = writeStartupResult(filepath.Join(dir, "done"), "wrote")
			return 0
		}
		time.Sleep(2 * time.Millisecond)
	}
	_ = writeStartupResult(filepath.Join(dir, "done"), "timeout")
	return 1
}

func waitStartupActorResult(ctx context.Context, path string) (string, error) {
	deadline := time.NewTicker(2 * time.Millisecond)
	defer deadline.Stop()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for {
		if data, err := os.ReadFile(path); err == nil {
			return strings.TrimSpace(string(data)), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout.C:
			return "", errors.New("actor checkpoint timed out")
		case <-deadline.C:
		}
	}
}

func writeStartupResult(path, result string) error {
	return os.WriteFile(path, []byte(result), 0o600)
}

func readBoundedLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if len(line) > 256 {
		return "", errors.New("synthetic control line exceeded limit")
	}
	return strings.TrimSpace(line), err
}

func stopProcess(command *exec.Cmd) {
	if command == nil {
		return
	}
	if command.Process != nil && command.ProcessState == nil {
		_ = command.Process.Kill()
		_ = command.Wait()
	}
}
