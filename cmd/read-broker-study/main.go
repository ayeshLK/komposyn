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
	"io"
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
	OwnerHoldArguments    []string                 `json:"owner_hold_bubblewrap_arguments,omitempty"`
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
	Name                           string                         `json:"name"`
	Status                         string                         `json:"status"`
	Classification                 string                         `json:"classification"`
	Expected                       string                         `json:"expected"`
	Observed                       string                         `json:"observed"`
	WorkerStarted                  bool                           `json:"worker_started"`
	WorkerReady                    bool                           `json:"worker_ready"`
	WorkerJoinConfirmed            bool                           `json:"worker_join_confirmed"`
	CapabilitySent                 bool                           `json:"capability_sent"`
	CapabilityHeld                 bool                           `json:"capability_received_held"`
	ReadStarted                    bool                           `json:"read_started"`
	Published                      bool                           `json:"published"`
	ResultBytesReturned            bool                           `json:"result_bytes_returned"`
	BytesMatchTarget               bool                           `json:"bytes_match_target"`
	BytesMatchApproved             bool                           `json:"bytes_match_approved_object"`
	ReadEvidencePresent            bool                           `json:"read_evidence_present"`
	ReadEvidenceCase               string                         `json:"read_evidence_case,omitempty"`
	WorkerOutcome                  readbrokerstudy.ReadOutcome    `json:"worker_outcome,omitempty"`
	WorkerGenerations              *readbrokerstudy.GenerationSet `json:"worker_generations,omitempty"`
	WorkerEffect                   string                         `json:"worker_effect,omitempty"`
	WorkerLimitation               string                         `json:"worker_limitation,omitempty"`
	OnceGrantConsumptions          int                            `json:"once_grant_consumptions"`
	AuthorizationAttempted         bool                           `json:"authorization_attempted"`
	AuthorizationReceiptAccepted   bool                           `json:"authorization_receipt_accepted"`
	AuthorizationReceiptInvocation string                         `json:"authorization_receipt_invocation,omitempty"`
	AuthorizationReceiptClass      readbrokerstudy.ResourceClass  `json:"authorization_receipt_class,omitempty"`
	AuthorizationReceiptIdentity   *readbrokerstudy.FileIdentity  `json:"authorization_receipt_identity,omitempty"`
	PreparedClass                  readbrokerstudy.ResourceClass  `json:"prepared_class,omitempty"`
	PreparedIdentity               *readbrokerstudy.FileIdentity  `json:"prepared_identity,omitempty"`
	CapabilityIdentity             *readbrokerstudy.FileIdentity  `json:"capability_identity,omitempty"`
	ReplayOutcome                  readbrokerstudy.ReadOutcome    `json:"replay_outcome,omitempty"`
	ReplayPrevented                bool                           `json:"replay_prevented"`
	ReplayAdditionalWorkers        int                            `json:"replay_additional_workers"`
	ActorStarted                   bool                           `json:"actor_started"`
	ActorStartState                string                         `json:"actor_start_state,omitempty"`
	ActorReady                     bool                           `json:"actor_ready"`
	ActorTransition                bool                           `json:"actor_transition_completed"`
	ActorExitConfirmed             bool                           `json:"actor_exit_confirmed"`
	ActorDetail                    string                         `json:"actor_detail,omitempty"`
	GenerationsBefore              *readbrokerstudy.GenerationSet `json:"generations_before,omitempty"`
	GenerationsAfter               *readbrokerstudy.GenerationSet `json:"generations_after,omitempty"`
	WorkerProbe                    readbrokerstudy.WorkerProbe    `json:"worker_probe,omitempty"`
	OwnerLifecycle                 *ownerLifecycleEvidence        `json:"owner_lifecycle,omitempty"`
	FixtureEffect                  string                         `json:"fixture_effect"`
	AuthorityA                     string                         `json:"authority_a"`
	AuthorityB                     string                         `json:"authority_b"`
	Detail                         string                         `json:"detail,omitempty"`
}

type ownerLifecycleEvidence struct {
	RegistrationAttempted     bool                          `json:"registration_attempted"`
	InitialRegistrationResult string                        `json:"initial_registration_result"`
	CleanupPending            bool                          `json:"cleanup_pending"`
	WorkerJoinConfirmed       bool                          `json:"worker_join_confirmed"`
	PolicyBefore              readbrokerstudy.GenerationSet `json:"policy_before"`
	PolicyAfterRegistration   readbrokerstudy.GenerationSet `json:"policy_after_registration"`
	WriteDescriptorIdentity   readbrokerstudy.FileIdentity  `json:"write_descriptor_identity"`
	PinnedIdentity            readbrokerstudy.FileIdentity  `json:"pinned_identity"`
	WriteDescriptorMatchesPin bool                          `json:"write_descriptor_matches_pin"`
	WriteAuthorized           bool                          `json:"write_authorized"`
	WriteStarted              bool                          `json:"write_started"`
	WriteCompleted            bool                          `json:"write_completed"`
	Published                 bool                          `json:"published"`
	TemporaryRouteMatchesPin  bool                          `json:"temporary_route_matches_pin"`
	EffectiveRouteReserved    bool                          `json:"effective_route_reserved"`
	RetiredIdentityRetained   bool                          `json:"retired_identity_retained"`
	Events                    []string                      `json:"events,omitempty"`
	Detail                    string                        `json:"detail,omitempty"`
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
			OwnerHoldArguments:    readbrokerstudy.EffectiveBubblewrapArgumentsWithCapabilityHold(backend.BindMode),
			WorkerBindMode:        backend.BindMode,
			RuntimeReadExceptions: []string{"none"},
			Environment:           "host command environment contains only synthetic sentinel; Bubblewrap --clearenv; worker verifies sentinel absent and any PWD is /",
			InheritedDescriptors:  workerDescriptors(backend.BindMode),
			CapabilityHandoff:     "one read-only regular-file FD by SCM_RIGHTS only after worker readiness, once authorization, and generation revalidation",
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
			"exact-file protected routes and pinned sensitive identities are enforced; external alias-only changes do not reclassify a captured ordinary invocation",
			"a pinned directory does not pin every current or future descendant; unpinned same-device files are refused",
			"the bootstrap uses --as-pid-1 so Bubblewrap's --sync-fd can pass verifier FD 6; worker seccomp allows only the Go runtime's exact shared-thread clone flag sets, denies clone3 and process-like clone, and needs native startup validation",
			"Bubblewrap 0.9 file-snapshot mode requires host /proc/self/exe and sealed memfd support; it refuses the worker if the running image exceeds 7 MiB or snapshot/copy verification fails",
			"the 0.9 staging verifier runs inside the copied worker, so its independent trust comes from the already-running parent image, sealed memfd bytes, and trusted Bubblewrap copy; the self-comparison is an integrity check, not its own trust anchor",
			"the independent actor uses only disposable synthetic fixture paths and a fixed operation allowlist",
			"owner lifecycle rows exercise synthetic registration and rotation only; they do not wire real credentials, settings, Token Logger, or provider owners",
			"a protected-directory ancestor race remains ineligible when the candidate shares its filesystem because incomplete descendant coverage refuses reads before worker setup",
			"mount crossing, recursive-child relocation, parent-death and incomplete-shutdown cleanup have no integrated actor scenario",
		},
		NativeGaps: []string{
			"ordinary late-alias behavior and fresh direct protected-route refusal are separate actor cases; neither implies continuous path containment",
			"production owner registration, credential rotation, and cleanup integration are not implemented",
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
		{"actor_late_alias_existing_invocation", "an approved ordinary invocation continues for its opened object after an independent actor adds a protected-route alias"},
		{"late_alias_protected_route_refusal", "a fresh request through the protected route is refused even when it names an ordinary object by alias"},
		{"actor_selected_root_relocated_after_scan", "the opened object remains readable after its selected root object moves outside the selected pathname under B"},
		{"actor_parent_relocated_outside_root_after_scan", "the opened object remains readable after its parent moves outside the selected root under B"},
		{"actor_target_replaced_after_classification", "the approved opened object is read; the replacement at its former path is never read"},
		{"actor_unregistered_protected_descendant_moved_out", "independent actor moves an unregistered child out and coverage refusal occurs before authorization or worker launch"},
		{"actor_protected_ancestor_relocation", "pending: protected-directory coverage currently refuses eligible same-device targets before an actor race can be staged"},
		{"owner_registration_before_release", "same prepared object is registered before descriptor handoff; no stale read is published and the owner writes only after direct-worker join and pin match"},
		{"owner_registration_before_release_registered_identity_alias_denied", "fresh request through an alias of the registered object is refused"},
		{"owner_registration_while_capability_held", "real worker holds the same read-only object capability before registration; owner waits for direct-worker join before writing"},
		{"owner_registration_while_capability_held_registered_identity_alias_denied", "fresh request through an alias of the registered object is refused"},
		{"owner_registration_after_capture_suppresses_stale_publication", "authoritative registration after real capture suppresses stale publication; owner writes only after join and pin match"},
		{"owner_rotation_retains_old_new_pins_and_publishes_registered_replacement", "old and replacement objects remain pinned; replacement is written only after registration and published from the registered temporary object"},
		{"owner_rotation_old_alias_denied", "old protected identity remains denied through a pre-existing alias"},
		{"owner_rotation_renamed_old_identity_denied", "old protected identity remains denied after rename"},
		{"owner_rotation_new_alias_denied", "new protected identity remains denied through an alias"},
		{"owner_registration_failure_blocks_write", "failed owner registration leaves the synthetic sensitive write blocked"},
		{"owner_incomplete_cleanup_blocks_write", "incomplete real-worker cleanup blocks sensitive writes; unsafe unjoined-worker condition is explicitly unavailable"},
		{"provider_missing_explicit_once", "provider-only source refuses without explicit once approval before worker access"},
		{"provider_only_ordinary_positive_control", "separate ordinary fixture remains readable under its own once authorization"},
		{"provider_ordinary_receipt_rejected_for_alias", "ordinary identity/class/route receipt cannot authorize a registered provider alias"},
		{"provider_stale_policy_receipt_rejected", "provider receipt is rejected after policy generation changes"},
		{"provider_stale_root_receipt_rejected", "provider receipt is rejected after root generation changes"},
		{"provider_stale_grant_receipt_rejected", "provider receipt is rejected after grant generation changes"},
		{"provider_explicit_once_success_and_replay", "exact provider once approval reads once and replay dispatches no worker"},
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
	results = append(results, executeNativeActorRaceCase(session, authorizer, worker, root, aliasScope, aliasTarget, "actor_late_alias_existing_invocation", "actor-protected-alias-1", "an approved ordinary invocation continues for its opened object after an independent actor adds a protected-route alias", readbrokerstudy.NativeActorHardlink, "actor-alias-scope/ordinary.fixture", "actor-protected-alias.fixture", "supports_b", func() bool {
		ordinaryInfo, ordinaryErr := os.Stat(aliasTarget)
		protectedInfo, protectedErr := os.Stat(protectedAlias)
		return ordinaryErr == nil && protectedErr == nil && os.SameFile(ordinaryInfo, protectedInfo)
	}, "actor added a protected-route alias; the already-authorized ordinary object was read and published"))
	if err := session.SelectRoot(root); err != nil {
		return results, err
	}
	results = append(results, executeCase(session, authorizer, root, "late_alias_protected_route_refusal", protectedAlias, "late-alias-protected-route-1", "a fresh request through the registered protected route is refused without consuming approval"))

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
	ownerCases, err := runOwnerLifecycleCases(bwrap, worker, root)
	if err != nil {
		return results, err
	}
	results = append(results, ownerCases...)
	providerCases, err := runProviderApprovalCases(bwrap, worker, root)
	if err != nil {
		return results, err
	}
	results = append(results, providerCases...)
	return results, nil
}

func newStudySession(bwrap, workerPath, root string, hold bool) (*readbrokerstudy.Registry, *readbrokerstudy.OnceGrantFixture, readbrokerstudy.FixtureSession, error) {
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		return nil, nil, nil, err
	}
	var launcher readbrokerstudy.WorkerLauncher
	if hold {
		launcher, err = readbrokerstudy.NewWorkerLauncherWithCapabilityHold(bwrap, workerPath)
	} else {
		launcher, err = readbrokerstudy.NewWorkerLauncher(bwrap, workerPath)
	}
	if err != nil {
		_ = registry.Close()
		return nil, nil, nil, err
	}
	authorizer := readbrokerstudy.NewOnceGrantFixture()
	session, err := readbrokerstudy.NewFixtureSession(registry, readbrokerstudy.NewBroker(registry), launcher, authorizer)
	if err != nil {
		_ = launcher.Close()
		_ = registry.Close()
		return nil, nil, nil, err
	}
	if err := session.SelectRoot(root); err != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = session.Close(closeCtx)
		return nil, nil, nil, err
	}
	return registry, authorizer, session, nil
}

func closeStudySession(session readbrokerstudy.FixtureSession) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return session.Close(ctx)
}

func evidenceFor(session readbrokerstudy.FixtureSession, invocationID string) (*readbrokerstudy.ReadEvidence, bool) {
	for _, evidence := range session.Evidence() {
		if evidence.Case == invocationID {
			item := evidence
			return &item, true
		}
	}
	return nil, false
}

func runOwnerLifecycleCases(bwrap, workerPath, parent string) ([]caseResult, error) {
	results := make([]caseResult, 0, 12)
	for _, test := range []struct {
		name       string
		hold       bool
		checkpoint readbrokerstudy.Checkpoint
	}{
		{name: "owner_registration_before_release", checkpoint: readbrokerstudy.CheckpointBeforeHandoff},
		{name: "owner_registration_while_capability_held", hold: true, checkpoint: readbrokerstudy.CheckpointAfterCapabilityHeld},
	} {
		rows, err := runOwnerBarrierCase(bwrap, workerPath, parent, test.name, test.hold, test.checkpoint)
		if err != nil {
			return results, err
		}
		results = append(results, rows...)
	}
	afterCapture, err := runOwnerAfterCaptureCase(bwrap, workerPath, parent)
	if err != nil {
		return results, err
	}
	results = append(results, afterCapture)
	rotationRows, err := runOwnerRotationCase(bwrap, workerPath, parent)
	if err != nil {
		return results, err
	}
	results = append(results, rotationRows...)
	results = append(results, runOwnerRegistrationFailureCase(bwrap, workerPath, parent))
	results = append(results, caseResult{
		Name: "owner_incomplete_cleanup_blocks_write", Status: "unavailable", Classification: "unavailable",
		Expected:      "an unconfirmed real-worker join keeps the fixture owner write blocked and preserves cleanup ownership",
		Observed:      "not induced: the bounded sandbox worker is directly killable; manufacturing a native unjoined child would require an unsafe or unreviewed failure mechanism",
		FixtureEffect: "no synthetic sensitive bytes written; no incomplete-join native claim", AuthorityA: "no conclusion", AuthorityB: "no conclusion",
		Detail: "injected cleanup-pending regressions remain unit evidence; native namespace-descendant cleanup remains unproven",
	})
	return results, nil
}

func runOwnerAfterCaptureCase(bwrap, workerPath, parent string) (caseResult, error) {
	name := "owner_registration_after_capture_suppresses_stale_publication"
	root := filepath.Join(parent, name)
	if err := os.Mkdir(root, 0o700); err != nil {
		return caseResult{}, err
	}
	target := filepath.Join(root, "ordinary-to-sensitive.fixture")
	if err := os.WriteFile(target, []byte("synthetic ordinary bytes captured before registration"), 0o600); err != nil {
		return caseResult{}, err
	}
	writeFile, err := os.OpenFile(target, os.O_RDWR, 0)
	if err != nil {
		return caseResult{}, err
	}
	defer writeFile.Close()
	registry, authorizer, session, err := newStudySession(bwrap, workerPath, root, false)
	if err != nil {
		return caseResult{}, err
	}
	defer closeStudySession(session)
	invocationID := "owner-after-capture-read"
	if err := authorizer.ApproveOnce(invocationID); err != nil {
		return caseResult{}, err
	}
	owner := &fixtureOwnerWriter{
		session: session, registry: registry, owner: "synthetic-credential-owner",
		registrations: []readbrokerstudy.OwnerRegistration{{Owner: "synthetic-credential-owner", Class: readbrokerstudy.ClassCredential, Path: target}},
		writeFile:     writeFile, contents: []byte("synthetic owner bytes after stale capture was suppressed"),
	}
	var beginErr error
	var beginCalled bool
	session.SetCheckpointHook(func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint == readbrokerstudy.CheckpointAfterCapture && !beginCalled {
			beginCalled = true
			beginErr = owner.Begin()
		}
		return nil
	})
	readDone := make(chan struct{})
	readCtx, cancelRead := context.WithTimeout(context.Background(), 15*time.Second)
	result, readErr := session.Read(readCtx, root, target, invocationID)
	cancelRead()
	close(readDone)
	session.SetCheckpointHook(nil)
	workerEvidence, hasEvidence := evidenceFor(session, invocationID)
	completionCtx, cancelCompletion := context.WithTimeout(context.Background(), 3*time.Second)
	ownerEvidence := owner.Complete(completionCtx, readDone, workerEvidence, true)
	cancelCompletion()
	published := session.PublishedResults()
	row := caseResult{
		Name: name, Expected: "a real worker captures and joins; authoritative registration changes policy before publication; the stale result is suppressed and owner writes only after join and matching pin",
		Observed: string(result.Outcome), OnceGrantConsumptions: authorizer.ConsumptionCount(invocationID),
		GenerationsBefore: &ownerEvidence.PolicyBefore, GenerationsAfter: &ownerEvidence.PolicyAfterRegistration,
		WorkerStarted:       workerEvidence != nil && workerEvidence.WorkerStarted,
		WorkerReady:         workerEvidence != nil && workerEvidence.WorkerReady,
		WorkerJoinConfirmed: workerEvidence != nil && workerEvidence.WorkerJoined,
		CapabilitySent:      workerEvidence != nil && workerEvidence.CapabilitySent,
		CapabilityHeld:      workerEvidence != nil && workerEvidence.CapabilityHeld,
		ReadStarted:         workerEvidence != nil && workerEvidence.ReadStarted,
		Published:           workerEvidence != nil && workerEvidence.Published,
		WorkerProbe:         probeFor(workerEvidence), OwnerLifecycle: &ownerEvidence,
		AuthorizationAttempted: true, AuthorizationReceiptAccepted: true,
		PreparedClass: readbrokerstudy.ClassOrdinary,
		AuthorityA:    "no conclusion", AuthorityB: "authoritative policy invalidation and stale-publication suppression only",
		FixtureEffect: fmt.Sprintf("published result sink entries: %d", len(published)),
	}
	if workerEvidence != nil {
		id := ownerEvidence.WriteDescriptorIdentity
		row.PreparedIdentity = &id
		applyReadEvidence(&row, result, *workerEvidence, true)
	}
	pass := readErr == nil && beginCalled && beginErr == nil && result.Outcome == readbrokerstudy.ReadCancelled && hasEvidence &&
		workerEvidence.WorkerStarted && workerEvidence.WorkerReady && workerEvidence.WorkerJoined && workerEvidence.CapabilitySent &&
		workerEvidence.ReadStarted && !workerEvidence.Published && authorizer.ConsumptionCount(invocationID) == 1 && len(published) == 0 &&
		ownerEvidence.WriteAuthorized && ownerEvidence.WriteStarted && ownerEvidence.WriteCompleted && ownerEvidence.WorkerJoinConfirmed &&
		ownerEvidence.WriteDescriptorMatchesPin && ownerEvidence.PolicyAfterRegistration.Policy > ownerEvidence.PolicyBefore.Policy
	row.Status = "inconclusive"
	row.Classification = "inconclusive"
	row.Detail = "worker capture, owner registration, policy invalidation, join, suppressed sink, or pinned owner write evidence was incomplete"
	if pass {
		row.Status = "measured_pass"
		row.Classification = "owner_lifecycle"
		row.FixtureEffect = "worker read was captured but returned to no result sink after registration; owner wrote synthetic bytes only after the joined worker and matching pin were confirmed"
	}
	return row, nil
}

func runOwnerBarrierCase(bwrap, workerPath, parent, name string, hold bool, checkpoint readbrokerstudy.Checkpoint) ([]caseResult, error) {
	root := filepath.Join(parent, name)
	if err := os.Mkdir(root, 0o700); err != nil {
		return nil, err
	}
	target := filepath.Join(root, "ordinary-to-sensitive.fixture")
	if err := os.WriteFile(target, []byte("synthetic ordinary contents before owner registration"), 0o600); err != nil {
		return nil, err
	}
	writeFile, err := os.OpenFile(target, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	defer writeFile.Close()
	registry, authorizer, session, err := newStudySession(bwrap, workerPath, root, hold)
	if err != nil {
		return nil, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = closeStudySession(session)
		}
	}()
	invocationID := name + "-read"
	if err := authorizer.ApproveOnce(invocationID); err != nil {
		return nil, err
	}
	owner := &fixtureOwnerWriter{
		session: session, registry: registry, owner: "synthetic-credential-owner",
		registrations: []readbrokerstudy.OwnerRegistration{{Owner: "synthetic-credential-owner", Class: readbrokerstudy.ClassCredential, Path: target}},
		writeFile:     writeFile, contents: []byte("synthetic sensitive bytes written only after registration and worker join"),
	}
	readDone := make(chan struct{})
	var beginCalled bool
	var beginErr error
	session.SetCheckpointHook(func(current readbrokerstudy.Checkpoint) error {
		if current == checkpoint && !beginCalled {
			beginCalled = true
			beginErr = owner.Begin()
		}
		return nil
	})
	readCtx, cancelRead := context.WithTimeout(context.Background(), 12*time.Second)
	result, readErr := session.Read(readCtx, root, target, invocationID)
	cancelRead()
	close(readDone)
	session.SetCheckpointHook(nil)
	workerEvidence, hasEvidence := evidenceFor(session, invocationID)
	completionCtx, cancelCompletion := context.WithTimeout(context.Background(), 3*time.Second)
	ownerEvidence := owner.Complete(completionCtx, readDone, workerEvidence, true)
	cancelCompletion()
	expected := "register the exact prepared object before capability release; no FD handoff or read occurs; owner writes after the affected worker joins and the descriptor matches the pin"
	if hold {
		expected = "the real worker receives and holds the exact prepared object's read-only FD; registration terminates and joins that worker before the pinned owner write"
	}
	row := caseResult{
		Name: name, Expected: expected,
		Observed: string(result.Outcome), OnceGrantConsumptions: authorizer.ConsumptionCount(invocationID),
		GenerationsBefore: &ownerEvidence.PolicyBefore, GenerationsAfter: &ownerEvidence.PolicyAfterRegistration,
		WorkerStarted:       workerEvidence != nil && workerEvidence.WorkerStarted,
		WorkerReady:         workerEvidence != nil && workerEvidence.WorkerReady,
		WorkerJoinConfirmed: workerEvidence != nil && workerEvidence.WorkerJoined,
		CapabilitySent:      workerEvidence != nil && workerEvidence.CapabilitySent,
		CapabilityHeld:      workerEvidence != nil && workerEvidence.CapabilityHeld,
		ReadStarted:         workerEvidence != nil && workerEvidence.ReadStarted,
		Published:           workerEvidence != nil && workerEvidence.Published,
		WorkerProbe:         probeFor(workerEvidence), OwnerLifecycle: &ownerEvidence,
		AuthorizationAttempted: true, AuthorizationReceiptAccepted: true,
		PreparedClass: readbrokerstudy.ClassOrdinary,
		AuthorityA:    "no conclusion", AuthorityB: "registration invalidation and owner barrier only; no filesystem authority contract result",
		FixtureEffect: "",
	}
	if workerEvidence != nil {
		id := ownerEvidence.WriteDescriptorIdentity
		row.PreparedIdentity = &id
		applyReadEvidence(&row, result, *workerEvidence, true)
	}
	stateMatches := readErr == nil && beginCalled && errors.Is(beginErr, readbrokerstudy.ErrCleanupPending) && result.Outcome == readbrokerstudy.ReadNotExecuted && hasEvidence &&
		workerEvidence.WorkerStarted && workerEvidence.WorkerReady && workerEvidence.WorkerJoined &&
		workerEvidence.CapabilitySent == hold && workerEvidence.CapabilityHeld == hold && !workerEvidence.ReadStarted && !workerEvidence.Published &&
		authorizer.ConsumptionCount(invocationID) == 1 && ownerEvidence.CleanupPending && ownerEvidence.WorkerJoinConfirmed &&
		ownerEvidence.WriteAuthorized && ownerEvidence.WriteStarted && ownerEvidence.WriteCompleted && ownerEvidence.WriteDescriptorMatchesPin &&
		ownerEvidence.PolicyAfterRegistration.Policy > ownerEvidence.PolicyBefore.Policy
	if hold && workerEvidence != nil {
		stateMatches = stateMatches && workerEvidence.CapabilityHeld && workerEvidence.CapabilitySent
	}
	row.Status = "inconclusive"
	row.Classification = "inconclusive"
	row.Detail = "required worker, registration, join, write identity, or no-publication observation was not established"
	if stateMatches {
		row.Status = "measured_pass"
		row.Classification = "owner_lifecycle"
		row.FixtureEffect = "same synthetic object was registered and pinned; the worker was joined before the owner wrote through the matching descriptor; stale read was not published"
		row.Detail = "fixture owner observed cleanup pending and retried only after the session read returned with confirmed direct-worker join"
	} else if readErr != nil {
		row.Detail = "coordinator error prevented the owner lifecycle scenario from completing"
	}
	alias := target + ".registered-alias"
	if err := os.Link(target, alias); err != nil {
		return []caseResult{row}, nil
	}
	aliasRow := protectedIdentityCase(session, authorizer, root, name+"_registered_identity_alias_denied", alias, name+"-alias")
	if closeErr := closeStudySession(session); closeErr != nil {
		row.Status = "inconclusive"
		row.Classification = "inconclusive"
		row.Detail = "owner scenario completed but its session cleanup was not clean"
	}
	closed = true
	return []caseResult{row, aliasRow}, nil
}

func probeFor(evidence *readbrokerstudy.ReadEvidence) readbrokerstudy.WorkerProbe {
	if evidence == nil {
		return readbrokerstudy.WorkerProbe{}
	}
	return evidence.Probe
}

func protectedIdentityCase(session readbrokerstudy.FixtureSession, auth *readbrokerstudy.OnceGrantFixture, root, name, path, invocationID string) caseResult {
	identity, idErr := identityAt(path)
	if idErr != nil {
		return caseResult{Name: name, Status: "fixture_error", Classification: "inconclusive", Expected: "registered sensitive identity alias refuses before worker access", Observed: "fixture identity unavailable", FixtureEffect: "none", AuthorityA: "no conclusion", AuthorityB: "no conclusion", Detail: "synthetic alias identity could not be inspected"}
	}
	_ = auth.ApproveOnce(invocationID)
	before := session.GenerationSnapshot()
	result, readErr := session.Read(context.Background(), root, path, invocationID)
	after := session.GenerationSnapshot()
	consumed := auth.ConsumptionCount(invocationID)
	row := caseResult{Name: name, Expected: "a fresh generic request through an alias of a registered sensitive identity refuses before once consumption and worker launch", Observed: string(result.Outcome), OnceGrantConsumptions: consumed, GenerationsBefore: &before, GenerationsAfter: &after, PreparedClass: readbrokerstudy.ClassCredential, PreparedIdentity: &identity, AuthorityA: "not applicable; protected request refused", AuthorityB: "registered sensitive identity remains denied through aliases", FixtureEffect: "no worker/data handoff or publication"}
	if readErr == nil && result.Outcome == readbrokerstudy.ReadRefused && result.Detail == readbrokerstudy.ErrProtected.Error() && consumed == 0 {
		row.Status = "measured_pass"
		row.Classification = "protected_rule_enforced"
	} else {
		row.Status = "inconclusive"
		row.Classification = "inconclusive"
		row.Detail = "registered identity refusal or zero-grant/worker precondition was not established"
	}
	return row
}

func runOwnerRotationCase(bwrap, workerPath, parent string) ([]caseResult, error) {
	root := filepath.Join(parent, "owner-rotation")
	if err := os.Mkdir(root, 0o700); err != nil {
		return nil, err
	}
	oldPath := filepath.Join(root, "auth-effective.fixture")
	oldArchive := filepath.Join(root, "auth-old-archive.fixture")
	newTemp := filepath.Join(root, "auth-new-temporary.fixture")
	newAlias := filepath.Join(root, "auth-new-alias.fixture")
	oldAlias := filepath.Join(root, "auth-old-alias.fixture")
	if err := os.WriteFile(oldPath, []byte("synthetic prior protected object"), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(newTemp, nil, 0o600); err != nil {
		return nil, err
	}
	oldIdentity, err := identityAt(oldPath)
	if err != nil {
		return nil, err
	}
	if err := os.Link(oldPath, oldAlias); err != nil {
		return nil, err
	}
	newFile, err := os.OpenFile(newTemp, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	defer newFile.Close()
	registry, authorizer, session, err := newStudySession(bwrap, workerPath, root, false)
	if err != nil {
		return nil, err
	}
	if err := session.Register(readbrokerstudy.OwnerRegistration{Owner: "rotation-owner", Class: readbrokerstudy.ClassCredential, Path: oldPath}); err != nil {
		_ = closeStudySession(session)
		return nil, err
	}
	owner := &fixtureOwnerWriter{
		session: session, registry: registry, owner: "rotation-owner",
		registrations: []readbrokerstudy.OwnerRegistration{
			{Owner: "rotation-owner", Class: readbrokerstudy.ClassCredential, Path: newTemp},
			{Owner: "rotation-owner", Class: readbrokerstudy.ClassCredential, Path: oldPath},
		},
		writeFile: newFile, contents: []byte("synthetic rotated sensitive bytes"),
		archiveFrom: oldPath, archiveTo: oldArchive, publishFrom: newTemp, publishTo: oldPath,
	}
	beginErr := owner.Begin()
	completionCtx, cancelCompletion := context.WithTimeout(context.Background(), 3*time.Second)
	ownerEvidence := owner.Complete(completionCtx, nil, nil, false)
	cancelCompletion()
	newIdentity := ownerEvidence.PinnedIdentity
	view := registry.View()
	oldPin, newPin, tempRouteMatchesPin, effectiveRouteReserved := false, false, false, false
	for _, entry := range view.Entries {
		if entry.Owner != "rotation-owner" || entry.Class != readbrokerstudy.ClassCredential {
			continue
		}
		oldPin = oldPin || entry.Identity == oldIdentity
		newPin = newPin || entry.Identity == newIdentity
		tempRouteMatchesPin = tempRouteMatchesPin || (entry.Path == newTemp && entry.Identity == newIdentity)
		effectiveRouteReserved = effectiveRouteReserved || entry.Path == oldPath
	}
	newAtEffective, effectiveErr := identityAt(oldPath)
	oldAtArchive, archiveErr := identityAt(oldArchive)
	ownerEvidence.TemporaryRouteMatchesPin = tempRouteMatchesPin
	ownerEvidence.EffectiveRouteReserved = effectiveRouteReserved
	ownerEvidence.RetiredIdentityRetained = oldPin && archiveErr == nil && oldAtArchive == oldIdentity
	rotationOK := beginErr == nil && ownerEvidence.WriteAuthorized && ownerEvidence.WriteCompleted && ownerEvidence.Published && ownerEvidence.WriteDescriptorMatchesPin &&
		oldPin && newPin && tempRouteMatchesPin && effectiveRouteReserved && effectiveErr == nil && newAtEffective == newIdentity && ownerEvidence.RetiredIdentityRetained
	row := caseResult{
		Name:   "owner_rotation_retains_old_new_pins_and_publishes_registered_replacement",
		Status: "inconclusive", Classification: "inconclusive",
		Expected: "register/pin an empty temporary replacement and effective route before writing; publish only the same pinned object; retain old identity through alias and rename",
		Observed: "fixture rotation completed", FixtureEffect: "synthetic owner rotation effect inspected", OnceGrantConsumptions: 0,
		GenerationsBefore: &ownerEvidence.PolicyBefore, GenerationsAfter: &ownerEvidence.PolicyAfterRegistration,
		PreparedClass: readbrokerstudy.ClassCredential, PreparedIdentity: &newIdentity, OwnerLifecycle: &ownerEvidence,
		AuthorityA: "no conclusion", AuthorityB: "registered old/new identity lifecycle only",
	}
	if rotationOK {
		row.Status = "measured_pass"
		row.Classification = "owner_lifecycle"
		row.FixtureEffect = "old identity remained pinned at its alias/archive; the new temporary path matched its pin, was written through the matching descriptor, and the same object was published at the reserved effective route"
	} else {
		row.Detail = "registration, identity, pin retention, write ordering, or publication preconditions were not established"
	}
	rows := []caseResult{row}
	for _, item := range []struct{ name, path, id string }{
		{name: "owner_rotation_old_alias_denied", path: oldAlias, id: "rotation-old-alias"},
		{name: "owner_rotation_renamed_old_identity_denied", path: oldArchive, id: "rotation-old-renamed"},
		{name: "owner_rotation_new_alias_denied", path: newAlias, id: "rotation-new-alias"},
	} {
		if item.name == "owner_rotation_new_alias_denied" {
			if err := os.Link(oldPath, newAlias); err != nil {
				rows = append(rows, caseResult{Name: item.name, Status: "unavailable", Classification: "unavailable", Expected: "new protected identity alias refuses", Observed: "synthetic hard-link fixture unavailable", FixtureEffect: "no alias created", AuthorityA: "no conclusion", AuthorityB: "no conclusion", Detail: "fixture filesystem refused a hard link"})
				continue
			}
		}
		rows = append(rows, protectedIdentityCase(session, authorizer, root, item.name, item.path, item.id))
	}
	if closeErr := closeStudySession(session); closeErr != nil {
		rows[0].Status = "inconclusive"
		rows[0].Classification = "inconclusive"
		rows[0].Detail = "rotation fixture succeeded but study session cleanup failed"
	}
	return rows, nil
}

func runOwnerRegistrationFailureCase(bwrap, workerPath, parent string) caseResult {
	root := filepath.Join(parent, "owner-registration-failure")
	if err := os.Mkdir(root, 0o700); err != nil {
		return caseResult{Name: "owner_registration_failure_blocks_write", Status: "fixture_error", Classification: "inconclusive", Detail: "synthetic failure fixture could not be created"}
	}
	target := filepath.Join(root, "empty.fixture")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		return caseResult{Name: "owner_registration_failure_blocks_write", Status: "fixture_error", Classification: "inconclusive", Detail: "synthetic write fixture could not be created"}
	}
	writeFile, err := os.OpenFile(target, os.O_RDWR, 0)
	if err != nil {
		return caseResult{Name: "owner_registration_failure_blocks_write", Status: "fixture_error", Classification: "inconclusive", Detail: "synthetic write descriptor could not be opened"}
	}
	defer writeFile.Close()
	registry, _, session, err := newStudySession(bwrap, workerPath, root, false)
	if err != nil {
		return caseResult{Name: "owner_registration_failure_blocks_write", Status: "unavailable", Classification: "unavailable", Expected: "failed registration leaves owner write blocked", Observed: "study session could not be prepared", FixtureEffect: "empty file preserved", AuthorityA: "no conclusion", AuthorityB: "no conclusion", Detail: "trusted worker session setup unavailable"}
	}
	owner := &fixtureOwnerWriter{session: session, registry: registry, owner: "failed-owner", registrations: []readbrokerstudy.OwnerRegistration{{Owner: "failed-owner", Class: readbrokerstudy.ClassCredential, Path: filepath.Join(root, "missing-parent", "credential.fixture")}}, writeFile: writeFile, contents: []byte("must never be written")}
	registerErr := owner.Begin()
	completion := owner.Complete(context.Background(), nil, nil, false)
	stat, statErr := writeFile.Stat()
	pass := registerErr != nil && !completion.WriteAuthorized && !completion.WriteStarted && !completion.WriteCompleted && statErr == nil && stat.Size() == 0
	row := caseResult{Name: "owner_registration_failure_blocks_write", Expected: "failed owner registration preserves the empty object and never authorizes or starts the sensitive write", Observed: "registration failed and the owner writer remained blocked", FixtureEffect: "empty synthetic file preserved", AuthorityA: "no conclusion", AuthorityB: "owner failure gate only", OwnerLifecycle: &completion}
	if pass {
		row.Status = "measured_pass"
		row.Classification = "owner_lifecycle"
	} else {
		row.Status = "unexpected"
		row.Classification = "inconclusive"
		row.Detail = "registration failure did not keep the synthetic write blocked"
	}
	if closeErr := closeStudySession(session); closeErr != nil {
		row.Status = "inconclusive"
		row.Classification = "inconclusive"
		row.Detail = "registration failure was observed but session cleanup was not confirmed"
	}
	return row
}

type providerFixture struct {
	root             string
	provider         string
	providerContents []byte
	alias            string
	ordinary         string
	registry         *readbrokerstudy.Registry
	authorizer       *readbrokerstudy.OnceGrantFixture
	mutating         *studyMutatingAuthorizer
	session          readbrokerstudy.FixtureSession
}

type studyMutatingAuthorizer struct {
	delegate *readbrokerstudy.OnceGrantFixture
	attempts map[string]int
	receipts map[string]readbrokerstudy.AuthorizationReceipt
	returned map[string]readbrokerstudy.AuthorizationReceipt
	reuse    map[string]string
	mutate   map[string]func(*readbrokerstudy.AuthorizationReceipt)
	after    map[string]func(readbrokerstudy.AuthorizationRequest)
}

func (a *studyMutatingAuthorizer) AuthorizeAndConsume(ctx context.Context, request readbrokerstudy.AuthorizationRequest) (readbrokerstudy.AuthorizationReceipt, error) {
	a.attempts[request.InvocationID]++
	receipt, err := a.delegate.AuthorizeAndConsume(ctx, request)
	if err != nil {
		return readbrokerstudy.AuthorizationReceipt{}, err
	}
	a.receipts[request.InvocationID] = receipt
	if source := a.reuse[request.InvocationID]; source != "" {
		if previous, ok := a.receipts[source]; ok {
			receipt = previous
		}
	}
	if mutate := a.mutate[request.InvocationID]; mutate != nil {
		mutate(&receipt)
	}
	if after := a.after[request.InvocationID]; after != nil {
		after(request)
	}
	a.returned[request.InvocationID] = receipt
	return receipt, nil
}

func newProviderFixture(bwrap, workerPath, parent, name string) (*providerFixture, error) {
	root := filepath.Join(parent, name)
	if err := os.Mkdir(root, 0o700); err != nil {
		return nil, err
	}
	provider := filepath.Join(root, "registered-provider-source.fixture")
	alias := filepath.Join(root, "provider-source-alias.fixture")
	ordinary := filepath.Join(root, "ordinary.fixture")
	providerContents := []byte("synthetic provider source bytes for authorization study\n")
	if err := os.WriteFile(provider, providerContents, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(ordinary, []byte("separate synthetic ordinary fixture\n"), 0o600); err != nil {
		return nil, err
	}
	if err := os.Link(provider, alias); err != nil {
		return nil, err
	}
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		return nil, err
	}
	if err := registry.Register(readbrokerstudy.OwnerRegistration{Owner: "provider-only-owner", Class: readbrokerstudy.ClassProvider, Path: provider}); err != nil {
		_ = registry.Close()
		return nil, err
	}
	launcher, err := readbrokerstudy.NewWorkerLauncher(bwrap, workerPath)
	if err != nil {
		_ = registry.Close()
		return nil, err
	}
	authorizer := readbrokerstudy.NewOnceGrantFixture()
	mutating := &studyMutatingAuthorizer{
		delegate: authorizer,
		attempts: make(map[string]int), receipts: make(map[string]readbrokerstudy.AuthorizationReceipt), returned: make(map[string]readbrokerstudy.AuthorizationReceipt),
		reuse: make(map[string]string), mutate: make(map[string]func(*readbrokerstudy.AuthorizationReceipt)),
		after: make(map[string]func(readbrokerstudy.AuthorizationRequest)),
	}
	session, err := readbrokerstudy.NewFixtureSession(registry, readbrokerstudy.NewBroker(registry), launcher, mutating)
	if err != nil {
		_ = launcher.Close()
		_ = registry.Close()
		return nil, err
	}
	if err := session.SelectRoot(root); err != nil {
		_ = closeStudySession(session)
		return nil, err
	}
	return &providerFixture{root: root, provider: provider, providerContents: append([]byte(nil), providerContents...), alias: alias, ordinary: ordinary, registry: registry, authorizer: authorizer, mutating: mutating, session: session}, nil
}

func runProviderApprovalCases(bwrap, workerPath, parent string) ([]caseResult, error) {
	rows := make([]caseResult, 0, 7)
	missing, err := newProviderFixture(bwrap, workerPath, parent, "provider-missing-approval")
	if err != nil {
		return rows, err
	}
	rows = append(rows, runProviderReadCase(missing, "provider_missing_explicit_once", "provider-missing-once", missing.provider, false, "provider source refuses without explicit once approval before worker data access"))
	if err := closeStudySession(missing.session); err != nil {
		return rows, err
	}

	ordinaryReuse, err := newProviderFixture(bwrap, workerPath, parent, "provider-ordinary-receipt")
	if err != nil {
		return rows, err
	}
	ordinaryID := "provider-ordinary-source-receipt"
	if err := ordinaryReuse.authorizer.ApproveOnce(ordinaryID); err != nil {
		_ = closeStudySession(ordinaryReuse.session)
		return rows, err
	}
	ordinaryRow := executeCase(ordinaryReuse.session, ordinaryReuse.authorizer, ordinaryReuse.root, "provider_only_ordinary_positive_control", ordinaryReuse.ordinary, ordinaryID, "ordinary fixture is separate from provider-only protected registration")
	_, hasOrdinaryReceipt := ordinaryReuse.mutating.receipts[ordinaryID]
	ordinaryRow.AuthorizationAttempted = ordinaryReuse.mutating.attempts[ordinaryID] == 1
	ordinaryRow.AuthorizationReceiptAccepted = ordinaryRow.Status == "measured_pass"
	ordinaryRow.PreparedClass = readbrokerstudy.ClassOrdinary
	if id, idErr := identityAt(ordinaryReuse.ordinary); idErr == nil {
		ordinaryRow.PreparedIdentity = &id
	}
	rows = append(rows, ordinaryRow)
	providerReuseID := "provider-reuse-ordinary-receipt"
	_ = ordinaryReuse.authorizer.ApproveOnce(providerReuseID)
	if hasOrdinaryReceipt {
		ordinaryReuse.mutating.reuse[providerReuseID] = ordinaryID
	}
	rows = append(rows, runProviderReadCase(ordinaryReuse, "provider_ordinary_receipt_rejected_for_alias", providerReuseID, ordinaryReuse.alias, true, "a once receipt minted for the ordinary fixture cannot authorize the registered provider identity through an alias"))
	if err := closeStudySession(ordinaryReuse.session); err != nil {
		return rows, err
	}

	for _, mutation := range []string{"policy", "root", "grant"} {
		fixture, err := newProviderFixture(bwrap, workerPath, parent, "provider-stale-"+mutation)
		if err != nil {
			return rows, err
		}
		invocationID := "provider-stale-" + mutation
		if err := fixture.authorizer.ApproveOnce(invocationID); err != nil {
			_ = closeStudySession(fixture.session)
			return rows, err
		}
		switch mutation {
		case "policy":
			reserved := filepath.Join(fixture.root, "registered-during-authorization.fixture")
			if err := os.WriteFile(reserved, []byte("synthetic registration target"), 0o600); err != nil {
				_ = closeStudySession(fixture.session)
				return rows, err
			}
			fixture.mutating.after[invocationID] = func(readbrokerstudy.AuthorizationRequest) {
				_ = fixture.session.Register(readbrokerstudy.OwnerRegistration{Owner: "policy-revision-fixture", Class: readbrokerstudy.ClassCredential, Path: reserved})
			}
		case "root":
			otherRoot := filepath.Join(fixture.root, "other-selected-root")
			if err := os.Mkdir(otherRoot, 0o700); err != nil {
				_ = closeStudySession(fixture.session)
				return rows, err
			}
			fixture.mutating.after[invocationID] = func(readbrokerstudy.AuthorizationRequest) {
				_ = fixture.session.SelectRoot(otherRoot)
				_ = fixture.session.SelectRoot(fixture.root)
			}
		case "grant":
			fixture.mutating.after[invocationID] = func(request readbrokerstudy.AuthorizationRequest) {
				_ = fixture.session.Revoke(request.InvocationID)
			}
		}
		rows = append(rows, runProviderReadCase(fixture, "provider_stale_"+mutation+"_receipt_rejected", invocationID, fixture.provider, true, "a correctly minted provider receipt becomes stale before dispatch after an authoritative generation change"))
		if err := closeStudySession(fixture.session); err != nil {
			return rows, err
		}
	}

	explicit, err := newProviderFixture(bwrap, workerPath, parent, "provider-explicit-once")
	if err != nil {
		return rows, err
	}
	rows = append(rows, runProviderReadCase(explicit, "provider_explicit_once_success_and_replay", "provider-explicit-once", explicit.provider, true, "only exact once approval bound to the provider route and object succeeds; invocation replay is refused"))
	if err := closeStudySession(explicit.session); err != nil {
		return rows, err
	}
	return rows, nil
}

func runProviderReadCase(fixture *providerFixture, name, invocationID, target string, approve bool, expected string) caseResult {
	identity, identityErr := identityAt(target)
	if identityErr != nil {
		return caseResult{Name: name, Status: "fixture_error", Classification: "inconclusive", Expected: expected, Observed: "synthetic source identity unavailable", FixtureEffect: "none", AuthorityA: "no conclusion", AuthorityB: "no conclusion", Detail: "provider fixture identity could not be inspected"}
	}
	if approve {
		_ = fixture.authorizer.ApproveOnce(invocationID)
	}
	before := fixture.session.GenerationSnapshot()
	readCtx, cancelRead := context.WithTimeout(context.Background(), 15*time.Second)
	result, readErr := fixture.session.Read(readCtx, fixture.root, target, invocationID)
	cancelRead()
	after := fixture.session.GenerationSnapshot()
	workerEvidence, hasEvidence := evidenceFor(fixture.session, invocationID)
	consumed := fixture.authorizer.ConsumptionCount(invocationID)
	bytesMatch := result.Outcome == readbrokerstudy.ReadSucceeded && bytes.Equal(result.Bytes, fixture.providerContents)
	row := caseResult{
		Name: name, Expected: expected, Observed: string(result.Outcome), OnceGrantConsumptions: consumed,
		AuthorizationAttempted:       fixture.mutating.attempts[invocationID] == 1,
		AuthorizationReceiptAccepted: false, PreparedClass: readbrokerstudy.ClassProvider, PreparedIdentity: &identity,
		BytesMatchTarget: bytesMatch, BytesMatchApproved: bytesMatch,
		GenerationsBefore: &before, GenerationsAfter: &after, AuthorityA: "no conclusion", AuthorityB: "provider authorization correlation only",
		FixtureEffect: "no provider path or source contents included in report",
	}
	if receipt, ok := fixture.mutating.returned[invocationID]; ok {
		row.AuthorizationReceiptInvocation = receipt.InvocationID
		row.AuthorizationReceiptClass = receipt.Class
		identity := receipt.Identity
		row.AuthorizationReceiptIdentity = &identity
	}
	if hasEvidence {
		row.WorkerStarted = workerEvidence.WorkerStarted
		row.WorkerReady = workerEvidence.WorkerReady
		row.WorkerJoinConfirmed = workerEvidence.WorkerJoined
		row.CapabilitySent = workerEvidence.CapabilitySent
		row.CapabilityHeld = workerEvidence.CapabilityHeld
		row.ReadStarted = workerEvidence.ReadStarted
		row.Published = workerEvidence.Published
		row.WorkerProbe = workerEvidence.Probe
		applyReadEvidence(&row, result, *workerEvidence, true)
	}
	if fixture.mutating.attempts[invocationID] > 0 && result.Outcome == readbrokerstudy.ReadSucceeded {
		row.AuthorizationReceiptAccepted = true
	}
	passed := false
	if name == "provider_missing_explicit_once" {
		passed = readErr == nil && result.Outcome == readbrokerstudy.ReadRefused && !row.AuthorizationReceiptAccepted && consumed == 0 && !row.WorkerStarted && !row.CapabilitySent && !row.ReadStarted && !row.Published
	} else if name == "provider_explicit_once_success_and_replay" {
		passed = readErr == nil && result.Outcome == readbrokerstudy.ReadSucceeded && row.AuthorizationReceiptAccepted && hasEvidence && workerEvidence.WorkerStarted && workerEvidence.WorkerReady && workerEvidence.WorkerJoined && workerEvidence.CapabilitySent && workerEvidence.ReadStarted && workerEvidence.Published && row.BytesMatchApproved && consumed == 1
		if passed {
			replayCtx, cancelReplay := context.WithTimeout(context.Background(), 15*time.Second)
			replay, replayErr := fixture.session.Read(replayCtx, fixture.root, target, invocationID)
			cancelReplay()
			row.ReplayOutcome = replay.Outcome
			row.ReplayPrevented = replayErr == nil && replay.Outcome == readbrokerstudy.ReadRefused && replay.Detail == readbrokerstudy.ErrInvocationID.Error()
			row.ReplayAdditionalWorkers = 0
			if !row.ReplayPrevented {
				passed = false
			}
		}
	} else {
		passed = readErr == nil && result.Outcome == readbrokerstudy.ReadNotExecuted && !row.AuthorizationReceiptAccepted && !row.WorkerStarted && !row.CapabilitySent && !row.ReadStarted && !row.Published && consumed == 1
	}
	if passed {
		row.Status = "measured_pass"
		row.Classification = "coordinator_control"
		if name == "provider_explicit_once_success_and_replay" {
			row.Classification = "supports_b"
			row.FixtureEffect = "real sandbox worker read and published the approved provider fixture once; replay started no second worker"
		}
		row.Detail = boundText(result.Detail, 512)
	} else {
		row.Status = "inconclusive"
		row.Classification = "inconclusive"
		row.Detail = "provider authorization, stale receipt, worker, or replay evidence differed from the expected outcome"
	}
	return row
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
	case "ordinary_positive", "provider_source_explicit_once", "provider_only_ordinary_positive_control", "actor_late_alias_existing_invocation":
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
		case "protected_direct", "protected_existing_hardlink", "protected_late_hardlink", "protected_object_renamed_into_ordinary_tree", "protected_location_replaced_before_open", "ordinary_object_moved_into_protected_location", "late_alias_protected_route_refusal":
			stateMatches = stateMatches && result.Detail == readbrokerstudy.ErrProtected.Error()
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
	row := caseResult{Name: name, Status: status, Classification: classification, Expected: expected, Observed: string(result.Outcome), WorkerStarted: ev.WorkerStarted, WorkerReady: ev.WorkerReady, WorkerJoinConfirmed: ev.WorkerJoined, CapabilitySent: ev.CapabilitySent, CapabilityHeld: ev.CapabilityHeld, ReadStarted: ev.ReadStarted, Published: ev.Published, BytesMatchTarget: bytesMatch, BytesMatchApproved: bytesMatch, OnceGrantConsumptions: consumptionCount, GenerationsBefore: &generationsBefore, GenerationsAfter: &generationsAfter, WorkerProbe: ev.Probe, FixtureEffect: effect, AuthorityA: a, AuthorityB: b, Detail: boundText(detail, 768)}
	applyReadEvidence(&row, result, ev, ev.Case == invocationID)
	return row
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
		WorkerJoinConfirmed: ev.WorkerJoined, CapabilitySent: ev.CapabilitySent, CapabilityHeld: ev.CapabilityHeld,
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
	applyReadEvidence(&base, result, ev, ev.Case == invocationID)
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
	stateMatches := result.Outcome == readbrokerstudy.ReadSucceeded && base.BytesMatchTarget && ev.WorkerStarted && ev.WorkerReady && ev.WorkerJoined && ev.CapabilitySent && ev.ReadStarted && ev.Published && auth.ConsumptionCount(invocationID) == 1 && generationsBefore == generationsAfter
	if stateMatches {
		base.Status = "measured_pass"
		base.Classification = "supports_b"
		base.FixtureEffect = effect
		switch name {
		case "actor_selected_root_relocated_after_scan", "actor_parent_relocated_outside_root_after_scan":
			base.AuthorityA = "violation of strict live-path containment: the opened object was read after its selected pathname moved outside the authorized path"
		case "actor_late_alias_existing_invocation":
			base.AuthorityA = "no conclusion: this alias-only scenario leaves the original read object under its selected route and evaluates approved contract B"
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

func applyReadEvidence(row *caseResult, result readbrokerstudy.WorkerResult, evidence readbrokerstudy.ReadEvidence, present bool) {
	if row == nil {
		return
	}
	row.ResultBytesReturned = result.Outcome == readbrokerstudy.ReadSucceeded
	row.ReadEvidencePresent = present
	if !present {
		return
	}
	row.ReadEvidenceCase = evidence.Case
	if evidence.CapabilitySent {
		identity := evidence.CapabilityIdentity
		row.CapabilityIdentity = &identity
	}
	row.WorkerOutcome = evidence.Outcome
	generations := evidence.Generations
	row.WorkerGenerations = &generations
	row.WorkerEffect = evidence.Effect
	row.WorkerLimitation = evidence.Limitation
}

type fixtureOwnerWriter struct {
	session       readbrokerstudy.FixtureSession
	registry      *readbrokerstudy.Registry
	owner         string
	registrations []readbrokerstudy.OwnerRegistration
	writeFile     *os.File
	contents      []byte
	publishFrom   string
	publishTo     string
	archiveFrom   string
	archiveTo     string
	evidence      ownerLifecycleEvidence
	beginErr      error
}

func (o *fixtureOwnerWriter) Begin() error {
	o.evidence.RegistrationAttempted = true
	o.evidence.PolicyBefore = o.session.GenerationSnapshot()
	if o.writeFile == nil {
		o.beginErr = errors.New("fixture owner has no held write descriptor")
		o.evidence.InitialRegistrationResult = "failed"
		o.evidence.Detail = "fixture write descriptor unavailable"
		return o.beginErr
	}
	identity, err := studyIdentity(o.writeFile)
	if err != nil {
		o.beginErr = err
		o.evidence.InitialRegistrationResult = "failed"
		o.evidence.Detail = "fixture write descriptor identity could not be measured"
		return err
	}
	o.evidence.WriteDescriptorIdentity = identity
	o.evidence.Events = append(o.evidence.Events, "owner_registration_started")
	o.beginErr = o.session.RegisterRotation(o.owner, o.registrations)
	o.evidence.PolicyAfterRegistration = o.session.GenerationSnapshot()
	switch {
	case o.beginErr == nil:
		o.evidence.InitialRegistrationResult = "registered"
		o.evidence.Events = append(o.evidence.Events, "registration_committed")
	case errors.Is(o.beginErr, readbrokerstudy.ErrCleanupPending):
		o.evidence.InitialRegistrationResult = "cleanup_pending"
		o.evidence.CleanupPending = true
		o.evidence.Events = append(o.evidence.Events, "registration_committed_cleanup_pending")
	default:
		o.evidence.InitialRegistrationResult = "failed"
		o.evidence.Detail = "fixture registration failed"
	}
	return o.beginErr
}

func (o *fixtureOwnerWriter) Complete(ctx context.Context, readDone <-chan struct{}, readEvidence *readbrokerstudy.ReadEvidence, requireJoin bool) ownerLifecycleEvidence {
	if o.beginErr != nil && !errors.Is(o.beginErr, readbrokerstudy.ErrCleanupPending) {
		return o.evidence
	}
	if requireJoin && !o.evidence.CleanupPending {
		if readDone == nil {
			o.evidence.Detail = "required join confirmation has no read-completion signal"
			return o.evidence
		}
		select {
		case <-readDone:
			o.evidence.Events = append(o.evidence.Events, "affected_read_returned")
		case <-ctx.Done():
			o.evidence.Detail = "affected read did not finish before the owner deadline"
			return o.evidence
		}
		if readEvidence == nil || !readEvidence.WorkerStarted || !readEvidence.WorkerJoined {
			o.evidence.Detail = "required direct worker join was not confirmed"
			return o.evidence
		}
		o.evidence.WorkerJoinConfirmed = true
		o.evidence.Events = append(o.evidence.Events, "direct_worker_join_confirmed")
	}
	if o.evidence.CleanupPending {
		if readDone == nil {
			o.evidence.Detail = "cleanup pending; no read-completion signal was supplied"
			return o.evidence
		}
		select {
		case <-readDone:
			o.evidence.Events = append(o.evidence.Events, "affected_read_returned")
		case <-ctx.Done():
			o.evidence.Detail = "cleanup pending; affected read did not finish before the owner deadline"
			return o.evidence
		}
		if readEvidence == nil || !readEvidence.WorkerStarted || !readEvidence.WorkerJoined {
			o.evidence.Detail = "cleanup pending; direct worker join was not confirmed"
			return o.evidence
		}
		o.evidence.WorkerJoinConfirmed = true
		o.evidence.Events = append(o.evidence.Events, "direct_worker_join_confirmed")
		retryErr := o.session.RegisterRotation(o.owner, o.registrations)
		if retryErr != nil {
			o.evidence.Detail = "idempotent registration retry after join failed"
			return o.evidence
		}
		o.evidence.Events = append(o.evidence.Events, "registration_retry_confirmed")
	} else if requireJoin && !o.evidence.WorkerJoinConfirmed {
		o.evidence.Detail = "required active worker did not produce a cleanup-pending registration barrier"
		return o.evidence
	}

	var pin readbrokerstudy.FileIdentity
	writeIdentity := o.evidence.WriteDescriptorIdentity
	for _, registration := range o.registrations {
		matched := false
		for _, entry := range o.registry.View().Entries {
			if entry.Owner == registration.Owner && entry.Class == registration.Class && entry.Path == registration.Path {
				if entry.Identity == writeIdentity {
					pin = entry.Identity
					matched = true
					break
				}
			}
		}
		if registration.Path == o.registrations[0].Path && !matched {
			o.evidence.Detail = "registered write target does not identify the owner's open descriptor"
			return o.evidence
		}
	}
	if pin == (readbrokerstudy.FileIdentity{}) || pin != writeIdentity {
		o.evidence.Detail = "no retained protected pin matches the owner's write descriptor"
		return o.evidence
	}
	o.evidence.PinnedIdentity = pin
	o.evidence.WriteDescriptorMatchesPin = true
	if requireJoin && !o.evidence.WorkerJoinConfirmed {
		o.evidence.Detail = "owner refused the write because the affected worker was not confirmed joined"
		return o.evidence
	}
	o.evidence.WriteAuthorized = true
	o.evidence.Events = append(o.evidence.Events, "sensitive_write_authorized_after_pin_and_join")
	o.evidence.WriteStarted = true
	o.evidence.Events = append(o.evidence.Events, "sensitive_write_started")
	n, err := o.writeFile.WriteAt(o.contents, 0)
	if err == nil && n != len(o.contents) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = o.writeFile.Truncate(int64(len(o.contents)))
	}
	if err == nil {
		err = o.writeFile.Sync()
	}
	if err != nil {
		o.evidence.Detail = "synthetic sensitive write failed"
		return o.evidence
	}
	o.evidence.WriteCompleted = true
	o.evidence.Events = append(o.evidence.Events, "sensitive_write_completed")
	if o.archiveFrom != "" || o.archiveTo != "" {
		if o.archiveFrom == "" || o.archiveTo == "" || os.Rename(o.archiveFrom, o.archiveTo) != nil {
			o.evidence.Detail = "old fixture object could not be moved to its archive route"
			return o.evidence
		}
		o.evidence.Events = append(o.evidence.Events, "old_object_archived")
	}
	if o.publishFrom != "" || o.publishTo != "" {
		if o.publishFrom == "" || o.publishTo == "" || os.Rename(o.publishFrom, o.publishTo) != nil {
			o.evidence.Detail = "registered fixture replacement could not be published"
			return o.evidence
		}
		publishedIdentity, statErr := identityAt(o.publishTo)
		if statErr != nil || publishedIdentity != pin {
			o.evidence.Detail = "published fixture object differs from the pinned write target"
			return o.evidence
		}
		o.evidence.Published = true
		o.evidence.Events = append(o.evidence.Events, "registered_replacement_published")
	}
	return o.evidence
}

func identityAt(path string) (readbrokerstudy.FileIdentity, error) {
	file, err := os.Open(path)
	if err != nil {
		return readbrokerstudy.FileIdentity{}, err
	}
	defer file.Close()
	return studyIdentity(file)
}

func studyIdentity(file *os.File) (readbrokerstudy.FileIdentity, error) {
	if file == nil {
		return readbrokerstudy.FileIdentity{}, errors.New("fixture descriptor is nil")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return readbrokerstudy.FileIdentity{}, err
	}
	return readbrokerstudy.FileIdentity{Device: uint64(stat.Dev), Inode: uint64(stat.Ino), Type: uint32(stat.Mode & unix.S_IFMT)}, nil
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
