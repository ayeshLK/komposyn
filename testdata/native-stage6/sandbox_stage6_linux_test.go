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

//go:build linux && komposyn_stage6_study

package sandbox

import (
	"context"
	"debug/elf"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	stdruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	"github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
	"github.com/ayeshLK/komposyn/internal/harness/runtime"
	"github.com/ayeshLK/komposyn/internal/harness/settings"
	provider "github.com/ayeshLK/komposyn/plugins/model-provider"
	"github.com/ayeshLK/komposyn/plugins/tools"
)

func init() {
	if handled, code := WorkerBootstrap(os.Args[1:]); handled {
		os.Exit(code)
	}
	if handled, err := readbrokerstudy.RunNativeActorMode(os.Args[1:]); handled {
		if err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
}

type stage6Case struct {
	ID                        string         `json:"case_id"`
	Status                    string         `json:"status"`
	EvidenceKind              string         `json:"evidence_kind,omitempty"`
	Reason                    string         `json:"reason,omitempty"`
	FailureDiagnostic         string         `json:"failure_diagnostic,omitempty"`
	LaunchPolicy              map[string]any `json:"launch_policy,omitempty"`
	WorkerStarted             bool           `json:"worker_started"`
	WorkerReady               bool           `json:"worker_ready"`
	WorkerJoined              bool           `json:"worker_joined"`
	CapabilityTransferred     bool           `json:"capability_transferred"`
	CapabilityHeld            bool           `json:"capability_held"`
	ReadStarted               bool           `json:"read_started"`
	ReadOutcome               string         `json:"read_outcome,omitempty"`
	ProbePassed               bool           `json:"required_probe_passed"`
	OnceApprovalCount         int32          `json:"once_approval_count"`
	OnceGrantConsumptionCount int            `json:"once_grant_consumption_count"`
	GrantRevisionBefore       uint64         `json:"grant_revision_before"`
	GrantRevisionAfter        uint64         `json:"grant_revision_after"`
	RemainingGrantCount       int            `json:"remaining_grant_count"`
	ProviderRequestCount      int            `json:"provider_request_count"`
	FixtureBytesMatched       bool           `json:"fixture_bytes_matched"`
	DisclosureWithheld        bool           `json:"disclosure_withheld"`
	StaleDataAbsent           bool           `json:"stale_data_absent"`
	LateResponseReturned      bool           `json:"late_response_returned"`
	LateDeltaAttempted        bool           `json:"late_delta_attempted"`
	LateDeltaSuppressed       bool           `json:"late_delta_suppressed"`
	ActorStarted              bool           `json:"actor_started"`
	ActorTransitionCompleted  bool           `json:"actor_transition_completed"`
	ActorJoined               bool           `json:"actor_joined"`
	ActorTransitionCount      int            `json:"actor_transition_count"`
	AuthorityObservation      string         `json:"authority_observation,omitempty"`
	CleanupConfirmed          bool           `json:"cleanup_confirmed"`
	CoordinatorStarted        bool           `json:"coordinator_started"`
	CoordinatorJoined         bool           `json:"coordinator_joined"`
	BubblewrapExitObserved    bool           `json:"bubblewrap_exit_observed"`
	NamespaceInitExitObserved bool           `json:"namespace_init_exit_observed"`
	ObservedProcessesGone     bool           `json:"observed_processes_gone"`
}

func stage6CaptureReadFailure(caseID string, coordinator readbrokerstudy.HostCoordinator) {
	if coordinator == nil {
		return
	}
	evidence := coordinator.Evidence()
	if len(evidence) == 0 {
		return
	}
	last := evidence[len(evidence)-1]
	stage6Update(caseID, func(item *stage6Case) {
		item.WorkerStarted = last.WorkerStarted
		item.WorkerReady = last.WorkerReady
		item.WorkerJoined = last.WorkerJoined
		item.CapabilityTransferred = last.CapabilitySent
		item.CapabilityHeld = last.CapabilityHeld
		item.ReadStarted = last.ReadStarted
		item.ReadOutcome = string(last.Outcome)
		item.ProbePassed = last.Probe.NoNewPrivs && last.Probe.SeccompMode == 2 && last.Probe.SocketCreationDenied && last.Probe.PathOpenDenied && last.Probe.ReadCapabilityInitiallyAbsent && last.Probe.OnlyProtocolDescriptor && last.Probe.EnvironmentCleared
		item.FailureDiagnostic = boundedStage6Diagnostic(last.FailureDetail)
	})
}

func boundedStage6Diagnostic(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 768 {
		value = value[:768]
	}
	return value
}

func stage6RequestedBindMode() readbrokerstudy.BindMode {
	switch os.Getenv("KOMPOSYN_STAGE6_BIND_MODE") {
	case "", "auto":
		return ""
	case string(readbrokerstudy.BindModeDescriptor):
		return readbrokerstudy.BindModeDescriptor
	case string(readbrokerstudy.BindModeFileSnapshot):
		return readbrokerstudy.BindModeFileSnapshot
	default:
		return readbrokerstudy.BindMode("invalid-stage6-bind-mode")
	}
}

type stage6Report struct {
	SchemaVersion int            `json:"schema_version"`
	SourceCommit  string         `json:"source_commit"`
	RunnerVersion string         `json:"runner_version"`
	OS            string         `json:"os"`
	Architecture  string         `json:"architecture"`
	Kernel        string         `json:"kernel"`
	UID           int            `json:"uid"`
	Backend       map[string]any `json:"backend"`
	WorkerImage   map[string]any `json:"worker_image"`
	LaunchPolicy  map[string]any `json:"launch_policy"`
	Overall       string         `json:"overall_status"`
	Cases         []stage6Case   `json:"cases"`
	Limitations   []string       `json:"limitations"`
}

type stage6BoundedBuffer struct {
	mu   sync.Mutex
	data []byte
	max  int
}

func (b *stage6BoundedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.max <= 0 {
		b.max = 4096
	}
	remaining := b.max - len(b.data)
	if remaining > 0 {
		if remaining > len(data) {
			remaining = len(data)
		}
		b.data = append(b.data, data[:remaining]...)
	}
	return len(data), nil
}

func (b *stage6BoundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

var stage6 = newStage6Report()

var stage6Mu sync.Mutex

func newStage6Report() stage6Report {
	ids := []string{
		"ordinary_chat_without_root", "read_refused_without_root_or_backend", "synthetic_once_authority_flow", "positive_exact_read_and_replay", "empty_file_read",
		"protected_auth_refusal", "protected_settings_refusal", "provider_source_owner",
		"incomplete_owner_inventory", "actor_helper_control", "independent_namespace_actor", "expected_once_transitions",
		"captured_batch_revocation", "pre_handoff_owner_invalidation", "post_handoff_owner_invalidation",
		"late_response_and_delta_retirement", "cancellation_and_cleanup", "launch_profile_and_limits",
		"mutation_profile", "shell_profile", "parent_death_cleanup",
	}
	syntheticCases := map[string]struct{}{
		"ordinary_chat_without_root":           {},
		"read_refused_without_root_or_backend": {},
		"synthetic_once_authority_flow":        {},
		"provider_source_owner":                {},
		"incomplete_owner_inventory":           {},
	}
	actorFixtureCases := map[string]struct{}{"actor_helper_control": {}}
	workerLifecycleCases := map[string]struct{}{"parent_death_cleanup": {}}
	unimplementedCases := map[string]struct{}{
		"mutation_profile": {},
		"shell_profile":    {},
	}
	cases := make([]stage6Case, 0, len(ids))
	for _, id := range ids {
		item := stage6Case{ID: id, Status: "unavailable", Reason: "native-integrated case was not executed"}
		_, synthetic := syntheticCases[id]
		_, unimplemented := unimplementedCases[id]
		if synthetic {
			item.EvidenceKind = "synthetic-runtime-composition"
			item.Reason = "synthetic runtime/composition control was not executed"
		} else if _, actorFixture := actorFixtureCases[id]; actorFixture {
			item.EvidenceKind = "synthetic-actor-fixture"
			item.Reason = "synthetic separate-process actor helper control was not executed"
		} else if unimplemented {
			item.EvidenceKind = "not-implemented"
			item.Reason = "case is not implemented by Stage 6 runner version 8"
		} else if _, workerLifecycle := workerLifecycleCases[id]; workerLifecycle {
			item.EvidenceKind = "native-worker-lifecycle"
			item.Reason = "fixed worker parent-death lifecycle case was not executed"
		} else {
			item.EvidenceKind = "native-integrated"
		}
		cases = append(cases, item)
	}
	workerImage := map[string]any{"profile": "stage6-study-test-binary"}
	limitations := []string{
		"Native acceptance remains incomplete until every required case is measured on the final integrated profile.",
		"Provider-source positive authorization/rotation, mutation, shell, aggregate cgroup quotas, and non-Linux enforcement are not established here.",
	}
	if digest := os.Getenv("KOMPOSYN_STAGE6_WORKER_IMAGE_SHA256"); digest != "" {
		sizeBytes, _ := strconv.ParseInt(os.Getenv("KOMPOSYN_STAGE6_WORKER_IMAGE_SIZE"), 10, 64)
		workerImage = map[string]any{
			"profile": "cmd-harness-worker-image", "sha256": digest,
			"size_bytes": sizeBytes, "bind_mode": "file-snapshot",
		}
		limitations = append(limitations,
			"The native profile launches the exact source-built cmd/harness executable as the worker, while the coordinator and provider fixture remain the Stage 6 test composition.",
			"Parent-death cleanup is unavailable in cmd/harness worker-image mode because its helper process uses the Stage 6 test binary; the shipped-image worker lifecycle is not claimed.",
		)
	} else {
		limitations = append(limitations,
			"This Stage 6 profile uses a test-only runtime composition and does not validate native launch of the shipped command image or real-provider integration.",
		)
	}
	return stage6Report{
		SchemaVersion: 1, SourceCommit: os.Getenv("KOMPOSYN_STAGE6_SOURCE_COMMIT"), RunnerVersion: "8",
		OS: "linux", Architecture: stdruntime.GOARCH, Kernel: stage6KernelRelease(), UID: os.Getuid(), Overall: "stage6_profile_incomplete",
		Backend: map[string]any{}, WorkerImage: workerImage, LaunchPolicy: map[string]any{}, Cases: cases,
		Limitations: limitations,
	}
}

func stage6KernelRelease() string {
	var name unix.Utsname
	if err := unix.Uname(&name); err != nil {
		return ""
	}
	value := make([]byte, 0, len(name.Release))
	for _, character := range name.Release {
		if character == 0 {
			break
		}
		value = append(value, byte(character))
	}
	return string(value)
}

func TestMain(m *testing.M) {
	code := m.Run()
	stage6Mu.Lock()
	if code != 0 {
		stage6.Overall = "candidate_failed_or_incomplete"
	} else if trusted, _ := stage6.Backend["trusted"].(bool); !trusted {
		for index := range stage6.Cases {
			if stage6.Cases[index].Status == "inconclusive" {
				stage6.Cases[index].Status = "unavailable"
				stage6.Cases[index].Reason = "case did not run because trusted backend validation refused before backend probes or worker launch"
			}
		}
		stage6.Overall = "native_unavailable"
	} else if allNativeCasesUnavailable(stage6.Cases) {
		stage6.Overall = "native_unavailable"
	}
	data, err := json.MarshalIndent(stage6, "", "  ")
	stage6Mu.Unlock()
	if path := os.Getenv("KOMPOSYN_STAGE6_REPORT"); path != "" && err == nil {
		_ = os.WriteFile(path, append(data, '\n'), 0600)
	}
	os.Exit(code)
}

func allNativeCasesUnavailable(cases []stage6Case) bool {
	foundNative := false
	for _, item := range cases {
		if item.EvidenceKind != "native-integrated" {
			continue
		}
		foundNative = true
		if item.Status != "unavailable" {
			return false
		}
	}
	return foundNative
}

func TestStage6StudyWorkerImageMeetsLaunchValidation(t *testing.T) {
	executable, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatalf("study worker image could not be opened: %v", err)
	}
	defer executable.Close()
	var image unix.Stat_t
	if err := unix.Fstat(int(executable.Fd()), &image); err != nil {
		t.Fatalf("study worker image identity could not be inspected: %v", err)
	}
	if image.Mode&unix.S_IFMT != unix.S_IFREG || image.Size <= 0 || image.Size > 7<<20 {
		t.Fatalf("study worker image does not meet the fixed regular-file size bound: size=%d", image.Size)
	}
	imageELF, err := elf.NewFile(executable)
	if err != nil {
		t.Fatalf("stripped study image is not a valid ELF executable: %v", err)
	}
	defer imageELF.Close()
	if imageELF.Type != elf.ET_EXEC && imageELF.Type != elf.ET_DYN {
		t.Fatalf("study image has unsupported ELF type: %s", imageELF.Type)
	}
	for _, program := range imageELF.Progs {
		if program.Type == elf.PT_INTERP {
			t.Fatal("study worker image has a dynamic interpreter")
		}
	}
}

func TestStage6ReportClassifiesEveryCase(t *testing.T) {
	report := newStage6Report()
	counts := map[string]int{}
	for _, item := range report.Cases {
		if item.EvidenceKind == "" {
			t.Fatalf("case %q has no evidence classification", item.ID)
		}
		counts[item.EvidenceKind]++
	}
	want := map[string]int{
		"synthetic-runtime-composition": 5,
		"synthetic-actor-fixture":       1,
		"native-integrated":             12,
		"native-worker-lifecycle":       1,
		"not-implemented":               2,
	}
	if len(counts) != len(want) {
		t.Fatalf("unexpected evidence classifications: got %v want %v", counts, want)
	}
	for kind, count := range want {
		if counts[kind] != count {
			t.Errorf("%s cases = %d, want %d", kind, counts[kind], count)
		}
	}
	if !allNativeCasesUnavailable(report.Cases) {
		t.Fatal("synthetic or unimplemented cases incorrectly prevented native_unavailable classification")
	}
	for index := range report.Cases {
		if report.Cases[index].EvidenceKind == "native-integrated" {
			report.Cases[index].Status = "measured_pass"
			break
		}
	}
	if allNativeCasesUnavailable(report.Cases) {
		t.Fatal("a measured native case was classified as wholly unavailable")
	}
}

func TestStage6DiagnosticIsBoundedAndControlSafe(t *testing.T) {
	got := boundedStage6Diagnostic("startup\nfailed\x00" + strings.Repeat("x", 900))
	if strings.ContainsAny(got, "\r\n\x00") || len(got) > 768 || !strings.HasPrefix(got, "startup failed ") {
		t.Fatalf("bounded diagnostic=%q length=%d", got, len(got))
	}
}

func TestStage6PIDFDExitObservationUsesProcessIdentity(t *testing.T) {
	if os.Getenv("KOMPOSYN_STAGE6_PIDFD_CHILD") == "1" {
		for {
			time.Sleep(time.Hour)
		}
	}
	child := exec.Command(os.Args[0], "-test.run=^TestStage6PIDFDExitObservationUsesProcessIdentity$")
	child.Stdout, child.Stderr = io.Discard, io.Discard
	child.Env = []string{"KOMPOSYN_STAGE6_PIDFD_CHILD=1"}
	if err := child.Start(); err != nil {
		t.Fatal("could not start bounded process-observation fixture")
	}
	joined := false
	defer func() {
		if !joined {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()

	deadline := time.Now().Add(2 * time.Second)
	var identity readbrokerstudy.HostProcessIdentity
	for time.Now().Before(deadline) {
		start, err := readbrokerstudy.ReadHostProcessStartTime(child.Process.Pid)
		if err == nil && start != 0 {
			identity = readbrokerstudy.HostProcessIdentity{PID: child.Process.Pid, StartTimeTicks: start}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if identity.PID == 0 {
		t.Fatal("process fixture did not become observable within its deadline")
	}
	pidfd, err := readbrokerstudy.OpenVerifiedPIDFD(identity)
	if err != nil {
		t.Skip("kernel or host policy does not provide verified pidfds")
	}
	defer unix.Close(pidfd)
	if waitForPIDFDExit(pidfd, 20*time.Millisecond) {
		t.Fatal("live process pidfd was reported exited")
	}
	if processIdentityGone(identity) {
		t.Fatal("live process identity was reported gone")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal("could not stop bounded process-observation fixture")
	}
	_ = child.Wait()
	joined = true
	if !waitForPIDFDExit(pidfd, 2*time.Second) {
		t.Fatal("pidfd did not become readable after process exit")
	}
	if !processIdentityGone(identity) {
		t.Fatal("reaped process identity was still present")
	}
}

// TestStage6ParentDeathCoordinatorHelper runs only in its isolated re-exec
// process. Its pipe carries bounded, private process identities to the test
// supervisor; those identities are never written to the shareable report.
func TestStage6ParentDeathCoordinatorHelper(t *testing.T) {
	if os.Getenv("KOMPOSYN_PARENT_DEATH_HELPER") != "1" {
		return
	}
	if os.Getenv("KOMPOSYN_PARENT_DEATH_STALL") == "1" {
		// Keep descriptor 3 open without sending an event so the supervisor's
		// handshake deadline and process cleanup path can be tested offline.
		time.Sleep(time.Hour)
		return
	}
	pipe := os.NewFile(3, "parent-death-handshake")
	if pipe == nil {
		t.Fatal("private parent-death handshake descriptor is missing")
	}
	defer pipe.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := readbrokerstudy.RunParentDeathStudyCoordinatorForStage6(ctx, os.Getenv("KOMPOSYN_PARENT_DEATH_BWRAP"), os.Getenv("KOMPOSYN_PARENT_DEATH_TARGET"), pipe, stage6RequestedBindMode()); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("fixed parent-death worker coordinator failed: %v", err)
	}
}

func TestStage6ParentDeathCleanup(t *testing.T) {
	stage6Begin("parent_death_cleanup")
	if os.Getenv("KOMPOSYN_STAGE6_WORKER_IMAGE") != "" {
		stage6Skip("parent_death_cleanup", "cmd/harness worker-image mode does not use the test-binary coordinator in its parent-death helper")
		t.Skip("cmd/harness worker-image mode does not claim shipped-image parent-death behavior")
	}
	backendPath := os.Getenv("KOMPOSYN_STAGE6_BWRAP")
	if backendPath == "" {
		backendPath = "/usr/bin/bwrap"
	}
	inspection := readbrokerstudy.InspectBubblewrapForStage6(backendPath, stage6RequestedBindMode())
	stage6Mu.Lock()
	stage6.Backend = map[string]any{
		"path": backendPath, "uid": inspection.UID, "mode": inspection.Mode,
		"trusted": inspection.Trusted, "sha256": inspection.SHA256,
		"version": inspection.Version, "bind_mode": inspection.BindMode,
		"features": append([]string(nil), inspection.Capabilities...), "reason": inspection.Error,
	}
	stage6Mu.Unlock()
	if !inspection.Trusted || inspection.Error != "" {
		stage6Skip("parent_death_cleanup", "trusted backend validation refused before backend probes or worker launch")
		t.Skip("trusted native Bubblewrap backend unavailable; no probe or worker launch was attempted")
	}
	selfStart, err := readbrokerstudy.ReadHostProcessStartTime(os.Getpid())
	if err != nil || selfStart == 0 {
		stage6Skip("parent_death_cleanup", "host process identity observation is unavailable before worker launch")
		t.Skip("host process identity observation is unavailable")
	}
	probePIDFD, err := readbrokerstudy.OpenVerifiedPIDFD(readbrokerstudy.HostProcessIdentity{PID: os.Getpid(), StartTimeTicks: selfStart})
	if err != nil {
		stage6Skip("parent_death_cleanup", "verified pidfd observation is unavailable before worker launch")
		t.Skip("verified pidfd observation is unavailable")
	}
	_ = unix.Close(probePIDFD)
	stage6Update("parent_death_cleanup", func(item *stage6Case) {
		item.LaunchPolicy = map[string]any{
			"effective_bubblewrap_arguments": readbrokerstudy.EffectiveBubblewrapArgumentsWithCapabilityHold(inspection.BindMode),
			"required_namespaces":            []string{"mount", "user", "pid", "ipc", "uts", "network"},
			"network":                        "unshared; no network access",
			"root_mount":                     "private 8 MiB tmpfs",
			"worker_mount":                   stage6WorkerMountPolicy(),
			"worker_environment":             []string{"KOMPOSYN_READBROKER_SENTINEL=synthetic-only"},
			"inherited_descriptors":          []string{"fd 3: seccomp filter", "fd 4: local protocol socket", "fd 5: consumed worker source", "fd 6: retained worker verification image"},
			"target_capability":              "one synthetic read-only file descriptor transferred after worker readiness and held without read release",
			"authorization_scope":            "profile-only lifecycle fixture; does not exercise runtime grants or approval",
			"termination":                    "test coordinator is killed after startup, probes, capability-held acknowledgement and verified pidfd observation",
			"aggregate_cgroup_limits":        "not provisioned or claimed",
		}
	})

	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal("could not make synthetic parent-death root private")
	}
	target := filepath.Join(root, "synthetic-parent-death.fixture")
	if err := os.WriteFile(target, []byte("synthetic held-read fixture\n"), 0o600); err != nil {
		t.Fatal("could not create synthetic parent-death fixture")
	}

	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal("could not create private parent-death handshake")
	}
	defer readPipe.Close()
	if err := unix.SetNonblock(int(readPipe.Fd()), true); err != nil {
		_ = writePipe.Close()
		t.Fatal("could not make private handshake reader nonblocking")
	}
	helper := exec.Command(os.Args[0], "-test.run=^TestStage6ParentDeathCoordinatorHelper$")
	helper.ExtraFiles = []*os.File{writePipe}
	helperOutput := &stage6BoundedBuffer{max: 4096}
	helper.Stdout, helper.Stderr = helperOutput, helperOutput
	helper.Env = []string{
		"KOMPOSYN_PARENT_DEATH_HELPER=1",
		"KOMPOSYN_PARENT_DEATH_BWRAP=" + backendPath,
		"KOMPOSYN_PARENT_DEATH_TARGET=" + target,
		"KOMPOSYN_STAGE6_BIND_MODE=" + string(stage6RequestedBindMode()),
		"TMPDIR=" + os.TempDir(),
	}
	if err := helper.Start(); err != nil {
		_ = writePipe.Close()
		t.Fatal("could not start bounded parent-death coordinator")
	}
	_ = writePipe.Close()
	joined := false
	defer func() {
		if !joined && helper.Process != nil {
			_ = helper.Process.Kill()
			_ = helper.Wait()
			joined = true
			stage6Update("parent_death_cleanup", func(item *stage6Case) { item.CoordinatorJoined = joined })
		}
	}()
	stage6Update("parent_death_cleanup", func(item *stage6Case) { item.CoordinatorStarted = true })

	phases := make(map[string]bool)
	var identities struct {
		Bubblewrap    readbrokerstudy.HostProcessIdentity `json:"bubblewrap"`
		NamespaceInit readbrokerstudy.HostProcessIdentity `json:"namespace_init"`
	}
	handshakeCtx, cancel := context.WithTimeout(context.Background(), 28*time.Second)
	defer cancel()
	var pending []byte
	for {
		event, readErr := readParentDeathEvent(handshakeCtx, int(readPipe.Fd()), &pending)
		if errors.Is(readErr, context.DeadlineExceeded) || errors.Is(readErr, context.Canceled) {
			_ = helper.Process.Kill()
			_ = helper.Wait()
			joined = true
			stage6RecordParentDeathDiagnostic(helper, target, backendPath)
			t.Fatalf("fixed worker did not reach held-capability state before deadline: %v", readErr)
		}
		if readErr != nil {
			// A malformed frame, poll/read error, or unexpected protocol event
			// does not imply that the coordinator exited. Stop it before Wait
			// so this diagnostic failure path remains bounded too.
			_ = stopAndJoinFailedCoordinator(helper)
			joined = true
			stage6RecordParentDeathDiagnostic(helper, target, backendPath)
			t.Fatalf("private parent-death handshake read failed: %v", readErr)
		}
		phases[event.Phase] = true
		if event.Phase == "worker_cleanup" {
			stage6Update("parent_death_cleanup", func(item *stage6Case) { item.WorkerJoined = event.WorkerJoined })
		}
		if event.Phase == "processes_observed" {
			identities.Bubblewrap, identities.NamespaceInit = event.Bubblewrap, event.NamespaceInit
			break
		}
	}
	if !phases["worker_started"] || !phases["worker_ready"] || !phases["capability_held"] || identities.Bubblewrap.PID <= 0 || identities.NamespaceInit.PID <= 0 {
		t.Fatal("fixed worker did not acknowledge startup, held capability and process identities")
	}
	stage6Update("parent_death_cleanup", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady = true, true
		item.CapabilityTransferred, item.CapabilityHeld = true, true
		item.ReadStarted = false
	})
	bwrapFD, err := readbrokerstudy.OpenVerifiedPIDFD(identities.Bubblewrap)
	if err != nil {
		t.Fatalf("observed Bubblewrap identity could not be pinned before parent death: %v", err)
	}
	defer unix.Close(bwrapFD)
	initFD, err := readbrokerstudy.OpenVerifiedPIDFD(identities.NamespaceInit)
	if err != nil {
		t.Fatalf("observed namespace-init identity could not be pinned before parent death: %v", err)
	}
	defer unix.Close(initFD)
	if err := helper.Process.Kill(); err != nil {
		t.Fatalf("could not terminate held-read coordinator: %v", err)
	}
	helpErr := helper.Wait()
	joined = true
	if helpErr == nil {
		t.Fatal("coordinator unexpectedly exited successfully after forced parent death")
	}
	stage6Update("parent_death_cleanup", func(item *stage6Case) { item.CoordinatorJoined = true })
	bwrapExited := waitForPIDFDExit(bwrapFD, 5*time.Second)
	initExited := waitForPIDFDExit(initFD, 5*time.Second)
	bwrapGone := bwrapExited && waitForProcessIdentityGone(identities.Bubblewrap, 5*time.Second)
	initGone := initExited && waitForProcessIdentityGone(identities.NamespaceInit, 5*time.Second)
	processesGone := bwrapGone && initGone
	stage6Update("parent_death_cleanup", func(item *stage6Case) {
		item.BubblewrapExitObserved, item.NamespaceInitExitObserved = bwrapExited, initExited
		item.ObservedProcessesGone, item.CleanupConfirmed = processesGone, processesGone
	})
	if !processesGone {
		t.Fatal("observed Bubblewrap or namespace-init process remained after bounded parent-death cleanup")
	}
	stage6PassWithEvidence("parent_death_cleanup", "native-worker-lifecycle", "killing the coordinator after the fixed worker held its read descriptor, without sending read release, caused the observed Bubblewrap process and PID-namespace init to exit and disappear; this is limited to the fixed profile and does not prove arbitrary-descendant cleanup")
}

func stopAndJoinFailedCoordinator(helper *exec.Cmd) error {
	if helper == nil || helper.Process == nil {
		return errors.New("parent-death coordinator process is unavailable")
	}
	_ = helper.Process.Kill()
	return helper.Wait()
}

func TestStage6StopAndJoinFailedCoordinator(t *testing.T) {
	const helperEnv = "KOMPOSYN_STAGE6_STOP_COORDINATOR_HELPER"
	if os.Getenv(helperEnv) == "1" {
		ready := os.NewFile(3, "coordinator-ready")
		if ready == nil {
			os.Exit(2)
		}
		if _, err := ready.Write([]byte{1}); err != nil {
			os.Exit(3)
		}
		select {}
	}

	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal("could not create coordinator fixture handshake")
	}
	defer readPipe.Close()
	helper := exec.Command(os.Args[0], "-test.run=^TestStage6StopAndJoinFailedCoordinator$")
	helper.ExtraFiles = []*os.File{writePipe}
	helper.Stdout, helper.Stderr = io.Discard, io.Discard
	helper.Env = append(os.Environ(), helperEnv+"=1")
	if err := helper.Start(); err != nil {
		_ = writePipe.Close()
		t.Fatal("could not start coordinator fixture")
	}
	_ = writePipe.Close()
	var ready [1]byte
	if _, err := io.ReadFull(readPipe, ready[:]); err != nil || ready[0] != 1 {
		_ = stopAndJoinFailedCoordinator(helper)
		t.Fatalf("coordinator fixture did not reach its wait state: byte=%v err=%v", ready, err)
	}
	if err := stopAndJoinFailedCoordinator(helper); err == nil || helper.ProcessState == nil || helper.ProcessState.Success() {
		t.Fatalf("failed coordinator was not terminated and reaped: wait=%v state=%v", err, helper.ProcessState)
	}
}

func stage6RecordParentDeathDiagnostic(helper *exec.Cmd, target, backendPath string) {
	var detail string
	if captured, ok := helper.Stderr.(*stage6BoundedBuffer); ok {
		detail = captured.String()
	}
	for _, privatePath := range []string{filepath.Dir(target), target, backendPath, os.Args[0]} {
		if privatePath != "" {
			detail = strings.ReplaceAll(detail, privatePath, "<PRIVATE_PATH>")
		}
	}
	if helper.ProcessState != nil {
		detail = strings.TrimSpace(detail + " helper_exit_code=" + fmt.Sprint(helper.ProcessState.ExitCode()))
	}
	detail = boundedStage6Diagnostic(detail)
	stage6Update("parent_death_cleanup", func(item *stage6Case) { item.FailureDiagnostic = detail })
}

const maxParentDeathEventBytes = 4096

// readParentDeathEvent uses poll on a nonblocking pipe so its context deadline
// can interrupt a coordinator that keeps the pipe open without sending data.
func readParentDeathEvent(ctx context.Context, fd int, pending *[]byte) (readbrokerstudy.ParentDeathStudyEvent, error) {
	for {
		if newline := indexByte(*pending, '\n'); newline >= 0 {
			if newline > maxParentDeathEventBytes {
				return readbrokerstudy.ParentDeathStudyEvent{}, errors.New("parent-death event exceeded its byte limit")
			}
			line := append([]byte(nil), (*pending)[:newline]...)
			*pending = (*pending)[newline+1:]
			var event readbrokerstudy.ParentDeathStudyEvent
			if err := json.Unmarshal(line, &event); err != nil {
				return event, fmt.Errorf("decode bounded parent-death event: %w", err)
			}
			return event, nil
		}
		if len(*pending) > maxParentDeathEventBytes {
			return readbrokerstudy.ParentDeathStudyEvent{}, errors.New("parent-death event exceeded its byte limit")
		}
		if err := ctx.Err(); err != nil {
			return readbrokerstudy.ParentDeathStudyEvent{}, err
		}
		wait := 100 * time.Millisecond
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < wait {
			wait = time.Until(deadline)
		}
		if wait < 0 {
			wait = 0
		}
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN | unix.POLLHUP}}
		_, err := unix.Poll(poll, int(wait/time.Millisecond))
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return readbrokerstudy.ParentDeathStudyEvent{}, fmt.Errorf("poll parent-death handshake: %w", err)
		}
		if poll[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) == 0 {
			continue
		}
		chunk := make([]byte, maxParentDeathEventBytes+1)
		n, readErr := unix.Read(fd, chunk)
		if readErr != nil {
			if errors.Is(readErr, unix.EAGAIN) || errors.Is(readErr, unix.EWOULDBLOCK) || errors.Is(readErr, unix.EINTR) {
				continue
			}
			return readbrokerstudy.ParentDeathStudyEvent{}, fmt.Errorf("read parent-death handshake: %w", readErr)
		}
		if n == 0 {
			return readbrokerstudy.ParentDeathStudyEvent{}, io.EOF
		}
		*pending = append(*pending, chunk[:n]...)
	}
}

func indexByte(data []byte, value byte) int {
	for i, current := range data {
		if current == value {
			return i
		}
	}
	return -1
}

func waitForPIDFDExit(fd int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		ms := int(remaining / time.Millisecond)
		if ms > 100 {
			ms = 100
		}
		_, err := unix.Poll(poll, ms)
		if err == nil && poll[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) != 0 {
			return true
		}
		if err != nil && !errors.Is(err, unix.EINTR) {
			return false
		}
	}
	return false
}

func processIdentityGone(identity readbrokerstudy.HostProcessIdentity) bool {
	startTime, err := readbrokerstudy.ReadHostProcessStartTime(identity.PID)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	return err == nil && startTime != identity.StartTimeTicks
}

func waitForProcessIdentityGone(identity readbrokerstudy.HostProcessIdentity, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if processIdentityGone(identity) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStage6ParentDeathHandshakeDeadlineIsInterruptible(t *testing.T) {
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal("could not create stalled-handshake fixture")
	}
	if err := unix.SetNonblock(int(readPipe.Fd()), true); err != nil {
		t.Fatal("could not make stalled-handshake reader nonblocking")
	}
	defer readPipe.Close()
	helper := exec.Command(os.Args[0], "-test.run=^TestStage6ParentDeathCoordinatorHelper$")
	helper.ExtraFiles = []*os.File{writePipe}
	helper.Stdout, helper.Stderr = io.Discard, io.Discard
	helper.Env = []string{"KOMPOSYN_PARENT_DEATH_HELPER=1", "KOMPOSYN_PARENT_DEATH_STALL=1"}
	if err := helper.Start(); err != nil {
		_ = writePipe.Close()
		t.Fatal("could not start stalled-handshake fixture")
	}
	_ = writePipe.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	var pending []byte
	_, err = readParentDeathEvent(ctx, int(readPipe.Fd()), &pending)
	if !errors.Is(err, context.DeadlineExceeded) {
		_ = helper.Process.Kill()
		_ = helper.Wait()
		t.Fatalf("stalled coordinator error=%v, want deadline", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		_ = helper.Process.Kill()
		_ = helper.Wait()
		t.Fatalf("handshake deadline took %s, want under one second", elapsed)
	}
	if err := helper.Process.Kill(); err != nil {
		t.Fatalf("could not terminate stalled coordinator: %v", err)
	}
	if err := helper.Wait(); err == nil {
		t.Fatal("stalled coordinator unexpectedly exited successfully")
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cleanupCancel()
	var pendingDrain []byte
	if _, err := readParentDeathEvent(cleanupCtx, int(readPipe.Fd()), &pendingDrain); !errors.Is(err, io.EOF) {
		t.Fatalf("bounded stalled-handshake cleanup returned %v, want EOF", err)
	}
}

func TestStage6DelayedOrphanReapingControl(t *testing.T) {
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		t.Skipf("child-subreaper control unavailable: %v", err)
	}
	defer func() {
		if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 0, 0, 0, 0); err != nil {
			t.Errorf("restore child-subreaper setting: %v", err)
		}
	}()
	helper := exec.Command(os.Args[0], "-test.run=^TestStage6DelayedReapFixtureHelper$")
	helper.Env = []string{"KOMPOSYN_DELAYED_REAP_PARENT=1"}
	output, err := helper.Output()
	if err != nil {
		t.Fatalf("start orphan-reaping control: %v", err)
	}
	var pid int
	if _, err := fmt.Sscanf(string(output), "%d", &pid); err != nil || pid <= 0 {
		t.Fatalf("orphan-reaping control returned invalid pid %q: %v", output, err)
	}
	identityDeadline := time.Now().Add(2 * time.Second)
	var identity readbrokerstudy.HostProcessIdentity
	for time.Now().Before(identityDeadline) {
		start, statErr := readbrokerstudy.ReadHostProcessStartTime(pid)
		if statErr == nil && start != 0 {
			identity = readbrokerstudy.HostProcessIdentity{PID: pid, StartTimeTicks: start}
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if identity.PID == 0 {
		t.Fatal("orphaned child identity was not observable")
	}
	reaped := false
	defer func() {
		if reaped {
			return
		}
		_ = unix.Kill(pid, unix.SIGKILL)
		var status unix.WaitStatus
		_, _ = unix.Wait4(pid, &status, 0, nil)
	}()
	pidfd, err := readbrokerstudy.OpenVerifiedPIDFD(identity)
	if err != nil {
		t.Fatalf("open orphaned child pidfd: %v", err)
	}
	defer unix.Close(pidfd)
	if !waitForPIDFDExit(pidfd, 2*time.Second) {
		t.Fatal("orphaned child did not exit")
	}
	if processIdentityGone(identity) {
		t.Fatal("orphaned child disappeared before the owning subreaper called wait4")
	}
	var status unix.WaitStatus
	if _, err := unix.Wait4(pid, &status, 0, nil); err != nil {
		t.Fatalf("subreaper could not reap delayed orphan: %v", err)
	}
	reaped = true
	if !waitForProcessIdentityGone(identity, time.Second) {
		t.Fatal("process identity remained after explicit orphan reaping")
	}
}

func TestStage6DelayedReapFixtureHelper(t *testing.T) {
	if os.Getenv("KOMPOSYN_DELAYED_REAP_CHILD") == "1" {
		time.Sleep(150 * time.Millisecond)
		return
	}
	if os.Getenv("KOMPOSYN_DELAYED_REAP_PARENT") != "1" {
		return
	}
	child := exec.Command(os.Args[0], "-test.run=^TestStage6DelayedReapFixtureHelper$")
	child.Env = []string{"KOMPOSYN_DELAYED_REAP_CHILD=1"}
	child.Stdout, child.Stderr = io.Discard, io.Discard
	if err := child.Start(); err != nil {
		t.Fatal("start orphan fixture child")
	}
	if _, err := fmt.Fprintln(os.Stdout, child.Process.Pid); err != nil {
		t.Fatal("publish orphan fixture identity")
	}
	return
}

func TestStage6ActorHelperControlUsesSeparateProcess(t *testing.T) {
	stage6Begin("actor_helper_control")
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("make synthetic actor root private: %v", err)
	}
	source := filepath.Join(root, "before.fixture")
	target := filepath.Join(root, "after.fixture")
	want := "synthetic actor control\n"
	if err := os.WriteFile(source, []byte(want), 0o600); err != nil {
		t.Fatalf("write synthetic actor source: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	actor, err := readbrokerstudy.NewNativeActor(ctx, os.Args[0], root, readbrokerstudy.NativeActorRename, "before.fixture", "after.fixture")
	if err != nil {
		t.Fatalf("start synthetic actor helper: %v", err)
	}
	t.Cleanup(func() {
		if err := actor.Close(); err != nil {
			t.Errorf("ensure synthetic actor helper cleanup: %v", err)
		}
	})
	if err := actor.Run(ctx); err != nil {
		t.Fatalf("run synthetic actor helper: %v", err)
	}
	if err := actor.Close(); err != nil {
		t.Fatalf("close synthetic actor helper: %v", err)
	}
	if !actor.Ready() || !actor.TransitionCompleted() || !actor.Joined() {
		t.Fatalf("actor lifecycle ready=%t transition=%t joined=%t", actor.Ready(), actor.TransitionCompleted(), actor.Joined())
	}
	if _, err := os.Lstat(source); !os.IsNotExist(err) {
		t.Fatalf("synthetic actor left the original name: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != want {
		t.Fatalf("synthetic actor target=%q err=%v, want fixture bytes", data, err)
	}
	stage6Update("actor_helper_control", func(item *stage6Case) {
		item.EvidenceKind = "synthetic-actor-fixture"
		item.ActorStarted, item.ActorTransitionCompleted, item.ActorJoined = actor.Ready(), actor.TransitionCompleted(), actor.Joined()
		item.CleanupConfirmed = actor.Joined()
	})
	stage6PassWithEvidence("actor_helper_control", "synthetic-actor-fixture", "the isolated fixed actor re-exec mode performed one bounded fixture rename and was joined; this does not exercise Bubblewrap or enforce filesystem authority")
}

func stage6Update(id string, update func(*stage6Case)) {
	stage6Mu.Lock()
	defer stage6Mu.Unlock()
	for index := range stage6.Cases {
		if stage6.Cases[index].ID == id {
			update(&stage6.Cases[index])
			return
		}
	}
}

func stage6Begin(id string) {
	stage6Update(id, func(item *stage6Case) {
		item.Status = "inconclusive"
		item.Reason = "case started; assertions not yet complete"
	})
}

func stage6Pass(id string, reason string) {
	stage6PassWithEvidence(id, "native-integrated", reason)
}

func stage6PassSynthetic(id string, reason string) {
	stage6PassWithEvidence(id, "synthetic-runtime-composition", reason)
}

func stage6PassWithEvidence(id, evidenceKind, reason string) {
	stage6Update(id, func(item *stage6Case) {
		item.Status, item.EvidenceKind, item.Reason = "measured_pass", evidenceKind, reason
	})
}

func stage6Skip(id, reason string) {
	stage6Update(id, func(item *stage6Case) { item.Status, item.Reason = "unavailable", reason })
}

type stage6Provider struct {
	mu              sync.Mutex
	requests        []provider.ChatRequest
	target          string
	blockSecond     bool
	firstToolCall   bool
	multipleReads   bool
	secondTarget    string
	lateDelta       string
	lateResponses   atomic.Int32
	lateDeltas      atomic.Int32
	secondStarted   chan struct{}
	releaseSecond   chan struct{}
	secondStartOnce sync.Once
}

func (p *stage6Provider) ID() provider.ID { return provider.OpenAI }
func (p *stage6Provider) AuthMethods() []provider.AuthMethod {
	return []provider.AuthMethod{provider.AuthAPIKey}
}
func (*stage6Provider) RouteCapabilities(method provider.AuthMethod) provider.RouteCapabilities {
	if method == provider.AuthAPIKey {
		return provider.RouteCapabilities{ClientTools: provider.SupportSupported, Images: provider.SupportUnsupported}
	}
	return provider.RouteCapabilities{ClientTools: provider.SupportUnsupported, Images: provider.SupportUnsupported}
}
func (p *stage6Provider) Authenticate(_ context.Context, credential provider.Credential) (provider.AuthResult, error) {
	if credential.APIKey == "" {
		return provider.AuthResult{}, errors.New("synthetic provider credential missing")
	}
	return provider.AuthResult{Provider: provider.OpenAI}, nil
}
func (p *stage6Provider) ListModels(context.Context, provider.Credential) ([]provider.Model, error) {
	return []provider.Model{{ID: "stage6-fixture-model", DisplayName: "Stage 6 fixture", Compatibility: provider.CompatibilitySupported}}, nil
}
func (p *stage6Provider) StreamChat(ctx context.Context, request provider.ChatRequest, delta func(string)) (provider.Result, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	call := len(p.requests)
	p.mu.Unlock()
	if call == 1 && !p.firstToolCall {
		return provider.Result{RequestID: request.RequestID, ModelID: request.ModelID, Outcome: provider.OutcomeCompleted,
			Completion: provider.CompletionFinal, Text: "Synthetic fixture answer.",
			Native: []json.RawMessage{json.RawMessage(`{"type":"message","content":"Synthetic fixture answer."}`)}}, nil
	}
	if call == 1 {
		paths := []struct{ callID, itemID, path string }{{"stage6-read", "stage6-item-read", p.target}}
		if p.multipleReads {
			paths = append(paths, struct{ callID, itemID, path string }{"stage6-read-b", "stage6-item-read-b", p.secondTarget})
		}
		calls := make([]provider.ToolCall, 0, len(paths))
		nativeCalls := make([]json.RawMessage, 0, len(paths))
		for _, item := range paths {
			arguments, _ := json.Marshal(map[string]string{"path": item.path})
			nativeArguments, _ := json.Marshal(string(arguments))
			nativeCalls = append(nativeCalls, json.RawMessage(`{"type":"function_call","id":"`+item.itemID+`","call_id":"`+item.callID+`","name":"read_file","arguments":`+string(nativeArguments)+`,"status":"completed"}`))
			calls = append(calls, provider.ToolCall{CallID: item.callID, ToolID: tools.ID("read_file"), Arguments: arguments})
		}
		return provider.Result{
			RequestID: request.RequestID, ModelID: request.ModelID, Outcome: provider.OutcomeCompleted,
			Completion: provider.CompletionToolCalls, Text: "Reading the exact fixture.", Native: nativeCalls, ToolCalls: calls,
		}, nil
	}
	if call == 2 && p.blockSecond {
		p.secondStartOnce.Do(func() { close(p.secondStarted) })
		select {
		case <-p.releaseSecond:
		case <-ctx.Done():
			return provider.Result{}, ctx.Err()
		}
	}
	if call == 2 && p.lateDelta != "" && delta != nil {
		p.lateDeltas.Add(1)
		delta(p.lateDelta)
	}
	if call == 2 {
		p.lateResponses.Add(1)
	}
	return provider.Result{RequestID: request.RequestID, ModelID: request.ModelID, Outcome: provider.OutcomeCompleted,
		Completion: provider.CompletionFinal, Text: "Synthetic fixture answer.",
		Native: []json.RawMessage{json.RawMessage(`{"type":"message","content":"Synthetic fixture answer."}`)}}, nil
}
func (p *stage6Provider) captured() []provider.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.ChatRequest(nil), p.requests...)
}

type stage6ReadHost struct {
	service       *Service
	beforeHandoff func() error
}

var _ runtime.FilesystemReadAdmissionHost = (*stage6ReadHost)(nil)
var _ runtime.FilesystemReadAdmissionInvalidator = (*stage6ReadHost)(nil)
var _ runtime.FilesystemReadExecutionHost = (*stage6ReadHost)(nil)
var _ runtime.FilesystemReadExecutionInvalidator = (*stage6ReadHost)(nil)
var _ runtime.FilesystemReadPublicationGate = (*stage6ReadHost)(nil)

func (h *stage6ReadHost) AdmitFilesystemRead(ctx context.Context, invocation tools.Invocation, destination string) (runtime.FilesystemReadAdmission, error) {
	return h.service.AdmitFilesystemRead(ctx, invocation, destination)
}
func (h *stage6ReadHost) PrepareAdmittedFilesystemRead(ctx context.Context, admission runtime.FilesystemReadAdmission, revision uint64) (runtime.FilesystemReadPreparation, error) {
	return h.service.PrepareAdmittedFilesystemRead(ctx, admission, revision)
}
func (h *stage6ReadHost) ExecuteAdmittedFilesystemRead(ctx context.Context, admission runtime.FilesystemReadAdmission, preparation runtime.FilesystemReadPreparation, reference *runtime.ReadExecutionReference, authority runtime.FilesystemReadExecutionAuthority) (*runtime.PrivateReadCapture, error) {
	return h.service.ExecuteAdmittedFilesystemRead(ctx, admission, preparation, reference, authority)
}
func (h *stage6ReadHost) CloseFilesystemReadAdmission(ctx context.Context, admission runtime.FilesystemReadAdmission) error {
	return h.service.CloseFilesystemReadAdmission(ctx, admission)
}
func (h *stage6ReadHost) SetFilesystemReadInvalidator(invalidate func(string)) {
	h.service.SetFilesystemReadInvalidator(invalidate)
}
func (h *stage6ReadHost) InvalidateFilesystemReadExecutions(conversationID string) error {
	return h.service.InvalidateFilesystemReadExecutions(conversationID)
}
func (h *stage6ReadHost) WithFilesystemReadPublicationCurrent(ctx context.Context, conversationID, root string, generation, policyRevision, coverageRevision uint64, operation func() error) error {
	if h.beforeHandoff != nil {
		if err := h.beforeHandoff(); err != nil {
			return err
		}
	}
	return h.service.WithFilesystemReadPublicationCurrent(ctx, conversationID, root, generation, policyRevision, coverageRevision, operation)
}

type stage6ExecutionPreparation struct {
	admission runtime.FilesystemReadAdmission
}

func (p *stage6ExecutionPreparation) Metadata() runtime.FilesystemReadAdmission {
	return p.admission
}
func (*stage6ExecutionPreparation) WithAuthorityCommit(_ context.Context, operation func() error) error {
	return operation()
}
func (*stage6ExecutionPreparation) Close(context.Context) error { return nil }

type stage6ExecutionImplementation struct {
	calls              int
	validForwardedArgs bool
	wantAdmission      runtime.FilesystemReadAdmission
	wantPreparation    runtime.FilesystemReadPreparation
}

var _ implementation = (*stage6ExecutionImplementation)(nil)

func (*stage6ExecutionImplementation) Protect(context.Context, string, string, string, *os.File) error {
	return nil
}
func (*stage6ExecutionImplementation) Close(context.Context) error { return nil }
func (*stage6ExecutionImplementation) CoverageSnapshot() CoverageSnapshot {
	return CoverageSnapshot{}
}
func (*stage6ExecutionImplementation) BackendStatus() (bool, string) {
	return true, "synthetic execution forwarding"
}
func (*stage6ExecutionImplementation) PrepareGenericRead(context.Context, string, string) error {
	return nil
}

func (i *stage6ExecutionImplementation) ExecuteAdmittedFilesystemRead(_ context.Context, admission runtime.FilesystemReadAdmission, preparation runtime.FilesystemReadPreparation, reference *runtime.ReadExecutionReference, authority runtime.FilesystemReadExecutionAuthority) (*runtime.PrivateReadCapture, error) {
	i.calls++
	i.validForwardedArgs = sameFilesystemReadAdmission(i.wantAdmission, admission) && preparation == i.wantPreparation && reference != nil && authority != nil
	return nil, nil
}

type stage6ExecutionAuthority struct{}

func (stage6ExecutionAuthority) Claim() error                             { return nil }
func (stage6ExecutionAuthority) WithCurrent(operation func() error) error { return operation() }
func (stage6ExecutionAuthority) AuthorizationBinding() string             { return "synthetic-binding" }
func (stage6ExecutionAuthority) Stage(runtime.FilesystemReadAdmission, runtime.FilesystemReadExecutionResult) (*runtime.PrivateReadCapture, error) {
	return nil, nil
}

func TestStage6ExecutionForwarderReachesService(t *testing.T) {
	impl := &stage6ExecutionImplementation{}
	service := &Service{impl: impl}
	admission := runtime.FilesystemReadAdmission{
		Reference: &struct{}{}, ServiceID: "synthetic-service", ConversationID: "synthetic-conversation",
		TurnID: "synthetic-turn", CallID: "synthetic-call", Root: "/synthetic-root", Target: "/synthetic-root/fixture",
		AdmissionBinding: "synthetic-admission", Identity: "synthetic-object", Destination: "synthetic-destination",
	}
	inner := &stage6ExecutionPreparation{admission: admission}
	wrapped := &serviceAdmissionPreparation{service: service, inner: inner, expected: admission}
	impl.wantAdmission, impl.wantPreparation = admission, inner
	host := &stage6ReadHost{service: service}
	if _, err := host.ExecuteAdmittedFilesystemRead(context.Background(), admission, wrapped, &runtime.ReadExecutionReference{}, stage6ExecutionAuthority{}); err != nil {
		t.Fatalf("synthetic execution forwarding failed: %v", err)
	}
	if impl.calls != 1 {
		t.Fatalf("service executions=%d, want exactly one", impl.calls)
	}
	if !impl.validForwardedArgs {
		t.Fatal("service execution did not receive the exact prepared admission, reference, and authority")
	}
}

type stage6SyntheticHostImplementation struct {
	executions  int
	invalidator func(string)
}

var _ implementation = (*stage6SyntheticHostImplementation)(nil)
var _ readAdmissionImplementation = (*stage6SyntheticHostImplementation)(nil)

func (*stage6SyntheticHostImplementation) Protect(context.Context, string, string, string, *os.File) error {
	return nil
}
func (*stage6SyntheticHostImplementation) Close(context.Context) error { return nil }
func (*stage6SyntheticHostImplementation) CoverageSnapshot() CoverageSnapshot {
	return CoverageSnapshot{Revision: 1, Composition: "stage6-synthetic", Capability: "synthetic-read", Owners: []OwnerCoverage{
		{Owner: "auth", State: OwnerRegistered}, {Owner: "settings", State: OwnerRegistered},
	}}
}
func (*stage6SyntheticHostImplementation) BackendStatus() (bool, string) {
	return true, "synthetic execution test only"
}
func (*stage6SyntheticHostImplementation) PrepareGenericRead(context.Context, string, string) error {
	return nil
}
func (i *stage6SyntheticHostImplementation) AdmitFilesystemRead(_ context.Context, invocation tools.Invocation, destination, root string, rootGeneration, coverageRevision uint64) (runtime.FilesystemReadAdmission, error) {
	return runtime.FilesystemReadAdmission{
		Reference: &struct{}{}, ServiceID: "stage6-synthetic-service", ConversationID: invocation.ConversationID,
		TurnID: invocation.TurnID, CallID: invocation.ProviderCallID, Root: root,
		Deadline: time.Now().Add(30 * time.Second), AdmissionBinding: "stage6-synthetic-" + invocation.ProviderCallID,
		Target: filepath.Join(root, "synthetic.txt"), Class: "ordinary", Identity: "stage6-synthetic-object",
		RootGeneration: rootGeneration, PolicyRevision: 1, CoverageRevision: coverageRevision, Destination: destination,
	}, nil
}
func (*stage6SyntheticHostImplementation) PrepareAdmittedFilesystemRead(_ context.Context, admission runtime.FilesystemReadAdmission, _ uint64) (runtime.FilesystemReadPreparation, error) {
	return &stage6ExecutionPreparation{admission: admission}, nil
}
func (*stage6SyntheticHostImplementation) CloseFilesystemReadAdmission(context.Context, runtime.FilesystemReadAdmission) error {
	return nil
}
func (*stage6SyntheticHostImplementation) SelectFilesystemRoot(context.Context, runtime.FilesystemRootTransition) error {
	return nil
}
func (*stage6SyntheticHostImplementation) InvalidateFilesystemAdmissions(context.Context) error {
	return nil
}
func (i *stage6SyntheticHostImplementation) SetFilesystemReadInvalidator(invalidate func(string)) {
	i.invalidator = invalidate
}
func (*stage6SyntheticHostImplementation) WithFilesystemReadPublicationCurrent(_ uint64, _ uint64, operation func() error) error {
	return operation()
}
func (*stage6SyntheticHostImplementation) WithFilesystemReadExecutionCurrent(operation func() error) error {
	return operation()
}
func (*stage6SyntheticHostImplementation) InvalidateFilesystemReadExecutions(string) error {
	return nil
}
func (i *stage6SyntheticHostImplementation) ExecuteAdmittedFilesystemRead(_ context.Context, admission runtime.FilesystemReadAdmission, _ runtime.FilesystemReadPreparation, _ *runtime.ReadExecutionReference, authority runtime.FilesystemReadExecutionAuthority) (*runtime.PrivateReadCapture, error) {
	i.executions++
	if err := authority.Claim(); err != nil {
		return nil, err
	}
	var capture *runtime.PrivateReadCapture
	err := authority.WithCurrent(func() error {
		var stageErr error
		capture, stageErr = authority.Stage(admission, runtime.FilesystemReadExecutionResult{
			Outcome: tools.OutcomeSucceeded, Bytes: []byte("stage6-synthetic-forwarded-content"),
			ServiceID: admission.ServiceID, AdmissionBinding: admission.AdmissionBinding,
			AuthorizationBinding: authority.AuthorizationBinding(), Identity: admission.Identity,
			RootGeneration: admission.RootGeneration, PolicyRevision: admission.PolicyRevision,
			CoverageRevision: admission.CoverageRevision, Destination: admission.Destination,
			ReadStarted: true, WorkerJoined: true, CapabilitiesRetired: true,
		})
		return stageErr
	})
	return capture, err
}

func TestStage6SyntheticRuntimeReadForwarding(t *testing.T) {
	stage6Begin("synthetic_once_authority_flow")
	root := t.TempDir()
	impl := &stage6SyntheticHostImplementation{}
	service := &Service{impl: impl}
	base := &stage6Provider{target: filepath.Join(root, "synthetic.txt"), firstToolCall: true, secondStarted: make(chan struct{}), releaseSecond: make(chan struct{})}
	providers, err := model.NewRegistry(base)
	if err != nil {
		t.Fatal(err)
	}
	authStore := auth.Store{Path: filepath.Join(root, "synthetic-auth.json"), Protection: service}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var approvals atomic.Int32
	if err := authStore.RegisterProtection(ctx); err != nil {
		t.Fatalf("synthetic auth owner registration failed: %v", err)
	}
	chat := runtime.NewChatWithToolRuntime(providers, authStore, nil, runtime.ToolRuntimeConfig{
		Registry: tools.NewRegistry(), Preparer: service, ReadAdmission: &stage6ReadHost{service: service}, RootHost: service,
		Approval: runtime.ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			approvals.Add(1)
			return permissions.ApprovalOnce, "", nil
		}), Grants: permissions.NewMemoryStore(),
	})
	if err := chat.AddProfile(ctx, "stage6-synthetic-profile", "openai", "synthetic profile", "synthetic-only", nil); err != nil {
		t.Fatalf("synthetic profile setup failed: %v", err)
	}
	discovery := chat.Discover(ctx)
	if len(discovery.Models) != 1 {
		t.Fatalf("synthetic provider discovery returned %d models", len(discovery.Models))
	}
	if err := chat.Select(ctx, discovery.Models[0].Key(), false); err != nil {
		t.Fatal(err)
	}
	if err := chat.SelectFilesystemRoot(ctx, root); err != nil {
		t.Fatal(err)
	}
	if err := runtime.EnableNativeReadStudy(chat); err != nil {
		t.Fatal(err)
	}
	grantRevisionBefore, grantsBefore := runtime.NativeReadStudyGrantState(chat)
	if grantsBefore != 0 {
		t.Fatalf("synthetic read had %d preexisting grants, want none", grantsBefore)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if err := chat.CloseContext(cleanupCtx); err != nil {
			t.Errorf("synthetic chat cleanup failed: %v", err)
		}
		if err := service.Close(cleanupCtx); err != nil {
			t.Errorf("synthetic service cleanup failed: %v", err)
		}
	})
	turn, err := chat.Send(ctx, "read synthetic fixture", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("synthetic read continuation failed: outcome=%s err=%v", turn.Outcome, err)
	}
	requests := base.captured()
	if len(requests) != 2 || impl.executions != 1 || approvals.Load() != 1 {
		t.Fatalf("synthetic read requests=%d executions=%d approvals=%d, want 2, 1 and 1", len(requests), impl.executions, approvals.Load())
	}
	first, _ := json.Marshal(requests[0].Messages)
	if strings.Contains(string(first), "stage6-synthetic-forwarded-content") {
		t.Fatal("synthetic read bytes appeared before execution and provider continuation")
	}
	content, found := readResultContent(requests[1], "stage6-read")
	if !found || content != `{"outcome":"succeeded","truncated":false,"content":"stage6-synthetic-forwarded-content"}` {
		t.Fatalf("synthetic forwarded result=%q found=%t", content, found)
	}
	grantRevisionAfter, grantsAfter := runtime.NativeReadStudyGrantState(chat)
	if grantRevisionAfter != grantRevisionBefore+2 || grantsAfter != 0 {
		t.Fatalf("synthetic once-grant transition revision=%d→%d remaining=%d, want two transitions and no reusable grant", grantRevisionBefore, grantRevisionAfter, grantsAfter)
	}
	stage6Update("synthetic_once_authority_flow", func(item *stage6Case) {
		item.EvidenceKind, item.OnceApprovalCount, item.OnceGrantConsumptionCount = "synthetic-runtime-composition", approvals.Load(), 1
		item.GrantRevisionBefore, item.GrantRevisionAfter, item.RemainingGrantCount = grantRevisionBefore, grantRevisionAfter, grantsAfter
		item.ProviderRequestCount, item.WorkerJoined, item.FixtureBytesMatched = len(requests), true, true
	})
	stage6PassSynthetic("synthetic_once_authority_flow", "runtime performed one explicit once approval, advanced the grant revision for approval and consumption, executed once, and retained no reusable grant")
}

type stage6Fixture struct {
	service             *Service
	chat                *runtime.Chat
	provider            *stage6Provider
	authStore           auth.Store
	root                string
	authPath            string
	content             string
	approvals           atomic.Int32
	approvalWaitAt      atomic.Int32
	approvalEntered     chan struct{}
	approvalRelease     chan struct{}
	approvalReleaseOnce sync.Once
	approvalWaitOnce    sync.Once
	secondContent       string
	reportCases         []string
}

func newStage6HostService(backendPath string, holdWorker bool) (*Service, error) {
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		return nil, err
	}
	workerPath, err := os.Executable()
	if err != nil {
		_ = registry.Close()
		return nil, err
	}
	var launcher readbrokerstudy.WorkerLauncher
	if imagePath := os.Getenv("KOMPOSYN_STAGE6_WORKER_IMAGE"); imagePath != "" {
		if stage6RequestedBindMode() != readbrokerstudy.BindModeFileSnapshot {
			_ = registry.Close()
			return nil, errors.New("cmd/harness worker-image profile requires file-snapshot mode")
		}
		launcher, err = readbrokerstudy.NewWorkerLauncherForStage6Image(backendPath, imagePath, os.Getenv("KOMPOSYN_STAGE6_WORKER_IMAGE_SHA256"), holdWorker)
	} else {
		launcher, err = readbrokerstudy.NewWorkerLauncherForStage6(backendPath, workerPath, stage6RequestedBindMode(), holdWorker)
	}
	if err != nil {
		_ = registry.Close()
		return nil, err
	}
	impl, err := composeLinuxImplementation(registry, launcher, nil)
	if err != nil {
		return nil, err
	}
	return &Service{impl: impl}, nil
}

func stage6WorkerMountPolicy() string {
	if os.Getenv("KOMPOSYN_STAGE6_WORKER_IMAGE_SHA256") != "" {
		return "one exact-digest, sealed snapshot of the source-built cmd/harness executable, read-only"
	}
	return "one fixed Stage 6 test binary, read-only"
}

func newStage6Fixture(t *testing.T, caseID, targetKind string, blockSecond bool, beforeHandoff func() error) *stage6Fixture {
	return newStage6FixtureWithHold(t, caseID, targetKind, blockSecond, beforeHandoff, false)
}

func newStage6FixtureWithHold(t *testing.T, caseID, targetKind string, blockSecond bool, beforeHandoff func() error, holdWorker bool) *stage6Fixture {
	t.Helper()
	backendPath := os.Getenv("KOMPOSYN_STAGE6_BWRAP")
	if backendPath == "" {
		backendPath = "/usr/bin/bwrap"
	}
	inspection := readbrokerstudy.InspectBubblewrapForStage6(backendPath, stage6RequestedBindMode())
	stage6Mu.Lock()
	stage6.Backend = map[string]any{
		"path": backendPath, "uid": inspection.UID, "mode": inspection.Mode,
		"trusted": inspection.Trusted, "sha256": inspection.SHA256,
		"version": inspection.Version, "bind_mode": inspection.BindMode,
		"features": append([]string(nil), inspection.Capabilities...), "reason": inspection.Error,
	}
	var effectiveArguments []string
	if inspection.Trusted && inspection.Error == "" {
		effectiveArguments = readbrokerstudy.EffectiveBubblewrapArguments(inspection.BindMode)
	}
	stage6.LaunchPolicy = map[string]any{
		"effective_bubblewrap_arguments": effectiveArguments,
		"required_namespaces":            []string{"mount", "user", "pid", "ipc", "uts", "network"},
		"network":                        "unshared; no network access",
		"root_mount":                     "private 8 MiB tmpfs",
		"worker_mount":                   stage6WorkerMountPolicy(),
		"runtime_mounts":                 []string{},
		"extra_descriptors":              []string{"fd 3: seccomp filter", "fd 4: local protocol socket", "fd 5: worker mount source (consumed by Bubblewrap)", "fd 6: worker verification image (retained through startup)"},
		"worker_environment":             []string{"KOMPOSYN_READBROKER_SENTINEL=synthetic-only"},
		"aggregate_cgroup_limits":        "not provisioned or claimed",
		"process_creation":               "denied by worker seccomp after Go thread bootstrap",
	}
	stage6Mu.Unlock()
	if !inspection.Trusted || inspection.Error != "" {
		for _, id := range []string{"positive_exact_read_and_replay", "empty_file_read", "protected_auth_refusal", "protected_settings_refusal", "independent_namespace_actor", "pre_handoff_owner_invalidation", "post_handoff_owner_invalidation", "cancellation_and_cleanup", "launch_profile_and_limits", "late_response_and_delta_retirement", "expected_once_transitions"} {
			stage6Skip(id, "trusted backend validation refused before backend probes or worker launch")
		}
		t.Skip("trusted native Bubblewrap backend unavailable; no probe or worker launch was attempted")
	}
	service, err := newStage6HostService(backendPath, holdWorker)
	if err != nil {
		stage6Skip(caseID, "trusted backend passed inspection but host service composition was unavailable")
		t.Skip("host sandbox service could not be composed")
	}
	available, detail := service.BackendStatus()
	if !available {
		_ = service.Close(context.Background())
		stage6Mu.Lock()
		stage6.Backend["service_status"] = detail
		stage6Mu.Unlock()
		stage6Skip(caseID, "trusted backend passed inspection but the required native launch profile was unavailable")
		t.Skip("trusted backend did not satisfy the native launch profile")
	}
	root := t.TempDir()
	authPath := filepath.Join(root, "synthetic-auth.json")
	settingsPath := filepath.Join(root, "synthetic-settings.json")
	authStore := auth.Store{Path: authPath, Protection: service}
	settingsStore := settings.Store{Path: settingsPath, Protection: service}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := authStore.RegisterProtection(ctx); err != nil {
		_ = service.Close(context.Background())
		t.Fatalf("synthetic auth owner registration failed: %v", err)
	}
	if err := settingsStore.RegisterProtection(ctx); err != nil {
		_ = service.Close(context.Background())
		t.Fatalf("synthetic settings owner registration failed: %v", err)
	}
	if err := settingsStore.SaveContext(ctx, settings.Settings{Plugin: settings.DefaultUI}); err != nil {
		_ = service.Close(context.Background())
		t.Fatalf("synthetic settings setup failed: %v", err)
	}
	content := "stage6-synthetic-private-content"
	secondContent := "stage6-second-synthetic-private-content"
	if targetKind == "empty" {
		content = ""
	}
	target := filepath.Join(root, "notes.txt")
	if targetKind == "auth" {
		target, _ = authStore.EffectivePath()
		content = "synthetic-never-real-key"
	} else if targetKind == "settings" {
		target, _ = settingsStore.EffectivePath()
		content = "synthetic-settings-marker"
	} else if err := os.WriteFile(target, []byte(content), 0600); err != nil {
		_ = service.Close(context.Background())
		t.Fatalf("synthetic target setup failed: %v", err)
	}
	secondTarget := filepath.Join(root, "second-notes.txt")
	if targetKind == "multiple" {
		if err := os.WriteFile(secondTarget, []byte(secondContent), 0600); err != nil {
			_ = service.Close(context.Background())
			t.Fatalf("second synthetic target setup failed: %v", err)
		}
	}
	base := &stage6Provider{target: target, secondTarget: secondTarget, multipleReads: targetKind == "multiple", blockSecond: blockSecond, firstToolCall: true, secondStarted: make(chan struct{}), releaseSecond: make(chan struct{})}
	providers, err := model.NewRegistry(base)
	if err != nil {
		_ = service.Close(context.Background())
		t.Fatalf("fixture provider registry failed: %v", err)
	}
	fixture := &stage6Fixture{service: service, provider: base, authStore: authStore, root: root, authPath: authPath, content: content, secondContent: secondContent, approvalEntered: make(chan struct{}), approvalRelease: make(chan struct{})}
	config := runtime.ToolRuntimeConfig{
		Registry: tools.NewRegistry(), Preparer: service, ReadAdmission: &stage6ReadHost{service: service, beforeHandoff: beforeHandoff}, RootHost: service,
		Approval: runtime.ToolApprovalFunc(func(ctx context.Context, _ permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			if err := ctx.Err(); err != nil {
				return "", "", err
			}
			count := fixture.approvals.Add(1)
			if fixture.approvalWaitAt.Load() == count {
				fixture.approvalWaitOnce.Do(func() { close(fixture.approvalEntered) })
				select {
				case <-fixture.approvalRelease:
				case <-ctx.Done():
					return "", "", ctx.Err()
				}
			}
			return permissions.ApprovalOnce, "", nil
		}), Grants: permissions.NewMemoryStore(),
	}
	fixture.chat = runtime.NewChatWithToolRuntime(providers, authStore, nil, config)
	if err := fixture.chat.AddProfile(ctx, "stage6-profile", "openai", "synthetic Stage 6 profile", "synthetic-never-real-key", nil); err != nil {
		_ = fixture.chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatalf("synthetic chat profile setup failed: %v", err)
	}
	discovery := fixture.chat.Discover(ctx)
	if len(discovery.Models) != 1 {
		_ = fixture.chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatalf("synthetic model discovery failed: model count=%d failures=%d", len(discovery.Models), len(discovery.Failures))
	}
	if err := fixture.chat.Select(ctx, discovery.Models[0].Key(), false); err != nil {
		_ = fixture.chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatalf("synthetic model selection failed: %v", err)
	}
	if err := fixture.chat.SelectFilesystemRoot(ctx, root); err != nil {
		_ = fixture.chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatalf("synthetic filesystem root setup failed: %v", err)
	}
	if err := runtime.EnableNativeReadStudy(fixture.chat); err != nil {
		_ = fixture.chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatalf("test-only read path setup failed: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 12*time.Second)
		defer stop()
		clean := true
		if err := fixture.chat.CloseContext(cleanupCtx); err != nil {
			t.Errorf("chat cleanup was not confirmed: %v", err)
			clean = false
		}
		if err := service.Close(cleanupCtx); err != nil {
			t.Errorf("sandbox service cleanup was not confirmed: %v", err)
			clean = false
		}
		if clean {
			for _, id := range fixture.reportCases {
				stage6Update(id, func(item *stage6Case) { item.CleanupConfirmed = true })
			}
		}
	})
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		if impl, ok := fixture.service.impl.(*linuxImplementation); ok {
			stage6CaptureReadFailure(caseID, impl.session)
		}
	})
	return fixture
}

func TestStage6OrdinaryChatWithoutRoot(t *testing.T) {
	stage6Begin("ordinary_chat_without_root")
	root := t.TempDir()
	base := &stage6Provider{firstToolCall: false, secondStarted: make(chan struct{}), releaseSecond: make(chan struct{})}
	providers, err := model.NewRegistry(base)
	if err != nil {
		t.Fatalf("fixture provider registry failed: %v", err)
	}
	backendPath := filepath.Join(root, "missing-bubblewrap")
	service, err := NewHostService(backendPath)
	if err != nil {
		t.Fatalf("unavailable backend service should preserve text chat: %v", err)
	}
	if available, _ := service.BackendStatus(); available {
		t.Fatal("missing synthetic backend was reported available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := auth.Store{Path: filepath.Join(root, "synthetic-auth.json"), Protection: service}
	settingsStore := settings.Store{Path: filepath.Join(root, "synthetic-settings.json"), Protection: service}
	if err := store.RegisterProtection(ctx); err != nil {
		_ = service.Close(context.Background())
		t.Fatalf("auth owner registration failed with unavailable backend: %v", err)
	}
	if err := settingsStore.RegisterProtection(ctx); err != nil {
		_ = service.Close(context.Background())
		t.Fatalf("settings owner registration failed with unavailable backend: %v", err)
	}
	if _, err := settingsStore.Load(); err != nil {
		_ = service.Close(context.Background())
		t.Fatalf("settings loading failed with unavailable backend: %v", err)
	}
	config := runtime.ToolRuntimeConfig{
		Registry: tools.NewRegistry(), Preparer: service, ReadAdmission: service, RootHost: service,
	}
	chat := runtime.NewChatWithToolRuntime(providers, store, nil, config)
	if err := chat.AddProfile(ctx, "ordinary-profile", "openai", "synthetic profile", "synthetic-never-real-key", nil); err != nil {
		_ = chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatalf("synthetic profile setup failed: %v", err)
	}
	discovery := chat.Discover(ctx)
	if len(discovery.Models) != 1 {
		t.Fatalf("synthetic model discovery failed: models=%d failures=%d", len(discovery.Models), len(discovery.Failures))
	}
	if err := chat.Select(ctx, discovery.Models[0].Key(), false); err != nil {
		t.Fatalf("synthetic model selection failed: %v", err)
	}
	turn, err := chat.Send(ctx, "ordinary text chat", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted || len(base.captured()) != 1 {
		t.Fatalf("ordinary chat without a filesystem root failed: outcome=%s err=%v", turn.Outcome, err)
	}
	if chat.Snapshot().FilesystemRoot.Selected {
		t.Fatal("ordinary chat unexpectedly selected a filesystem root")
	}
	impl, ok := service.impl.(*linuxImplementation)
	if !ok || impl.session != nil {
		t.Fatal("unavailable backend created a worker coordinator")
	}
	if err := chat.CloseContext(ctx); err != nil {
		t.Fatalf("ordinary chat cleanup failed: %v", err)
	}
	if err := service.Close(ctx); err != nil {
		t.Fatalf("unavailable sandbox service cleanup failed: %v", err)
	}
	stage6Update("ordinary_chat_without_root", func(item *stage6Case) { item.ProviderRequestCount, item.CleanupConfirmed = 1, true })
	stage6PassSynthetic("ordinary_chat_without_root", "fake-provider text chat completed with a safely unavailable sandbox service and no selected root or worker")
}

func TestStage6ReadRefusedWithoutRootOrBackend(t *testing.T) {
	stage6Begin("read_refused_without_root_or_backend")
	root := t.TempDir()
	target := filepath.Join(root, "synthetic-private.txt")
	content := "stage6-no-root-private-content"
	if err := os.WriteFile(target, []byte(content), 0600); err != nil {
		t.Fatalf("synthetic target creation failed: %v", err)
	}
	base := &stage6Provider{target: target, firstToolCall: true, secondStarted: make(chan struct{}), releaseSecond: make(chan struct{})}
	providers, err := model.NewRegistry(base)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewHostService(filepath.Join(root, "missing-bubblewrap"))
	if err != nil {
		t.Fatalf("missing backend should leave text chat available: %v", err)
	}
	available, _ := service.BackendStatus()
	if available {
		t.Fatal("missing synthetic backend was reported available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store := auth.Store{Path: filepath.Join(root, "synthetic-auth.json"), Protection: service}
	settingsStore := settings.Store{Path: filepath.Join(root, "synthetic-settings.json"), Protection: service}
	if err := store.RegisterProtection(ctx); err != nil {
		_ = service.Close(context.Background())
		t.Fatalf("synthetic auth owner registration failed: %v", err)
	}
	if err := settingsStore.RegisterProtection(ctx); err != nil {
		_ = service.Close(context.Background())
		t.Fatalf("synthetic settings owner registration failed: %v", err)
	}
	if _, err := settingsStore.Load(); err != nil {
		_ = service.Close(context.Background())
		t.Fatalf("settings loading failed with unavailable backend: %v", err)
	}
	var approvals atomic.Int32
	chat := runtime.NewChatWithToolRuntime(providers, store, nil, runtime.ToolRuntimeConfig{
		Registry: tools.NewRegistry(), Preparer: service, ReadAdmission: &stage6ReadHost{service: service}, RootHost: service,
		Approval: runtime.ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			approvals.Add(1)
			return permissions.ApprovalOnce, "", nil
		}), Grants: permissions.NewMemoryStore(),
	})
	if err := chat.AddProfile(ctx, "stage6-no-root-profile", "openai", "synthetic profile", "synthetic-only-key", nil); err != nil {
		_ = chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatalf("synthetic profile setup failed: %v", err)
	}
	discovery := chat.Discover(ctx)
	if len(discovery.Models) != 1 {
		_ = chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatalf("synthetic model discovery returned %d models", len(discovery.Models))
	}
	if err := chat.Select(ctx, discovery.Models[0].Key(), false); err != nil {
		_ = chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatalf("synthetic model selection failed: %v", err)
	}
	if chat.Snapshot().FilesystemRoot.Selected {
		_ = chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatal("read refusal fixture unexpectedly selected a filesystem root")
	}
	if err := runtime.EnableNativeReadStudy(chat); err != nil {
		_ = chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatalf("test-only read path setup failed: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		clean := true
		if err := chat.CloseContext(cleanupCtx); err != nil {
			t.Errorf("no-root chat cleanup failed: %v", err)
			clean = false
		}
		if err := service.Close(cleanupCtx); err != nil {
			t.Errorf("no-root service cleanup failed: %v", err)
			clean = false
		}
		if clean {
			stage6Update("read_refused_without_root_or_backend", func(item *stage6Case) { item.CleanupConfirmed = true })
		}
	})
	turn, err := chat.Send(ctx, "read the synthetic private fixture", nil)
	if err != nil || turn.Outcome != provider.OutcomeUnsupported {
		t.Fatalf("unadvertised read call without a root was not rejected as unsupported: outcome=%s err=%v", turn.Outcome, err)
	}
	requests := base.captured()
	if len(requests) != 1 {
		t.Fatalf("provider requests=%d, want only the initial tool-call request", len(requests))
	}
	for _, definition := range requests[0].Tools {
		if definition.ID == tools.ReadFileID {
			t.Fatal("read_file was advertised without an explicitly selected root")
		}
	}
	for _, request := range requests {
		encoded, _ := json.Marshal(request.Messages)
		if strings.Contains(string(encoded), content) {
			t.Fatal("private fixture content reached the provider without a selected root")
		}
	}
	if approvals.Load() != 0 {
		t.Fatalf("approval interactions=%d, want no approval before root admission", approvals.Load())
	}
	impl, ok := service.impl.(*linuxImplementation)
	if !ok || impl.session != nil {
		t.Fatal("no-root refusal unexpectedly created a worker coordinator")
	}
	_, messages, results, markerFound, payloadFree := runtime.NativeReadStudyHistoryEvidence(chat, content)
	if messages != 0 || results != 0 || markerFound || !payloadFree {
		t.Fatalf("no-root refusal history was not payload-free: messages=%d results=%d marker=%t payload_free=%t", messages, results, markerFound, payloadFree)
	}
	stage6Update("read_refused_without_root_or_backend", func(item *stage6Case) {
		item.EvidenceKind, item.ProviderRequestCount, item.DisclosureWithheld, item.StaleDataAbsent = "synthetic-runtime-composition", len(requests), true, true
	})
	stage6PassSynthetic("read_refused_without_root_or_backend", "without a selected root the runtime omitted read_file from provider definitions and rejected an unsolicited call as unsupported before approval, worker creation, history retention, or disclosure")
}

func TestStage6ProviderSourceOwnerRemainsUnsupported(t *testing.T) {
	stage6Begin("provider_source_owner")
	root := t.TempDir()
	backendPath := filepath.Join(root, "missing-bubblewrap")
	service, err := NewHostService(backendPath)
	if err != nil {
		t.Fatalf("missing backend should preserve the fail-closed owner snapshot: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Errorf("provider-source refusal service cleanup failed: %v", err)
		} else {
			stage6Update("provider_source_owner", func(item *stage6Case) { item.CleanupConfirmed = true })
		}
	})
	sourcePath := filepath.Join(root, "synthetic-provider-source.jsonl")
	if err := os.WriteFile(sourcePath, []byte("synthetic-source-fixture\n"), 0600); err != nil {
		t.Fatalf("synthetic source fixture creation failed: %v", err)
	}
	snapshot := service.CoverageSnapshot()
	var sourceOwner *OwnerCoverage
	for index := range snapshot.Owners {
		if snapshot.Owners[index].Owner == "provider-source" {
			owner := snapshot.Owners[index]
			sourceOwner = &owner
			break
		}
	}
	if sourceOwner == nil || sourceOwner.State != OwnerUnsupported || sourceOwner.Routes != 0 || sourceOwner.Identities != 0 || sourceOwner.Reason == "" {
		t.Fatalf("provider-source owner was not explicitly unsupported: %+v", sourceOwner)
	}
	file, err := os.Open(sourcePath)
	if err != nil {
		t.Fatalf("open synthetic source fixture: %v", err)
	}
	registerErr := service.Protect(context.Background(), "provider-source", "provider_source", sourcePath, file)
	closeErr := file.Close()
	if registerErr == nil {
		t.Fatal("current chat composition accepted an unowned provider-source registration")
	}
	if closeErr != nil {
		t.Fatalf("synthetic source descriptor cleanup failed: %v", closeErr)
	}
	after := service.CoverageSnapshot()
	for _, owner := range after.Owners {
		if owner.Owner == "provider-source" && (owner.State != OwnerUnsupported || owner.Routes != 0 || owner.Identities != 0) {
			t.Fatalf("failed source registration changed the protected-owner inventory: %+v", owner)
		}
	}
	if impl, ok := service.impl.(*linuxImplementation); !ok || impl.session != nil {
		t.Fatal("synthetic source-owner refusal unexpectedly created a native worker coordinator")
	}
	stage6Update("provider_source_owner", func(item *stage6Case) { item.DisclosureWithheld, item.StaleDataAbsent = true, true })
	stage6PassSynthetic("provider_source_owner", "current memory-chat composition has no provider-source lifecycle; synthetic registration was refused and left the owner unsupported with zero routes/identities")
}

type stage6NeverStartedLauncher struct {
	starts atomic.Int32
}

func (l *stage6NeverStartedLauncher) Start(context.Context) (readbrokerstudy.SandboxWorker, error) {
	l.starts.Add(1)
	return nil, readbrokerstudy.ErrUnavailable
}

func (*stage6NeverStartedLauncher) Close() error { return nil }

func TestStage6IncompleteOwnerInventoryBlocksRuntimeAdmission(t *testing.T) {
	stage6Begin("incomplete_owner_inventory")
	root := t.TempDir()
	content := "stage6-incomplete-inventory-private-content"
	target := filepath.Join(root, "synthetic-private.txt")
	if err := os.WriteFile(target, []byte(content), 0600); err != nil {
		t.Fatalf("synthetic target creation failed: %v", err)
	}
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	launcher := &stage6NeverStartedLauncher{}
	impl, err := composeLinuxImplementation(registry, launcher, nil)
	if err != nil {
		t.Fatalf("synthetic host coordinator setup failed: %v", err)
	}
	service := &Service{impl: impl}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	authStore := auth.Store{Path: filepath.Join(root, "synthetic-chat-auth.json")}
	base := &stage6Provider{target: target, firstToolCall: true, secondStarted: make(chan struct{}), releaseSecond: make(chan struct{})}
	providers, err := model.NewRegistry(base)
	if err != nil {
		_ = service.Close(context.Background())
		t.Fatal(err)
	}
	var approvals atomic.Int32
	chat := runtime.NewChatWithToolRuntime(providers, authStore, nil, runtime.ToolRuntimeConfig{
		Registry: tools.NewRegistry(), Preparer: service, ReadAdmission: &stage6ReadHost{service: service}, RootHost: service,
		Approval: runtime.ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			approvals.Add(1)
			return permissions.ApprovalOnce, "", nil
		}), Grants: permissions.NewMemoryStore(),
	})
	if err := chat.AddProfile(ctx, "stage6-incomplete-profile", "openai", "synthetic profile", "synthetic-only-key", nil); err != nil {
		_ = chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatalf("synthetic profile setup failed: %v", err)
	}
	discovery := chat.Discover(ctx)
	if len(discovery.Models) != 1 {
		_ = chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatalf("synthetic model discovery returned %d models", len(discovery.Models))
	}
	if err := chat.Select(ctx, discovery.Models[0].Key(), false); err != nil {
		_ = chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatalf("synthetic model selection failed: %v", err)
	}
	if err := chat.SelectFilesystemRoot(ctx, root); err != nil {
		_ = chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatalf("synthetic root selection failed: %v", err)
	}
	if err := runtime.EnableNativeReadStudy(chat); err != nil {
		_ = chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatalf("test-only read path setup failed: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := chat.CloseContext(ctx); err != nil {
			t.Errorf("incomplete-inventory chat cleanup failed: %v", err)
		}
		if err := service.Close(ctx); err != nil {
			t.Errorf("incomplete-inventory service cleanup failed: %v", err)
		} else {
			stage6Update("incomplete_owner_inventory", func(item *stage6Case) { item.CleanupConfirmed = true })
		}
	})
	coverage := service.CoverageSnapshot()
	for _, ownerID := range []string{auth.SensitiveOwnerAuth, auth.SensitiveOwnerSettings} {
		found := false
		for _, owner := range coverage.Owners {
			if owner.Owner == ownerID {
				found = owner.State == OwnerRequired && owner.Routes == 0 && owner.Identities == 0
				break
			}
		}
		if !found {
			t.Fatalf("required owner %q did not remain incomplete in the inventory: %+v", ownerID, coverage.Owners)
		}
	}
	turn, err := chat.Send(ctx, "read the synthetic private fixture", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("incomplete inventory refusal did not permit safe text continuation: outcome=%s err=%v", turn.Outcome, err)
	}
	requests := base.captured()
	if len(requests) != 2 {
		t.Fatalf("provider requests=%d, want initial tool call plus one safe continuation", len(requests))
	}
	if len(requests[0].Tools) != 1 || requests[0].Tools[0].ID != tools.ReadFileID {
		t.Fatalf("selected-root request advertised unexpected tools: %+v", requests[0].Tools)
	}
	toolResult, found := readResultContent(requests[1], "stage6-read")
	if !found {
		t.Fatal("safe continuation omitted the correlated read refusal result")
	}
	var refusal struct {
		Outcome   string `json:"outcome"`
		Truncated bool   `json:"truncated"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal([]byte(toolResult), &refusal); err != nil || refusal.Outcome != string(tools.OutcomeNotExecuted) || refusal.Truncated || refusal.Content == "" {
		t.Fatalf("incomplete-inventory result=%q, want bounded not_executed envelope (err=%v)", toolResult, err)
	}
	for _, request := range requests {
		encoded, _ := json.Marshal(request.Messages)
		if strings.Contains(string(encoded), content) {
			t.Fatal("private fixture content reached the provider through incomplete owner coverage")
		}
	}
	if approvals.Load() != 0 {
		t.Fatalf("approval interactions=%d, want inventory refusal before approval", approvals.Load())
	}
	if launcher.starts.Load() != 0 || len(impl.session.AdmissionConversations()) != 0 {
		t.Fatalf("incomplete inventory created worker/admission state: starts=%d admissions=%v", launcher.starts.Load(), impl.session.AdmissionConversations())
	}
	_, messages, results, markerFound, payloadFree := runtime.NativeReadStudyHistoryEvidence(chat, content)
	if messages == 0 || results != 1 || markerFound || !payloadFree {
		t.Fatalf("incomplete-inventory history was not payload-free: messages=%d results=%d marker=%t payload_free=%t", messages, results, markerFound, payloadFree)
	}
	stage6Update("incomplete_owner_inventory", func(item *stage6Case) {
		item.EvidenceKind, item.ProviderRequestCount, item.DisclosureWithheld, item.StaleDataAbsent = "synthetic-runtime-composition", len(requests), true, true
	})
	stage6PassSynthetic("incomplete_owner_inventory", "runtime advertised the selected-root read, but Service refused admission with required auth/settings inventory incomplete; no approval or worker admission occurred, and only a correlated not_executed result reached the safe text continuation")
}

func TestStage6PositiveReadAndSameContextReplay(t *testing.T) {
	stage6Begin("positive_exact_read_and_replay")
	fixture := newStage6Fixture(t, "positive_exact_read_and_replay", "ordinary", false, nil)
	fixture.reportCases = []string{"positive_exact_read_and_replay"}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	grantRevisionBefore, grantsBefore := runtime.NativeReadStudyGrantState(fixture.chat)
	if grantsBefore != 0 {
		t.Fatalf("unexpected preexisting grants=%d", grantsBefore)
	}
	turn, err := fixture.chat.Send(ctx, "summarize the synthetic fixture", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("positive read turn failed: outcome=%s err=%v", turn.Outcome, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 2 {
		t.Fatalf("provider requests after one read=%d, want 2", len(requests))
	}
	first, _ := json.Marshal(requests[0].Messages)
	second, _ := json.Marshal(requests[1].Messages)
	if strings.Contains(string(first), fixture.content) || !strings.Contains(string(second), fixture.content) {
		t.Fatal("fixture bytes were not withheld before read and disclosed at the expected provider handoff")
	}
	if !fixture.chat.Snapshot().FilesystemRoot.Selected {
		t.Fatal("selected root was unexpectedly retired after the read")
	}
	turn, err = fixture.chat.Send(ctx, "continue in the same conversation", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("same-context replay failed: outcome=%s err=%v", turn.Outcome, err)
	}
	requests = fixture.provider.captured()
	if len(requests) != 3 {
		t.Fatalf("provider requests after replay=%d, want 3", len(requests))
	}
	third, _ := json.Marshal(requests[2].Messages)
	if !strings.Contains(string(third), fixture.content) {
		t.Fatal("same-context replay omitted the previously committed fixture result")
	}
	impl, ok := fixture.service.impl.(*linuxImplementation)
	if !ok || impl.session == nil {
		t.Fatal("real host service coordinator evidence is unavailable")
	}
	evidence := impl.session.Evidence()
	if len(evidence) != 1 {
		t.Fatalf("worker reads=%d, want exactly one across replay", len(evidence))
	}
	item := evidence[0]
	probe := item.Probe
	if item.Outcome != readbrokerstudy.ReadSucceeded || !item.WorkerStarted || !item.WorkerReady || !item.WorkerJoined || !item.CapabilitySent || !item.ReadStarted || !probe.NoNewPrivs || probe.SeccompMode != 2 || !probe.SocketCreationDenied || !probe.PathOpenDenied || !probe.ReadCapabilityInitiallyAbsent || !probe.OnlyProtocolDescriptor || !probe.EnvironmentCleared {
		t.Fatal("native worker lifecycle or required sandbox probe was not established")
	}
	if fixture.approvals.Load() != 1 {
		t.Fatalf("once approval interactions=%d, want exactly one", fixture.approvals.Load())
	}
	grantRevisionAfter, grantsAfter := runtime.NativeReadStudyGrantState(fixture.chat)
	if grantRevisionAfter != grantRevisionBefore+2 || grantsAfter != 0 {
		t.Fatalf("once-grant transition revision=%d→%d remaining=%d, want one approval transition, one consumption transition, no grants", grantRevisionBefore, grantRevisionAfter, grantsAfter)
	}
	grantRevisionAfterReplay, grantsAfterReplay := runtime.NativeReadStudyGrantState(fixture.chat)
	if grantRevisionAfterReplay != grantRevisionAfter || grantsAfterReplay != 0 {
		t.Fatal("same-context replay changed or restored the consumed once grant")
	}
	stage6Update("positive_exact_read_and_replay", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = item.WorkerStarted || evidence[0].WorkerStarted, evidence[0].WorkerReady, evidence[0].WorkerJoined
		item.CapabilityTransferred, item.ReadStarted = evidence[0].CapabilitySent, evidence[0].ReadStarted
		item.ProbePassed, item.OnceApprovalCount, item.ProviderRequestCount = true, fixture.approvals.Load(), len(requests)
		item.OnceGrantConsumptionCount, item.GrantRevisionBefore, item.GrantRevisionAfter, item.RemainingGrantCount = 1, grantRevisionBefore, grantRevisionAfter, grantsAfter
		item.FixtureBytesMatched = true
	})
	stage6Pass("positive_exact_read_and_replay", "actual sandbox Service, runtime approval/once consumption, fixed Bubblewrap worker, provider handoff and replay used one read")
	stage6Update("expected_once_transitions", func(item *stage6Case) {
		item.OnceApprovalCount, item.OnceGrantConsumptionCount = fixture.approvals.Load(), 1
		item.GrantRevisionBefore, item.GrantRevisionAfter, item.RemainingGrantCount = grantRevisionBefore, grantRevisionAfter, grantsAfter
	})
	stage6Pass("expected_once_transitions", "one explicit once approval created a revision, runtime dispatch consumed it at the next revision, and no grant remained")
}

func TestStage6IndependentActorMovesOpenedObjectAfterFinalRevalidation(t *testing.T) {
	stage6Begin("independent_namespace_actor")
	fixture := newStage6Fixture(t, "independent_namespace_actor", "ordinary", false, nil)
	fixture.reportCases = []string{"independent_namespace_actor"}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()

	actorRoot := filepath.Dir(fixture.root)
	outside := filepath.Join(actorRoot, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatalf("create disposable actor destination: %v", err)
	}
	archive := filepath.Join(actorRoot, "archive")
	if err := os.Mkdir(archive, 0o700); err != nil {
		t.Fatalf("create disposable actor archive: %v", err)
	}
	target := filepath.Join(fixture.root, "nested", "notes.txt")
	if err := os.Remove(filepath.Join(fixture.root, "notes.txt")); err != nil {
		t.Fatalf("remove unused synthetic root-level target: %v", err)
	}
	if err := os.Mkdir(filepath.Dir(target), 0o700); err != nil {
		t.Fatalf("create synthetic target parent: %v", err)
	}
	if err := os.WriteFile(target, []byte(fixture.content), 0o600); err != nil {
		t.Fatalf("write nested synthetic target: %v", err)
	}
	fixture.provider.target = target
	before, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat approved fixture before actor: %v", err)
	}
	rootRelative := filepath.Base(fixture.root)
	actors := make([]*readbrokerstudy.NativeActor, 0, 3)
	runActor := func(action readbrokerstudy.NativeActorAction, source, destination string) {
		t.Helper()
		actor, err := readbrokerstudy.NewNativeActor(ctx, os.Args[0], actorRoot, action, source, destination)
		if err != nil {
			t.Fatalf("start independent namespace actor: %v", err)
		}
		actors = append(actors, actor)
		t.Cleanup(func() {
			if err := actor.Close(); err != nil {
				t.Errorf("ensure independent namespace actor cleanup: %v", err)
			}
		})
		if !actor.Ready() {
			t.Fatal("independent namespace actor did not complete startup handshake")
		}
		if err := actor.Run(ctx); err != nil {
			t.Fatalf("independent namespace actor transition failed: %v", err)
		}
		if err := actor.Close(); err != nil {
			t.Fatalf("join independent namespace actor: %v", err)
		}
		if !actor.TransitionCompleted() || !actor.Joined() {
			t.Fatalf("independent namespace actor completion=%t joined=%t", actor.TransitionCompleted(), actor.Joined())
		}
	}

	impl := fixture.service.impl.(*linuxImplementation)
	if impl.session == nil {
		t.Fatal("native fixture session is unavailable")
	}
	impl.session.SetCheckpointHook(func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint != readbrokerstudy.CheckpointAfterFinalRevalidation {
			return nil
		}
		// These independent processes move the authorized object with its
		// parent and selected root, then replace its final pathname. The fixed
		// worker receives only the already-open descriptor after they exit.
		runActor(readbrokerstudy.NativeActorRename, filepath.Join(rootRelative, "nested"), filepath.Join("outside", "nested"))
		runActor(readbrokerstudy.NativeActorRename, rootRelative, filepath.Join("outside", "selected-root"))
		runActor(readbrokerstudy.NativeActorReplace, filepath.Join("outside", "nested", "notes.txt"), filepath.Join("archive", "original-notes.txt"))
		return nil
	})
	turn, sendErr := fixture.chat.Send(ctx, "read the approved synthetic fixture", nil)
	if sendErr != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("read of the retained object did not complete after its pathname moved: outcome=%s err=%v", turn.Outcome, sendErr)
	}
	if _, err := os.Lstat(fixture.root); !os.IsNotExist(err) {
		t.Fatalf("selected-root pathname remains after actor relocation: lstat=%v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("approved target pathname remains after parent relocation: lstat=%v", err)
	}
	replacement := filepath.Join(outside, "nested", "notes.txt")
	replacementBytes, err := os.ReadFile(replacement)
	if err != nil || string(replacementBytes) != "actor-substitute\n" {
		t.Fatalf("replacement path content=%q err=%v, want actor substitute", replacementBytes, err)
	}
	moved := filepath.Join(archive, "original-notes.txt")
	after, err := os.Stat(moved)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("archived target is not the approved opened object: same=%t err=%v", err == nil && os.SameFile(before, after), err)
	}
	if rel, err := filepath.Rel(fixture.root, moved); err != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("retained object archive did not leave the selected-root pathname: relative=%q err=%v", rel, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 2 {
		t.Fatalf("provider requests=%d, want initial request and one read continuation", len(requests))
	}
	content, found := readResultContent(requests[1], "stage6-read")
	if !found || !strings.Contains(content, fixture.content) {
		t.Fatalf("provider continuation did not contain bytes from the approved opened object: found=%t", found)
	}
	evidence := impl.session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadSucceeded || !evidence[0].WorkerStarted || !evidence[0].WorkerReady || !evidence[0].WorkerJoined || !evidence[0].CapabilitySent || !evidence[0].ReadStarted || !evidence[0].Probe.NoNewPrivs || evidence[0].Probe.SeccompMode != 2 || !evidence[0].Probe.SocketCreationDenied || !evidence[0].Probe.PathOpenDenied || !evidence[0].Probe.ReadCapabilityInitiallyAbsent || !evidence[0].Probe.OnlyProtocolDescriptor || !evidence[0].Probe.EnvironmentCleared {
		t.Fatal("moved-object read did not complete through the required probed sandbox worker")
	}
	if fixture.approvals.Load() != 1 {
		t.Fatalf("once approval interactions=%d, want one", fixture.approvals.Load())
	}
	allActorsCompleted := len(actors) == 3
	for _, actor := range actors {
		allActorsCompleted = allActorsCompleted && actor.Ready() && actor.TransitionCompleted() && actor.Joined()
	}
	if !allActorsCompleted {
		t.Fatal("not all independent namespace actors completed and joined")
	}
	stage6Update("independent_namespace_actor", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = evidence[0].WorkerStarted, evidence[0].WorkerReady, evidence[0].WorkerJoined
		item.CapabilityTransferred, item.ReadStarted = evidence[0].CapabilitySent, evidence[0].ReadStarted
		item.ProbePassed, item.OnceApprovalCount, item.ProviderRequestCount = true, fixture.approvals.Load(), len(requests)
		item.FixtureBytesMatched, item.ActorStarted, item.ActorTransitionCompleted, item.ActorJoined = true, allActorsCompleted, allActorsCompleted, allActorsCompleted
		item.ActorTransitionCount = len(actors)
		item.AuthorityObservation = "three independent same-user actors moved the target parent and selected root outside their approved pathnames, then replaced the target pathname after final revalidation; the worker disclosed bytes from the retained opened object, consistent with object authority B; strict live-path containment A is not claimed"
	})
	stage6Pass("independent_namespace_actor", "three separate actor processes moved the target parent and selected root outside their approved pathnames, then replaced the target pathname after final revalidation; the sandbox worker read the same opened object and its bytes reached the capturing provider under authority B")
}

func TestStage6CancellationAndCleanupBeforeReadRelease(t *testing.T) {
	stage6Begin("cancellation_and_cleanup")
	fixture := newStage6FixtureWithHold(t, "cancellation_and_cleanup", "ordinary", false, nil, true)
	fixture.reportCases = []string{"cancellation_and_cleanup"}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()

	impl, ok := fixture.service.impl.(*linuxImplementation)
	if !ok || impl.session == nil {
		t.Fatal("native fixture session is unavailable")
	}
	session := impl.session
	checkpointReached := atomic.Bool{}
	impl.session.SetCheckpointHook(func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint != readbrokerstudy.CheckpointAfterCapabilityHeld {
			return nil
		}
		checkpointReached.Store(true)
		cancel()
		return context.Canceled
	})
	turn, sendErr := fixture.chat.Send(ctx, "read the approved synthetic fixture", nil)
	if !checkpointReached.Load() {
		t.Fatal("worker did not reach the held-capability checkpoint")
	}
	if sendErr != nil && !errors.Is(sendErr, context.Canceled) {
		t.Fatalf("cancelled read returned an unrelated error: %v", sendErr)
	}
	if turn.Outcome != provider.OutcomeInterrupted || ctx.Err() == nil {
		t.Fatalf("cancelled held read outcome=%s context=%v", turn.Outcome, ctx.Err())
	}
	requests := fixture.provider.captured()
	if len(requests) != 1 {
		t.Fatalf("provider requests=%d, want initial request only; cancelled turn must not continue", len(requests))
	}
	initial, err := json.Marshal(requests[0].Messages)
	if err != nil || strings.Contains(string(initial), fixture.content) {
		t.Fatalf("unread fixture bytes appeared in provider input: marshal_error=%v", err)
	}
	evidence := impl.session.Evidence()
	if len(evidence) != 1 {
		t.Fatalf("worker evidence entries=%d, want one", len(evidence))
	}
	initialEvidence := evidence[0]
	if (initialEvidence.Outcome != readbrokerstudy.ReadCancelled && initialEvidence.Outcome != readbrokerstudy.ReadUnknown) || !initialEvidence.WorkerStarted || !initialEvidence.WorkerReady || !initialEvidence.CapabilitySent || !initialEvidence.CapabilityHeld || initialEvidence.ReadStarted || initialEvidence.Published || !initialEvidence.Probe.NoNewPrivs || initialEvidence.Probe.SeccompMode != 2 || !initialEvidence.Probe.SocketCreationDenied || !initialEvidence.Probe.PathOpenDenied || !initialEvidence.Probe.ReadCapabilityInitiallyAbsent || !initialEvidence.Probe.OnlyProtocolDescriptor || !initialEvidence.Probe.EnvironmentCleared {
		t.Fatalf("held-worker cancellation initial evidence=%+v", initialEvidence)
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cleanupCancel()
	for {
		err := fixture.chat.CloseContext(cleanupCtx)
		if err == nil {
			break
		}
		if !errors.Is(err, runtime.ErrFilesystemRootPending) {
			t.Fatalf("bounded shutdown failed while joining cancelled held worker: %v", err)
		}
		select {
		case <-cleanupCtx.Done():
			t.Fatalf("bounded shutdown did not join cancelled held worker: %v", errors.Join(err, cleanupCtx.Err()))
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := fixture.service.Close(cleanupCtx); err != nil {
		t.Fatalf("bounded service shutdown did not drain cancelled worker: %v", err)
	}
	evidence = session.Evidence()
	if len(evidence) != 1 {
		t.Fatalf("worker evidence after bounded cleanup=%d, want one", len(evidence))
	}
	item := evidence[0]
	if item.Outcome != initialEvidence.Outcome || item.WorkerJoined != initialEvidence.WorkerJoined || !item.CapabilityHeld || item.ReadStarted || item.Published {
		t.Fatalf("cleanup rewrote the original operation evidence: before=%+v after=%+v", initialEvidence, item)
	}
	if active := session.ActiveOperationCount(); active != 0 {
		t.Fatalf("active operations after bounded cleanup=%d", active)
	}
	if published := session.PublishedResults(); len(published) != 0 {
		t.Fatalf("cancelled held read published %d results", len(published))
	}
	if fixture.approvals.Load() != 1 {
		t.Fatalf("once approval count=%d, want exactly one consumed approval", fixture.approvals.Load())
	}
	stage6Update("cancellation_and_cleanup", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined, item.CleanupConfirmed = true, true, true, true
		item.CapabilityTransferred, item.CapabilityHeld, item.ReadStarted = true, true, false
		item.ReadOutcome, item.ProbePassed, item.OnceApprovalCount = string(initialEvidence.Outcome), true, fixture.approvals.Load()
		item.ProviderRequestCount, item.DisclosureWithheld, item.StaleDataAbsent = len(requests), true, true
	})
	stage6Pass("cancellation_and_cleanup", "cancelled after the worker held the approved descriptor but before read release; the initial outcome remained cancelled or unknown as cleanup evidence allowed, a separate bounded shutdown joined the worker, and no read, publication, or provider continuation occurred")
}

func TestStage6LaunchProfileAndLimits(t *testing.T) {
	stage6Begin("launch_profile_and_limits")
	fixture := newStage6Fixture(t, "launch_profile_and_limits", "ordinary", false, nil)
	fixture.reportCases = []string{"launch_profile_and_limits"}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()

	marker := "stage6-boundary-marker-"
	oversized := strings.Repeat(marker, readbrokerstudy.MaxReadBytes/len(marker)+2)
	target := filepath.Join(fixture.root, "notes.txt")
	if err := os.WriteFile(target, []byte(oversized), 0o600); err != nil {
		t.Fatalf("write bounded synthetic fixture: %v", err)
	}
	fixture.content = oversized
	turn, sendErr := fixture.chat.Send(ctx, "read the approved synthetic fixture", nil)
	if sendErr != nil || turn.Outcome != provider.OutcomeInterrupted {
		t.Fatalf("oversized read did not stop without disclosure: outcome=%s err=%v", turn.Outcome, sendErr)
	}
	requests := fixture.provider.captured()
	if len(requests) != 1 {
		t.Fatalf("provider requests=%d, want no continuation after the bounded partial result", len(requests))
	}
	initial, err := json.Marshal(requests[0].Messages)
	if err != nil || strings.Contains(string(initial), marker) {
		t.Fatal("synthetic file content appeared in the provider request before a complete read")
	}
	impl, ok := fixture.service.impl.(*linuxImplementation)
	if !ok || impl.session == nil {
		t.Fatal("native fixture session is unavailable")
	}
	evidence := impl.session.Evidence()
	if len(evidence) != 1 {
		t.Fatalf("worker evidence entries=%d, want one", len(evidence))
	}
	read := evidence[0]
	if read.Outcome != readbrokerstudy.ReadPartial || !read.WorkerStarted || !read.WorkerReady || !read.WorkerJoined || !read.CapabilitySent || !read.ReadStarted || read.Published || !read.Probe.NoNewPrivs || read.Probe.SeccompMode != 2 || !read.Probe.SocketCreationDenied || !read.Probe.PathOpenDenied || !read.Probe.ReadCapabilityInitiallyAbsent || !read.Probe.OnlyProtocolDescriptor || !read.Probe.EnvironmentCleared {
		t.Fatalf("bounded read evidence=%+v", read)
	}
	if fixture.approvals.Load() != 1 {
		t.Fatalf("once approval count=%d, want one", fixture.approvals.Load())
	}
	stage6Mu.Lock()
	args, argsOK := stage6.LaunchPolicy["effective_bubblewrap_arguments"].([]string)
	mode, modeOK := stage6.Backend["bind_mode"].(readbrokerstudy.BindMode)
	aggregateLimits := stage6.LaunchPolicy["aggregate_cgroup_limits"]
	stage6Mu.Unlock()
	if !argsOK || !modeOK {
		t.Fatalf("effective Bubblewrap policy is unavailable: args=%t mode=%t", argsOK, modeOK)
	}
	hasSequence := func(want ...string) bool {
		for start := 0; start+len(want) <= len(args); start++ {
			matched := true
			for offset := range want {
				if args[start+offset] != want[offset] {
					matched = false
					break
				}
			}
			if matched {
				return true
			}
		}
		return false
	}
	for _, required := range [][]string{
		{"--die-with-parent"}, {"--new-session"}, {"--unshare-user"}, {"--unshare-pid"},
		{"--unshare-ipc"}, {"--unshare-uts"}, {"--unshare-net"}, {"--as-pid-1"},
		{"--disable-userns"}, {"--cap-drop", "ALL"}, {"--clearenv"}, {"--size", "8388608"},
		{"--tmpfs", "/"}, {"--sync-fd", "6"}, {"--seccomp-fd"},
	} {
		if !hasSequence(required...) {
			t.Fatalf("effective Bubblewrap arguments lack %q: %v", required, args)
		}
	}
	switch mode {
	case readbrokerstudy.BindModeDescriptor:
		if !hasSequence("--ro-bind-fd", "5", "/worker") {
			t.Fatalf("descriptor bind mode arguments=%v", args)
		}
	case readbrokerstudy.BindModeFileSnapshot:
		if !hasSequence("--perms", "0500", "--ro-bind-data", "5", "/worker") {
			t.Fatalf("sealed snapshot bind mode arguments=%v", args)
		}
	default:
		t.Fatalf("unsupported worker bind mode %q", mode)
	}
	for _, hostPath := range []string{"/proc", "/home", "/run", "/dev"} {
		for _, arg := range args {
			if arg == hostPath {
				t.Fatalf("launch policy exposes an unreviewed host path %q", hostPath)
			}
		}
	}
	if aggregateLimits != "not provisioned or claimed" || readbrokerstudy.MaxReadBytes != 64<<10 {
		t.Fatalf("resource policy changed unexpectedly: aggregate=%v max_read=%d", aggregateLimits, readbrokerstudy.MaxReadBytes)
	}
	stage6Update("launch_profile_and_limits", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = true, true, true
		item.CapabilityTransferred, item.ReadStarted, item.ReadOutcome = true, true, string(read.Outcome)
		item.ProbePassed, item.OnceApprovalCount, item.ProviderRequestCount = true, fixture.approvals.Load(), len(requests)
		item.DisclosureWithheld, item.StaleDataAbsent = true, true
	})
	stage6Pass("launch_profile_and_limits", "the selected Bubblewrap mode established the fixed namespace/tmpfs/seccomp/FD/environment profile; an oversized synthetic file was capped at 64 KiB and withheld as partial; aggregate cgroup quotas remain unprovisioned and unclaimed")
}

func TestStage6EmptyFileRead(t *testing.T) {
	stage6Begin("empty_file_read")
	fixture := newStage6Fixture(t, "empty_file_read", "empty", false, nil)
	fixture.reportCases = []string{"empty_file_read"}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	turn, err := fixture.chat.Send(ctx, "read the empty synthetic fixture", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("empty-file read failed: outcome=%s err=%v", turn.Outcome, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 2 {
		t.Fatalf("provider requests=%d, want initial and read continuation", len(requests))
	}
	content, found := readResultContent(requests[1], "stage6-read")
	if !found || content != `{"outcome":"succeeded","truncated":false,"content":""}` {
		t.Fatalf("empty-file tool result=%q found=%t", content, found)
	}
	impl := fixture.service.impl.(*linuxImplementation)
	evidence := impl.session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadSucceeded || !evidence[0].ReadStarted || !evidence[0].WorkerJoined {
		t.Fatal("empty file did not complete through one joined worker read")
	}
	if fixture.approvals.Load() != 1 {
		t.Fatalf("once approvals=%d, want 1", fixture.approvals.Load())
	}
	stage6Update("empty_file_read", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = evidence[0].WorkerStarted, evidence[0].WorkerReady, evidence[0].WorkerJoined
		item.CapabilityTransferred, item.ReadStarted = evidence[0].CapabilitySent, evidence[0].ReadStarted
		item.OnceApprovalCount, item.ProviderRequestCount = fixture.approvals.Load(), len(requests)
		item.FixtureBytesMatched = true
	})
	stage6Pass("empty_file_read", "empty regular-file content was returned as an empty successful result after explicit approval and worker join")
}

func readResultContent(request provider.ChatRequest, callID string) (string, bool) {
	for _, message := range request.Messages {
		if message.Role == provider.RoleTool && message.ToolResult != nil && message.ToolResult.CallID == callID {
			return message.ToolResult.Content, true
		}
	}
	return "", false
}

func TestStage6ProtectedAuthObjectIsWithheld(t *testing.T) {
	stage6Begin("protected_auth_refusal")
	fixture := newStage6Fixture(t, "protected_auth_refusal", "auth", false, nil)
	fixture.reportCases = []string{"protected_auth_refusal"}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	turn, err := fixture.chat.Send(ctx, "request the protected fixture", nil)
	if err != nil || turn.Outcome != provider.OutcomeInterrupted {
		t.Fatalf("protected auth refusal did not interrupt before continuation: outcome=%s err=%v", turn.Outcome, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 1 {
		t.Fatalf("provider requests=%d, want only the initial request before protected-read refusal", len(requests))
	}
	for _, request := range requests {
		encoded, _ := json.Marshal(request.Messages)
		if strings.Contains(string(encoded), "synthetic-never-real-key") {
			t.Fatal("protected auth marker reached provider")
		}
	}
	impl, ok := fixture.service.impl.(*linuxImplementation)
	if !ok || impl.session == nil || len(impl.session.Evidence()) != 0 {
		t.Fatal("protected auth refusal started or recorded a worker")
	}
	if fixture.approvals.Load() != 0 {
		t.Fatalf("protected auth request reached approval UI %d times", fixture.approvals.Load())
	}
	_, historyMessages, toolResults, markerFound, payloadFree := runtime.NativeReadStudyHistoryEvidence(fixture.chat, "synthetic-never-real-key")
	if historyMessages == 0 || toolResults != 1 || markerFound || !payloadFree {
		t.Fatalf("protected auth refusal history was not payload-free: messages=%d tool_results=%d marker=%t payload_free=%t", historyMessages, toolResults, markerFound, payloadFree)
	}
	stage6Update("protected_auth_refusal", func(item *stage6Case) {
		item.ProviderRequestCount, item.DisclosureWithheld = len(requests), true
		item.OnceApprovalCount, item.WorkerStarted, item.WorkerJoined = fixture.approvals.Load(), false, false
		item.StaleDataAbsent = true
	})
	stage6Pass("protected_auth_refusal", "registered synthetic auth route refused before approval, worker launch or provider disclosure")
}

func TestStage6ProtectedSettingsObjectIsWithheld(t *testing.T) {
	stage6Begin("protected_settings_refusal")
	fixture := newStage6Fixture(t, "protected_settings_refusal", "settings", false, nil)
	fixture.reportCases = []string{"protected_settings_refusal"}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	turn, err := fixture.chat.Send(ctx, "request the protected fixture", nil)
	if err != nil || turn.Outcome != provider.OutcomeInterrupted {
		t.Fatalf("protected settings refusal did not interrupt before continuation: outcome=%s err=%v", turn.Outcome, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 1 {
		t.Fatalf("provider requests=%d, want only the initial request before protected-read refusal", len(requests))
	}
	for _, request := range requests {
		encoded, _ := json.Marshal(request.Messages)
		if strings.Contains(string(encoded), "synthetic-settings-marker") {
			t.Fatal("protected settings marker reached provider")
		}
	}
	impl, ok := fixture.service.impl.(*linuxImplementation)
	if !ok || impl.session == nil || len(impl.session.Evidence()) != 0 {
		t.Fatal("protected settings refusal started or recorded a worker")
	}
	if fixture.approvals.Load() != 0 {
		t.Fatalf("protected settings request reached approval UI %d times", fixture.approvals.Load())
	}
	_, historyMessages, toolResults, markerFound, payloadFree := runtime.NativeReadStudyHistoryEvidence(fixture.chat, "synthetic-settings-marker")
	if historyMessages == 0 || toolResults != 1 || markerFound || !payloadFree {
		t.Fatalf("protected settings refusal history was not payload-free: messages=%d tool_results=%d marker=%t payload_free=%t", historyMessages, toolResults, markerFound, payloadFree)
	}
	stage6Update("protected_settings_refusal", func(item *stage6Case) {
		item.ProviderRequestCount, item.DisclosureWithheld = len(requests), true
		item.OnceApprovalCount, item.WorkerStarted, item.WorkerJoined = fixture.approvals.Load(), false, false
		item.StaleDataAbsent = true
	})
	stage6Pass("protected_settings_refusal", "registered synthetic settings route refused before approval, worker launch or provider disclosure")
}

func TestStage6ClearDuringSecondApprovalWithholdsFirstCapture(t *testing.T) {
	stage6Begin("captured_batch_revocation")
	fixture := newStage6Fixture(t, "captured_batch_revocation", "multiple", false, nil)
	fixture.reportCases = []string{"captured_batch_revocation"}
	fixture.approvalWaitAt.Store(2)
	defer fixture.approvalReleaseOnce.Do(func() { close(fixture.approvalRelease) })
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	type sendResult struct {
		turn runtime.Turn
		err  error
	}
	result := make(chan sendResult, 1)
	go func() {
		turn, err := fixture.chat.Send(ctx, "read two synthetic files", nil)
		result <- sendResult{turn: turn, err: err}
	}()
	select {
	case <-fixture.approvalEntered:
	case <-time.After(12 * time.Second):
		t.Fatal("second read did not reach the approval interaction")
	}
	if _, err := fixture.chat.ClearToolGrants(); err != nil {
		t.Fatalf("clear during the pending approval failed: %v", err)
	}
	fixture.approvalReleaseOnce.Do(func() { close(fixture.approvalRelease) })
	select {
	case completed := <-result:
		if completed.err != nil || completed.turn.Outcome != provider.OutcomeInterrupted {
			t.Fatalf("revoked read batch did not interrupt without continuation: outcome=%s err=%v", completed.turn.Outcome, completed.err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("turn did not finish after clearing authority and releasing approval")
	}
	requests := fixture.provider.captured()
	if len(requests) != 1 {
		t.Fatalf("provider accepted %d requests; pending batch must be withheld", len(requests)-1)
	}
	encoded, _ := json.Marshal(requests[0].Messages)
	if strings.Contains(string(encoded), fixture.content) || strings.Contains(string(encoded), fixture.secondContent) {
		t.Fatal("captured batch bytes reached the provider after grants were cleared")
	}
	sensitive, messages, toolResults, markersFound, payloadFree := runtime.NativeReadStudyHistoryEvidence(fixture.chat, fixture.content, fixture.secondContent)
	if sensitive || messages == 0 || toolResults != 2 || markersFound || !payloadFree {
		t.Fatalf("revoked capture history was not limited to correlated payload-free results: sensitive=%t messages=%d tool_results=%d markers=%t payload_free=%t", sensitive, messages, toolResults, markersFound, payloadFree)
	}
	impl := fixture.service.impl.(*linuxImplementation)
	evidence := impl.session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadSucceeded || !evidence[0].WorkerJoined {
		t.Fatal("first read was not completed and joined before the second approval wait")
	}
	if fixture.approvals.Load() != 2 {
		t.Fatalf("approval interactions=%d, want first and pending second call", fixture.approvals.Load())
	}
	stage6Update("captured_batch_revocation", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = evidence[0].WorkerStarted, evidence[0].WorkerReady, evidence[0].WorkerJoined
		item.CapabilityTransferred, item.ReadStarted = evidence[0].CapabilitySent, evidence[0].ReadStarted
		item.OnceApprovalCount, item.ProviderRequestCount = fixture.approvals.Load(), len(requests)
		item.DisclosureWithheld, item.StaleDataAbsent = true, true
	})
	stage6Pass("captured_batch_revocation", "first capture remained private while the second call awaited approval; clearing grants invalidated the batch before provider handoff")
}

func TestStage6OwnerRotationBeforeHandoffWithholdsCapture(t *testing.T) {
	stage6Begin("pre_handoff_owner_invalidation")
	entered := make(chan struct{})
	release := make(chan struct{})
	var paused atomic.Bool
	hook := func() error {
		if paused.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		return nil
	}
	fixture := newStage6Fixture(t, "pre_handoff_owner_invalidation", "ordinary", false, hook)
	fixture.reportCases = []string{"pre_handoff_owner_invalidation"}
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	result := make(chan error, 1)
	go func() {
		_, err := fixture.chat.Send(context.Background(), "read before owner rotation", nil)
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(12 * time.Second):
		t.Fatal("capture did not reach the pre-handoff checkpoint")
	}
	rotateSyntheticAuth(t, fixture)
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("invalidated capture unexpectedly completed as a disclosed turn")
		}
	case <-time.After(12 * time.Second):
		t.Fatal("send did not terminate after owner invalidation")
	}
	requests := fixture.provider.captured()
	if len(requests) != 1 {
		t.Fatalf("provider accepted %d requests; stale capture must not be handed off", len(requests))
	}
	for _, request := range requests {
		encoded, _ := json.Marshal(request.Messages)
		if strings.Contains(string(encoded), fixture.content) {
			t.Fatal("invalidated read bytes appeared in a provider request")
		}
	}
	if fixture.chat.Snapshot().Active {
		t.Fatal("chat remained active after the refused handoff")
	}
	historySensitive, historyMessages, toolResults, markerFound, payloadFree := runtime.NativeReadStudyHistoryEvidence(fixture.chat, fixture.content)
	if historySensitive || markerFound || !payloadFree {
		t.Fatalf("retired history contained stale read data: sensitive=%t messages=%d tool_results=%d marker=%t payload_envelope_valid=%t", historySensitive, historyMessages, toolResults, markerFound, payloadFree)
	}
	for _, turn := range fixture.chat.Snapshot().Turns {
		if strings.Contains(turn.Text, fixture.content) || strings.Contains(turn.Refusal, fixture.content) || strings.Contains(turn.Message, fixture.content) {
			t.Fatal("invalidated read bytes appeared in a retained turn snapshot")
		}
	}
	impl := fixture.service.impl.(*linuxImplementation)
	evidence := impl.session.Evidence()
	if len(evidence) != 1 || !evidence[0].WorkerJoined || !evidence[0].ReadStarted {
		t.Fatal("the completed read and confirmed join were not observed before invalidation")
	}
	stage6Update("pre_handoff_owner_invalidation", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = evidence[0].WorkerStarted, evidence[0].WorkerReady, evidence[0].WorkerJoined
		item.CapabilityTransferred, item.ReadStarted = evidence[0].CapabilitySent, evidence[0].ReadStarted
		item.ProviderRequestCount, item.DisclosureWithheld, item.StaleDataAbsent = len(requests), true, true
		item.OnceApprovalCount = fixture.approvals.Load()
	})
	stage6Pass("pre_handoff_owner_invalidation", "actual Service owner rotation completed at a deterministic pre-gate checkpoint; no stale capture reached the provider")
}

func TestStage6OwnerRotationAfterHandoffRetiresContinuation(t *testing.T) {
	stage6Begin("post_handoff_owner_invalidation")
	stage6Begin("late_response_and_delta_retirement")
	fixture := newStage6Fixture(t, "post_handoff_owner_invalidation", "ordinary", true, nil)
	fixture.reportCases = []string{"post_handoff_owner_invalidation", "late_response_and_delta_retirement"}
	fixture.provider.lateDelta = "stage6-late-sensitive-delta"
	var deltaMu sync.Mutex
	var deltas []string
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(fixture.provider.releaseSecond) })
	type sendResult struct {
		turn runtime.Turn
		err  error
	}
	result := make(chan sendResult, 1)
	go func() {
		turn, err := fixture.chat.Send(context.Background(), "read before post-handoff rotation", func(value string) {
			deltaMu.Lock()
			deltas = append(deltas, value)
			deltaMu.Unlock()
		})
		result <- sendResult{turn: turn, err: err}
	}()
	select {
	case <-fixture.provider.secondStarted:
	case <-time.After(12 * time.Second):
		t.Fatal("fake provider did not accept the second request")
	}
	requests := fixture.provider.captured()
	if len(requests) != 2 {
		t.Fatalf("provider accepted requests=%d, want two at handoff", len(requests))
	}
	second, _ := json.Marshal(requests[1].Messages)
	if !strings.Contains(string(second), fixture.content) {
		t.Fatal("handoff-before-invalidation request did not contain the committed synthetic bytes")
	}
	rotateSyntheticAuth(t, fixture)
	releaseOnce.Do(func() { close(fixture.provider.releaseSecond) })
	select {
	case completed := <-result:
		if completed.err != nil || completed.turn.Outcome != provider.OutcomeInterrupted {
			t.Fatalf("late provider response after invalidation was not retired: outcome=%s err=%v", completed.turn.Outcome, completed.err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("provider turn did not finish after release")
	}
	sensitive, messages := runtime.NativeReadStudyState(fixture.chat)
	if sensitive || messages != 0 {
		t.Fatal("owner invalidation did not retire the committed in-memory continuation")
	}
	deltaMu.Lock()
	observedDeltas := append([]string(nil), deltas...)
	deltaMu.Unlock()
	if len(observedDeltas) != 0 {
		t.Fatalf("late provider delta reached the UI after owner invalidation: %q", observedDeltas)
	}
	if got := fixture.provider.lateResponses.Load(); got != 1 {
		t.Fatalf("late provider response returned %d times, want exactly once", got)
	}
	if got := fixture.provider.lateDeltas.Load(); got != 1 {
		t.Fatalf("late provider delta was attempted %d times, want exactly once", got)
	}
	impl := fixture.service.impl.(*linuxImplementation)
	evidence := impl.session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadSucceeded || !evidence[0].WorkerJoined || !evidence[0].CapabilitySent || !evidence[0].ReadStarted {
		t.Fatal("post-handoff case did not observe one completed, joined read worker")
	}
	stage6Update("post_handoff_owner_invalidation", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = evidence[0].WorkerStarted, evidence[0].WorkerReady, evidence[0].WorkerJoined
		item.CapabilityTransferred, item.ReadStarted = evidence[0].CapabilitySent, evidence[0].ReadStarted
		item.OnceApprovalCount, item.ProviderRequestCount = fixture.approvals.Load(), len(requests)
		item.FixtureBytesMatched, item.StaleDataAbsent = true, true
	})
	stage6Pass("post_handoff_owner_invalidation", "fake provider accepted the disclosure before the real owner rotation; the late response was rejected and continuation retired without a recall claim")
	stage6Update("late_response_and_delta_retirement", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = evidence[0].WorkerStarted, evidence[0].WorkerReady, evidence[0].WorkerJoined
		item.CapabilityTransferred, item.ReadStarted = evidence[0].CapabilitySent, evidence[0].ReadStarted
		item.OnceApprovalCount, item.ProviderRequestCount = fixture.approvals.Load(), len(requests)
		item.FixtureBytesMatched, item.StaleDataAbsent = true, true
		item.LateResponseReturned, item.LateDeltaAttempted, item.LateDeltaSuppressed = true, true, len(observedDeltas) == 0
	})
	stage6Pass("late_response_and_delta_retirement", "late provider response and delta after owner invalidation were suppressed; prior accepted disclosure is not claimed recalled")
}

func rotateSyntheticAuth(t *testing.T, fixture *stage6Fixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := fixture.authStore.ReplaceAPIKeyContext(ctx, "stage6-profile", "synthetic rotated profile", "synthetic-never-real-rotated-key", nil); err != nil {
		t.Fatalf("synthetic auth owner rotation failed: %v", err)
	}
}
