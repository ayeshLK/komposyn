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

// Command read-broker-study runs a disposable Linux broker-plus-sandbox
// feasibility experiment. It never connects to a provider or registers a
// production tool.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
	"golang.org/x/sys/unix"
)

type report struct {
	Schema       string       `json:"schema"`
	StartedUTC   string       `json:"started_utc"`
	Commit       string       `json:"commit"`
	Source       string       `json:"source"`
	Environment  environment  `json:"environment"`
	LaunchPolicy launchPolicy `json:"launch_policy"`
	Availability string       `json:"availability"`
	Unavailable  string       `json:"unavailable_reason,omitempty"`
	Cases        []caseResult `json:"cases"`
	NativeGaps   []string     `json:"native_evidence_gaps"`
	Limitations  []string     `json:"limitations"`
}

type environment struct {
	OS                     string                   `json:"os"`
	Architecture           string                   `json:"architecture"`
	Kernel                 string                   `json:"kernel"`
	GoVersion              string                   `json:"go_version"`
	UID                    uint32                   `json:"uid"`
	StudyExecutableSHA256  string                   `json:"study_executable_sha256"`
	UserNamespaceClone     string                   `json:"unprivileged_userns_clone_sysctl"`
	NamespaceProbeStatus   string                   `json:"namespace_functional_probe_status"`
	SeccompActionAvailable bool                     `json:"seccomp_action_available"`
	CgroupV2               bool                     `json:"cgroup_v2"`
	CgroupControllers      []string                 `json:"cgroup_controllers,omitempty"`
	CgroupDelegationStatus string                   `json:"cgroup_delegation_status"`
	AppArmorProfileStatus  string                   `json:"apparmor_profile_status"`
	BubblewrapPath         string                   `json:"bubblewrap_path,omitempty"`
	BubblewrapVersion      string                   `json:"bubblewrap_version,omitempty"`
	BubblewrapSHA256       string                   `json:"bubblewrap_sha256,omitempty"`
	BubblewrapUID          uint32                   `json:"bubblewrap_uid,omitempty"`
	BubblewrapMode         string                   `json:"bubblewrap_mode,omitempty"`
	BubblewrapTrusted      bool                     `json:"bubblewrap_trusted"`
	BubblewrapCapabilities []string                 `json:"bubblewrap_capabilities,omitempty"`
	BubblewrapBindMode     readbrokerstudy.BindMode `json:"bubblewrap_bind_mode,omitempty"`
	BubblewrapError        string                   `json:"bubblewrap_error,omitempty"`
	Restrictions           map[string]string        `json:"host_restrictions,omitempty"`
}

type launchPolicy struct {
	Backend               string                   `json:"backend"`
	EffectiveArguments    []string                 `json:"effective_bubblewrap_arguments"`
	WorkerBindMode        readbrokerstudy.BindMode `json:"worker_bind_mode,omitempty"`
	Namespaces            []string                 `json:"required_namespaces"`
	Network               string                   `json:"network"`
	Root                  string                   `json:"root"`
	WorkerMount           string                   `json:"worker_mount"`
	WorkerSnapshot        string                   `json:"worker_snapshot"`
	RuntimeReadExceptions []string                 `json:"runtime_read_exceptions"`
	Environment           string                   `json:"environment"`
	InheritedDescriptors  []string                 `json:"inherited_descriptors"`
	CapabilityHandoff     string                   `json:"capability_handoff"`
	WorkerVerification    string                   `json:"worker_mount_verification"`
	Seccomp               string                   `json:"seccomp"`
	ResourceLimits        map[string]string        `json:"resource_limits"`
	UnrestrictedFallback  bool                     `json:"unrestricted_fallback"`
}

type caseResult struct {
	Name                  string                      `json:"name"`
	Status                string                      `json:"status"`
	Expected              string                      `json:"expected"`
	Observed              string                      `json:"observed"`
	WorkerStarted         bool                        `json:"worker_started"`
	CapabilitySent        bool                        `json:"capability_sent"`
	ReadStarted           bool                        `json:"read_started"`
	Published             bool                        `json:"published"`
	BytesMatchTarget      bool                        `json:"bytes_match_target"`
	OnceGrantConsumptions int                         `json:"once_grant_consumptions"`
	WorkerProbe           readbrokerstudy.WorkerProbe `json:"worker_probe,omitempty"`
	FixtureEffect         string                      `json:"fixture_effect"`
	AuthorityA            string                      `json:"authority_a"`
	AuthorityB            string                      `json:"authority_b"`
	Detail                string                      `json:"detail,omitempty"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--readbrokerstudy-bootstrap" {
		os.Exit(readbrokerstudy.WorkerMain())
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "read-broker-study:", err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("read-broker-study", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	commit := flags.String("commit", "", "repository source commit under test")
	bwrap := flags.String("bwrap", "/usr/bin/bwrap", "trusted absolute Bubblewrap path")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *commit == "" || flags.NArg() != 0 {
		return errors.New("--commit is required and positional arguments are not accepted")
	}
	started := time.Now().UTC()
	backend := readbrokerstudy.InspectBubblewrap(*bwrap)
	workerMount, workerVerification := workerMountPolicy(backend.BindMode)
	report := report{
		Schema: "komposyn.read-broker-study.v1", StartedUTC: started.Format(time.RFC3339),
		Commit: *commit, Source: "cmd/read-broker-study; internal/harness/readbrokerstudy",
		Environment: inspectEnvironmentWithBackend(*bwrap, backend),
		LaunchPolicy: launchPolicy{
			Backend:               "system Bubblewrap; fixed static Go worker; Linux only",
			Namespaces:            []string{"user", "mount", "pid", "ipc", "uts", "net"},
			Network:               "unshared network namespace; worker seccomp denies socket creation and network syscalls",
			Root:                  "private 8 MiB worker tmpfs; no host root or home mount",
			WorkerMount:           workerMount,
			WorkerSnapshot:        "host opens /proc/self/exe, confirms the supplied executable path names that same device/inode, copies at most 7 MiB into a sealed memfd, reopens it read-only, and verifies size/seals; user-owned path permissions are not the trust anchor",
			EffectiveArguments:    effectiveBubblewrapArguments(backend.BindMode),
			WorkerBindMode:        backend.BindMode,
			RuntimeReadExceptions: []string{"none"},
			Environment:           "host command environment contains only synthetic sentinel; Bubblewrap --clearenv; worker verifies sentinel absent and any PWD is /",
			InheritedDescriptors:  []string{"0 /dev/null input", "1 output discarded", "2 bounded stderr capture", "3 exact seccomp program read then closed by bootstrap before ready", "4 AF_UNIX SOCK_SEQPACKET control; no read capability at startup", "5 fresh read-only sealed snapshot description consumed by Bubblewrap during descriptor bind or byte construction", "6 independent read-only sealed snapshot description retained by --sync-fd through bootstrap, then closed before seccomp/readiness"},
			CapabilityHandoff:     "one read-only regular-file FD by SCM_RIGHTS after readiness, authorization, and generation revalidation",
			WorkerVerification:    workerVerification,
			Seccomp:               "fixed worker sets no_new_privs and installs architecture-checked BPF with SECCOMP_FILTER_FLAG_TSYNC; pathname opens, socket creation, clone3, and process-like clone flags are denied; only the exact Go runtime thread clone flag sets are allowed; probes run before readiness",
			ResourceLimits:        map[string]string{"wall_clock": "5 seconds per worker", "result": "64 KiB", "stderr": "4 KiB captured", "worker_snapshot": "7 MiB maximum; sealed memfd plus Bubblewrap's transient descriptor-fed copy; larger worker images refused", "scratch": "8 MiB private worker root", "aggregate_process_memory_cpu": "not provisioned or tested"},
			UnrestrictedFallback:  false,
		},
		Limitations: []string{
			"typed-read experiments do not establish shell safety",
			"aggregate cgroup quotas are deferred; exhaustion safety is not claimed",
			"object identity cannot recognize independent copies of sensitive bytes",
			"policy scans are bounded observations and do not serialize independent namespace writers",
			"a pinned directory does not pin every current or future descendant; unpinned same-device files are refused",
			"the bootstrap uses --as-pid-1 so Bubblewrap's --sync-fd can pass verifier FD 6; worker seccomp allows only the Go runtime's exact shared-thread clone flag sets, denies clone3 and process-like clone, and needs native startup validation",
			"Bubblewrap 0.9 file-snapshot mode requires host /proc/self/exe and sealed memfd support; it refuses the worker if the running image exceeds 7 MiB or snapshot/copy verification fails",
			"the 0.9 staging verifier runs inside the copied worker, so its independent trust comes from the already-running parent image, sealed memfd bytes, and trusted Bubblewrap copy; the self-comparison is an integrity check, not its own trust anchor",
			"the command's protected-object cases use disposable fixtures; independent-actor namespace races are currently covered only by package tests with injected workers, not by this Bubblewrap command",
			"replacement after open/classification, root/parent movement, mount crossing, ancestor replacement, and recursive-child relocation have no integrated sandboxed-actor scenario",
		},
		NativeGaps: []string{
			"same-user independent actor synchronized between final protected-location scan and descriptor release",
			"credential rotation with old and new protected identities/aliases in one live sandboxed session",
			"root and parent relocation/replacement while a sandboxed worker is active",
			"target replacement after open/classification with a real Bubblewrap worker",
			"protected ancestor relocation/replacement, mount-boundary behavior, and recursive-child relocation",
			"parent death and descendant termination evidence",
			"worker descriptor cleanup after incomplete coordinator shutdown",
		},
	}

	if err := validateRunInputs(*bwrap); err != nil {
		report.Availability = "unavailable"
		report.Unavailable = stableError(err)
		report.Cases = unavailableCases(report.Unavailable)
	} else {
		report.Availability = "available"
		cases, err := runFixtures(*bwrap)
		if err != nil {
			return err
		}
		report.Cases = cases
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func unavailableCases(reason string) []caseResult {
	planned := []struct{ name, expected string }{
		{"ordinary_positive", "explicit once approval reads the opened fixture object"},
		{"protected_direct", "credential object refused before worker capability handoff"},
		{"provider_source_explicit_once", "registered provider source read only after explicit once approval"},
		{"protected_existing_hardlink", "existing alias to protected object refused"},
		{"protected_late_hardlink", "late alias to protected object refused"},
		{"protected_object_renamed_into_ordinary_tree", "protected identity remains denied after rename"},
		{"protected_location_replaced_before_open", "protected pathname remains denied after replacement"},
		{"ordinary_object_moved_into_protected_location", "reserved protected location denies moved object"},
		{"cancel_before_release", "no read capability released after cancellation"},
		{"revoke_after_capture_before_publish", "captured bytes suppressed after revocation"},
		{"registered_directory_descendant_inside", "descendant denied while below pinned directory"},
		{"registered_directory_descendant_moved_out", "unpinned same-device descendant refused"},
	}
	results := make([]caseResult, 0, len(planned))
	for _, item := range planned {
		results = append(results, caseResult{Name: item.name, Status: "unavailable", Expected: item.expected, Observed: "candidate profile refused before fixture creation; no worker started", FixtureEffect: "none", AuthorityA: "no conclusion", AuthorityB: "no conclusion", Detail: reason})
	}
	return results
}

func validateRunInputs(bwrap string) error {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return errors.New("native profile is implemented only for Linux amd64 and arm64")
	}
	executable, err := os.Executable()
	if err != nil {
		return errors.New("study executable path is unavailable")
	}
	if err := validateStatic(executable); err != nil {
		return fmt.Errorf("study worker executable is unavailable: %w", err)
	}
	launcher, err := readbrokerstudy.NewWorkerLauncher(bwrap, executable)
	if err != nil {
		return fmt.Errorf("candidate profile preflight refused: %w", err)
	}
	return launcher.Close()
}

func validateStatic(path string) error {
	// The launcher performs the authoritative static ELF check as well. This
	// preflight only rejects a symlinked or missing executable without running it.
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("study executable is not a regular file")
	}
	return nil
}

func runFixtures(bwrap string) ([]caseResult, error) {
	worker, err := os.Executable()
	if err != nil {
		return nil, err
	}
	launcher, err := readbrokerstudy.NewWorkerLauncher(bwrap, worker)
	if err != nil {
		return nil, err
	}
	base, err := os.MkdirTemp("", "komposyn-readbroker-fixture-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(base)
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		return nil, err
	}
	protectedDir := filepath.Join(root, "protected")
	if err := os.Mkdir(protectedDir, 0o700); err != nil {
		return nil, err
	}
	protected := filepath.Join(protectedDir, "credential.fixture")
	ordinary := filepath.Join(root, "ordinary.fixture")
	if err := os.WriteFile(ordinary, []byte("fixture-only-ordinary-content\n"), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(protected, []byte("fixture-only-protected-content\n"), 0o600); err != nil {
		return nil, err
	}
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		return nil, err
	}
	authorizer := readbrokerstudy.NewOnceGrantFixture()
	session, err := readbrokerstudy.NewFixtureSession(registry, readbrokerstudy.NewBroker(registry), launcher, authorizer)
	if err != nil {
		_ = registry.Close()
		return nil, err
	}
	defer session.Close(context.Background())
	if err := session.SelectRoot(root); err != nil {
		return nil, err
	}
	if err := session.Register(readbrokerstudy.OwnerRegistration{Owner: "fixture-auth", Class: readbrokerstudy.ClassCredential, Path: protected}); err != nil {
		return nil, err
	}

	results := make([]caseResult, 0, 16)
	results = append(results, executeCase(session, authorizer, root, "ordinary_positive", ordinary, "ordinary-1", "explicit once approval reads the opened fixture object"))
	results = append(results, executeCase(session, authorizer, root, "protected_direct", protected, "credential-1", "credential object is refused before worker capability handoff"))

	providerSource := filepath.Join(root, "provider-source.fixture")
	if err := os.WriteFile(providerSource, []byte("synthetic provider source\n"), 0o600); err != nil {
		return nil, err
	}
	if err := session.Register(readbrokerstudy.OwnerRegistration{Owner: "fixture-provider", Class: readbrokerstudy.ClassProvider, Path: providerSource}); err != nil {
		return nil, err
	}
	results = append(results, executeCase(session, authorizer, root, "provider_source_explicit_once", providerSource, "provider-source-1", "registered provider source requires explicit once approval"))

	alias := filepath.Join(root, "protected-alias.fixture")
	if err := os.Link(protected, alias); err != nil {
		results = append(results, caseResult{Name: "protected_existing_hardlink", Status: "unavailable", Expected: "protected identity refused", Observed: "fixture hard-link creation unavailable", FixtureEffect: "no alias created", AuthorityA: "no conclusion", AuthorityB: "no conclusion", Detail: "host filesystem refused hard-link fixture"})
	} else {
		results = append(results, executeCase(session, authorizer, root, "protected_existing_hardlink", alias, "credential-alias-1", "registered object remains protected through an existing hard-link alias"))
	}

	late := filepath.Join(root, "late-alias.fixture")
	if err := os.Link(protected, late); err != nil {
		results = append(results, caseResult{Name: "protected_late_hardlink", Status: "unavailable", Expected: "protected identity refused", Observed: "fixture hard-link creation unavailable", FixtureEffect: "no alias created", AuthorityA: "no conclusion", AuthorityB: "no conclusion", Detail: "host filesystem refused late hard-link fixture"})
	} else {
		results = append(results, executeCase(session, authorizer, root, "protected_late_hardlink", late, "credential-late-alias-1", "late alias to a pinned protected object is refused"))
	}

	moveSource := filepath.Join(root, "protected", "move-me.fixture")
	moveTarget := filepath.Join(root, "renamed-protected.fixture")
	if err := os.WriteFile(moveSource, []byte("synthetic protected object\n"), 0o600); err != nil {
		return nil, err
	}
	if err := session.Register(readbrokerstudy.OwnerRegistration{Owner: "fixture-move", Class: readbrokerstudy.ClassCredential, Path: moveSource}); err != nil {
		return nil, err
	}
	if err := os.Rename(moveSource, moveTarget); err != nil {
		return nil, err
	}
	results = append(results, executeCase(session, authorizer, root, "protected_object_renamed_into_ordinary_tree", moveTarget, "credential-renamed-1", "pinned protected object remains denied after rename"))

	replacement := filepath.Join(root, "protected", "replace-me.fixture")
	if err := os.WriteFile(replacement, []byte("synthetic protected original\n"), 0o600); err != nil {
		return nil, err
	}
	if err := session.Register(readbrokerstudy.OwnerRegistration{Owner: "fixture-replace", Class: readbrokerstudy.ClassCredential, Path: replacement}); err != nil {
		return nil, err
	}
	if err := os.Remove(replacement); err != nil {
		return nil, err
	}
	if err := os.WriteFile(replacement, []byte("synthetic replacement\n"), 0o600); err != nil {
		return nil, err
	}
	results = append(results, executeCase(session, authorizer, root, "protected_location_replaced_before_open", replacement, "credential-replacement-1", "registered protected location remains denied after replacement"))

	reserved := filepath.Join(root, "protected", "reserved.fixture")
	ordinaryToProtected := filepath.Join(root, "ordinary-to-protected.fixture")
	if err := session.Register(readbrokerstudy.OwnerRegistration{Owner: "fixture-reservation", Class: readbrokerstudy.ClassCredential, Path: reserved}); err != nil {
		return nil, err
	}
	if err := os.WriteFile(ordinaryToProtected, []byte("synthetic moved object\n"), 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(ordinaryToProtected, reserved); err != nil {
		return nil, err
	}
	results = append(results, executeCase(session, authorizer, root, "ordinary_object_moved_into_protected_location", reserved, "moved-into-protected-1", "reserved protected location denies an object moved into it"))

	cancelPath := filepath.Join(root, "cancel-before-release.fixture")
	if err := os.WriteFile(cancelPath, []byte("synthetic cancel fixture\n"), 0o600); err != nil {
		return nil, err
	}
	results = append(results, executeCaseWithHook(session, authorizer, root, "cancel_before_release", cancelPath, "cancel-before-release-1", "worker may start, but no read capability is released", func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint == readbrokerstudy.CheckpointBeforeHandoff {
			return context.Canceled
		}
		return nil
	}))

	revokePath := filepath.Join(root, "revoke-after-capture.fixture")
	if err := os.WriteFile(revokePath, []byte("synthetic captured fixture\n"), 0o600); err != nil {
		return nil, err
	}
	revokeID := "revoke-after-capture-1"
	results = append(results, executeCaseWithHook(session, authorizer, root, "revoke_after_capture_before_publish", revokePath, revokeID, "captured bytes are not published after grant generation changes", func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint == readbrokerstudy.CheckpointAfterCapture {
			return session.Revoke(revokeID)
		}
		return nil
	}))

	// Directory registration intentionally comes last: unpinned regular files
	// sharing that filesystem are fail-closed for the rest of the registry's
	// lifetime, including unrelated synthetic fixture files.
	protectedDir = filepath.Join(root, "registered-directory")
	if err := os.Mkdir(protectedDir, 0o700); err != nil {
		return nil, err
	}
	if err := session.Register(readbrokerstudy.OwnerRegistration{Owner: "fixture-directory", Class: readbrokerstudy.ClassSettings, Path: protectedDir, Directory: true}); err != nil {
		return nil, err
	}
	descendant := filepath.Join(protectedDir, "descendant.fixture")
	if err := os.WriteFile(descendant, []byte("synthetic directory descendant\n"), 0o600); err != nil {
		return nil, err
	}
	results = append(results, executeCase(session, authorizer, root, "registered_directory_descendant_inside", descendant, "directory-child-1", "descendant is refused while located beneath registered directory"))
	movedDescendant := filepath.Join(root, "moved-directory-descendant.fixture")
	if err := os.Rename(descendant, movedDescendant); err != nil {
		return nil, err
	}
	results = append(results, executeCase(session, authorizer, root, "registered_directory_descendant_moved_out", movedDescendant, "directory-child-moved-1", "unpinned same-device descendant is refused for incomplete identity coverage"))
	return results, nil
}

func executeCase(session readbrokerstudy.FixtureSession, auth *readbrokerstudy.OnceGrantFixture, root, name, path, invocationID, expected string) caseResult {
	return executeCaseWithHook(session, auth, root, name, path, invocationID, expected, nil)
}

func executeCaseWithHook(session readbrokerstudy.FixtureSession, auth *readbrokerstudy.OnceGrantFixture, root, name, path, invocationID, expected string, hook readbrokerstudy.CheckpointHook) caseResult {
	_ = auth.ApproveOnce(invocationID)
	expectedBytes, snapshotErr := os.ReadFile(path)
	if snapshotErr != nil {
		return caseResult{Name: name, Status: "fixture_error", Expected: expected, Observed: "synthetic fixture snapshot could not be read", FixtureEffect: "none", AuthorityA: "no conclusion", AuthorityB: "no conclusion", Detail: "fixture setup failed before invocation"}
	}
	session.SetCheckpointHook(hook)
	defer session.SetCheckpointHook(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	result, err := session.Read(ctx, root, path, invocationID)
	if err != nil {
		return caseResult{Name: name, Status: "failed", Expected: expected, Observed: "fixture coordinator returned an error", FixtureEffect: "unknown", AuthorityA: "no conclusion", AuthorityB: "no conclusion", Detail: "coordinator error suppressed"}
	}
	evidence := session.Evidence()
	var ev readbrokerstudy.ReadEvidence
	for _, item := range evidence {
		if item.Case == invocationID {
			ev = item
		}
	}
	expectedOutcome := readbrokerstudy.ReadRefused
	switch name {
	case "ordinary_positive", "provider_source_explicit_once":
		expectedOutcome = readbrokerstudy.ReadSucceeded
	case "cancel_before_release", "revoke_after_capture_before_publish":
		expectedOutcome = readbrokerstudy.ReadCancelled
	}
	bytesMatch := result.Outcome == readbrokerstudy.ReadSucceeded && bytes.Equal(result.Bytes, expectedBytes)
	consumptionCount := auth.ConsumptionCount(invocationID)
	stateMatches := result.Outcome == expectedOutcome
	switch expectedOutcome {
	case readbrokerstudy.ReadSucceeded:
		stateMatches = stateMatches && bytesMatch && ev.WorkerStarted && ev.CapabilitySent && ev.ReadStarted && ev.Published && consumptionCount == 1
	case readbrokerstudy.ReadRefused:
		stateMatches = stateMatches && !ev.WorkerStarted && !ev.CapabilitySent && !ev.ReadStarted && !ev.Published && consumptionCount == 0 && len(result.Bytes) == 0
	case readbrokerstudy.ReadCancelled:
		stateMatches = stateMatches && ev.WorkerStarted && !ev.Published && len(result.Bytes) == 0 && consumptionCount == 1
		if name == "cancel_before_release" {
			stateMatches = stateMatches && !ev.CapabilitySent && !ev.ReadStarted
		} else {
			stateMatches = stateMatches && ev.CapabilitySent && ev.ReadStarted
		}
	}
	status := "unexpected"
	if stateMatches {
		status = "measured_pass"
	} else if expectedOutcome != readbrokerstudy.ReadRefused && !ev.WorkerStarted {
		status = "unavailable"
	}
	a := "no conclusion"
	b := "no conclusion"
	if result.Outcome == readbrokerstudy.ReadSucceeded {
		a = "pass for this non-racing read only; no live-path race conclusion"
		b = "consistent with object-capability read; not proof of full contract B"
	} else if result.Outcome == readbrokerstudy.ReadRefused {
		a = "not required for protected fixture; refusal observed"
		b = "not applicable; access was refused"
	}
	effect := "unexpected fixture state"
	if status == "unavailable" {
		effect = "candidate worker did not start; no read effect established"
	}
	if stateMatches {
		switch expectedOutcome {
		case readbrokerstudy.ReadSucceeded:
			effect = "synthetic target bytes read by worker and published exactly once"
		case readbrokerstudy.ReadRefused:
			effect = "refused before worker launch and capability transfer"
		case readbrokerstudy.ReadCancelled:
			if name == "cancel_before_release" {
				effect = "worker joined without receiving read capability"
			} else {
				effect = "worker completed capture; bytes suppressed before publication"
			}
		}
	}
	detail := result.Detail
	if detail == "" {
		detail = "fixture result bytes and filesystem paths omitted"
	}
	return caseResult{Name: name, Status: status, Expected: expected, Observed: string(result.Outcome), WorkerStarted: ev.WorkerStarted, CapabilitySent: ev.CapabilitySent, ReadStarted: ev.ReadStarted, Published: ev.Published, BytesMatchTarget: bytesMatch, OnceGrantConsumptions: consumptionCount, WorkerProbe: ev.Probe, FixtureEffect: effect, AuthorityA: a, AuthorityB: b, Detail: boundText(detail, 768)}
}

func boundText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func inspectEnvironment(bwrap string) environment {
	return inspectEnvironmentWithBackend(bwrap, readbrokerstudy.InspectBubblewrap(bwrap))
}

func inspectEnvironmentWithBackend(bwrap string, backend readbrokerstudy.BackendInspection) environment {
	result := environment{OS: runtime.GOOS, Architecture: runtime.GOARCH, GoVersion: runtime.Version(), Restrictions: map[string]string{}, NamespaceProbeStatus: "not independently probed; worker startup availability and worker-level probes are recorded per case", CgroupDelegationStatus: "not tested; host cgroup hierarchy is not modified", AppArmorProfileStatus: "unavailable until read-only profile listing succeeds"}
	result.UID = uint32(os.Getuid())
	if executable, err := os.Executable(); err == nil {
		if data, readErr := os.ReadFile(executable); readErr == nil {
			sum := sha256.Sum256(data)
			result.StudyExecutableSHA256 = hex.EncodeToString(sum[:])
		}
	}
	var uname unix.Utsname
	if unix.Uname(&uname) == nil {
		result.Kernel = strings.TrimRight(string(uname.Release[:]), "\x00")
	}
	if stat, err := os.Stat("/proc/sys/kernel/unprivileged_userns_clone"); err == nil && stat.Mode().IsRegular() {
		if value, err := os.ReadFile("/proc/sys/kernel/unprivileged_userns_clone"); err == nil {
			result.UserNamespaceClone = strings.TrimSpace(string(value))
		}
	}
	if info, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err == nil && info.Mode().IsRegular() {
		result.CgroupV2 = true
		if value, err := os.ReadFile("/sys/fs/cgroup/cgroup.controllers"); err == nil {
			result.CgroupControllers = strings.Fields(string(value))
		}
	}
	if value, err := os.ReadFile("/proc/sys/kernel/seccomp/actions_avail"); err == nil {
		for _, action := range strings.Fields(string(value)) {
			if action == "kill_process" && strings.Contains(string(value), "errno") {
				result.SeccompActionAvailable = true
			}
		}
	}
	if _, err := os.ReadFile("/sys/kernel/security/apparmor/profiles"); err == nil {
		result.AppArmorProfileStatus = "profile list readable; active profile names omitted from report"
	} else if errors.Is(err, os.ErrPermission) {
		result.AppArmorProfileStatus = "read denied; active profile could not be established"
	}
	if value, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, line := range strings.Split(string(value), "\n") {
			if strings.HasPrefix(line, "Seccomp:") || strings.HasPrefix(line, "NoNewPrivs:") {
				key, val, _ := strings.Cut(line, ":")
				result.Restrictions[strings.ToLower(key)] = strings.TrimSpace(val)
			}
		}
	}
	result.BubblewrapPath = backend.Path
	result.BubblewrapUID = backend.UID
	if backend.Mode != 0 {
		result.BubblewrapMode = fmt.Sprintf("%04o", backend.Mode)
	}
	result.BubblewrapSHA256 = backend.SHA256
	result.BubblewrapVersion = backend.Version
	result.BubblewrapTrusted = backend.Trusted
	result.BubblewrapCapabilities = append([]string(nil), backend.Capabilities...)
	result.BubblewrapBindMode = backend.BindMode
	result.BubblewrapError = backend.Error
	return result
}

func effectiveBubblewrapArguments(mode readbrokerstudy.BindMode) []string {
	return readbrokerstudy.EffectiveBubblewrapArguments(mode)
}

func workerMountPolicy(mode readbrokerstudy.BindMode) (string, string) {
	switch mode {
	case readbrokerstudy.BindModeDescriptor:
		return "Bubblewrap consumes sealed memfd FD 5 as a read-only descriptor bind at /worker; the fixed bootstrap compares /worker device/inode to independent FD 6 before seccomp",
			"fixed worker starts as PID 1; compares /worker device/inode with independent fd 6 before installing seccomp; fd 6 and seccomp fd 3 close before readiness; consumed fd 5 is absent"
	case readbrokerstudy.BindModeFileSnapshot:
		return "Bubblewrap 0.9 uses --perms 0500 --ro-bind-data FD 5 /worker: it copies sealed memfd bytes to a private staging file, applies executable mode, read-only bind-mounts it into the empty worker root, then consumes FD 5",
			"trusted parent snapshot and Bubblewrap copy establish the executable bytes before first execution; copied bootstrap hashes /worker against sealed verifier FD 6, closes it, installs seccomp, and probes before readiness; self-hash is not an independent trust anchor"
	default:
		return "unavailable", "unavailable"
	}
}

func stableError(err error) string {
	message := err.Error()
	if strings.Contains(message, "/") {
		return "trusted backend or fixed worker validation failed; see local stderr for bounded diagnostics"
	}
	return message
}
