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
	"strconv"
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
	Name                  string                         `json:"name"`
	Status                string                         `json:"status"`
	Classification        string                         `json:"classification"`
	Expected              string                         `json:"expected"`
	Observed              string                         `json:"observed"`
	WorkerStarted         bool                           `json:"worker_started"`
	WorkerReady           bool                           `json:"worker_ready"`
	WorkerJoinConfirmed   bool                           `json:"worker_join_confirmed"`
	CapabilitySent        bool                           `json:"capability_sent"`
	ReadStarted           bool                           `json:"read_started"`
	Published             bool                           `json:"published"`
	ResultBytesReturned   bool                           `json:"result_bytes_returned"`
	BytesMatchTarget      bool                           `json:"bytes_match_target"`
	BytesMatchApproved    bool                           `json:"bytes_match_approved_object"`
	ReadEvidencePresent   bool                           `json:"read_evidence_present"`
	ReadEvidenceCase      string                         `json:"read_evidence_case,omitempty"`
	WorkerOutcome         readbrokerstudy.ReadOutcome    `json:"worker_outcome,omitempty"`
	WorkerGenerations     *readbrokerstudy.GenerationSet `json:"worker_generations,omitempty"`
	WorkerEffect          string                         `json:"worker_effect,omitempty"`
	WorkerLimitation      string                         `json:"worker_limitation,omitempty"`
	OnceGrantConsumptions int                            `json:"once_grant_consumptions"`
	ActorStarted          bool                           `json:"actor_started"`
	ActorStartState       string                         `json:"actor_start_state,omitempty"`
	ActorReady            bool                           `json:"actor_ready"`
	ActorTransition       bool                           `json:"actor_transition_completed"`
	ActorExitConfirmed    bool                           `json:"actor_exit_confirmed"`
	ActorDetail           string                         `json:"actor_detail,omitempty"`
	GenerationsBefore     *readbrokerstudy.GenerationSet `json:"generations_before,omitempty"`
	GenerationsAfter      *readbrokerstudy.GenerationSet `json:"generations_after,omitempty"`
	WorkerProbe           readbrokerstudy.WorkerProbe    `json:"worker_probe,omitempty"`
	FixtureEffect         string                         `json:"fixture_effect"`
	AuthorityA            string                         `json:"authority_a"`
	AuthorityB            string                         `json:"authority_b"`
	Detail                string                         `json:"detail,omitempty"`
}

func main() {
	if handled, err := readbrokerstudy.RunNativeActorMode(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, "read-broker-study actor:", err)
			os.Exit(1)
		}
		return
	}
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
			WorkerSnapshot:        workerSnapshotPolicy(backend.BindMode),
			EffectiveArguments:    effectiveBubblewrapArguments(backend.BindMode),
			WorkerBindMode:        backend.BindMode,
			RuntimeReadExceptions: []string{"none"},
			Environment:           "host command environment contains only synthetic sentinel; Bubblewrap --clearenv; worker verifies sentinel absent and any PWD is /",
			InheritedDescriptors:  workerDescriptors(backend.BindMode),
			CapabilityHandoff:     "one read-only regular-file FD by SCM_RIGHTS only after protected-location admission, worker readiness, once authorization, and generation revalidation",
			WorkerVerification:    workerVerification,
			Seccomp:               "fixed worker sets no_new_privs and installs architecture-checked BPF with SECCOMP_FILTER_FLAG_TSYNC; pathname opens, socket creation, clone3, and process-like clone flags are denied; only the exact Go runtime thread clone flag sets are allowed; probes run before readiness",
			ResourceLimits:        workerResourceLimits(backend.BindMode),
			UnrestrictedFallback:  false,
		},
		Limitations: []string{
			"typed-read experiments do not establish shell safety",
			"aggregate cgroup quotas are deferred; exhaustion safety is not claimed",
			"object identity cannot recognize independent copies of sensitive bytes",
			"policy scans are bounded observations and do not serialize independent namespace writers",
			"read admission refuses mutable credential/settings/database locations and ordinary reads exposed to mutable provider locations before once-grant consumption; this makes synthetic user-owned-root worker scenarios unavailable",
			"a pinned directory does not pin every current or future descendant; unpinned same-device files are refused",
			"the bootstrap uses --as-pid-1 so Bubblewrap's --sync-fd can pass verifier FD 6; worker seccomp allows only the Go runtime's exact shared-thread clone flag sets, denies clone3 and process-like clone, and needs native startup validation",
			"Bubblewrap 0.9 file-snapshot mode requires host /proc/self/exe and sealed memfd support; it refuses the worker if the running image exceeds 7 MiB or snapshot/copy verification fails",
			"the 0.9 staging verifier runs inside the copied worker, so its independent trust comes from the already-running parent image, sealed memfd bytes, and trusted Bubblewrap copy; the self-comparison is an integrity check, not its own trust anchor",
			"the independent actor uses only disposable synthetic fixture paths and a fixed operation allowlist",
			"the actor suite exercises exact-file protected aliases but does not establish live credential rotation or protected-directory descendant identity coverage",
			"a protected-directory ancestor race remains ineligible when the candidate shares its filesystem because incomplete descendant coverage refuses reads before worker setup",
			"mount crossing, recursive-child relocation, parent-death and incomplete-shutdown cleanup have no integrated actor scenario",
		},
		NativeGaps: []string{
			"the former scan-to-handoff protected-alias race is no longer re-run because mutable protected-location ancestry is refused before preparation; a separate actor case measures only the earlier admission refusal",
			"credential rotation with old and new protected identities/aliases in one live sandboxed session",
			"registered protected-ancestor relocation/replacement while a sandboxed read remains eligible; current same-device descendant coverage refuses before worker setup",
			"mount-boundary behavior and recursive-child relocation",
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
		{"registered_directory_descendant_moved_out", "scope refused before authorization because protected-directory descendant identity coverage is incomplete"},
		{"actor_protected_alias_scan_to_handoff", "no protected object capability is transferred after an independent actor introduces a protected alias"},
		{"actor_protected_alias_admission_refusal", "an independent actor creates an alias before read admission, which refuses before approval consumption or worker capability release"},
		{"actor_selected_root_relocated_after_scan", "the opened object remains readable after its selected root object moves outside the selected pathname under B"},
		{"actor_parent_relocated_outside_root_after_scan", "the opened object remains readable after its parent moves outside the selected root under B"},
		{"actor_target_replaced_after_classification", "the approved opened object is read; the replacement at its former path is never read"},
		{"actor_unregistered_protected_descendant_moved_out", "independent actor moves an unregistered child out and coverage refusal occurs before authorization or worker launch"},
		{"actor_protected_ancestor_relocation", "pending: protected-directory coverage currently refuses eligible same-device targets before an actor race can be staged"},
		{"study_session_cleanup", "bounded session close and confirmed join for every started worker"},
	}
	results := make([]caseResult, 0, len(planned))
	for _, item := range planned {
		result := caseResult{Name: item.name, Status: "unavailable", Classification: "unavailable", Expected: item.expected, Observed: "candidate profile refused before fixture creation; no worker or actor scenario started", FixtureEffect: "none", AuthorityA: "no conclusion", AuthorityB: "no conclusion", Detail: reason}
		if strings.HasPrefix(item.name, "actor_") {
			result.ActorStartState = "not_attempted_candidate_unavailable"
		}
		results = append(results, result)
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

func runFixtures(bwrap string) (results []caseResult, runErr error) {
	worker, err := os.Executable()
	if err != nil {
		return results, err
	}
	launcher, err := readbrokerstudy.NewWorkerLauncher(bwrap, worker)
	if err != nil {
		return results, err
	}
	base, err := os.MkdirTemp("", "komposyn-readbroker-fixture-")
	if err != nil {
		return results, err
	}
	defer os.RemoveAll(base)
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		return results, err
	}
	protectedDir := filepath.Join(root, "protected")
	if err := os.Mkdir(protectedDir, 0o700); err != nil {
		return results, err
	}
	protected := filepath.Join(protectedDir, "credential.fixture")
	ordinary := filepath.Join(root, "ordinary.fixture")
	if err := os.WriteFile(ordinary, []byte("fixture-only-ordinary-content\n"), 0o600); err != nil {
		return results, err
	}
	if err := os.WriteFile(protected, []byte("fixture-only-protected-content\n"), 0o600); err != nil {
		return results, err
	}
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		return results, err
	}
	authorizer := readbrokerstudy.NewOnceGrantFixture()
	session, err := readbrokerstudy.NewFixtureSession(registry, readbrokerstudy.NewBroker(registry), launcher, authorizer)
	if err != nil {
		_ = registry.Close()
		return results, err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		closeErr := session.Close(closeCtx)
		cancel()
		allWorkersJoined := true
		for _, item := range session.Evidence() {
			if item.WorkerStarted && !item.WorkerJoined {
				allWorkersJoined = false
				break
			}
		}
		cleanup := caseResult{
			Name: "study_session_cleanup", Expected: "session shutdown is bounded and every started worker is joined",
			WorkerJoinConfirmed: allWorkersJoined,
			AuthorityA:          "no conclusion", AuthorityB: "no conclusion",
		}
		if closeErr == nil && allWorkersJoined {
			cleanup.Status = "measured_pass"
			cleanup.Classification = "coordinator_control"
			cleanup.Observed = "session closed within the cleanup deadline"
			cleanup.FixtureEffect = "every started direct worker child was joined"
		} else {
			cleanup.Status = "inconclusive"
			cleanup.Classification = "inconclusive"
			cleanup.Observed = "session cleanup did not establish complete direct-worker joins"
			cleanup.FixtureEffect = "cleanup completion is unknown or incomplete"
			cleanup.Detail = "session close returned an error or a started worker lacks confirmed join evidence"
		}
		results = append(results, cleanup)
	}()
	if err := session.SelectRoot(root); err != nil {
		return results, err
	}
	if err := session.Register(readbrokerstudy.OwnerRegistration{Owner: "fixture-auth", Class: readbrokerstudy.ClassCredential, Path: protected}); err != nil {
		return results, err
	}
	if admissionErr := registry.ReadAdmission(readbrokerstudy.ClassOrdinary); admissionErr != nil {
		results, err = runProtectedAliasAdmissionCase(session, authorizer, worker, root, ordinary)
		if err != nil {
			return results, err
		}
		results = append(results, admissionUnavailableCases("not run: conservative admission refused the synthetic user-owned protected-location ancestry")...)
		return results, nil
	}

	results = make([]caseResult, 0, 24)
	results = append(results, executeCase(session, authorizer, root, "ordinary_positive", ordinary, "ordinary-1", "explicit once approval reads the opened fixture object"))
	results = append(results, executeCase(session, authorizer, root, "protected_direct", protected, "credential-1", "credential object is refused before worker capability handoff"))

	providerSource := filepath.Join(root, "provider-source.fixture")
	if err := os.WriteFile(providerSource, []byte("synthetic provider source\n"), 0o600); err != nil {
		return results, err
	}
	if err := session.Register(readbrokerstudy.OwnerRegistration{Owner: "fixture-provider", Class: readbrokerstudy.ClassProvider, Path: providerSource}); err != nil {
		return results, err
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
		return results, err
	}
	if err := session.Register(readbrokerstudy.OwnerRegistration{Owner: "fixture-move", Class: readbrokerstudy.ClassCredential, Path: moveSource}); err != nil {
		return results, err
	}
	if err := os.Rename(moveSource, moveTarget); err != nil {
		return results, err
	}
	results = append(results, executeCase(session, authorizer, root, "protected_object_renamed_into_ordinary_tree", moveTarget, "credential-renamed-1", "pinned protected object remains denied after rename"))

	replacement := filepath.Join(root, "protected", "replace-me.fixture")
	if err := os.WriteFile(replacement, []byte("synthetic protected original\n"), 0o600); err != nil {
		return results, err
	}
	if err := session.Register(readbrokerstudy.OwnerRegistration{Owner: "fixture-replace", Class: readbrokerstudy.ClassCredential, Path: replacement}); err != nil {
		return results, err
	}
	if err := os.Remove(replacement); err != nil {
		return results, err
	}
	if err := os.WriteFile(replacement, []byte("synthetic replacement\n"), 0o600); err != nil {
		return results, err
	}
	results = append(results, executeCase(session, authorizer, root, "protected_location_replaced_before_open", replacement, "credential-replacement-1", "registered protected location remains denied after replacement"))

	reserved := filepath.Join(root, "protected", "reserved.fixture")
	ordinaryToProtected := filepath.Join(root, "ordinary-to-protected.fixture")
	if err := session.Register(readbrokerstudy.OwnerRegistration{Owner: "fixture-reservation", Class: readbrokerstudy.ClassCredential, Path: reserved}); err != nil {
		return results, err
	}
	if err := os.WriteFile(ordinaryToProtected, []byte("synthetic moved object\n"), 0o600); err != nil {
		return results, err
	}
	if err := os.Rename(ordinaryToProtected, reserved); err != nil {
		return results, err
	}
	results = append(results, executeCase(session, authorizer, root, "ordinary_object_moved_into_protected_location", reserved, "moved-into-protected-1", "reserved protected location denies an object moved into it"))

	cancelPath := filepath.Join(root, "cancel-before-release.fixture")
	if err := os.WriteFile(cancelPath, []byte("synthetic cancel fixture\n"), 0o600); err != nil {
		return results, err
	}
	results = append(results, executeCaseWithHook(session, authorizer, root, "cancel_before_release", cancelPath, "cancel-before-release-1", "worker may start, but no read capability is released", func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint == readbrokerstudy.CheckpointBeforeHandoff {
			return context.Canceled
		}
		return nil
	}))

	revokePath := filepath.Join(root, "revoke-after-capture.fixture")
	if err := os.WriteFile(revokePath, []byte("synthetic captured fixture\n"), 0o600); err != nil {
		return results, err
	}
	revokeID := "revoke-after-capture-1"
	results = append(results, executeCaseWithHook(session, authorizer, root, "revoke_after_capture_before_publish", revokePath, revokeID, "captured bytes are not published after grant generation changes", func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint == readbrokerstudy.CheckpointAfterCapture {
			return session.Revoke(revokeID)
		}
		return nil
	}))

	rootScope := filepath.Join(root, "actor-root-scope")
	rootTarget := filepath.Join(rootScope, "ordinary.fixture")
	rootMoved := filepath.Join(root, "actor-root-relocated")
	if err := os.Mkdir(rootScope, 0o700); err != nil {
		return results, err
	}
	if err := os.WriteFile(rootTarget, []byte("approved object in selected root\n"), 0o600); err != nil {
		return results, err
	}
	if err := session.SelectRoot(rootScope); err != nil {
		return results, err
	}
	results = append(results, executeNativeActorRaceCase(session, authorizer, worker, root, rootScope, rootTarget, "actor_selected_root_relocated_after_scan", "actor-root-relocate-1", "the selected root object is renamed outside its selected pathname after final revalidation; only the opened object may be read", readbrokerstudy.NativeActorRename, "actor-root-scope", "actor-root-relocated", "supports_b", func() bool {
		return !pathExists(rootScope) && pathExists(filepath.Join(rootMoved, "ordinary.fixture"))
	}, "actor moved the selected root object; the opened fixture object remained available"))

	parentScope := filepath.Join(root, "actor-parent-scope")
	parentDir := filepath.Join(parentScope, "nested")
	parentTarget := filepath.Join(parentDir, "ordinary.fixture")
	parentMoved := filepath.Join(root, "actor-parent-relocated")
	if err := os.MkdirAll(parentDir, 0o700); err != nil {
		return results, err
	}
	if err := os.WriteFile(parentTarget, []byte("approved object in selected parent\n"), 0o600); err != nil {
		return results, err
	}
	if err := session.SelectRoot(parentScope); err != nil {
		return results, err
	}
	results = append(results, executeNativeActorRaceCase(session, authorizer, worker, root, parentScope, parentTarget, "actor_parent_relocated_outside_root_after_scan", "actor-parent-relocate-1", "the opened parent moves outside the selected root after final revalidation; only the opened object may be read", readbrokerstudy.NativeActorRename, "actor-parent-scope/nested", "actor-parent-relocated", "supports_b", func() bool {
		return !pathExists(parentDir) && pathExists(filepath.Join(parentMoved, "ordinary.fixture"))
	}, "actor moved the opened parent outside the selected root; the opened fixture object remained available"))

	targetScope := filepath.Join(root, "actor-target-scope")
	targetPath := filepath.Join(targetScope, "replace.fixture")
	targetArchive := filepath.Join(targetScope, "approved-open-object.fixture")
	if err := os.Mkdir(targetScope, 0o700); err != nil {
		return results, err
	}
	if err := os.WriteFile(targetPath, []byte("original approved object bytes\n"), 0o600); err != nil {
		return results, err
	}
	if err := session.SelectRoot(targetScope); err != nil {
		return results, err
	}
	results = append(results, executeNativeActorRaceCase(session, authorizer, worker, root, targetScope, targetPath, "actor_target_replaced_after_classification", "actor-target-replace-1", "the target pathname is replaced after final revalidation; the approved open object, not the substitute, is read", readbrokerstudy.NativeActorReplace, "actor-target-scope/replace.fixture", "actor-target-scope/approved-open-object.fixture", "supports_b", func() bool {
		got, err := os.ReadFile(targetPath)
		return err == nil && string(got) == "actor-substitute\n" && pathExists(targetArchive)
	}, "actor replaced the target pathname and retained the opened object under a fixture archive name"))

	aliasScope := filepath.Join(root, "actor-alias-scope")
	aliasTarget := filepath.Join(aliasScope, "ordinary.fixture")
	protectedAlias := filepath.Join(root, "actor-protected-alias.fixture")
	if err := os.Mkdir(aliasScope, 0o700); err != nil {
		return results, err
	}
	if err := os.WriteFile(aliasTarget, []byte("ordinary object later aliased into protected location\n"), 0o600); err != nil {
		return results, err
	}
	if err := session.Register(readbrokerstudy.OwnerRegistration{Owner: "fixture-race-protected-alias", Class: readbrokerstudy.ClassCredential, Path: protectedAlias}); err != nil {
		return results, err
	}
	if err := session.SelectRoot(aliasScope); err != nil {
		return results, err
	}
	results = append(results, executeNativeActorRaceCase(session, authorizer, worker, root, aliasScope, aliasTarget, "actor_protected_alias_scan_to_handoff", "actor-protected-alias-1", "a registered protected file location receives a hard link to the prepared ordinary object after final revalidation; no worker read may occur", readbrokerstudy.NativeActorHardlink, "actor-alias-scope/ordinary.fixture", "actor-protected-alias.fixture", "violation", func() bool {
		ordinaryInfo, ordinaryErr := os.Stat(aliasTarget)
		protectedInfo, protectedErr := os.Stat(protectedAlias)
		return ordinaryErr == nil && protectedErr == nil && os.SameFile(ordinaryInfo, protectedInfo)
	}, "actor introduced a hard-link alias at a registered protected file location"))

	// Directory registration intentionally comes last: unpinned regular files
	// sharing that filesystem are fail-closed for the rest of the registry's
	// lifetime, including unrelated synthetic fixture files. The helper also
	// restores the selected root after the preceding actor cases.
	directoryCases, err := runDirectoryCoverageCases(session, authorizer, root)
	if err != nil {
		return results, err
	}
	results = append(results, directoryCases...)
	protectedDir = filepath.Join(root, "registered-directory")
	actorMovedDescendant := filepath.Join(root, "actor-moved-directory-descendant.fixture")
	actorDescendant := filepath.Join(protectedDir, "actor-descendant.fixture")
	if err := os.WriteFile(actorDescendant, []byte("synthetic actor-moved protected directory child\n"), 0o600); err != nil {
		return results, err
	}
	actorContext, actorCancel := context.WithTimeout(context.Background(), 5*time.Second)
	actor, actorErr := readbrokerstudy.NewNativeActor(actorContext, worker, root, readbrokerstudy.NativeActorRename, "registered-directory/actor-descendant.fixture", "actor-moved-directory-descendant.fixture")
	if actorErr != nil {
		actorCancel()
		results = append(results, unavailableActorCase("actor_unregistered_protected_descendant_moved_out", "independent actor moves an unregistered child out of a registered protected directory before preparation; coverage refusal must precede authorization and worker launch", "actor fixture process did not become ready"))
	} else {
		actorErr = actor.Run(actorContext)
		actorCloseErr := actor.Close()
		actorCancel()
		if actorErr != nil || actorCloseErr != nil || !actor.TransitionCompleted() || !actor.Joined() || !pathExists(actorMovedDescendant) {
			results = append(results, unavailableActorCase("actor_unregistered_protected_descendant_moved_out", "independent actor moves an unregistered child out of a registered protected directory before preparation; coverage refusal must precede authorization and worker launch", "actor fixture transition or process cleanup was not confirmed"))
		} else {
			refused := executeCase(session, authorizer, root, "actor_unregistered_protected_descendant_moved_out", actorMovedDescendant, "actor-directory-child-moved-1", "independent actor moved an unregistered child; incomplete directory coverage must refuse before once-grant consumption or worker launch")
			refused.ActorStarted = true
			refused.ActorReady = true
			refused.ActorTransition = actor.TransitionCompleted()
			refused.ActorExitConfirmed = actor.Joined()
			refused.ActorDetail = "independent actor transition completed before read preparation"
			if refused.Status == "measured_pass" {
				refused.Classification = "protected_rule_enforced"
				refused.FixtureEffect = "actor moved the protected-directory child; read refused before authorization and worker launch"
			} else {
				refused.Status = "inconclusive"
				refused.Classification = "inconclusive"
			}
			results = append(results, refused)
		}
	}
	results = append(results, caseResult{Name: "actor_protected_ancestor_relocation", Status: "inconclusive", Classification: "inconclusive", Expected: "measure a protected-directory ancestor transition around an eligible worker read", Observed: "no eligible same-device ordinary target can pass the existing incomplete-descendant-coverage refusal", FixtureEffect: "no actor transition attempted", AuthorityA: "no conclusion", AuthorityB: "no conclusion", Detail: "pending: a pinned protected directory on the candidate file's device makes generic regular-file reads ineligible before worker launch; do not weaken this gate to stage the race"})
	return results, nil
}

func runProtectedAliasAdmissionCase(session readbrokerstudy.FixtureSession, auth *readbrokerstudy.OnceGrantFixture, executable, root, ordinary string) ([]caseResult, error) {
	name := "actor_protected_alias_admission_refusal"
	expected := "an independent actor creates a late protected hard-link alias before the read request; admission refuses before once consumption or worker handoff"
	protectedAlias := filepath.Join(root, "reserved-alias.fixture")
	if err := session.Register(readbrokerstudy.OwnerRegistration{Owner: "fixture-late-alias", Class: readbrokerstudy.ClassCredential, Path: protectedAlias}); err != nil {
		return nil, err
	}
	if err := session.SelectRoot(root); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	actor, err := readbrokerstudy.NewNativeActor(ctx, executable, root, readbrokerstudy.NativeActorHardlink, filepath.Base(ordinary), filepath.Base(protectedAlias))
	if err != nil {
		return []caseResult{unavailableActorCase(name, expected, "independent actor did not become ready: "+boundText(err.Error(), 256))}, nil
	}
	actorReady := actor.Ready()
	transitionErr := actor.Run(ctx)
	closeErr := actor.Close()
	actorTransition := actor.TransitionCompleted()
	actorJoined := actor.Joined()
	ordinaryInfo, ordinaryErr := os.Stat(ordinary)
	aliasInfo, aliasErr := os.Stat(protectedAlias)
	fixtureAlias := ordinaryErr == nil && aliasErr == nil && os.SameFile(ordinaryInfo, aliasInfo)
	if err := auth.ApproveOnce("protected-alias-admission-1"); err != nil {
		return []caseResult{unavailableActorCase(name, expected, "fixture once approval could not be staged")}, nil
	}
	generationsBefore := session.GenerationSnapshot()
	result, readErr := session.Read(ctx, root, ordinary, "protected-alias-admission-1")
	generationsAfter := session.GenerationSnapshot()
	var readEvidence readbrokerstudy.ReadEvidence
	evidenceFound := false
	for _, evidence := range session.Evidence() {
		if evidence.Case == "protected-alias-admission-1" {
			readEvidence = evidence
			evidenceFound = true
		}
	}
	return []caseResult{protectedAliasAdmissionResult(
		name, expected, "registered credential locations must remain unavailable to generic reads",
		readbrokerstudy.ErrProtectedLocationMutable.Error(),
		true, actorReady, actorTransition, actorJoined, fixtureAlias,
		transitionErr != nil || closeErr != nil, result, readErr, readEvidence, evidenceFound,
		auth.ConsumptionCount("protected-alias-admission-1"), generationsBefore, generationsAfter,
	)}, nil
}

func protectedAliasAdmissionResult(name, expected, protectedRule, admissionDetail string, actorStarted, actorReady, actorTransition, actorExited, fixtureAlias, actorLifecycleError bool, result readbrokerstudy.WorkerResult, readErr error, evidence readbrokerstudy.ReadEvidence, evidenceFound bool, onceConsumptions int, generationsBefore, generationsAfter readbrokerstudy.GenerationSet) caseResult {
	activity := evidence.CapabilitySent || evidence.ReadStarted || evidence.Published || len(result.Bytes) > 0
	actorConfirmed := actorStarted && actorReady && actorTransition && actorExited && fixtureAlias && !actorLifecycleError
	status, classification := "inconclusive", "inconclusive"
	if actorConfirmed && activity {
		status, classification = "observed_violation", "violation"
	} else if actorConfirmed && readErr == nil && result.Outcome == readbrokerstudy.ReadRefused && result.Detail == admissionDetail && !evidence.WorkerStarted && !activity && onceConsumptions == 0 && len(result.Bytes) == 0 && generationsBefore == generationsAfter {
		status, classification = "measured_pass", "admission_refusal"
	} else if !actorStarted || !actorReady {
		status, classification = "unavailable", "unavailable"
	}
	observed := string(result.Outcome)
	if readErr != nil {
		observed = "coordinator_error"
	}
	actorDetail := "actor transition and direct-process exit were not fully confirmed"
	if actorConfirmed {
		actorDetail = "actor reached READY, completed the alias transition, and was reaped"
	}
	effect := fmt.Sprintf("actor_alias=%t; worker_started=%t; worker_ready=%t; worker_joined=%t; capability_sent=%t; read_started=%t; result_bytes_returned=%t; published=%t", fixtureAlias, evidence.WorkerStarted, evidence.WorkerReady, evidence.WorkerJoined, evidence.CapabilitySent, evidence.ReadStarted, len(result.Bytes) > 0, evidence.Published)
	authorityB := "no conclusion about worker object authority"
	detail := result.Detail
	if readErr != nil {
		detail = "coordinator error suppressed; observed worker evidence retained"
	}
	if status == "observed_violation" {
		authorityB = "violation of mandatory registered-location policy: " + protectedRule
		detail = "actor alias was confirmed and worker capability/read/publication activity was observed; path and content omitted"
	} else if status == "measured_pass" {
		detail = admissionDetail
	}
	if detail == "" {
		detail = "required actor and pre-admission refusal invariants were not established"
	}
	if !evidenceFound {
		evidence = readbrokerstudy.ReadEvidence{}
	}
	var workerGenerations *readbrokerstudy.GenerationSet
	if evidenceFound {
		generations := evidence.Generations
		workerGenerations = &generations
	}
	return caseResult{
		Name: name, Status: status, Classification: classification, Expected: expected, Observed: observed,
		WorkerStarted: evidence.WorkerStarted, WorkerReady: evidence.WorkerReady,
		WorkerJoinConfirmed: evidence.WorkerJoined, CapabilitySent: evidence.CapabilitySent,
		ReadStarted: evidence.ReadStarted, Published: evidence.Published,
		ResultBytesReturned: len(result.Bytes) > 0,
		BytesMatchTarget:    evidence.BytesMatchTarget, BytesMatchApproved: evidence.BytesMatchTarget,
		ReadEvidencePresent: evidenceFound, ReadEvidenceCase: evidence.Case,
		WorkerOutcome: evidence.Outcome, WorkerGenerations: workerGenerations,
		WorkerEffect: evidence.Effect, WorkerLimitation: evidence.Limitation,
		OnceGrantConsumptions: onceConsumptions, ActorStarted: actorStarted,
		ActorStartState: actorStartState(actorStarted), ActorReady: actorReady, ActorTransition: actorTransition,
		ActorExitConfirmed: actorExited, ActorDetail: actorDetail,
		GenerationsBefore: &generationsBefore, GenerationsAfter: &generationsAfter,
		WorkerProbe: evidence.Probe, FixtureEffect: effect,
		AuthorityA: "no conclusion about strict live host-path containment", AuthorityB: authorityB,
		Detail: boundText(detail, 768),
	}
}

func actorStartState(started bool) string {
	if started {
		return "started"
	}
	return "not_confirmed"
}

func admissionUnavailableCases(reason string) []caseResult {
	cases := unavailableCases(reason)
	filtered := make([]caseResult, 0, len(cases)-2)
	for _, item := range cases {
		if item.Name == "study_session_cleanup" || item.Name == "actor_protected_alias_admission_refusal" {
			continue
		}
		item.Observed = "not executed because conservative protected-location admission refused before authorization and worker setup"
		item.Detail = reason
		if strings.HasPrefix(item.Name, "actor_") {
			item.ActorStartState = "not_attempted_admission_refusal"
		}
		if item.Name == "actor_protected_alias_scan_to_handoff" {
			item.Detail = "the scan-to-handoff race was not exercised in this run; the supplied earlier native archive remains an observed violation; the separate admission-refusal case ran instead"
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func runDirectoryCoverageCases(session readbrokerstudy.FixtureSession, auth *readbrokerstudy.OnceGrantFixture, root string) ([]caseResult, error) {
	if err := session.SelectRoot(root); err != nil {
		return nil, err
	}
	protectedDir := filepath.Join(root, "registered-directory")
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
	inside := executeCase(session, auth, root, "registered_directory_descendant_inside", descendant, "directory-child-1", "descendant is refused as a registered protected resource")
	movedDescendant := filepath.Join(root, "moved-directory-descendant.fixture")
	if err := os.Rename(descendant, movedDescendant); err != nil {
		return nil, err
	}
	moved := executeCase(session, auth, root, "registered_directory_descendant_moved_out", movedDescendant, "directory-child-moved-1", "scope is refused because protected-directory descendant identity coverage is incomplete")
	return []caseResult{inside, moved}, nil
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
	generationsBefore := session.GenerationSnapshot()
	result, err := session.Read(ctx, root, path, invocationID)
	if err != nil {
		return caseResult{Name: name, Status: "failed", Expected: expected, Observed: "fixture coordinator returned an error", FixtureEffect: "unknown", AuthorityA: "no conclusion", AuthorityB: "no conclusion", Detail: "coordinator error suppressed"}
	}
	generationsAfter := session.GenerationSnapshot()
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
	case "registered_directory_descendant_moved_out":
		expectedOutcome = readbrokerstudy.ReadRefused
	}
	bytesMatch := result.Outcome == readbrokerstudy.ReadSucceeded && bytes.Equal(result.Bytes, expectedBytes)
	consumptionCount := auth.ConsumptionCount(invocationID)
	stateMatches := result.Outcome == expectedOutcome
	switch expectedOutcome {
	case readbrokerstudy.ReadSucceeded:
		stateMatches = stateMatches && bytesMatch && ev.WorkerStarted && ev.WorkerReady && ev.WorkerJoined && ev.CapabilitySent && ev.ReadStarted && ev.Published && consumptionCount == 1
	case readbrokerstudy.ReadRefused:
		stateMatches = stateMatches && !ev.WorkerStarted && !ev.CapabilitySent && !ev.ReadStarted && !ev.Published && consumptionCount == 0 && len(result.Bytes) == 0
		switch name {
		case "registered_directory_descendant_inside":
			stateMatches = stateMatches && result.Detail == readbrokerstudy.ErrProtected.Error()
		case "registered_directory_descendant_moved_out", "actor_unregistered_protected_descendant_moved_out":
			stateMatches = stateMatches && result.Detail == readbrokerstudy.ErrDirectoryCoverage.Error()
		}
	case readbrokerstudy.ReadCancelled:
		stateMatches = stateMatches && ev.WorkerStarted && ev.WorkerReady && ev.WorkerJoined && !ev.Published && len(result.Bytes) == 0 && consumptionCount == 1
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
	classification := "inconclusive"
	if stateMatches {
		switch expectedOutcome {
		case readbrokerstudy.ReadSucceeded:
			classification = "supports_b"
		case readbrokerstudy.ReadRefused:
			classification = "protected_rule_enforced"
		case readbrokerstudy.ReadCancelled:
			classification = "coordinator_control"
		}
	} else if status == "unavailable" {
		classification = "unavailable"
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
	return caseResult{Name: name, Status: status, Classification: classification, Expected: expected, Observed: string(result.Outcome), WorkerStarted: ev.WorkerStarted, WorkerReady: ev.WorkerReady, WorkerJoinConfirmed: ev.WorkerJoined, CapabilitySent: ev.CapabilitySent, ReadStarted: ev.ReadStarted, Published: ev.Published, BytesMatchTarget: bytesMatch, BytesMatchApproved: bytesMatch, OnceGrantConsumptions: consumptionCount, GenerationsBefore: &generationsBefore, GenerationsAfter: &generationsAfter, WorkerProbe: ev.Probe, FixtureEffect: effect, AuthorityA: a, AuthorityB: b, Detail: boundText(detail, 768)}
}

func executeNativeActorRaceCase(session readbrokerstudy.FixtureSession, auth *readbrokerstudy.OnceGrantFixture, executable, fixtureRoot, selectedRoot, target, name, invocationID, expected string, action readbrokerstudy.NativeActorAction, actorSource, actorTarget, expectedClassification string, verifyFixture func() bool, effect string) caseResult {
	if err := session.SelectRoot(selectedRoot); err != nil {
		return unavailableActorCase(name, expected, "selected-root fixture could not be prepared")
	}
	initialBytes, err := os.ReadFile(target)
	if err != nil {
		return unavailableActorCase(name, expected, "approved-object fixture could not be opened before the actor transition")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	actor, err := readbrokerstudy.NewNativeActor(ctx, executable, fixtureRoot, action, actorSource, actorTarget)
	if err != nil {
		return unavailableActorCase(name, expected, "independent actor did not become ready")
	}
	actorStarted := true
	actorReady := actor.Ready()
	_ = auth.ApproveOnce(invocationID)
	generationsBefore := session.GenerationSnapshot()
	var transitionErr error
	session.SetCheckpointHook(func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint != readbrokerstudy.CheckpointAfterFinalRevalidation {
			return nil
		}
		transitionErr = actor.Run(ctx)
		return transitionErr
	})
	result, readErr := session.Read(ctx, selectedRoot, target, invocationID)
	session.SetCheckpointHook(nil)
	closeErr := actor.Close()
	generationsAfter := session.GenerationSnapshot()
	actorTransition := actor.TransitionCompleted()
	actorJoined := actor.Joined()
	evidence := session.Evidence()
	var ev readbrokerstudy.ReadEvidence
	for _, item := range evidence {
		if item.Case == invocationID {
			ev = item
			break
		}
	}
	base := caseResult{
		Name: name, Expected: expected, Observed: string(result.Outcome),
		WorkerStarted: ev.WorkerStarted, WorkerReady: ev.WorkerReady,
		WorkerJoinConfirmed: ev.WorkerJoined, CapabilitySent: ev.CapabilitySent,
		ReadStarted: ev.ReadStarted, Published: ev.Published,
		BytesMatchTarget:      result.Outcome == readbrokerstudy.ReadSucceeded && bytes.Equal(result.Bytes, initialBytes),
		BytesMatchApproved:    result.Outcome == readbrokerstudy.ReadSucceeded && bytes.Equal(result.Bytes, initialBytes),
		OnceGrantConsumptions: auth.ConsumptionCount(invocationID),
		ActorStarted:          actorStarted, ActorReady: actorReady,
		ActorStartState: "started",
		ActorTransition: actorTransition, ActorExitConfirmed: actorJoined,
		GenerationsBefore: &generationsBefore, GenerationsAfter: &generationsAfter,
		WorkerProbe: ev.Probe, FixtureEffect: "fixture transition or worker outcome was not established",
		AuthorityA: "no conclusion", AuthorityB: "no conclusion",
	}
	if readErr != nil {
		base.Status = "inconclusive"
		base.Classification = "inconclusive"
		base.Detail = "fixture coordinator returned an error; actor and fixture details are omitted"
		return base
	}
	if !ev.WorkerStarted {
		base.Status = "unavailable"
		base.Classification = "unavailable"
		base.FixtureEffect = "worker did not start; no namespace-race read was established"
		base.ActorDetail = "actor reached READY and was reaped without a transition because the worker checkpoint was unavailable"
		base.Detail = "real worker startup was unavailable for this case"
		return base
	}
	if transitionErr != nil || closeErr != nil || !actorReady || !actorJoined || !actorTransition || !verifyFixture() {
		base.Status = "inconclusive"
		base.Classification = "inconclusive"
		base.ActorDetail = "actor transition, fixture state, or direct-process cleanup was not confirmed"
		base.Detail = "required independent-actor precondition was not established"
		return base
	}
	base.ActorDetail = "independent actor reached READY, completed its fixture transition, and was reaped"
	base.Detail = boundText(result.Detail, 768)
	if base.Detail == "" {
		base.Detail = "fixture paths and synthetic content omitted"
	}
	if expectedClassification == "violation" {
		if ev.CapabilitySent || ev.ReadStarted {
			base.Status = "observed_violation"
			base.Classification = "violation"
			base.FixtureEffect = "protected alias appeared after the final scan; capability was released and the worker read started; publication=" + strconv.FormatBool(ev.Published)
			base.AuthorityA = "not the selected contract; protected-location transition occurred after its last scan"
			base.AuthorityB = "violation of the mandatory protected-location rule; later publication suppression does not undo worker access"
			return base
		}
		if ev.WorkerStarted && ev.WorkerReady && ev.WorkerJoined && auth.ConsumptionCount(invocationID) == 1 && !ev.Published {
			base.Status = "measured_pass"
			base.Classification = "protected_rule_enforced"
			base.FixtureEffect = "actor created the protected alias; no read capability was released or published"
			base.AuthorityA = "not the selected contract"
			base.AuthorityB = "mandatory protected-location rule remained enforced at the handoff boundary"
			return base
		}
		base.Status = "inconclusive"
		base.Classification = "inconclusive"
		base.Detail = "protected-alias transition completed but the worker boundary did not establish a classifiable handoff result"
		return base
	}
	stateMatches := result.Outcome == readbrokerstudy.ReadSucceeded && base.BytesMatchTarget && ev.WorkerStarted && ev.WorkerReady && ev.WorkerJoined && ev.CapabilitySent && ev.ReadStarted && ev.Published && auth.ConsumptionCount(invocationID) == 1 && generationsBefore == generationsAfter
	if stateMatches {
		base.Status = "measured_pass"
		base.Classification = "supports_b"
		base.FixtureEffect = effect
		switch name {
		case "actor_selected_root_relocated_after_scan", "actor_parent_relocated_outside_root_after_scan":
			base.AuthorityA = "violation of strict live-path containment: the opened object was read after its selected pathname moved outside the authorized path"
		default:
			base.AuthorityA = "exact-target live-path authority was not preserved; the result followed the approved object after pathname replacement"
		}
		base.AuthorityB = "consistent with authority following the originally opened object; this scenario alone does not prove the full contract"
		return base
	}
	base.Status = "unexpected"
	base.Classification = "inconclusive"
	base.Detail = "actor completed but worker outcome, approved-object bytes, grant use, generation, or cleanup differed from the expected B observation"
	return base
}

func unavailableActorCase(name, expected, detail string) caseResult {
	return caseResult{Name: name, Status: "unavailable", Classification: "unavailable", Expected: expected, Observed: "actor transition was not run", FixtureEffect: "none established", AuthorityA: "no conclusion", AuthorityB: "no conclusion", ActorStartState: "not_confirmed", ActorDetail: detail, Detail: detail}
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
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
		return "Bubblewrap consumes path-backed running-executable FD 5 as a read-only descriptor bind at /worker, checks mounted device/inode against FD 5 before exec, and bootstrap rechecks against independent FD 6 before seccomp",
			"fixed worker starts as PID 1; compares /worker device/inode with independent fd 6 before installing seccomp; fd 6 and seccomp fd 3 close before readiness; consumed fd 5 is absent"
	case readbrokerstudy.BindModeFileSnapshot:
		return "Bubblewrap 0.9 uses --perms 0500 --ro-bind-data FD 5 /worker: it copies sealed memfd bytes to a private staging file, applies executable mode, read-only bind-mounts it into the empty worker root, then consumes FD 5",
			"trusted parent snapshot and Bubblewrap copy establish the executable bytes before first execution; copied bootstrap hashes /worker against sealed verifier FD 6, closes it, installs seccomp, and probes before readiness; self-hash is not an independent trust anchor"
	default:
		return "unavailable", "unavailable"
	}
}

func workerSnapshotPolicy(mode readbrokerstudy.BindMode) string {
	switch mode {
	case readbrokerstudy.BindModeDescriptor:
		return "host opens /proc/self/exe and confirms the supplied executable path names the same device/inode; FD 5 and FD 6 are independently reopened read-only descriptions of that running executable; Bubblewrap resolves the FD path, then compares the mounted inode with FD 5 before exec; user-owned path permissions are not the trust anchor"
	case readbrokerstudy.BindModeFileSnapshot:
		return "host opens /proc/self/exe, confirms the supplied executable path names the same device/inode, copies at most 7 MiB into a fully sealed memfd, and independently reopens read-only descriptions for FD 5 and FD 6; user-owned path permissions are not the trust anchor"
	default:
		return "unavailable"
	}
}

func workerDescriptors(mode readbrokerstudy.BindMode) []string {
	descriptors := []string{
		"0 /dev/null input", "1 output discarded", "2 bounded stderr capture",
		"3 exact seccomp program read then closed by bootstrap before ready",
		"4 AF_UNIX SOCK_SEQPACKET control; no read capability at startup",
	}
	switch mode {
	case readbrokerstudy.BindModeDescriptor:
		descriptors = append(descriptors,
			"5 fresh read-only description of the running executable; consumed by Bubblewrap descriptor bind",
			"6 independent read-only description retained by --sync-fd through bootstrap, then closed before seccomp/readiness")
	case readbrokerstudy.BindModeFileSnapshot:
		descriptors = append(descriptors,
			"5 fresh read-only sealed memfd description consumed by Bubblewrap byte construction",
			"6 independent read-only sealed memfd description retained by --sync-fd through bootstrap, then closed before seccomp/readiness")
	default:
		descriptors = append(descriptors, "worker source descriptors unavailable")
	}
	return descriptors
}

func workerResourceLimits(mode readbrokerstudy.BindMode) map[string]string {
	limits := map[string]string{
		"wall_clock": "5 seconds per worker", "result": "64 KiB", "stderr": "4 KiB captured",
		"worker_image": "7 MiB maximum", "scratch": "8 MiB private worker root",
		"aggregate_process_memory_cpu": "not provisioned or tested",
	}
	if mode == readbrokerstudy.BindModeFileSnapshot {
		limits["worker_snapshot"] = "7 MiB maximum; sealed memfd plus Bubblewrap's transient descriptor-fed copy; larger worker images refused"
	}
	return limits
}

func stableError(err error) string {
	message := err.Error()
	if strings.Contains(message, "/") {
		return "trusted backend or fixed worker validation failed; see local stderr for bounded diagnostics"
	}
	return message
}
