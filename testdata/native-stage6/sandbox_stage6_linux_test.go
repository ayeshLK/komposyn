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
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	stdruntime "runtime"
	"slices"
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
	ID                          string         `json:"case_id"`
	Status                      string         `json:"status"`
	EvidenceKind                string         `json:"evidence_kind,omitempty"`
	Reason                      string         `json:"reason,omitempty"`
	FailureDiagnostic           string         `json:"failure_diagnostic,omitempty"`
	LaunchPolicy                map[string]any `json:"launch_policy,omitempty"`
	WorkerStarted               bool           `json:"worker_started"`
	WorkerReady                 bool           `json:"worker_ready"`
	WorkerJoined                bool           `json:"worker_joined"`
	SearchTraversalWorkerJoined bool           `json:"search_traversal_worker_joined"`
	SearchContentWorkerJoined   bool           `json:"search_content_worker_joined"`
	SearchSnapshotClosed        bool           `json:"search_snapshot_descriptor_closed"`
	SearchCandidatesClosed      bool           `json:"search_candidate_descriptors_closed"`
	CapabilityTransferred       bool           `json:"capability_transferred"`
	CapabilityHeld              bool           `json:"capability_held"`
	ReadStarted                 bool           `json:"read_started"`
	ReadOutcome                 string         `json:"read_outcome,omitempty"`
	ProbePassed                 bool           `json:"required_probe_passed"`
	OnceApprovalCount           int32          `json:"once_approval_count"`
	OnceGrantConsumptionCount   int            `json:"once_grant_consumption_count"`
	GrantRevisionBefore         uint64         `json:"grant_revision_before"`
	GrantRevisionAfter          uint64         `json:"grant_revision_after"`
	RemainingGrantCount         int            `json:"remaining_grant_count"`
	ProviderRequestCount        int            `json:"provider_request_count"`
	FixtureBytesMatched         bool           `json:"fixture_bytes_matched"`
	DisclosureWithheld          bool           `json:"disclosure_withheld"`
	StaleDataAbsent             bool           `json:"stale_data_absent"`
	LateResponseReturned        bool           `json:"late_response_returned"`
	LateDeltaAttempted          bool           `json:"late_delta_attempted"`
	LateDeltaSuppressed         bool           `json:"late_delta_suppressed"`
	ActorStarted                bool           `json:"actor_started"`
	ActorTransitionCompleted    bool           `json:"actor_transition_completed"`
	ActorJoined                 bool           `json:"actor_joined"`
	ActorTransitionCount        int            `json:"actor_transition_count"`
	AuthorityObservation        string         `json:"authority_observation,omitempty"`
	CleanupConfirmed            bool           `json:"cleanup_confirmed"`
	CoordinatorStarted          bool           `json:"coordinator_started"`
	CoordinatorJoined           bool           `json:"coordinator_joined"`
	BubblewrapExitObserved      bool           `json:"bubblewrap_exit_observed"`
	NamespaceInitExitObserved   bool           `json:"namespace_init_exit_observed"`
	ObservedProcessesGone       bool           `json:"observed_processes_gone"`
	FixtureFS                   fixtureFS      `json:"fixture_filesystems,omitempty"`
}

type fixtureFS []stage6FixtureFilesystem

// stage6FixtureFilesystem records properties observed at the fixture itself.
// It deliberately contains no host path, mount source, or source content.
type stage6FixtureFilesystem struct {
	Role                string   `json:"role"`
	Status              string   `json:"status"`
	FilesystemType      string   `json:"filesystem_type,omitempty"`
	BlockSize           int64    `json:"block_size_bytes,omitempty"`
	StatfsFlags         []string `json:"statfs_flags,omitempty"`
	MountStatus         string   `json:"mount_status"`
	MountFilesystemType string   `json:"mount_filesystem_type,omitempty"`
	MountRoot           string   `json:"mount_root_scope,omitempty"`
	MountOptions        []string `json:"mount_options"`
	SuperblockOptions   []string `json:"superblock_options"`
	ObservationFailure  string   `json:"observation_failure,omitempty"`
}

type stage6MountInfo struct {
	root       string
	filesystem string
	options    []string
	superopts  []string
}

func observeStage6FixtureFilesystem(path string) stage6FixtureFilesystem {
	observation := stage6FixtureFilesystem{Status: "unknown", MountStatus: "unknown"}
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		observation.Status = "failed"
		observation.MountStatus = "not_attempted"
		observation.ObservationFailure = "open_fixture_failed"
		return observation
	}
	defer unix.Close(fd)
	var stat unix.Statfs_t
	if err := unix.Fstatfs(fd, &stat); err != nil {
		observation.Status = "failed"
		observation.MountStatus = "not_attempted"
		observation.ObservationFailure = "statfs_failed"
		return observation
	}
	observation.Status = "recorded"
	observation.FilesystemType = fmt.Sprintf("0x%016x", uint64(stat.Type))
	observation.BlockSize = stat.Bsize
	observation.StatfsFlags = stage6StatfsFlags(stat.Flags)

	mount, ok := stage6MountInfoForFD(fd)
	if !ok {
		observation.Status = "partial"
		observation.MountStatus = "unavailable"
		observation.ObservationFailure = "mountinfo_match_unavailable"
		return observation
	}
	observation.MountStatus = "recorded"
	observation.MountFilesystemType = mount.filesystem
	if mount.root == "/" {
		observation.MountRoot = "filesystem_root"
	} else {
		observation.MountRoot = "subtree"
	}
	observation.MountOptions = mount.options
	observation.SuperblockOptions = mount.superopts
	return observation
}

func stage6StatfsFlags(flags int64) []string {
	const known = unix.ST_RDONLY | unix.ST_NOSUID | unix.ST_NODEV | unix.ST_NOEXEC | unix.ST_SYNCHRONOUS | unix.ST_MANDLOCK | unix.ST_NOATIME | unix.ST_NODIRATIME | unix.ST_RELATIME
	var result []string
	for _, flag := range []struct {
		bit  int64
		name string
	}{{unix.ST_RDONLY, "readonly"}, {unix.ST_NOSUID, "nosuid"}, {unix.ST_NODEV, "nodev"}, {unix.ST_NOEXEC, "noexec"}, {unix.ST_SYNCHRONOUS, "synchronous"}, {unix.ST_MANDLOCK, "mandatory_locking"}, {unix.ST_NOATIME, "noatime"}, {unix.ST_NODIRATIME, "nodiratime"}, {unix.ST_RELATIME, "relatime"}} {
		if flags&flag.bit != 0 {
			result = append(result, flag.name)
		}
	}
	if flags & ^known != 0 {
		result = append(result, "other_flags_present")
	}
	slices.Sort(result)
	return result
}

func stage6MountInfoForFD(fd int) (stage6MountInfo, bool) {
	mountIDData, err := os.ReadFile(filepath.Join("/proc/self/fdinfo", strconv.Itoa(fd)))
	if err != nil || len(mountIDData) > 16<<10 {
		return stage6MountInfo{}, false
	}
	mountID := ""
	for _, line := range strings.Split(string(mountIDData), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "mnt_id:" {
			mountID = fields[1]
			break
		}
	}
	if mountID == "" {
		return stage6MountInfo{}, false
	}
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return stage6MountInfo{}, false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var selected stage6MountInfo
	found := false
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		separator := slices.Index(fields, "-")
		if separator < 6 || len(fields) <= separator+3 {
			continue
		}
		if fields[0] != mountID {
			continue
		}
		found = true
		selected.root = stage6UnescapeMountInfo(fields[3])
		selected.filesystem = fields[separator+1]
		selected.options = stage6RelevantMountOptions(fields[5])
		selected.superopts = stage6RelevantMountOptions(fields[separator+3])
		break
	}
	if scanner.Err() != nil || !found {
		return stage6MountInfo{}, false
	}
	return selected, true
}

func stage6UnescapeMountInfo(value string) string {
	return strings.NewReplacer("\\040", " ", "\\011", "\t", "\\012", "\n", "\\134", "\\").Replace(value)
}

func stage6RelevantMountOptions(options string) []string {
	allowed := map[string]struct{}{
		"ro": {}, "rw": {}, "nosuid": {}, "nodev": {}, "noexec": {}, "relatime": {},
		"strictatime": {}, "noatime": {}, "lazytime": {}, "sync": {}, "dirsync": {},
	}
	seen := map[string]struct{}{}
	for _, option := range strings.Split(options, ",") {
		if _, ok := allowed[option]; ok {
			seen[option] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for option := range seen {
		result = append(result, option)
	}
	slices.Sort(result)
	return result
}

func stage6CaptureFixtureFilesystem(caseID, role, path string) {
	observation := observeStage6FixtureFilesystem(path)
	observation.Role = role
	stage6Update(caseID, func(item *stage6Case) {
		item.FixtureFS = append(item.FixtureFS, observation)
	})
}

func stage6FinalizeFixtureFilesystemEvidence(report *stage6Report) {
	roles := map[string]struct{}{}
	observations := 0
	failed := 0
	partialObservations := 0
	partialMounts := 0
	for _, item := range report.Cases {
		for _, observation := range item.FixtureFS {
			observations++
			roles[observation.Role] = struct{}{}
			switch observation.Status {
			case "failed", "unknown":
				failed++
			case "partial":
				partialObservations++
			}
			if observation.MountStatus != "recorded" {
				partialMounts++
			}
		}
	}
	roleNames := make([]string, 0, len(roles))
	for role := range roles {
		roleNames = append(roleNames, role)
	}
	slices.Sort(roleNames)
	status := "recorded"
	reason := ""
	if observations == 0 {
		status = "unavailable"
		reason = "no actual fixture paths were observed before study shutdown"
	} else if failed > 0 || partialObservations > 0 || partialMounts > 0 {
		status = "partial"
	}
	report.FixtureEvidence = map[string]any{
		"status":                         status,
		"observation_scope":              "actual Stage 6 fixture roots, selected listing/search scopes, and nested mount boundaries",
		"observations":                   observations,
		"failed_or_unknown_observations": failed,
		"partial_observations":           partialObservations,
		"partial_mount_observations":     partialMounts,
		"roles_observed":                 roleNames,
		"paths_disclosed":                false,
	}
	if reason != "" {
		report.FixtureEvidence["reason"] = reason
	}
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
	SchemaVersion   int            `json:"schema_version"`
	SourceCommit    string         `json:"source_commit"`
	RunnerVersion   string         `json:"runner_version"`
	OS              string         `json:"os"`
	Architecture    string         `json:"architecture"`
	Kernel          string         `json:"kernel"`
	UID             int            `json:"uid"`
	Backend         map[string]any `json:"backend"`
	WorkerImage     map[string]any `json:"worker_image"`
	LaunchPolicy    map[string]any `json:"launch_policy"`
	Overall         string         `json:"overall_status"`
	Cases           []stage6Case   `json:"cases"`
	Limitations     []string       `json:"limitations"`
	FixtureEvidence map[string]any `json:"fixture_filesystem_evidence"`
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
		"ordinary_chat_without_root", "read_refused_without_root_or_backend", "synthetic_once_authority_flow", "positive_exact_read_and_replay", "positive_directory_listing", "positive_recursive_directory_listing", "recursive_listing_nested_ignore_filters", "recursive_listing_entry_limit", "recursive_listing_cancellation_after_traversal", "recursive_descendant_movement_replacement", "directory_listing_movement_replacement", "directory_listing_owner_rotation", "directory_listing_mount_boundary", "search_mount_boundary", "directory_listing_worker_cleanup", "positive_content_search", "search_candidate_protected_substitution", "search_ignore_rules_changed_after_content", "search_content_worker_cancellation_cleanup", "search_selected_root_movement", "search_owner_rotation_withholds_capture", "search_revocation_withholds_capture", "empty_file_read",
		"protected_auth_refusal", "protected_settings_refusal", "provider_source_owner",
		"incomplete_owner_inventory", "recursive_listing_incomplete_owner_inventory", "actor_helper_control", "independent_namespace_actor", "expected_once_transitions",
		"captured_batch_revocation", "pre_handoff_owner_invalidation", "post_handoff_owner_invalidation",
		"late_response_and_delta_retirement", "cancellation_and_cleanup", "launch_profile_and_limits",
		"mutation_profile", "shell_profile", "parent_death_cleanup",
	}
	syntheticCases := map[string]struct{}{
		"ordinary_chat_without_root":                   {},
		"read_refused_without_root_or_backend":         {},
		"synthetic_once_authority_flow":                {},
		"provider_source_owner":                        {},
		"incomplete_owner_inventory":                   {},
		"recursive_listing_incomplete_owner_inventory": {},
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
			item.Reason = "case is outside the implemented Stage 6 profile"
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
		SchemaVersion: 2, SourceCommit: os.Getenv("KOMPOSYN_STAGE6_SOURCE_COMMIT"), RunnerVersion: "14",
		OS: "linux", Architecture: stdruntime.GOARCH, Kernel: stage6KernelRelease(), UID: os.Getuid(), Overall: "stage6_profile_incomplete",
		Backend: map[string]any{}, WorkerImage: workerImage, LaunchPolicy: map[string]any{}, Cases: cases,
		Limitations:     limitations,
		FixtureEvidence: map[string]any{"status": "not_observed", "observation_scope": "actual Stage 6 fixture root and selected listing/search directory; mountinfo fields are reduced to allowlisted characteristics", "paths_disclosed": false},
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
	stage6FinalizeFixtureFilesystemEvidence(&stage6)
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
		"synthetic-runtime-composition": 6,
		"synthetic-actor-fixture":       1,
		"native-integrated":             30,
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

func TestStage6FixtureFilesystemObservationUsesFixtureLocation(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "actual-fixture")
	if err := os.Mkdir(fixture, 0700); err != nil {
		t.Fatal(err)
	}
	var expected unix.Statfs_t
	if err := unix.Statfs(fixture, &expected); err != nil {
		t.Fatal(err)
	}
	got := observeStage6FixtureFilesystem(fixture)
	if got.Status != "recorded" && got.Status != "partial" {
		t.Fatalf("fixture filesystem observation status=%q failure=%q", got.Status, got.ObservationFailure)
	}
	if got.FilesystemType != fmt.Sprintf("0x%016x", uint64(expected.Type)) || got.BlockSize != expected.Bsize {
		t.Fatalf("observation does not match the actual fixture location: got=%+v expected_type=0x%016x expected_block_size=%d", got, uint64(expected.Type), expected.Bsize)
	}
	if got.MountStatus != "recorded" && got.MountStatus != "unavailable" {
		t.Fatalf("mount observation failure was not represented explicitly: %+v", got)
	}
	if got.MountStatus == "recorded" && got.MountFilesystemType == "" {
		t.Fatalf("matched mount filesystem type was not recorded: %+v", got)
	}
	for _, option := range got.MountOptions {
		if _, ok := map[string]bool{"ro": true, "rw": true, "nosuid": true, "nodev": true, "noexec": true, "relatime": true, "strictatime": true, "noatime": true, "lazytime": true, "sync": true, "dirsync": true}[option]; !ok {
			t.Fatalf("unexpected mount option %q", option)
		}
	}
}

func TestStage6FixtureFilesystemObservationReportsMissingLocation(t *testing.T) {
	got := observeStage6FixtureFilesystem(filepath.Join(t.TempDir(), "missing-fixture"))
	if got.Status != "failed" || got.ObservationFailure != "open_fixture_failed" || got.MountStatus != "not_attempted" {
		t.Fatalf("missing fixture location was represented as established provenance: %+v", got)
	}
}

func TestStage6FixtureFilesystemObservationDoesNotSubstituteOutputFilesystem(t *testing.T) {
	outputFixture := t.TempDir()
	var outputStat unix.Statfs_t
	if err := unix.Statfs(outputFixture, &outputStat); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.MkdirTemp("/dev/shm", "komposyn-stage6-fixture-")
	if err != nil {
		t.Skipf("a separate writable fixture filesystem is unavailable: %v", err)
	}
	defer os.RemoveAll(fixture)
	var fixtureStat unix.Statfs_t
	if err := unix.Statfs(fixture, &fixtureStat); err != nil {
		t.Fatal(err)
	}
	if fixtureStat.Type == outputStat.Type {
		t.Skip("available temporary locations use the same filesystem type")
	}
	got := observeStage6FixtureFilesystem(fixture)
	if got.Status != "recorded" || got.FilesystemType != fmt.Sprintf("0x%016x", uint64(fixtureStat.Type)) {
		t.Fatalf("fixture provenance was substituted with another filesystem: got=%+v fixture_type=0x%016x", got, uint64(fixtureStat.Type))
	}
	if outputStat.Type == fixtureStat.Type || got.FilesystemType == fmt.Sprintf("0x%016x", uint64(outputStat.Type)) {
		t.Fatalf("fixture observation matches output filesystem unexpectedly: fixture=%+v", got)
	}
}

func TestStage6FixtureFilesystemObservationFollowsSymlinkedParentMount(t *testing.T) {
	fixtureParent, err := os.MkdirTemp("/dev/shm", "komposyn-stage6-symlink-fixture-")
	if err != nil {
		t.Skipf("a writable tmpfs fixture location is unavailable: %v", err)
	}
	defer os.RemoveAll(fixtureParent)
	aliasParent := filepath.Join(t.TempDir(), "fixture-parent")
	if err := os.Symlink("/dev/shm", aliasParent); err != nil {
		t.Skipf("symlinked fixture parent is unavailable: %v", err)
	}
	fixture := filepath.Join(aliasParent, filepath.Base(fixtureParent))
	var expected unix.Statfs_t
	if err := unix.Statfs(fixture, &expected); err != nil {
		t.Fatal(err)
	}
	if uint64(expected.Type) != unix.TMPFS_MAGIC {
		t.Skipf("/dev/shm is not tmpfs: filesystem magic %#x", uint64(expected.Type))
	}
	got := observeStage6FixtureFilesystem(fixture)
	if got.Status != "recorded" || got.MountStatus != "recorded" {
		t.Fatalf("symlinked fixture filesystem provenance is incomplete: %+v", got)
	}
	if got.FilesystemType != fmt.Sprintf("0x%016x", uint64(expected.Type)) || got.MountFilesystemType != "tmpfs" {
		t.Fatalf("symlinked fixture filesystem and mount provenance disagree: %+v", got)
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
			"root_mount":                     "private 16 MiB tmpfs",
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

func stage6WorkerProbePassed(probe readbrokerstudy.WorkerProbe) bool {
	return probe.NoNewPrivs && probe.SeccompMode == 2 && probe.SocketCreationDenied && probe.PathOpenDenied &&
		probe.ReadCapabilityInitiallyAbsent && probe.OnlyProtocolDescriptor && probe.EnvironmentCleared
}

func stage6DirectoryListingProbePassed(probe readbrokerstudy.WorkerProbe) bool {
	return stage6WorkerProbePassed(probe) && probe.DirectoryTraversalSyscallsAllowed
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
	toolID          tools.ID
	listingOptions  json.RawMessage
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
		toolID := p.toolID
		if toolID == "" {
			toolID = tools.ReadFileID
		}
		paths := []struct{ callID, itemID, path string }{{"stage6-read", "stage6-item-read", p.target}}
		if p.multipleReads {
			paths = append(paths, struct{ callID, itemID, path string }{"stage6-read-b", "stage6-item-read-b", p.secondTarget})
		}
		calls := make([]provider.ToolCall, 0, len(paths))
		nativeCalls := make([]json.RawMessage, 0, len(paths))
		for _, item := range paths {
			argumentValues := map[string]any{"path": item.path}
			if (toolID == tools.ListFilesID || toolID == tools.SearchFilesID) && len(p.listingOptions) != 0 {
				var options map[string]json.RawMessage
				if json.Unmarshal(p.listingOptions, &options) != nil {
					return provider.Result{}, errors.New("synthetic listing options are invalid")
				}
				for key, value := range options {
					var decoded any
					if json.Unmarshal(value, &decoded) != nil {
						return provider.Result{}, errors.New("synthetic listing option is invalid")
					}
					argumentValues[key] = decoded
				}
			}
			arguments, _ := json.Marshal(argumentValues)
			nativeArguments, _ := json.Marshal(string(arguments))
			nativeCalls = append(nativeCalls, json.RawMessage(`{"type":"function_call","id":"`+item.itemID+`","call_id":"`+item.callID+`","name":"`+string(toolID)+`","arguments":`+string(nativeArguments)+`,"status":"completed"}`))
			calls = append(calls, provider.ToolCall{CallID: item.callID, ToolID: toolID, Arguments: arguments})
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
	reference     *runtime.ReadExecutionReference
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
	h.reference = reference
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
	readHost            *stage6ReadHost
	provider            *stage6Provider
	authStore           auth.Store
	root                string
	authPath            string
	content             string
	approvals           atomic.Int32
	approvalDescription string
	approvalWaitAt      atomic.Int32
	approvalEntered     chan struct{}
	approvalRelease     chan struct{}
	approvalReleaseOnce sync.Once
	approvalWaitOnce    sync.Once
	secondContent       string
	reportCases         []string
}

func enableRecursiveListingForNativeStudy(service *Service) error {
	if service == nil {
		return errors.New("native service is missing")
	}
	impl, ok := service.impl.(*linuxImplementation)
	if !ok {
		return errors.New("native service does not use the Linux implementation")
	}
	impl.mu.Lock()
	impl.listingOptionsAvailable = true
	impl.mu.Unlock()
	return nil
}

func enableSearchForNativeStudy(service *Service) error {
	if service == nil {
		return errors.New("native service is missing")
	}
	impl, ok := service.impl.(*linuxImplementation)
	if !ok || impl.session == nil {
		return errors.New("native service does not use the composed Linux worker")
	}
	if !service.SearchFilesAvailable() {
		return errors.New("native search worker profiles are unavailable")
	}
	return nil
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
	} else if os.Getenv("KOMPOSYN_STAGE6_DIRECTORY_MOUNT_ROOT") != "" {
		launcher, err = readbrokerstudy.NewWorkerLauncherForStage6InFixtureUserNamespace(
			backendPath, workerPath, stage6RequestedBindMode(), holdWorker,
		)
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
		"root_mount":                     "private 16 MiB tmpfs",
		"worker_mount":                   stage6WorkerMountPolicy(),
		"runtime_mounts":                 []string{},
		"extra_descriptors":              []string{"fd 3: seccomp filter", "fd 4: local protocol socket", "fd 5: worker mount source (consumed by Bubblewrap)", "fd 6: worker verification image (retained through startup)"},
		"worker_environment":             []string{"KOMPOSYN_READBROKER_SENTINEL=synthetic-only"},
		"aggregate_cgroup_limits":        "not provisioned or claimed",
		"process_creation":               "denied by worker seccomp after Go thread bootstrap",
	}
	stage6Mu.Unlock()
	if !inspection.Trusted || inspection.Error != "" {
		for _, id := range []string{"positive_exact_read_and_replay", "positive_directory_listing", "positive_recursive_directory_listing", "recursive_listing_nested_ignore_filters", "recursive_listing_entry_limit", "recursive_listing_cancellation_after_traversal", "recursive_descendant_movement_replacement", "directory_listing_movement_replacement", "directory_listing_owner_rotation", "directory_listing_mount_boundary", "search_mount_boundary", "directory_listing_worker_cleanup", "positive_content_search", "search_candidate_protected_substitution", "search_ignore_rules_changed_after_content", "search_content_worker_cancellation_cleanup", "search_selected_root_movement", "search_owner_rotation_withholds_capture", "search_revocation_withholds_capture", "empty_file_read", "protected_auth_refusal", "protected_settings_refusal", "independent_namespace_actor", "pre_handoff_owner_invalidation", "post_handoff_owner_invalidation", "cancellation_and_cleanup", "launch_profile_and_limits", "late_response_and_delta_retirement", "expected_once_transitions"} {
			stage6Skip(id, fmt.Sprintf(
				"trusted backend validation refused before backend probes or worker launch (uid=%d mode=%#o error=%s)",
				inspection.UID, inspection.Mode, boundedStage6Diagnostic(inspection.Error),
			))
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
	if targetKind == "directory-list-mounted" || targetKind == "directory-list-recursive-mounted" || targetKind == "content-search-mounted" {
		mountRoot := os.Getenv("KOMPOSYN_STAGE6_DIRECTORY_MOUNT_ROOT")
		if !filepath.IsAbs(mountRoot) || filepath.Clean(mountRoot) != mountRoot {
			_ = service.Close(context.Background())
			t.Fatal("directory mount fixture root is unavailable")
		}
		rootName := "runtime-root"
		if targetKind == "content-search-mounted" {
			rootName = "runtime-search-root"
		}
		root = filepath.Join(mountRoot, rootName)
		if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
			_ = service.Close(context.Background())
			t.Fatalf("directory mount fixture root is not a directory: %v", statErr)
		}
	}
	stage6CaptureFixtureFilesystem(caseID, "synthetic_fixture_root", root)
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
	toolID := tools.ReadFileID
	protectedMountSource := ""
	if targetKind == "empty" {
		content = ""
	}
	target := filepath.Join(root, "notes.txt")
	if targetKind == "directory-list" || targetKind == "directory-list-mounted" || targetKind == "directory-list-recursive" || targetKind == "directory-list-recursive-mounted" || targetKind == "directory-list-recursive-filtered" || targetKind == "directory-list-recursive-large" || targetKind == "content-search" || targetKind == "content-search-mounted" {
		scopeName := "listing-scope"
		if targetKind == "content-search-mounted" {
			scopeName = "search-scope"
		}
		target = filepath.Join(root, scopeName)
		toolID, content = tools.ListFilesID, "visible.go"
		if targetKind == "content-search" || targetKind == "content-search-mounted" {
			toolID = tools.SearchFilesID
		}
		if targetKind == "directory-list-mounted" || targetKind == "directory-list-recursive-mounted" || targetKind == "content-search-mounted" {
			if info, err := os.Stat(target); err != nil || !info.IsDir() {
				_ = service.Close(context.Background())
				t.Fatalf("mounted listing fixture directory unavailable: %v", err)
			}
		} else if err := os.Mkdir(target, 0700); err != nil {
			_ = service.Close(context.Background())
			t.Fatal(err)
		}
		for name, value := range map[string]string{"visible.go": "synthetic visible", ".hidden": "synthetic hidden", "ignored.tmp": "synthetic ignored"} {
			if err := os.WriteFile(filepath.Join(target, name), []byte(value), 0600); err != nil {
				_ = service.Close(context.Background())
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(target, ".gitignore"), []byte("ignored.tmp\n"), 0600); err != nil {
			_ = service.Close(context.Background())
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(target, ".git"), 0700); err != nil {
			_ = service.Close(context.Background())
			t.Fatal(err)
		}
		if targetKind == "directory-list-recursive" || targetKind == "directory-list-recursive-mounted" || targetKind == "directory-list-recursive-filtered" || targetKind == "directory-list-recursive-large" {
			directories := []string{"nested", ".private", filepath.Join(".git", "objects")}
			if targetKind == "directory-list-recursive-filtered" {
				directories = append(directories, "ignored-dir", filepath.Join("ignored-dir", "nested"))
			}
			for _, directory := range directories {
				if err := os.MkdirAll(filepath.Join(target, directory), 0700); err != nil {
					_ = service.Close(context.Background())
					t.Fatal(err)
				}
			}
			nestedFiles := map[string]string{"nested/child.go": "synthetic nested", ".private/hidden.go": "synthetic hidden", ".git/objects/secret.go": "synthetic git metadata"}
			if targetKind == "directory-list-recursive-filtered" {
				nestedFiles["nested/keep.tmp"] = "synthetic allowed nested ignore exception"
				nestedFiles["nested/drop.tmp"] = "synthetic ignored nested file"
				nestedFiles[".private/hidden.tmp"] = "synthetic hidden matching glob"
				nestedFiles["ignored-dir/nested/secret.tmp"] = "synthetic parent-ignored descendant"
				if err := os.WriteFile(filepath.Join(target, "nested", ".gitignore"), []byte("*.tmp\n!keep.tmp\n"), 0600); err != nil {
					_ = service.Close(context.Background())
					t.Fatalf("create nested ignore fixture: %v", err)
				}
				if err := os.WriteFile(filepath.Join(target, ".gitignore"), []byte("*.tmp\nignored-dir/\n"), 0600); err != nil {
					_ = service.Close(context.Background())
					t.Fatalf("create recursive ignore fixture: %v", err)
				}
			}
			if targetKind == "directory-list-recursive-large" {
				for index := range 10_020 {
					nestedFiles[fmt.Sprintf("nested/entry-%05d.txt", index)] = "synthetic bounded listing entry"
				}
			}
			for name, value := range nestedFiles {
				if err := os.WriteFile(filepath.Join(target, filepath.FromSlash(name)), []byte(value), 0600); err != nil {
					_ = service.Close(context.Background())
					t.Fatal(err)
				}
			}
		}
		if targetKind == "directory-list-mounted" || targetKind == "directory-list-recursive-mounted" {
			protectedMountSource = filepath.Join(root, "protected-mounted-source")
			if err := os.WriteFile(protectedMountSource, []byte("synthetic protected mount object"), 0600); err != nil {
				_ = service.Close(context.Background())
				t.Fatalf("create synthetic protected mount source: %v", err)
			}
			alias := filepath.Join(target, "protected-mounted-alias")
			if err := os.WriteFile(alias, []byte("mountpoint"), 0600); err != nil {
				_ = service.Close(context.Background())
				t.Fatalf("create protected mount alias target: %v", err)
			}
			if err := unix.Mount(protectedMountSource, alias, "", unix.MS_BIND, ""); err != nil {
				_ = service.Close(context.Background())
				t.Fatalf("bind mount synthetic protected object into listing scope: %v", err)
			}
			stage6CaptureFixtureFilesystem(caseID, "nested_bind_mount_alias", alias)
			t.Cleanup(func() { _ = unix.Unmount(alias, unix.MNT_DETACH) })
		}
	} else if targetKind == "auth" {
		target, _ = authStore.EffectivePath()
		content = "synthetic-never-real-key"
	} else if targetKind == "settings" {
		target, _ = settingsStore.EffectivePath()
		content = "synthetic-settings-marker"
	} else if err := os.WriteFile(target, []byte(content), 0600); err != nil {
		_ = service.Close(context.Background())
		t.Fatalf("synthetic target setup failed: %v", err)
	}
	if targetKind == "directory-list" || targetKind == "directory-list-mounted" || targetKind == "directory-list-recursive" || targetKind == "directory-list-recursive-mounted" || targetKind == "directory-list-recursive-filtered" || targetKind == "directory-list-recursive-large" || targetKind == "content-search" || targetKind == "content-search-mounted" {
		stage6CaptureFixtureFilesystem(caseID, "selected_listing_or_search_scope", target)
	}
	secondTarget := filepath.Join(root, "second-notes.txt")
	if targetKind == "multiple" {
		if err := os.WriteFile(secondTarget, []byte(secondContent), 0600); err != nil {
			_ = service.Close(context.Background())
			t.Fatalf("second synthetic target setup failed: %v", err)
		}
	}
	base := &stage6Provider{target: target, toolID: toolID, secondTarget: secondTarget, multipleReads: targetKind == "multiple", blockSecond: blockSecond, firstToolCall: true, secondStarted: make(chan struct{}), releaseSecond: make(chan struct{})}
	if targetKind == "directory-list-recursive" {
		base.listingOptions = json.RawMessage(`{"recursive":true,"include_hidden":true,"include_ignored":true,"name_globs":["*.go","*.tmp"]}`)
	}
	if targetKind == "directory-list-recursive-filtered" {
		base.listingOptions = json.RawMessage(`{"recursive":true,"include_hidden":false,"include_ignored":false,"name_globs":["*.tmp"]}`)
	}
	if targetKind == "directory-list-recursive-large" {
		base.listingOptions = json.RawMessage(`{"recursive":true,"include_hidden":false,"include_ignored":false,"name_globs":["*.txt"]}`)
	}
	if targetKind == "directory-list-recursive-mounted" {
		base.listingOptions = json.RawMessage(`{"recursive":true,"include_hidden":true,"include_ignored":true,"name_globs":["*"]}`)
	}
	if targetKind == "content-search" || targetKind == "content-search-mounted" {
		base.listingOptions = json.RawMessage(`{"query":"synthetic-never-real-key","recursive":true,"include_hidden":true,"include_ignored":true,"name_globs":["*.go"]}`)
	}
	providers, err := model.NewRegistry(base)
	if err != nil {
		_ = service.Close(context.Background())
		t.Fatalf("fixture provider registry failed: %v", err)
	}
	fixture := &stage6Fixture{service: service, provider: base, authStore: authStore, root: root, authPath: authPath, content: content, secondContent: secondContent, approvalEntered: make(chan struct{}), approvalRelease: make(chan struct{})}
	fixture.readHost = &stage6ReadHost{service: service, beforeHandoff: beforeHandoff}
	approval := runtime.ToolApprovalFunc(func(ctx context.Context, pending permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		count := fixture.approvals.Add(1)
		if len(pending.Request.Requirements) > 1 {
			fixture.approvalDescription = pending.Request.Requirements[1].Description
		}
		if fixture.approvalWaitAt.Load() == count {
			fixture.approvalWaitOnce.Do(func() { close(fixture.approvalEntered) })
			select {
			case <-fixture.approvalRelease:
			case <-ctx.Done():
				return "", "", ctx.Err()
			}
		}
		return permissions.ApprovalOnce, "", nil
	})
	var config runtime.ToolRuntimeConfig
	if targetKind == "directory-list" || targetKind == "directory-list-mounted" || targetKind == "directory-list-recursive" || targetKind == "directory-list-recursive-mounted" || targetKind == "directory-list-recursive-filtered" || targetKind == "directory-list-recursive-large" || targetKind == "content-search" || targetKind == "content-search-mounted" {
		if targetKind == "directory-list-recursive" || targetKind == "directory-list-recursive-mounted" || targetKind == "directory-list-recursive-filtered" || targetKind == "directory-list-recursive-large" {
			if err := enableRecursiveListingForNativeStudy(service); err != nil {
				_ = service.Close(context.Background())
				t.Fatal(err)
			}
		}
		if targetKind == "content-search" || targetKind == "content-search-mounted" {
			if err := enableSearchForNativeStudy(service); err != nil {
				_ = service.Close(context.Background())
				t.Fatal(err)
			}
		}
		config = runtime.BundledConversationToolRuntimeConfigWithFilesystemRead(
			approval,
			runtime.ToolClarificationFunc(func(context.Context, runtime.ClarificationRequest) (runtime.ClarificationResponse, error) {
				return runtime.ClarificationResponse{}, errors.New("synthetic clarification is unavailable")
			}),
			service,
		)
		config.ReadAdmission = fixture.readHost
		config.Preparer = service
		config.Grants = permissions.NewMemoryStore()
	} else {
		config = runtime.ToolRuntimeConfig{
			Registry: tools.NewRegistry(), Preparer: service, ReadAdmission: fixture.readHost, RootHost: service,
			Approval: approval, Grants: permissions.NewMemoryStore(),
		}
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
	if protectedMountSource != "" {
		protectedSource, err := os.Open(protectedMountSource)
		if err != nil {
			_ = fixture.chat.CloseContext(context.Background())
			_ = service.Close(context.Background())
			t.Fatalf("open synthetic protected mount source: %v", err)
		}
		if err := service.Protect(ctx, auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, authPath, protectedSource); err != nil {
			_ = protectedSource.Close()
			_ = fixture.chat.CloseContext(context.Background())
			_ = service.Close(context.Background())
			t.Fatalf("register synthetic protected mount source identity: %v", err)
		}
		_ = protectedSource.Close()
	}
	if err := runtime.EnableNativeReadStudy(fixture.chat); err != nil {
		_ = fixture.chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatalf("test-only read path setup failed: %v", err)
	}
	if targetKind == "directory-list" || targetKind == "directory-list-mounted" || targetKind == "directory-list-recursive" || targetKind == "directory-list-recursive-mounted" || targetKind == "directory-list-recursive-filtered" || targetKind == "directory-list-recursive-large" {
		if err := runtime.EnableNativeDirectoryListingStudy(fixture.chat); err != nil {
			_ = fixture.chat.CloseContext(context.Background())
			_ = service.Close(context.Background())
			t.Fatalf("test-only directory listing path setup failed: %v", err)
		}
	}
	if targetKind == "content-search" || targetKind == "content-search-mounted" {
		if err := runtime.EnableNativeSearchStudy(fixture.chat); err != nil {
			_ = fixture.chat.CloseContext(context.Background())
			_ = service.Close(context.Background())
			t.Fatalf("test-only search path setup failed: %v", err)
		}
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

func TestStage6RecursiveListingRefusesIncompleteOwnerInventory(t *testing.T) {
	stage6Begin("recursive_listing_incomplete_owner_inventory")
	root := t.TempDir()
	target := filepath.Join(root, "listing-scope")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "private-name.txt"), []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
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
	if err := enableRecursiveListingForNativeStudy(service); err != nil {
		_ = service.Close(context.Background())
		t.Fatalf("enable recursive listing for missing-owner fixture: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	base := &stage6Provider{
		target: target, toolID: tools.ListFilesID,
		listingOptions: json.RawMessage(`{"recursive":true,"include_hidden":false,"include_ignored":false,"name_globs":["*.txt"]}`),
		firstToolCall:  true, secondStarted: make(chan struct{}), releaseSecond: make(chan struct{}),
	}
	providers, err := model.NewRegistry(base)
	if err != nil {
		_ = service.Close(context.Background())
		t.Fatal(err)
	}
	var approvals atomic.Int32
	approval := runtime.ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		approvals.Add(1)
		return permissions.ApprovalOnce, "", nil
	})
	clarification := runtime.ToolClarificationFunc(func(context.Context, runtime.ClarificationRequest) (runtime.ClarificationResponse, error) {
		return runtime.ClarificationResponse{}, errors.New("synthetic clarification is unavailable")
	})
	authStore := auth.Store{Path: filepath.Join(root, "synthetic-chat-auth.json")}
	config := runtime.BundledConversationToolRuntimeConfigWithFilesystemRead(approval, clarification, service)
	config.ReadAdmission = &stage6ReadHost{service: service}
	config.Preparer = service
	config.Grants = permissions.NewMemoryStore()
	chat := runtime.NewChatWithToolRuntime(providers, authStore, nil, config)
	if err := chat.AddProfile(ctx, "stage6-incomplete-list-profile", "openai", "synthetic profile", "synthetic-only-key", nil); err != nil {
		_ = chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatal(err)
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
		t.Fatal(err)
	}
	if err := chat.SelectFilesystemRoot(ctx, root); err != nil {
		_ = chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatal(err)
	}
	if err := runtime.EnableNativeReadStudy(chat); err != nil {
		_ = chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatal(err)
	}
	if err := runtime.EnableNativeDirectoryListingStudy(chat); err != nil {
		_ = chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := chat.CloseContext(cleanupCtx); err != nil {
			t.Errorf("incomplete-listing chat cleanup failed: %v", err)
		}
		if err := service.Close(cleanupCtx); err != nil {
			t.Errorf("incomplete-listing service cleanup failed: %v", err)
		} else {
			stage6Update("recursive_listing_incomplete_owner_inventory", func(item *stage6Case) { item.CleanupConfirmed = true })
		}
	})
	turn, err := chat.Send(ctx, "list the synthetic private directory", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("incomplete-inventory listing did not return a safe continuation: outcome=%s err=%v", turn.Outcome, err)
	}
	requests := base.captured()
	if len(requests) != 2 {
		t.Fatalf("provider requests=%d, want initial listing call and refusal continuation", len(requests))
	}
	var listingDefinition *tools.Definition
	for index := range requests[0].Tools {
		if requests[0].Tools[index].ID == tools.ListFilesID {
			listingDefinition = &requests[0].Tools[index]
			break
		}
	}
	var listingSchema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if listingDefinition == nil || json.Unmarshal(listingDefinition.InputSchema, &listingSchema) != nil ||
		listingSchema.Properties["recursive"] == nil || listingSchema.Properties["name_globs"] == nil {
		definitions, _ := json.Marshal(requests[0].Tools)
		t.Fatalf("initial provider request did not advertise recursive list_files options: tools=%s", definitions)
	}
	var recursiveCallFound bool
	for _, message := range requests[1].Messages {
		for _, native := range message.Native {
			var call struct {
				Type      string `json:"type"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}
			if err := json.Unmarshal(native, &call); err != nil || call.Type != "function_call" || call.Name != string(tools.ListFilesID) {
				continue
			}
			var options struct {
				Recursive bool     `json:"recursive"`
				NameGlobs []string `json:"name_globs"`
			}
			if err := json.Unmarshal([]byte(call.Arguments), &options); err == nil && options.Recursive && slices.Equal(options.NameGlobs, []string{"*.txt"}) {
				recursiveCallFound = true
			}
		}
	}
	if !recursiveCallFound {
		t.Fatalf("provider continuation did not retain the explicit recursive list_files call: messages=%+v", requests[1].Messages)
	}
	if _, found := readResultContent(requests[1], "stage6-read"); !found {
		t.Fatal("incomplete-inventory continuation omitted correlated listing refusal")
	}
	resultText, _ := readResultContent(requests[1], "stage6-read")
	var refusal struct {
		Outcome string `json:"outcome"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(resultText), &refusal); err != nil || refusal.Outcome != string(tools.OutcomeNotExecuted) || refusal.Content == "" {
		t.Fatalf("incomplete inventory result=%q; want bounded not_executed refusal (err=%v)", resultText, err)
	}
	for _, request := range requests {
		encoded, _ := json.Marshal(request.Messages)
		if strings.Contains(string(encoded), "private-name.txt") {
			t.Fatal("private listing name reached provider with incomplete owner coverage")
		}
	}
	if approvals.Load() != 0 || launcher.starts.Load() != 0 || len(impl.session.AdmissionConversations()) != 0 {
		t.Fatalf("incomplete listing crossed trust boundary: approvals=%d starts=%d admissions=%v", approvals.Load(), launcher.starts.Load(), impl.session.AdmissionConversations())
	}
	_, messages, results, markerFound, payloadFree := runtime.NativeReadStudyHistoryEvidence(chat, "private-name.txt")
	if messages == 0 || results != 1 || markerFound || !payloadFree {
		t.Fatalf("incomplete listing history contains sensitive data: messages=%d results=%d marker=%t payload_free=%t", messages, results, markerFound, payloadFree)
	}
	stage6Update("recursive_listing_incomplete_owner_inventory", func(item *stage6Case) {
		item.EvidenceKind, item.ProviderRequestCount, item.DisclosureWithheld, item.StaleDataAbsent = "synthetic-runtime-composition", len(requests), true, true
	})
	stage6PassSynthetic("recursive_listing_incomplete_owner_inventory", "the provider advertised advanced listing options and requested recursive listing, then received only a correlated not_executed refusal because required auth/settings owner coverage was incomplete; no approval, admission, worker, count, or private name crossed the boundary")
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

func TestStage6DirectoryListingMountBoundary(t *testing.T) {
	stage6Begin("directory_listing_mount_boundary")
	if os.Getenv("KOMPOSYN_STAGE6_WORKER_IMAGE") != "" {
		stage6Skip("directory_listing_mount_boundary", "mounted-subdirectory fixture is measured only with the fixed Stage 6 test-binary worker profile")
		stage6Skip("search_mount_boundary", "mounted-subdirectory fixture is measured only with the fixed Stage 6 test-binary worker profile")
		t.Skip("mounted-subdirectory fixture is unavailable in shipped-worker image mode")
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
		reason := fmt.Sprintf(
			"trusted Bubblewrap validation refused before the mounted-directory fixture or worker launch (uid=%d mode=%#o error=%s)",
			inspection.UID, inspection.Mode, boundedStage6Diagnostic(inspection.Error),
		)
		stage6Skip("directory_listing_mount_boundary", reason)
		stage6Skip("search_mount_boundary", reason)
		t.Skip("trusted native Bubblewrap backend unavailable")
	}
	backendInfo, err := os.Stat(backendPath)
	if err != nil {
		t.Fatalf("inspect trusted Bubblewrap for nested fixture: %v", err)
	}
	if backendInfo.Size() <= 0 || backendInfo.Size() > 64<<20 {
		t.Fatalf("trusted Bubblewrap size is outside the nested fixture bound: size=%d err=%v", backendInfo.Size(), err)
	}
	backendData, err := os.ReadFile(backendPath)
	if err != nil || int64(len(backendData)) != backendInfo.Size() {
		t.Fatalf("read bounded trusted Bubblewrap for nested fixture: bytes=%d size=%d err=%v", len(backendData), backendInfo.Size(), err)
	}
	backendDigest := sha256.Sum256(backendData)
	if hex.EncodeToString(backendDigest[:]) != inspection.SHA256 {
		t.Fatal("trusted Bubblewrap changed while preparing the nested fixture")
	}
	root := t.TempDir()
	runtimeRoot := filepath.Join(root, "runtime-root")
	listingPath := filepath.Join(runtimeRoot, "listing-scope")
	mountpoint := filepath.Join(listingPath, "mounted-boundary")
	mountSource := filepath.Join(root, "synthetic-mounted-source")
	searchRoot := filepath.Join(root, "runtime-search-root")
	searchScope := filepath.Join(searchRoot, "search-scope")
	searchMountpoint := filepath.Join(searchScope, "mounted-boundary")
	searchMountSource := filepath.Join(root, "synthetic-search-mounted-source")
	nestedBackendInput := filepath.Join(root, "nested-bwrap-input")
	if err := os.WriteFile(nestedBackendInput, backendData, 0600); err != nil {
		t.Fatalf("prepare exact nested Bubblewrap input: %v", err)
	}
	backendData = nil
	backendFile, err := os.Open(nestedBackendInput)
	if err != nil {
		t.Fatalf("open exact nested Bubblewrap input: %v", err)
	}
	defer backendFile.Close()
	for _, directory := range []string{runtimeRoot, listingPath, mountpoint, mountSource, searchRoot, searchScope, searchMountpoint, searchMountSource} {
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatalf("prepare synthetic mount fixture: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(mountSource, "mounted-only-marker.txt"), []byte("synthetic mounted marker"), 0600); err != nil {
		t.Fatalf("prepare mounted marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(searchMountSource, "mounted.go"), []byte("synthetic-never-real-key\n"), 0600); err != nil {
		t.Fatalf("prepare search mounted marker: %v", err)
	}
	childReportPath := filepath.Join(root, "mounted-child-report.json")
	executable, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("resolve Stage 6 test image: %v", err)
	}
	executableDirectory := filepath.Dir(executable)
	arguments := []string{
		"--die-with-parent", "--new-session", "--unshare-user", "--uid", "0", "--gid", "0", "--cap-add", "CAP_SYS_ADMIN", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--unshare-net", "--as-pid-1",
		"--tmpfs", "/", "--dir", "/usr", "--ro-bind", "/usr", "/usr",
		"--dev", "/dev", "--proc", "/proc", "--dir", "/tmp", "--chmod", "1777", "/tmp",
	}
	for _, directory := range []string{"bin", "lib", "lib64"} {
		if _, err := os.Stat(filepath.Join("/usr", directory)); err == nil {
			arguments = append(arguments, "--symlink", filepath.Join("usr", directory), string(filepath.Separator)+directory)
		}
	}
	directoryTargets := map[string]bool{"/usr": true, "/tmp": true}
	appendDirectoryTargets := func(value string) {
		current := string(filepath.Separator)
		for _, component := range strings.Split(strings.Trim(value, string(filepath.Separator)), string(filepath.Separator)) {
			if component == "" {
				continue
			}
			current = filepath.Join(current, component)
			if !directoryTargets[current] {
				arguments = append(arguments, "--dir", current)
				directoryTargets[current] = true
			}
		}
	}
	appendDirectoryTargets(root)
	arguments = append(arguments, "--bind", root, root, "--dir", "/trusted", "--dir", "/trusted/bin", "--file", "3", "/trusted/bin/bwrap", "--chmod", "0755", "/trusted/bin/bwrap")
	appendDirectoryTargets(executableDirectory)
	arguments = append(arguments, "--ro-bind", executableDirectory, executableDirectory, "--ro-bind", mountSource, mountpoint, "--ro-bind", searchMountSource, searchMountpoint)
	for key, value := range map[string]string{
		"HOME": "/tmp", "KOMPOSYN_STAGE6_BWRAP": "/trusted/bin/bwrap",
		"KOMPOSYN_STAGE6_DIRECTORY_MOUNT_ROOT": root, "KOMPOSYN_STAGE6_REPORT": childReportPath,
		"KOMPOSYN_STAGE6_SOURCE_COMMIT": os.Getenv("KOMPOSYN_STAGE6_SOURCE_COMMIT"),
		"KOMPOSYN_STAGE6_BIND_MODE":     os.Getenv("KOMPOSYN_STAGE6_BIND_MODE"),
	} {
		arguments = append(arguments, "--setenv", key, value)
	}
	arguments = append(arguments, "--chdir", "/tmp", "--", executable, "-test.run=^TestStage6(DirectoryListing|Search)MountBoundaryChild$", "-test.timeout=60s", "-test.count=1")
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, backendPath, arguments...)
	command.ExtraFiles = []*os.File{backendFile}
	output := &stage6BoundedBuffer{max: 16 << 10}
	command.Stdout, command.Stderr = output, output
	runErr := command.Run()
	if ctx.Err() != nil {
		stage6Skip("directory_listing_mount_boundary", "isolated mounted-directory fixture exceeded its bounded deadline")
		stage6Skip("search_mount_boundary", "isolated mounted-directory fixture exceeded its bounded deadline")
		t.Skip("mounted-directory helper exceeded its bounded deadline")
	}
	reportData, readErr := os.ReadFile(childReportPath)
	if readErr != nil {
		stage6Skip("directory_listing_mount_boundary", "outer Bubblewrap could not establish the isolated synthetic mount fixture")
		stage6Skip("search_mount_boundary", "outer Bubblewrap could not establish the isolated synthetic mount fixture")
		t.Skip("outer Bubblewrap did not establish the synthetic mount fixture")
	}
	if len(reportData) > 2<<20 {
		t.Fatal("mounted-directory child report exceeded its bound")
	}
	var childReport stage6Report
	if err := json.Unmarshal(reportData, &childReport); err != nil {
		t.Fatalf("mounted-directory child report was invalid: %v", err)
	}
	var measured *stage6Case
	for index := range childReport.Cases {
		if childReport.Cases[index].ID == "directory_listing_mount_boundary" {
			measured = &childReport.Cases[index]
			break
		}
	}
	if measured == nil {
		t.Fatalf("mounted-directory child report omitted its required case (run error=%v): output=%q", runErr, output.String())
	}
	if measured.Status == "unavailable" {
		stage6Skip("directory_listing_mount_boundary", "mounted-directory child profile unavailable: "+boundedStage6Diagnostic(measured.Reason))
	} else if measured.Status != "measured_pass" || !measured.WorkerStarted || !measured.WorkerJoined || !measured.CapabilityTransferred || !measured.ReadStarted || !measured.ProbePassed || !measured.CleanupConfirmed || !measured.FixtureBytesMatched {
		t.Fatalf("mounted-directory child case did not pass (run error=%v): output=%q case=%+v", runErr, output.String(), measured)
	}
	if measured.Status == "measured_pass" {
		stage6Update("directory_listing_mount_boundary", func(item *stage6Case) {
			item.FixtureFS = append(item.FixtureFS, measured.FixtureFS...)
			item.LaunchPolicy = measured.LaunchPolicy
			item.WorkerStarted, item.WorkerReady, item.WorkerJoined = measured.WorkerStarted, measured.WorkerReady, measured.WorkerJoined
			item.CapabilityTransferred, item.ReadStarted, item.ProbePassed = measured.CapabilityTransferred, measured.ReadStarted, measured.ProbePassed
			item.OnceApprovalCount, item.OnceGrantConsumptionCount = measured.OnceApprovalCount, measured.OnceGrantConsumptionCount
			item.ProviderRequestCount, item.FixtureBytesMatched = measured.ProviderRequestCount, measured.FixtureBytesMatched
			item.DisclosureWithheld, item.StaleDataAbsent, item.CleanupConfirmed = measured.DisclosureWithheld, measured.StaleDataAbsent, measured.CleanupConfirmed
		})
		stage6Pass("directory_listing_mount_boundary", "a synthetic bind-mounted child had a distinct native mount ID; the worker inherited the isolated fixture user namespace while retaining its own mount and PID namespaces; one-level listing disclosed the mountpoint name without exposing its marker")
	}

	var searchMeasured *stage6Case
	for index := range childReport.Cases {
		if childReport.Cases[index].ID == "search_mount_boundary" {
			searchMeasured = &childReport.Cases[index]
			break
		}
	}
	if searchMeasured == nil {
		t.Fatalf("mounted-directory child report omitted search case (run error=%v): output=%q", runErr, output.String())
	}
	if searchMeasured.Status == "unavailable" {
		stage6Skip("search_mount_boundary", "mounted search child profile unavailable: "+boundedStage6Diagnostic(searchMeasured.Reason))
		return
	}
	if runErr != nil || searchMeasured.Status != "measured_pass" || !searchMeasured.WorkerStarted || !searchMeasured.WorkerJoined || !searchMeasured.CapabilityTransferred || !searchMeasured.ReadStarted || !searchMeasured.ProbePassed || !searchMeasured.CleanupConfirmed || !searchMeasured.FixtureBytesMatched {
		t.Fatalf("mounted search child case did not pass (run error=%v): output=%q case=%+v", runErr, output.String(), searchMeasured)
	}
	stage6Update("search_mount_boundary", func(item *stage6Case) {
		item.FixtureFS = append(item.FixtureFS, searchMeasured.FixtureFS...)
		item.LaunchPolicy = searchMeasured.LaunchPolicy
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = searchMeasured.WorkerStarted, searchMeasured.WorkerReady, searchMeasured.WorkerJoined
		item.CapabilityTransferred, item.ReadStarted, item.ProbePassed = searchMeasured.CapabilityTransferred, searchMeasured.ReadStarted, searchMeasured.ProbePassed
		item.OnceApprovalCount, item.OnceGrantConsumptionCount = searchMeasured.OnceApprovalCount, searchMeasured.OnceGrantConsumptionCount
		item.ProviderRequestCount, item.FixtureBytesMatched = searchMeasured.ProviderRequestCount, searchMeasured.FixtureBytesMatched
		item.DisclosureWithheld, item.StaleDataAbsent, item.CleanupConfirmed = searchMeasured.DisclosureWithheld, searchMeasured.StaleDataAbsent, searchMeasured.CleanupConfirmed
	})
	stage6Pass("search_mount_boundary", "recursive content search returned an unmounted matching control while excluding matching content beneath a distinct native mount ID")
}

func TestStage6DirectoryListingMountBoundaryChild(t *testing.T) {
	if os.Getenv("KOMPOSYN_STAGE6_DIRECTORY_MOUNT_ROOT") == "" {
		t.Skip("private child used only by the outer mounted-directory fixture")
	}
	stage6Begin("directory_listing_mount_boundary")
	stage6Update("directory_listing_mount_boundary", func(item *stage6Case) {
		item.LaunchPolicy = map[string]any{
			"user_namespace":        "inherited the isolated fixture namespace; no additional user mapping requested",
			"outer_capability":      "CAP_SYS_ADMIN inside the isolated fixture user namespace, used only to create the nested worker namespaces",
			"worker_capabilities":   "dropped by the nested Bubblewrap worker profile",
			"mount_namespace":       "worker-private",
			"pid_namespace":         "worker-private",
			"network_namespace":     "inherited isolated fixture namespace; network syscalls are denied by worker seccomp",
			"disable_userns_option": "not used because the worker inherits the fixture user namespace; worker seccomp denies unshare, setns, and process-like clone after bootstrap",
		}
	})
	fixture := newStage6Fixture(t, "directory_listing_mount_boundary", "directory-list-recursive-mounted", false, nil)
	fixture.reportCases = []string{"directory_listing_mount_boundary"}
	target := filepath.Join(fixture.root, "listing-scope")
	mountpoint := filepath.Join(target, "mounted-boundary")
	parentMountID, err := stage6MountID(target)
	if err != nil {
		t.Fatalf("inspect selected directory mount ID: %v", err)
	}
	childMountID, err := stage6MountID(mountpoint)
	if err != nil || childMountID == parentMountID {
		t.Fatalf("fixture child is not a distinct mount: parent=%d child=%d err=%v", parentMountID, childMountID, err)
	}
	stage6CaptureFixtureFilesystem("directory_listing_mount_boundary", "nested_mount_boundary", mountpoint)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	turn, err := fixture.chat.Send(ctx, "list the synthetic fixture directory", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("mounted-directory listing turn outcome=%s err=%v", turn.Outcome, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 2 {
		t.Fatalf("provider requests=%d, want initial and listing continuation", len(requests))
	}
	continuation, _ := json.Marshal(requests[1].Messages)
	if !strings.Contains(string(continuation), "mounted-boundary") || !strings.Contains(string(continuation), "visible.go") || strings.Contains(string(continuation), "mounted-only-marker.txt") || strings.Contains(string(continuation), "protected-mounted-alias") {
		t.Fatalf("listing crossed the mounted child boundary: %s", continuation)
	}
	impl := fixture.service.impl.(*linuxImplementation)
	evidence := impl.session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadSucceeded || !evidence[0].WorkerStarted || !evidence[0].WorkerReady || !evidence[0].WorkerJoined || !evidence[0].CapabilitySent || !evidence[0].ReadStarted || !stage6DirectoryListingProbePassed(evidence[0].Probe) {
		t.Fatalf("mounted-directory listing worker evidence=%+v", evidence)
	}
	stage6Update("directory_listing_mount_boundary", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = evidence[0].WorkerStarted, evidence[0].WorkerReady, evidence[0].WorkerJoined
		item.CapabilityTransferred, item.ReadStarted, item.ProbePassed = evidence[0].CapabilitySent, evidence[0].ReadStarted, stage6DirectoryListingProbePassed(evidence[0].Probe)
		item.OnceApprovalCount, item.OnceGrantConsumptionCount, item.ProviderRequestCount = fixture.approvals.Load(), 1, len(requests)
		item.FixtureBytesMatched = true
	})
	stage6Pass("directory_listing_mount_boundary", "the actual mount-ID-distinct child directory was listed only as a direct entry, its mounted marker stayed hidden, and a protected auth object bind-mounted under an ordinary alias was filtered")
}

func TestStage6SearchMountBoundaryChild(t *testing.T) {
	if os.Getenv("KOMPOSYN_STAGE6_DIRECTORY_MOUNT_ROOT") == "" {
		t.Skip("private child used only by the outer mounted-directory fixture")
	}
	stage6Begin("search_mount_boundary")
	stage6Update("search_mount_boundary", func(item *stage6Case) {
		item.LaunchPolicy = map[string]any{
			"user_namespace":        "inherited the isolated fixture namespace; no additional user mapping requested",
			"outer_capability":      "CAP_SYS_ADMIN inside the isolated fixture user namespace, used only to create the nested worker namespaces",
			"worker_capabilities":   "dropped by the nested Bubblewrap worker profile",
			"mount_namespace":       "worker-private",
			"pid_namespace":         "worker-private",
			"network_namespace":     "inherited isolated fixture namespace; network syscalls are denied by worker seccomp",
			"disable_userns_option": "not used because the worker inherits the fixture user namespace; worker seccomp denies unshare, setns, and process-like clone after bootstrap",
		}
	})
	fixture := newStage6Fixture(t, "search_mount_boundary", "content-search-mounted", false, nil)
	fixture.reportCases = []string{"search_mount_boundary"}
	target := filepath.Join(fixture.root, "search-scope")
	mountpoint := filepath.Join(target, "mounted-boundary")
	parentMountID, err := stage6MountID(target)
	if err != nil {
		t.Fatalf("inspect selected search directory mount ID: %v", err)
	}
	childMountID, err := stage6MountID(mountpoint)
	if err != nil || childMountID == parentMountID {
		t.Fatalf("search fixture child is not a distinct mount: parent=%d child=%d err=%v", parentMountID, childMountID, err)
	}
	stage6CaptureFixtureFilesystem("search_mount_boundary", "nested_mount_boundary", mountpoint)
	nested := filepath.Join(target, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatalf("create unmounted search control directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "control.go"), []byte("synthetic-never-real-key\n"), 0600); err != nil {
		t.Fatalf("create unmounted matching control: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	turn, err := fixture.chat.Send(ctx, "search the synthetic mounted-boundary fixture", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("mounted-boundary search outcome=%s err=%v", turn.Outcome, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 2 {
		t.Fatalf("provider requests=%d, want initial and search continuation", len(requests))
	}
	continuation, _ := json.Marshal(requests[1].Messages)
	encoded := string(continuation)
	if !strings.Contains(encoded, "nested/control.go") || !strings.Contains(encoded, "synthetic-never-real-key") || strings.Contains(encoded, "mounted.go") {
		t.Fatalf("search crossed mount boundary or lost its unmounted control: %s", encoded)
	}
	impl := fixture.service.impl.(*linuxImplementation)
	evidence := impl.session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadSucceeded || !evidence[0].WorkerStarted || !evidence[0].WorkerJoined || !evidence[0].CapabilitySent || !evidence[0].ReadStarted || !stage6DirectoryListingProbePassed(evidence[0].SearchTraversalProbe) || !stage6WorkerProbePassed(evidence[0].SearchContentProbe) || evidence[0].SearchContentProbe.DirectoryTraversalSyscallsAllowed {
		t.Fatalf("mounted-boundary search worker evidence=%+v", evidence)
	}
	stage6Update("search_mount_boundary", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = evidence[0].WorkerStarted, evidence[0].WorkerReady, evidence[0].WorkerJoined
		item.CapabilityTransferred, item.ReadStarted = evidence[0].CapabilitySent, evidence[0].ReadStarted
		item.ProbePassed = stage6DirectoryListingProbePassed(evidence[0].SearchTraversalProbe) && stage6WorkerProbePassed(evidence[0].SearchContentProbe)
		item.OnceApprovalCount, item.OnceGrantConsumptionCount, item.ProviderRequestCount = fixture.approvals.Load(), 1, len(requests)
		item.FixtureBytesMatched, item.CleanupConfirmed = true, evidence[0].WorkerJoined
	})
	stage6Pass("search_mount_boundary", "the recursive search worker excluded matching content beneath an actual mount-ID-distinct child while returning the unmounted matching control")
}

func stage6MountID(path string) (uint64, error) {
	var stat unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_STATX_SYNC_AS_STAT, unix.STATX_MNT_ID, &stat); err != nil {
		return 0, err
	}
	if stat.Mask&unix.STATX_MNT_ID == 0 {
		return 0, errors.New("mount ID is unavailable")
	}
	return stat.Mnt_id, nil
}

func TestStage6PositiveContentSearch(t *testing.T) {
	stage6Begin("positive_content_search")
	fixture := newStage6Fixture(t, "positive_content_search", "content-search", false, nil)
	fixture.reportCases = []string{"positive_content_search"}
	target := filepath.Join(fixture.root, "listing-scope")
	nested := filepath.Join(target, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal("create nested search directory:", err)
	}
	control := filepath.Join(nested, "control.go")
	if err := os.WriteFile(control, []byte("prefix synthetic-never-real-key suffix\n"), 0600); err != nil {
		t.Fatal("create unprotected matching control:", err)
	}
	protectedAlias := filepath.Join(nested, "protected-auth-alias.go")
	if err := os.Link(fixture.authPath, protectedAlias); err != nil {
		t.Fatal("create protected auth hardlink alias:", err)
	}
	gitMarker := filepath.Join(target, ".git", "must-not-search.go")
	if err := os.WriteFile(gitMarker, []byte("synthetic-never-real-key\n"), 0600); err != nil {
		t.Fatal("create .git exclusion marker:", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	turn, err := fixture.chat.Send(ctx, "search this synthetic directory", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("search turn outcome=%s err=%v", turn.Outcome, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 2 {
		t.Fatalf("provider requests=%d, want initial and search continuation", len(requests))
	}
	definitionBytes, _ := json.Marshal(requests[0].Tools)
	if !strings.Contains(string(definitionBytes), string(tools.SearchFilesID)) || !strings.Contains(string(definitionBytes), "query") {
		t.Fatalf("search_files was not advertised with its query schema: %s", definitionBytes)
	}
	continuation, _ := json.Marshal(requests[1].Messages)
	encoded := string(continuation)
	if !strings.Contains(encoded, "nested/control.go") || !strings.Contains(encoded, "synthetic-never-real-key") || strings.Contains(encoded, "protected-auth-alias.go") || strings.Contains(encoded, "must-not-search.go") {
		t.Fatalf("search continuation did not preserve control result and protected/.git exclusions: %s", encoded)
	}
	if fixture.approvals.Load() != 1 {
		t.Fatalf("search once approvals=%d, want one", fixture.approvals.Load())
	}
	for _, want := range []string{target, "synthetic-never-real-key", "recursive=true", "include_hidden=true", "include_ignored=true", `name_globs=["*.go"]`, "openai/"} {
		if !strings.Contains(fixture.approvalDescription, want) {
			t.Fatalf("search approval omits %q: %s", want, fixture.approvalDescription)
		}
	}
	impl := fixture.service.impl.(*linuxImplementation)
	evidence := impl.session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadSucceeded || !evidence[0].WorkerStarted || !evidence[0].WorkerJoined || !evidence[0].CapabilitySent || !evidence[0].ReadStarted || !stage6DirectoryListingProbePassed(evidence[0].SearchTraversalProbe) || !stage6WorkerProbePassed(evidence[0].SearchContentProbe) || evidence[0].SearchContentProbe.DirectoryTraversalSyscallsAllowed {
		t.Fatalf("search content worker evidence=%+v", evidence)
	}
	stage6Update("positive_content_search", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = evidence[0].WorkerStarted, evidence[0].WorkerReady, evidence[0].WorkerJoined
		item.CapabilityTransferred, item.ReadStarted, item.ProbePassed = evidence[0].CapabilitySent, evidence[0].ReadStarted, stage6WorkerProbePassed(evidence[0].Probe)
		item.OnceApprovalCount, item.OnceGrantConsumptionCount, item.ProviderRequestCount = fixture.approvals.Load(), 1, len(requests)
		item.FixtureBytesMatched, item.CleanupConfirmed = true, evidence[0].WorkerJoined
	})
	stage6Pass("positive_content_search", "actual composed sandbox Service traversed the approved recursive directory, excluded a matching protected hardlink alias and .git content, and returned only the unprotected matching line through provider continuation")
}

func TestStage6SearchCandidateProtectedSubstitution(t *testing.T) {
	stage6Begin("search_candidate_protected_substitution")
	fixture := newStage6Fixture(t, "search_candidate_protected_substitution", "content-search", false, nil)
	fixture.reportCases = []string{"search_candidate_protected_substitution"}
	nested := filepath.Join(fixture.root, "listing-scope", "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal("create nested search directory:", err)
	}
	for _, name := range []string{"control.go", "race.go", "same-inode.go"} {
		if err := os.WriteFile(filepath.Join(nested, name), []byte("synthetic-never-real-key\n"), 0600); err != nil {
			t.Fatal("create unprotected matching control:", err)
		}
	}
	sameInodePath := filepath.Join(nested, "same-inode.go")
	sameInodeBefore, err := os.Stat(sameInodePath)
	if err != nil {
		t.Fatal("stat same-inode search candidate:", err)
	}
	impl := fixture.service.impl.(*linuxImplementation)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	transitioned := false
	actorJoined := false
	impl.session.SetCheckpointHook(func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint != readbrokerstudy.CheckpointAfterSearchContent || transitioned {
			return nil
		}
		if err := os.WriteFile(sameInodePath, bytes.Repeat([]byte{'x'}, len("synthetic-never-real-key\n")), 0600); err != nil {
			return err
		}
		sameInodeAfter, err := os.Stat(sameInodePath)
		if err != nil || !os.SameFile(sameInodeBefore, sameInodeAfter) || sameInodeAfter.Size() != sameInodeBefore.Size() {
			return errors.New("same-inode search candidate replacement was not an in-place same-size mutation")
		}
		if err := os.Mkdir(filepath.Join(fixture.root, "outside"), 0700); err != nil {
			return err
		}
		transitions := []struct {
			action readbrokerstudy.NativeActorAction
			source string
			target string
		}{
			{readbrokerstudy.NativeActorRename, "listing-scope/nested/race.go", "outside/race.go"},
			{readbrokerstudy.NativeActorHardlink, "synthetic-auth.json", "listing-scope/nested/race.go"},
		}
		for _, transition := range transitions {
			actor, err := readbrokerstudy.NewNativeActor(ctx, os.Args[0], fixture.root, transition.action, transition.source, transition.target)
			if err != nil {
				return err
			}
			if !actor.Ready() {
				_ = actor.Close()
				return errors.New("search protected-substitution actor did not become ready")
			}
			if err := actor.Run(ctx); err != nil {
				_ = actor.Close()
				return err
			}
			if err := actor.Close(); err != nil || !actor.Joined() || !actor.TransitionCompleted() {
				return errors.New("search protected-substitution actor was not joined")
			}
		}
		actorJoined = true
		transitioned = true
		return nil
	})
	turn, err := fixture.chat.Send(ctx, "search this synthetic directory", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted || !transitioned || !actorJoined {
		t.Fatalf("search turn outcome=%s transition=%t actor_joined=%t err=%v", turn.Outcome, transitioned, actorJoined, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 2 {
		t.Fatalf("provider requests=%d, want initial and search continuation", len(requests))
	}
	resultText, found := readResultContent(requests[1], "stage6-read")
	if !found {
		t.Fatal("search continuation omitted the correlated tool result")
	}
	var envelope struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(resultText), &envelope); err != nil {
		t.Fatal("decode search tool envelope:", err)
	}
	var result struct {
		Matches      []map[string]any `json:"matches"`
		Incomplete   bool             `json:"incomplete"`
		SkippedFiles int              `json:"skipped_files"`
	}
	if err := json.Unmarshal([]byte(envelope.Content), &result); err != nil {
		t.Fatal("decode search result:", err)
	}
	if len(result.Matches) != 1 || result.Matches[0]["path"] != "nested/control.go" || !result.Incomplete || result.SkippedFiles != 2 {
		t.Fatalf("changed candidates were not omitted with truthful incomplete status: %+v", result)
	}
	impl.session.SetCheckpointHook(nil)
	stage6Update("search_candidate_protected_substitution", func(item *stage6Case) {
		item.ActorStarted, item.ActorTransitionCompleted, item.ActorJoined, item.ActorTransitionCount = true, transitioned, actorJoined, 2
		item.WorkerStarted, item.WorkerJoined, item.ReadStarted = true, true, true
		item.CapabilityTransferred, item.OnceApprovalCount, item.OnceGrantConsumptionCount = true, fixture.approvals.Load(), 1
		item.ProviderRequestCount, item.FixtureBytesMatched, item.CleanupConfirmed = len(requests), true, true
	})
	stage6Pass("search_candidate_protected_substitution", "after the content worker completed, a same-size in-place write changed one matching inode and an independent actor replaced another matching candidate with a protected auth hardlink; host revalidation omitted both, counted two permitted skips, marked the result incomplete, and retained a matching unprotected control")
}

func TestStage6SearchContentWorkerCancellationCleanup(t *testing.T) {
	stage6Begin("search_content_worker_cancellation_cleanup")
	fixture := newStage6Fixture(t, "search_content_worker_cancellation_cleanup", "content-search", false, nil)
	fixture.reportCases = []string{"search_content_worker_cancellation_cleanup"}
	nested := filepath.Join(fixture.root, "listing-scope", "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal("create nested search directory:", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "control.go"), []byte("synthetic-never-real-key\n"), 0600); err != nil {
		t.Fatal("create matching search control:", err)
	}
	fixture.provider.listingOptions = json.RawMessage(`{"query":"synthetic","recursive":true,"include_hidden":true,"include_ignored":true,"name_globs":["*.go"]}`)
	impl := fixture.service.impl.(*linuxImplementation)
	impl.session.SetCheckpointHook(func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint == readbrokerstudy.CheckpointBeforeSearchContent {
			return context.Canceled
		}
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	turn, err := fixture.chat.Send(ctx, "search this synthetic directory", nil)
	if err != nil || turn.Outcome != provider.OutcomeInterrupted {
		t.Fatalf("cancelled search turn outcome=%s err=%v", turn.Outcome, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 1 {
		t.Fatalf("provider requests=%d, want no continuation after cancellation", len(requests))
	}
	requestMessages, _ := json.Marshal(requests[0].Messages)
	if strings.Contains(string(requestMessages), "synthetic-never-real-key") {
		t.Fatalf("cancelled search disclosed candidate data in the initial provider request: %s", requestMessages)
	}
	_, historyMessages, toolResults, markerFound, payloadFree := runtime.NativeReadStudyHistoryEvidence(fixture.chat, "synthetic-never-real-key")
	if historyMessages == 0 || toolResults != 1 || markerFound || !payloadFree {
		t.Fatalf("cancelled search history was not payload-free: messages=%d results=%d marker=%t payload_free=%t", historyMessages, toolResults, markerFound, payloadFree)
	}
	evidence := impl.session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadCancelled || !evidence[0].WorkerStarted || !evidence[0].WorkerReady || !evidence[0].WorkerJoined || evidence[0].Published || !stage6DirectoryListingProbePassed(evidence[0].SearchTraversalProbe) || !stage6WorkerProbePassed(evidence[0].SearchContentProbe) || !strings.Contains(evidence[0].FailureDetail, "search cancelled before content handoff") {
		t.Fatalf("cancelled content worker evidence=%+v", evidence)
	}
	if published := impl.session.PublishedResults(); len(published) != 0 {
		t.Fatalf("cancelled search published %d private results", len(published))
	}
	stage6Update("search_content_worker_cancellation_cleanup", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = evidence[0].WorkerStarted, evidence[0].WorkerReady, evidence[0].WorkerJoined
		item.ProbePassed, item.OnceApprovalCount, item.OnceGrantConsumptionCount = stage6WorkerProbePassed(evidence[0].SearchContentProbe), fixture.approvals.Load(), 1
		item.ProviderRequestCount, item.DisclosureWithheld, item.StaleDataAbsent, item.CleanupConfirmed = len(requests), true, true, evidence[0].WorkerJoined
	})
	stage6Pass("search_content_worker_cancellation_cleanup", "after the search traversal completed, cancellation at the content worker checkpoint joined the worker before its separate snapshot handoff; no private result was published or sent in a provider continuation")
}

func TestStage6SearchIgnoreRulesChangedAfterContentSuppressesDisclosure(t *testing.T) {
	const caseID = "search_ignore_rules_changed_after_content"
	const query = "synthetic-never-real-key"
	const privateMarker = "private-search-match-marker"
	stage6Begin(caseID)
	var traversalJoined, contentJoined, snapshotClosed, candidatesClosed bool = true, true, true, true
	var probesPassed, allDisclosureWithheld, allStaleDataAbsent bool = true, true, true
	var totalApprovals int32
	var totalProviderRequests int
	var firstGrantRevision, lastGrantRevision uint64
	for _, mutation := range []string{"nested", "ancestor"} {
		fixture := newStage6Fixture(t, caseID, "content-search", false, nil)
		fixture.reportCases = []string{caseID}
		nested := filepath.Join(fixture.root, "listing-scope", "nested")
		if err := os.Mkdir(nested, 0700); err != nil {
			t.Fatal("create nested search directory:", err)
		}
		if err := os.WriteFile(filepath.Join(nested, "control.go"), []byte(query+" "+privateMarker+"\n"), 0600); err != nil {
			t.Fatal("create matching search control:", err)
		}
		fixture.provider.listingOptions = json.RawMessage(`{"query":"` + query + `","recursive":true,"include_hidden":true,"include_ignored":false,"name_globs":["*.go"]}`)
		impl := fixture.service.impl.(*linuxImplementation)
		grantRevisionBefore, grantsBefore := runtime.NativeReadStudyGrantState(fixture.chat)
		if grantsBefore != 0 {
			t.Fatalf("%s preexisting grants=%d, want none", mutation, grantsBefore)
		}
		if mutation == "nested" {
			firstGrantRevision = grantRevisionBefore
		}
		mutated := false
		impl.session.SetCheckpointHook(func(checkpoint readbrokerstudy.Checkpoint) error {
			if checkpoint != readbrokerstudy.CheckpointAfterSearchContent || mutated {
				return nil
			}
			ignorePath, pattern := filepath.Join(nested, ".gitignore"), "control.go\n"
			if mutation == "ancestor" {
				ignorePath = filepath.Join(fixture.root, ".gitignore")
				pattern = "listing-scope/nested/control.go\n"
			}
			if err := os.WriteFile(ignorePath, []byte(pattern), 0600); err != nil {
				return err
			}
			mutated = true
			return nil
		})
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		turn, err := fixture.chat.Send(ctx, "search this synthetic directory", nil)
		cancel()
		if err != nil || turn.Outcome != provider.OutcomeCompleted || !mutated {
			t.Fatalf("%s ignore mutation search outcome=%s mutated=%t err=%v", mutation, turn.Outcome, mutated, err)
		}
		requests := fixture.provider.captured()
		if len(requests) != 2 {
			t.Fatalf("%s provider requests=%d, want initial and safe error continuation", mutation, len(requests))
		}
		totalProviderRequests += len(requests)
		toolResult, found := readResultContent(requests[1], "stage6-read")
		if !found {
			t.Fatalf("%s safe continuation omitted its correlated search result", mutation)
		}
		var envelope struct {
			Outcome string `json:"outcome"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(toolResult), &envelope); err != nil || envelope.Outcome != string(tools.OutcomeFailed) || envelope.Content != "Filesystem request failed; no names or contents were disclosed." {
			t.Fatalf("%s continuation result=%q decoded=%+v err=%v", mutation, toolResult, envelope, err)
		}
		if strings.Contains(toolResult, privateMarker) || strings.Contains(toolResult, "nested/control.go") {
			t.Fatalf("%s stale search data reached correlated tool result: %s", mutation, toolResult)
		}
		if published := impl.session.PublishedResults(); len(published) != 0 {
			t.Fatalf("%s stale search published %d private results", mutation, len(published))
		}
		evidence := impl.session.Evidence()
		if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadFailed || !evidence[0].WorkerJoined || !evidence[0].ReadStarted || evidence[0].Published || !evidence[0].SearchTraversalWorkerJoined || !evidence[0].SearchContentWorkerJoined || !evidence[0].SearchSnapshotClosed || !evidence[0].SearchCandidatesClosed {
			t.Fatalf("%s stale search evidence=%+v, want failed refusal with both workers joined and all descriptors closed", mutation, evidence)
		}
		if !stage6WorkerProbePassed(evidence[0].SearchTraversalProbe) || !stage6WorkerProbePassed(evidence[0].SearchContentProbe) {
			t.Fatalf("%s worker probes were not established for both phases: %+v", mutation, evidence[0])
		}
		_, historyMessages, toolResults, markerFound, payloadFree := runtime.NativeReadStudyHistoryEvidence(fixture.chat, privateMarker, "nested/control.go")
		if historyMessages == 0 || toolResults != 1 || markerFound || !payloadFree {
			t.Fatalf("%s stale search history markers=%t tool_results=%d payload_free=%t", mutation, markerFound, toolResults, payloadFree)
		}
		grantRevisionAfter, grantsAfter := runtime.NativeReadStudyGrantState(fixture.chat)
		if grantRevisionAfter != grantRevisionBefore+2 || grantsAfter != 0 || fixture.approvals.Load() != 1 {
			t.Fatalf("%s once grant transition revision=%d→%d grants=%d approvals=%d", mutation, grantRevisionBefore, grantRevisionAfter, grantsAfter, fixture.approvals.Load())
		}
		lastGrantRevision = grantRevisionAfter
		totalApprovals += fixture.approvals.Load()
		traversalJoined = traversalJoined && evidence[0].SearchTraversalWorkerJoined
		contentJoined = contentJoined && evidence[0].SearchContentWorkerJoined
		snapshotClosed = snapshotClosed && evidence[0].SearchSnapshotClosed
		candidatesClosed = candidatesClosed && evidence[0].SearchCandidatesClosed
		probesPassed = probesPassed && stage6WorkerProbePassed(evidence[0].SearchTraversalProbe) && stage6WorkerProbePassed(evidence[0].SearchContentProbe)
		allDisclosureWithheld = allDisclosureWithheld && !evidence[0].Published
		allStaleDataAbsent = allStaleDataAbsent && !markerFound && !strings.Contains(toolResult, "nested/control.go")
		impl.session.SetCheckpointHook(nil)
	}
	stage6Update(caseID, func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = true, true, traversalJoined && contentJoined
		item.SearchTraversalWorkerJoined, item.SearchContentWorkerJoined = traversalJoined, contentJoined
		item.SearchSnapshotClosed, item.SearchCandidatesClosed = snapshotClosed, candidatesClosed
		item.CapabilityTransferred, item.ReadStarted, item.ReadOutcome = true, true, string(readbrokerstudy.ReadFailed)
		item.ProbePassed = probesPassed
		item.OnceApprovalCount, item.OnceGrantConsumptionCount = totalApprovals, 2
		item.GrantRevisionBefore, item.GrantRevisionAfter, item.RemainingGrantCount = firstGrantRevision, lastGrantRevision, 0
		item.ProviderRequestCount, item.DisclosureWithheld, item.StaleDataAbsent = totalProviderRequests, allDisclosureWithheld, allStaleDataAbsent
		item.CleanupConfirmed = traversalJoined && contentJoined && snapshotClosed && candidatesClosed
	})
	stage6Pass(caseID, "after content scanning, added nested and applicable ancestor ignore rules caused safe failed tool results rather than stale disclosure; both worker joins, descriptor retirement, once-grant consumption, and sanitized provider continuations were verified")
}

func TestStage6SearchSelectedRootMovement(t *testing.T) {
	stage6Begin("search_selected_root_movement")
	fixture := newStage6Fixture(t, "search_selected_root_movement", "content-search", false, nil)
	fixture.reportCases = []string{"search_selected_root_movement"}
	selected := filepath.Join(fixture.root, "listing-scope")
	selectedNested := filepath.Join(selected, "nested")
	if err := os.Mkdir(selectedNested, 0700); err != nil {
		t.Fatal("create selected search subtree:", err)
	}
	if err := os.WriteFile(filepath.Join(selectedNested, "control.go"), []byte("synthetic-never-real-key\n"), 0600); err != nil {
		t.Fatal("create selected-object match:", err)
	}
	replacement := filepath.Join(fixture.root, "replacement-listing")
	if err := os.MkdirAll(filepath.Join(replacement, "nested"), 0700); err != nil {
		t.Fatal("create replacement directory:", err)
	}
	if err := os.WriteFile(filepath.Join(replacement, "nested", "decoy.go"), []byte("synthetic-never-real-key\n"), 0600); err != nil {
		t.Fatal("create replacement-object marker:", err)
	}
	if err := os.Mkdir(filepath.Join(fixture.root, "outside"), 0700); err != nil {
		t.Fatal("create moved-root destination:", err)
	}
	impl := fixture.service.impl.(*linuxImplementation)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var actorJoined atomic.Int32
	impl.session.SetCheckpointHook(func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint != readbrokerstudy.CheckpointAfterSearchContent {
			return nil
		}
		for _, transition := range []struct{ source, target string }{
			{"listing-scope", "outside/selected-listing"},
			{"replacement-listing", "listing-scope"},
		} {
			actor, err := readbrokerstudy.NewNativeActor(ctx, os.Args[0], fixture.root, readbrokerstudy.NativeActorRename, transition.source, transition.target)
			if err != nil {
				return err
			}
			if !actor.Ready() || actor.Run(ctx) != nil || actor.Close() != nil || !actor.Joined() || !actor.TransitionCompleted() {
				_ = actor.Close()
				return errors.New("search root-movement actor was not joined")
			}
			actorJoined.Add(1)
		}
		return nil
	})
	turn, err := fixture.chat.Send(ctx, "search this synthetic directory", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted || actorJoined.Load() != 2 {
		t.Fatalf("search root movement outcome=%s actors=%d err=%v", turn.Outcome, actorJoined.Load(), err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 2 {
		t.Fatalf("provider requests=%d, want initial and search continuation", len(requests))
	}
	continuation, _ := json.Marshal(requests[1].Messages)
	if !strings.Contains(string(continuation), "nested/control.go") || strings.Contains(string(continuation), "nested/decoy.go") || strings.Contains(string(continuation), "outside/selected-listing") {
		t.Fatalf("search left selected-directory object authority: %s", continuation)
	}
	evidence := impl.session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadSucceeded || !evidence[0].WorkerJoined || !stage6DirectoryListingProbePassed(evidence[0].SearchTraversalProbe) || !stage6WorkerProbePassed(evidence[0].SearchContentProbe) {
		t.Fatalf("root-movement search evidence=%+v", evidence)
	}
	stage6Update("search_selected_root_movement", func(item *stage6Case) {
		item.ActorStarted, item.ActorTransitionCompleted, item.ActorJoined, item.ActorTransitionCount = true, true, true, 2
		item.WorkerStarted, item.WorkerJoined, item.ProbePassed = evidence[0].WorkerStarted, evidence[0].WorkerJoined, stage6WorkerProbePassed(evidence[0].SearchContentProbe)
		item.OnceApprovalCount, item.OnceGrantConsumptionCount, item.ProviderRequestCount = fixture.approvals.Load(), 1, len(requests)
		item.FixtureBytesMatched, item.CleanupConfirmed = true, evidence[0].WorkerJoined
	})
	stage6Pass("search_selected_root_movement", "independent actors moved the selected directory and placed a replacement at its old pathname after content scan; search returned the selected object's matching file and excluded replacement content")
}

func TestStage6SearchOwnerRotationWithholdsCapture(t *testing.T) {
	stage6Begin("search_owner_rotation_withholds_capture")
	var fixture *stage6Fixture
	var rotated atomic.Bool
	fixture = newStage6Fixture(t, "search_owner_rotation_withholds_capture", "content-search", false, func() error {
		rotateSyntheticAuth(t, fixture)
		rotated.Store(true)
		return nil
	})
	fixture.reportCases = []string{"search_owner_rotation_withholds_capture"}
	nested := filepath.Join(fixture.root, "listing-scope", "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal("create nested search directory:", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "control.go"), []byte("synthetic-never-real-key\n"), 0600); err != nil {
		t.Fatal("create matching search control:", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	_, sendErr := fixture.chat.Send(ctx, "search this synthetic directory", nil)
	if sendErr == nil || !rotated.Load() {
		t.Fatalf("owner rotation did not invalidate pending search publication: rotated=%t err=%v", rotated.Load(), sendErr)
	}
	requests := fixture.provider.captured()
	if len(requests) != 1 {
		t.Fatalf("provider requests=%d, want only initial request after owner rotation", len(requests))
	}
	encoded, _ := json.Marshal(requests[0].Messages)
	if strings.Contains(string(encoded), "synthetic-never-real-key") || strings.Contains(string(encoded), "nested/control.go") {
		t.Fatalf("search data reached provider after owner rotation: %s", encoded)
	}
	evidence := fixture.service.impl.(*linuxImplementation).session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadSucceeded || !evidence[0].WorkerJoined || !stage6WorkerProbePassed(evidence[0].SearchContentProbe) {
		t.Fatalf("owner-rotation search worker was not joined before suppression: %+v", evidence)
	}
	stage6Update("search_owner_rotation_withholds_capture", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerJoined, item.ProbePassed = evidence[0].WorkerStarted, evidence[0].WorkerJoined, stage6WorkerProbePassed(evidence[0].SearchContentProbe)
		item.OnceApprovalCount, item.OnceGrantConsumptionCount, item.ProviderRequestCount = fixture.approvals.Load(), 1, len(requests)
		item.DisclosureWithheld, item.StaleDataAbsent, item.CleanupConfirmed = true, true, evidence[0].WorkerJoined
	})
	stage6Pass("search_owner_rotation_withholds_capture", "protected auth owner rotation after search capture but before provider handoff suppressed the pending search disclosure and retired its stale capture")
}

func TestStage6SearchRevocationWithholdsCapture(t *testing.T) {
	stage6Begin("search_revocation_withholds_capture")
	var fixture *stage6Fixture
	var revoked atomic.Bool
	fixture = newStage6Fixture(t, "search_revocation_withholds_capture", "content-search", false, func() error {
		if fixture.readHost == nil || fixture.readHost.reference == nil {
			return errors.New("search execution reference was not retained by the host fixture")
		}
		if err := fixture.chat.RevokeReadExecution(fixture.readHost.reference); err != nil {
			return err
		}
		revoked.Store(true)
		return nil
	})
	fixture.reportCases = []string{"search_revocation_withholds_capture"}
	nested := filepath.Join(fixture.root, "listing-scope", "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal("create nested search directory:", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "control.go"), []byte("synthetic-never-real-key\n"), 0600); err != nil {
		t.Fatal("create matching search control:", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	_, sendErr := fixture.chat.Send(ctx, "search this synthetic directory", nil)
	if sendErr == nil || !revoked.Load() {
		t.Fatalf("search execution was not revoked before publication: revoked=%t err=%v", revoked.Load(), sendErr)
	}
	requests := fixture.provider.captured()
	if len(requests) != 1 {
		t.Fatalf("provider requests=%d, want only initial request after revocation", len(requests))
	}
	encoded, _ := json.Marshal(requests[0].Messages)
	if strings.Contains(string(encoded), "synthetic-never-real-key") || strings.Contains(string(encoded), "nested/control.go") {
		t.Fatalf("revoked search result reached provider: %s", encoded)
	}
	evidence := fixture.service.impl.(*linuxImplementation).session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadSucceeded || !evidence[0].WorkerJoined || !stage6WorkerProbePassed(evidence[0].SearchContentProbe) {
		t.Fatalf("revoked search worker was not joined before suppression: %+v", evidence)
	}
	stage6Update("search_revocation_withholds_capture", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerJoined, item.ProbePassed = evidence[0].WorkerStarted, evidence[0].WorkerJoined, stage6WorkerProbePassed(evidence[0].SearchContentProbe)
		item.OnceApprovalCount, item.OnceGrantConsumptionCount, item.ProviderRequestCount = fixture.approvals.Load(), 1, len(requests)
		item.DisclosureWithheld, item.StaleDataAbsent, item.CleanupConfirmed = true, true, evidence[0].WorkerJoined
	})
	stage6Pass("search_revocation_withholds_capture", "runtime revoked the consumed search execution after capture but before provider handoff; the content worker was joined and the result was withheld")
}

func TestStage6PositiveDirectoryListing(t *testing.T) {
	stage6Begin("positive_directory_listing")
	fixture := newStage6Fixture(t, "positive_directory_listing", "directory-list", false, nil)
	fixture.reportCases = []string{"positive_directory_listing"}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	aliasPath := filepath.Join(fixture.root, "listing-scope", "protected-auth-alias.txt")
	if err := os.Link(fixture.authPath, aliasPath); err != nil {
		t.Fatalf("create synthetic hardlink alias to protected auth owner: %v", err)
	}
	if err := os.Mkdir(filepath.Join(fixture.root, "listing-scope", "nested-boundary"), 0700); err != nil {
		t.Fatalf("create one-level boundary directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "listing-scope", "nested-boundary", "must-not-be-listed.txt"), []byte("synthetic nested marker"), 0600); err != nil {
		t.Fatalf("create nested boundary marker: %v", err)
	}
	grantRevisionBefore, grantsBefore := runtime.NativeReadStudyGrantState(fixture.chat)
	if grantsBefore != 0 {
		t.Fatalf("unexpected preexisting once grants=%d", grantsBefore)
	}
	turn, err := fixture.chat.Send(ctx, "list the synthetic fixture directory", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("directory listing turn outcome=%s err=%v", turn.Outcome, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 2 {
		t.Fatalf("provider requests=%d, want initial and listing continuation", len(requests))
	}
	first, _ := json.Marshal(requests[0].Tools)
	second, _ := json.Marshal(requests[1].Messages)
	if !strings.Contains(string(first), string(tools.ListFilesID)) || !strings.Contains(string(second), "visible.go") || strings.Contains(string(second), ".hidden") || strings.Contains(string(second), "ignored.tmp") || strings.Contains(string(second), ".git") || strings.Contains(string(second), "protected-auth-alias.txt") || strings.Contains(string(second), "must-not-be-listed.txt") {
		t.Fatalf("directory listing disclosure did not honor bounded filters: definitions=%s continuation=%s", first, second)
	}
	if fixture.approvals.Load() != 1 {
		t.Fatalf("listing once approvals=%d, want one", fixture.approvals.Load())
	}
	grantRevisionAfter, grantsAfter := runtime.NativeReadStudyGrantState(fixture.chat)
	if grantRevisionAfter != grantRevisionBefore+2 || grantsAfter != 0 {
		t.Fatalf("once listing grant revision %d→%d remaining=%d", grantRevisionBefore, grantRevisionAfter, grantsAfter)
	}
	impl, ok := fixture.service.impl.(*linuxImplementation)
	if !ok || impl.session == nil {
		t.Fatal("native listing service lifecycle evidence unavailable")
	}
	evidence := impl.session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadSucceeded || !evidence[0].WorkerStarted || !evidence[0].WorkerReady || !evidence[0].WorkerJoined || !evidence[0].CapabilitySent || !evidence[0].ReadStarted || evidence[0].Probe.SeccompMode != 2 || !evidence[0].Probe.PathOpenDenied {
		t.Fatalf("native listing worker lifecycle not established: %+v", evidence)
	}
	stage6Update("positive_directory_listing", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = evidence[0].WorkerStarted, evidence[0].WorkerReady, evidence[0].WorkerJoined
		item.CapabilityTransferred, item.ReadStarted, item.ProbePassed = evidence[0].CapabilitySent, evidence[0].ReadStarted, stage6DirectoryListingProbePassed(evidence[0].Probe)
		item.OnceApprovalCount, item.OnceGrantConsumptionCount, item.ProviderRequestCount = fixture.approvals.Load(), 1, len(requests)
		item.GrantRevisionBefore, item.GrantRevisionAfter, item.RemainingGrantCount = grantRevisionBefore, grantRevisionAfter, grantsAfter
		item.FixtureBytesMatched = true
	})
	stage6Pass("positive_directory_listing", "actual sandbox Service, exact-directory once approval, bounded listing worker, ignore filtering and provider continuation succeeded")
}

func TestStage6PositiveRecursiveDirectoryListing(t *testing.T) {
	stage6Begin("positive_recursive_directory_listing")
	fixture := newStage6Fixture(t, "positive_recursive_directory_listing", "directory-list-recursive", false, nil)
	fixture.reportCases = []string{"positive_recursive_directory_listing"}
	protectedAlias := "nested/protected-auth-alias.go"
	if err := os.Link(fixture.authPath, filepath.Join(fixture.root, "listing-scope", filepath.FromSlash(protectedAlias))); err != nil {
		t.Fatalf("create nested protected hardlink alias: %v", err)
	}
	if err := os.Symlink("nested", filepath.Join(fixture.root, "listing-scope", "linked.go")); err != nil {
		t.Fatalf("create recursive-listing symlink fixture: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	turn, err := fixture.chat.Send(ctx, "list the synthetic fixture recursively", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("recursive listing outcome=%s err=%v", turn.Outcome, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 2 {
		t.Fatalf("provider request count=%d, want 2", len(requests))
	}
	continuation, _ := json.Marshal(requests[1].Messages)
	resultText, found := readResultContent(requests[1], "stage6-read")
	if !found {
		t.Fatalf("recursive continuation omitted listing result: %s", continuation)
	}
	var envelope struct {
		Outcome   string `json:"outcome"`
		Truncated bool   `json:"truncated"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal([]byte(resultText), &envelope); err != nil || envelope.Outcome != string(tools.OutcomeSucceeded) || envelope.Truncated {
		t.Fatalf("recursive result envelope is invalid (outcome=%q truncated=%t err=%v)", envelope.Outcome, envelope.Truncated, err)
	}
	var listing struct {
		Names      []string `json:"names"`
		Examined   int      `json:"examined"`
		Returned   int      `json:"returned"`
		Incomplete bool     `json:"incomplete"`
	}
	if err := json.Unmarshal([]byte(envelope.Content), &listing); err != nil {
		t.Fatalf("recursive listing content is invalid: %v", err)
	}
	slices.Sort(listing.Names)
	wantNames := []string{".private/hidden.go", "ignored.tmp", "linked.go", "nested/child.go", "visible.go"}
	if !slices.Equal(listing.Names, wantNames) || listing.Examined != len(wantNames) || listing.Returned != len(wantNames) || listing.Incomplete {
		t.Fatalf("recursive listing names/counts=%+v, want names=%q examined=%d returned=%d incomplete=false", listing, wantNames, len(wantNames), len(wantNames))
	}
	if strings.Contains(string(continuation), ".git") || strings.Contains(string(continuation), "secret.go") || strings.Contains(string(continuation), protectedAlias) || strings.Contains(string(continuation), "linked.go/child.go") {
		t.Fatalf("recursive listing disclosed repository metadata: %s", continuation)
	}
	for _, option := range []string{"recursive=true", "include_hidden=true", "include_ignored=true", "*.go", "*.tmp", "openai/stage6-fixture-model using auth profile stage6-profile"} {
		if !strings.Contains(fixture.approvalDescription, option) {
			t.Fatalf("approval omitted %q: %s", option, fixture.approvalDescription)
		}
	}
	impl := fixture.service.impl.(*linuxImplementation)
	evidence := impl.session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadSucceeded || !evidence[0].WorkerJoined || !evidence[0].CapabilitySent || !evidence[0].ReadStarted || !stage6DirectoryListingProbePassed(evidence[0].Probe) {
		t.Fatalf("recursive worker evidence=%+v", evidence)
	}
	if fixture.approvals.Load() != 1 {
		t.Fatalf("once approvals=%d, want one", fixture.approvals.Load())
	}
	stage6Update("positive_recursive_directory_listing", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = evidence[0].WorkerStarted, evidence[0].WorkerReady, evidence[0].WorkerJoined
		item.CapabilityTransferred, item.ReadStarted, item.ProbePassed = evidence[0].CapabilitySent, evidence[0].ReadStarted, stage6DirectoryListingProbePassed(evidence[0].Probe)
		item.OnceApprovalCount, item.OnceGrantConsumptionCount, item.ProviderRequestCount = fixture.approvals.Load(), 1, len(requests)
		item.FixtureBytesMatched = true
	})
	stage6Pass("positive_recursive_directory_listing", "recursive traversal returned relative paths, applied explicit hidden/ignored/glob options, excluded .git and protected aliases, refused symlink traversal, and continued only after once approval")
}

func TestStage6RecursiveListingNestedIgnoreAndGlobFilters(t *testing.T) {
	stage6Begin("recursive_listing_nested_ignore_filters")
	fixture := newStage6Fixture(t, "recursive_listing_nested_ignore_filters", "directory-list-recursive-filtered", false, nil)
	fixture.reportCases = []string{"recursive_listing_nested_ignore_filters"}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	turn, err := fixture.chat.Send(ctx, "list matching files recursively", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("filtered recursive listing outcome=%s err=%v", turn.Outcome, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 2 {
		t.Fatalf("provider requests=%d, want initial plus listing continuation", len(requests))
	}
	resultText, found := readResultContent(requests[1], "stage6-read")
	if !found {
		t.Fatal("provider continuation omitted recursive listing result")
	}
	var result struct {
		Names      []string `json:"names"`
		Examined   int      `json:"examined"`
		Returned   int      `json:"returned"`
		Incomplete bool     `json:"incomplete"`
	}
	var envelope struct {
		Outcome   string `json:"outcome"`
		Truncated bool   `json:"truncated"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal([]byte(resultText), &envelope); err != nil || envelope.Outcome != string(tools.OutcomeSucceeded) || envelope.Truncated {
		t.Fatalf("listing result envelope is invalid (outcome=%q truncated=%t err=%v)", envelope.Outcome, envelope.Truncated, err)
	}
	if err := json.Unmarshal([]byte(envelope.Content), &result); err != nil {
		t.Fatalf("listing content is invalid: %v", err)
	}
	if !slices.Equal(result.Names, []string{"nested/keep.tmp"}) || result.Examined != 1 || result.Returned != 1 || result.Incomplete {
		t.Fatalf("recursive ignore/glob result=%+v, want only nested exception with provider-visible count 1", result)
	}
	for _, forbidden := range []string{"nested/drop.tmp", "ignored.tmp", "ignored-dir", "secret.tmp", ".private", ".git", "hidden.tmp"} {
		if strings.Contains(resultText, forbidden) {
			t.Fatalf("filtered recursive result disclosed %q: %s", forbidden, resultText)
		}
	}
	for _, option := range []string{"recursive=true", "include_hidden=false", "include_ignored=false", "*.tmp", "openai/stage6-fixture-model using auth profile stage6-profile"} {
		if !strings.Contains(fixture.approvalDescription, option) {
			t.Fatalf("once approval omitted %q: %s", option, fixture.approvalDescription)
		}
	}
	if fixture.approvals.Load() != 1 {
		t.Fatalf("listing approvals=%d, want one", fixture.approvals.Load())
	}
	evidence := fixture.service.impl.(*linuxImplementation).session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadSucceeded || !evidence[0].WorkerJoined || !evidence[0].CapabilitySent || !evidence[0].ReadStarted || !stage6DirectoryListingProbePassed(evidence[0].Probe) {
		t.Fatalf("nested-ignore listing worker evidence=%+v", evidence)
	}
	stage6Update("recursive_listing_nested_ignore_filters", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = evidence[0].WorkerStarted, evidence[0].WorkerReady, evidence[0].WorkerJoined
		item.CapabilityTransferred, item.ReadStarted, item.ProbePassed = evidence[0].CapabilitySent, evidence[0].ReadStarted, stage6DirectoryListingProbePassed(evidence[0].Probe)
		item.OnceApprovalCount, item.OnceGrantConsumptionCount, item.ProviderRequestCount = fixture.approvals.Load(), 1, len(requests)
		item.FixtureBytesMatched = true
	})
	stage6Pass("recursive_listing_nested_ignore_filters", "actual Service and provider continuation applied a nested negation, preserved ignored-parent exclusions, did not prune traversal for a basename glob, excluded hidden and .git paths, and disclosed only permitted names/counts")
}

func TestStage6RecursiveListingEntryAndOutputLimits(t *testing.T) {
	stage6Begin("recursive_listing_entry_limit")
	fixture := newStage6Fixture(t, "recursive_listing_entry_limit", "directory-list-recursive-large", false, nil)
	fixture.reportCases = []string{"recursive_listing_entry_limit"}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	turn, err := fixture.chat.Send(ctx, "list all matching files recursively", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("large recursive listing outcome=%s err=%v", turn.Outcome, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 2 {
		t.Fatalf("provider requests=%d, want initial plus listing continuation", len(requests))
	}
	resultText, found := readResultContent(requests[1], "stage6-read")
	if !found {
		t.Fatal("provider continuation omitted bounded recursive listing result")
	}
	var result struct {
		Names      []string `json:"names"`
		Examined   int      `json:"examined"`
		Returned   int      `json:"returned"`
		Incomplete bool     `json:"incomplete"`
	}
	var envelope struct {
		Outcome   string `json:"outcome"`
		Truncated bool   `json:"truncated"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal([]byte(resultText), &envelope); err != nil || envelope.Outcome != string(tools.OutcomeSucceeded) || envelope.Truncated {
		t.Fatalf("bounded listing result envelope is invalid (outcome=%q truncated=%t err=%v)", envelope.Outcome, envelope.Truncated, err)
	}
	if err := json.Unmarshal([]byte(envelope.Content), &result); err != nil {
		t.Fatalf("bounded listing content is invalid: %v", err)
	}
	if len(result.Names) == 0 || len(result.Names) > 1_000 || result.Examined != len(result.Names) || result.Returned != len(result.Names) || !result.Incomplete {
		t.Fatalf("large recursive listing did not truthfully disclose a bounded partial result: names=%d examined=%d returned=%d incomplete=%t", len(result.Names), result.Examined, result.Returned, result.Incomplete)
	}
	for _, name := range result.Names {
		if strings.Contains(name, "protected-auth") || strings.Contains(name, ".git") {
			t.Fatalf("bounded listing disclosed protected metadata path %q", name)
		}
	}
	evidence := fixture.service.impl.(*linuxImplementation).session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadSucceeded || !evidence[0].WorkerJoined || !stage6DirectoryListingProbePassed(evidence[0].Probe) {
		t.Fatalf("bounded recursive worker evidence=%+v", evidence)
	}
	stage6Update("recursive_listing_entry_limit", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = evidence[0].WorkerStarted, evidence[0].WorkerReady, evidence[0].WorkerJoined
		item.CapabilityTransferred, item.ReadStarted, item.ProbePassed = evidence[0].CapabilitySent, evidence[0].ReadStarted, stage6DirectoryListingProbePassed(evidence[0].Probe)
		item.OnceApprovalCount, item.OnceGrantConsumptionCount, item.ProviderRequestCount = fixture.approvals.Load(), 1, len(requests)
		item.FixtureBytesMatched = true
	})
	stage6Pass("recursive_listing_entry_limit", "actual recursive worker and host filter returned a provider-visible partial result with bounded names/counts and incomplete=true after whole-tree traversal limits")
}

func TestStage6RecursiveListingCancellationAfterTraversalWithholdsResult(t *testing.T) {
	stage6Begin("recursive_listing_cancellation_after_traversal")
	fixture := newStage6Fixture(t, "recursive_listing_cancellation_after_traversal", "directory-list-recursive", false, nil)
	fixture.reportCases = []string{"recursive_listing_cancellation_after_traversal"}
	impl := fixture.service.impl.(*linuxImplementation)
	if impl.session == nil {
		t.Fatal("native listing session is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	checkpointReached := atomic.Bool{}
	impl.session.SetCheckpointHook(func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint != readbrokerstudy.CheckpointAfterCapture {
			return nil
		}
		checkpointReached.Store(true)
		cancel()
		return context.Canceled
	})
	turn, sendErr := fixture.chat.Send(ctx, "list the synthetic fixture recursively", nil)
	if !checkpointReached.Load() || sendErr != nil && !errors.Is(sendErr, context.Canceled) || turn.Outcome != provider.OutcomeInterrupted {
		t.Fatalf("post-traversal cancellation checkpoint=%t outcome=%s err=%v", checkpointReached.Load(), turn.Outcome, sendErr)
	}
	requests := fixture.provider.captured()
	if len(requests) != 1 {
		t.Fatalf("provider requests=%d, want no listing continuation after cancellation", len(requests))
	}
	encoded, _ := json.Marshal(requests[0].Messages)
	if strings.Contains(string(encoded), "nested/child.go") {
		t.Fatal("completed traversal result reached the provider after cancellation")
	}
	evidence := impl.session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadCancelled || !evidence[0].WorkerJoined || !evidence[0].CapabilitySent || !evidence[0].ReadStarted || evidence[0].Published || !stage6DirectoryListingProbePassed(evidence[0].Probe) {
		t.Fatalf("post-traversal cancellation evidence=%+v", evidence)
	}
	if published := impl.session.PublishedResults(); len(published) != 0 {
		t.Fatalf("post-traversal cancellation published %d private results", len(published))
	}
	stage6Update("recursive_listing_cancellation_after_traversal", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = evidence[0].WorkerStarted, evidence[0].WorkerReady, evidence[0].WorkerJoined
		item.CapabilityTransferred, item.ReadStarted, item.ProbePassed = evidence[0].CapabilitySent, evidence[0].ReadStarted, stage6DirectoryListingProbePassed(evidence[0].Probe)
		item.OnceApprovalCount, item.ProviderRequestCount = fixture.approvals.Load(), len(requests)
		item.DisclosureWithheld, item.StaleDataAbsent = true, true
	})
	stage6Pass("recursive_listing_cancellation_after_traversal", "cancellation at the deterministic post-traversal checkpoint retired the joined worker and suppressed the filtered names before provider continuation")
}

func TestStage6RecursiveListingDescendantMovementAndReplacement(t *testing.T) {
	stage6Begin("recursive_descendant_movement_replacement")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fixture := newStage6Fixture(t, "recursive_descendant_movement_replacement", "directory-list-recursive", false, nil)
	fixture.reportCases = []string{"recursive_descendant_movement_replacement"}
	impl := fixture.service.impl.(*linuxImplementation)
	actorRoot := filepath.Dir(fixture.root)
	outside := filepath.Join(actorRoot, "recursive-listing-outside")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	actorCompleted := false
	impl.session.SetCheckpointHook(func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint != readbrokerstudy.CheckpointAfterFinalRevalidation {
			return nil
		}
		source := filepath.Join(filepath.Base(fixture.root), "listing-scope", "nested")
		actor, err := readbrokerstudy.NewNativeActor(ctx, os.Args[0], actorRoot, readbrokerstudy.NativeActorRename, source, filepath.Join("recursive-listing-outside", "moved-nested"))
		if err != nil {
			return err
		}
		if !actor.Ready() {
			_ = actor.Close()
			return errors.New("descendant movement actor did not become ready")
		}
		if err := actor.Run(ctx); err != nil {
			_ = actor.Close()
			return err
		}
		if err := actor.Close(); err != nil || !actor.Joined() || !actor.TransitionCompleted() {
			return errors.New("descendant movement actor was not joined")
		}
		actorCompleted = true
		replacement := filepath.Join(fixture.root, "listing-scope", "nested")
		if err := os.Mkdir(replacement, 0700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(replacement, "replacement.go"), []byte("synthetic replacement"), 0600)
	})
	turn, err := fixture.chat.Send(ctx, "list the synthetic fixture recursively", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted || !actorCompleted {
		t.Fatalf("recursive descendant turn=%s actor=%t err=%v", turn.Outcome, actorCompleted, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 2 {
		t.Fatalf("provider requests=%d, want 2", len(requests))
	}
	continuation, _ := json.Marshal(requests[1].Messages)
	if !strings.Contains(string(continuation), "nested/replacement.go") || strings.Contains(string(continuation), "nested/child.go") || strings.Contains(string(continuation), "moved-nested") {
		t.Fatalf("recursive traversal escaped the pinned selected directory or followed a moved descendant: %s", continuation)
	}
	evidence := impl.session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadSucceeded || !evidence[0].WorkerJoined || !evidence[0].CapabilitySent || !stage6DirectoryListingProbePassed(evidence[0].Probe) {
		t.Fatalf("recursive descendant worker evidence=%+v", evidence)
	}
	stage6Update("recursive_descendant_movement_replacement", func(item *stage6Case) {
		item.ActorStarted, item.ActorTransitionCompleted, item.ActorJoined, item.ActorTransitionCount = true, true, true, 1
		item.AuthorityObservation = "selected directory object; descendant moved outside before traversal, only replacement still beneath pinned selected object was disclosed"
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = evidence[0].WorkerStarted, evidence[0].WorkerReady, evidence[0].WorkerJoined
		item.CapabilityTransferred, item.ReadStarted, item.ProbePassed = evidence[0].CapabilitySent, evidence[0].ReadStarted, stage6DirectoryListingProbePassed(evidence[0].Probe)
		item.OnceApprovalCount, item.OnceGrantConsumptionCount, item.ProviderRequestCount = fixture.approvals.Load(), 1, len(requests)
		item.FixtureBytesMatched, item.CleanupConfirmed = true, evidence[0].WorkerJoined
	})
	stage6Pass("recursive_descendant_movement_replacement", "an independent actor moved a descendant outside the pinned selected directory and a replacement remained inside; traversal disclosed only the replacement from the selected directory object")
}

func TestStage6DirectoryListingSurvivesSelectedDirectoryMovementAndReplacement(t *testing.T) {
	stage6Begin("directory_listing_movement_replacement")
	fixture := newStage6Fixture(t, "directory_listing_movement_replacement", "directory-list", false, nil)
	fixture.reportCases = []string{"directory_listing_movement_replacement"}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	actorRoot := filepath.Dir(fixture.root)
	outside := filepath.Join(actorRoot, "listing-outside")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatalf("create actor destination: %v", err)
	}
	impl := fixture.service.impl.(*linuxImplementation)
	if impl.session == nil {
		t.Fatal("native fixture session is unavailable")
	}
	actorCompleted := false
	impl.session.SetCheckpointHook(func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint != readbrokerstudy.CheckpointAfterFinalRevalidation {
			return nil
		}
		source := filepath.Join(filepath.Base(fixture.root), "listing-scope")
		actor, err := readbrokerstudy.NewNativeActor(ctx, os.Args[0], actorRoot, readbrokerstudy.NativeActorRename, source, filepath.Join("listing-outside", "moved-scope"))
		if err != nil {
			return err
		}
		t.Cleanup(func() {
			if err := actor.Close(); err != nil {
				t.Errorf("ensure directory movement actor cleanup: %v", err)
			}
		})
		if !actor.Ready() {
			return errors.New("directory movement actor did not complete its startup handshake")
		}
		if err := actor.Run(ctx); err != nil {
			return err
		}
		if err := actor.Close(); err != nil || !actor.TransitionCompleted() || !actor.Joined() {
			return errors.New("directory movement actor transition was not joined")
		}
		actorCompleted = true
		replacement := filepath.Join(fixture.root, "listing-scope")
		if err := os.Mkdir(replacement, 0700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(replacement, "attacker-only.txt"), []byte("replacement marker"), 0600)
	})
	turn, err := fixture.chat.Send(ctx, "list the synthetic fixture directory", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted || !actorCompleted {
		t.Fatalf("listing pinned directory after movement did not complete: outcome=%s actor=%t err=%v", turn.Outcome, actorCompleted, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 2 {
		t.Fatalf("provider requests=%d, want initial and listing continuation", len(requests))
	}
	continuation, _ := json.Marshal(requests[1].Messages)
	if !strings.Contains(string(continuation), "visible.go") || strings.Contains(string(continuation), "attacker-only.txt") {
		t.Fatalf("listing followed replacement pathname instead of the pinned directory: %s", continuation)
	}
	evidence := impl.session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadSucceeded || !evidence[0].WorkerJoined || !evidence[0].CapabilitySent || !evidence[0].ReadStarted || !stage6DirectoryListingProbePassed(evidence[0].Probe) {
		t.Fatalf("moved-directory listing lacked native worker evidence: %+v", evidence)
	}
	stage6Update("directory_listing_movement_replacement", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = evidence[0].WorkerStarted, evidence[0].WorkerReady, evidence[0].WorkerJoined
		item.CapabilityTransferred, item.ReadStarted, item.ProbePassed = evidence[0].CapabilitySent, evidence[0].ReadStarted, stage6DirectoryListingProbePassed(evidence[0].Probe)
		item.ActorStarted, item.ActorTransitionCompleted, item.ActorJoined, item.ActorTransitionCount = true, true, true, 1
		item.ProviderRequestCount, item.OnceApprovalCount, item.FixtureBytesMatched = len(requests), fixture.approvals.Load(), true
	})
	stage6Pass("directory_listing_movement_replacement", "an independent actor moved the selected directory after final revalidation and the original directory was listed through its pinned descriptor; a replacement pathname was not followed")
}

func TestStage6DirectoryListingOwnerRotationWithholdsCapture(t *testing.T) {
	stage6Begin("directory_listing_owner_rotation")
	var fixture *stage6Fixture
	var rotated atomic.Bool
	fixture = newStage6Fixture(t, "directory_listing_owner_rotation", "directory-list-recursive", false, func() error {
		rotateSyntheticAuth(t, fixture)
		rotated.Store(true)
		return nil
	})
	fixture.reportCases = []string{"directory_listing_owner_rotation"}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	_, sendErr := fixture.chat.Send(ctx, "list the synthetic fixture directory", nil)
	if sendErr == nil || !rotated.Load() {
		t.Fatalf("owner rotation did not invalidate the pending listing handoff: rotated=%t err=%v", rotated.Load(), sendErr)
	}
	requests := fixture.provider.captured()
	if len(requests) != 1 {
		t.Fatalf("provider requests=%d, want only initial request after owner rotation", len(requests))
	}
	encoded, _ := json.Marshal(requests[0].Messages)
	if strings.Contains(string(encoded), "visible.go") {
		t.Fatal("directory names reached the provider after protected-owner rotation")
	}
	impl := fixture.service.impl.(*linuxImplementation)
	evidence := impl.session.Evidence()
	if len(evidence) != 1 || !evidence[0].WorkerJoined || !evidence[0].ReadStarted || evidence[0].Outcome != readbrokerstudy.ReadSucceeded || !stage6DirectoryListingProbePassed(evidence[0].Probe) {
		t.Fatalf("owner-rotation listing worker was not joined before suppression: %+v", evidence)
	}
	stage6Update("directory_listing_owner_rotation", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = evidence[0].WorkerStarted, evidence[0].WorkerReady, evidence[0].WorkerJoined
		item.CapabilityTransferred, item.ReadStarted, item.ProbePassed = evidence[0].CapabilitySent, evidence[0].ReadStarted, stage6DirectoryListingProbePassed(evidence[0].Probe)
		item.OnceApprovalCount, item.ProviderRequestCount = fixture.approvals.Load(), len(requests)
		item.DisclosureWithheld, item.StaleDataAbsent = true, true
	})
	stage6Pass("directory_listing_owner_rotation", "protected auth owner rotation after capture but before provider handoff suppressed the pending directory-name disclosure and retired the stale capture")
}

func TestStage6DirectoryListingCancellationJoinsWorker(t *testing.T) {
	stage6Begin("directory_listing_worker_cleanup")
	fixture := newStage6FixtureWithHold(t, "directory_listing_worker_cleanup", "directory-list-recursive", false, nil, true)
	fixture.reportCases = []string{"directory_listing_worker_cleanup"}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	impl := fixture.service.impl.(*linuxImplementation)
	if impl.session == nil {
		t.Fatal("native fixture session is unavailable")
	}
	session := impl.session
	checkpointReached := atomic.Bool{}
	session.SetCheckpointHook(func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint != readbrokerstudy.CheckpointAfterCapabilityHeld {
			return nil
		}
		checkpointReached.Store(true)
		cancel()
		return context.Canceled
	})
	turn, sendErr := fixture.chat.Send(ctx, "list the synthetic fixture directory", nil)
	if !checkpointReached.Load() || sendErr != nil && !errors.Is(sendErr, context.Canceled) || turn.Outcome != provider.OutcomeInterrupted {
		t.Fatalf("held listing cancellation did not stop safely: checkpoint=%t outcome=%s err=%v", checkpointReached.Load(), turn.Outcome, sendErr)
	}
	requests := fixture.provider.captured()
	if len(requests) != 1 {
		t.Fatalf("provider requests=%d, want initial request only after listing cancellation", len(requests))
	}
	evidence := session.Evidence()
	if len(evidence) != 1 || (evidence[0].Outcome != readbrokerstudy.ReadCancelled && evidence[0].Outcome != readbrokerstudy.ReadUnknown) || !evidence[0].WorkerStarted || !evidence[0].WorkerReady || !evidence[0].CapabilitySent || !evidence[0].CapabilityHeld || evidence[0].ReadStarted || evidence[0].Published || !stage6DirectoryListingProbePassed(evidence[0].Probe) {
		t.Fatalf("cancelled listing worker lifecycle evidence=%+v", evidence)
	}
	initial := evidence[0]
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cleanupCancel()
	for {
		err := fixture.chat.CloseContext(cleanupCtx)
		if err == nil {
			break
		}
		if !errors.Is(err, runtime.ErrFilesystemRootPending) {
			t.Fatalf("bounded listing shutdown failed while joining cancelled worker: %v", err)
		}
		select {
		case <-cleanupCtx.Done():
			t.Fatalf("bounded shutdown did not join cancelled listing worker: %v", errors.Join(err, cleanupCtx.Err()))
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := fixture.service.Close(cleanupCtx); err != nil {
		t.Fatalf("bounded service shutdown did not drain cancelled listing worker: %v", err)
	}
	evidence = session.Evidence()
	if len(evidence) != 1 || evidence[0].WorkerJoined || evidence[0].Outcome != initial.Outcome || !evidence[0].CapabilityHeld || evidence[0].ReadStarted || evidence[0].Published {
		t.Fatalf("listing cleanup rewrote the original unknown operation observation: before=%+v after=%+v", initial, evidence)
	}
	if active := session.ActiveOperationCount(); active != 0 {
		t.Fatalf("active operations after cancelled listing cleanup=%d", active)
	}
	if published := session.PublishedResults(); len(published) != 0 {
		t.Fatalf("cancelled listing published %d private results", len(published))
	}
	stage6Update("directory_listing_worker_cleanup", func(item *stage6Case) {
		// CloseContext and Service.Close returned only after the session had
		// observed the delayed direct-process join and removed its obligation.
		// ReadEvidence intentionally remains the immutable initial observation.
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = evidence[0].WorkerStarted, evidence[0].WorkerReady, true
		item.CapabilityTransferred, item.CapabilityHeld, item.ReadStarted = evidence[0].CapabilitySent, evidence[0].CapabilityHeld, evidence[0].ReadStarted
		item.ReadOutcome, item.ProbePassed, item.OnceApprovalCount = string(evidence[0].Outcome), stage6DirectoryListingProbePassed(evidence[0].Probe), fixture.approvals.Load()
		item.ProviderRequestCount, item.DisclosureWithheld, item.StaleDataAbsent = len(requests), true, true
	})
	stage6Pass("directory_listing_worker_cleanup", "cancellation after the fixed worker held the directory descriptor caused bounded worker cleanup and join; no enumeration, publication, or provider continuation occurred")
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
		{"--disable-userns"}, {"--cap-drop", "ALL"}, {"--clearenv"}, {"--size", "16777216"},
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
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("protected auth refusal did not permit safe text continuation: outcome=%s err=%v", turn.Outcome, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 2 {
		t.Fatalf("provider requests=%d, want initial request plus safe refusal continuation", len(requests))
	}
	toolResult, found := readResultContent(requests[1], "stage6-read")
	var refusal struct {
		Outcome   string `json:"outcome"`
		Truncated bool   `json:"truncated"`
		Content   string `json:"content"`
	}
	if !found || json.Unmarshal([]byte(toolResult), &refusal) != nil || refusal.Outcome != string(tools.OutcomeNotExecuted) || refusal.Truncated || refusal.Content == "" || strings.Contains(refusal.Content, "synthetic-never-real-key") {
		t.Fatalf("protected auth continuation result=%q found=%t; want a bounded not_executed result without payload", toolResult, found)
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
	stage6Pass("protected_auth_refusal", "registered synthetic auth route refused before approval and worker launch; only a correlated not_executed result reached the fake provider, with no protected bytes disclosed")
}

func TestStage6ProtectedSettingsObjectIsWithheld(t *testing.T) {
	stage6Begin("protected_settings_refusal")
	fixture := newStage6Fixture(t, "protected_settings_refusal", "settings", false, nil)
	fixture.reportCases = []string{"protected_settings_refusal"}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	turn, err := fixture.chat.Send(ctx, "request the protected fixture", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("protected settings refusal did not permit safe text continuation: outcome=%s err=%v", turn.Outcome, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 2 {
		t.Fatalf("provider requests=%d, want initial request plus safe refusal continuation", len(requests))
	}
	toolResult, found := readResultContent(requests[1], "stage6-read")
	var refusal struct {
		Outcome   string `json:"outcome"`
		Truncated bool   `json:"truncated"`
		Content   string `json:"content"`
	}
	if !found || json.Unmarshal([]byte(toolResult), &refusal) != nil || refusal.Outcome != string(tools.OutcomeNotExecuted) || refusal.Truncated || refusal.Content == "" || strings.Contains(refusal.Content, "synthetic-settings-marker") {
		t.Fatalf("protected settings continuation result=%q found=%t; want a bounded not_executed result without payload", toolResult, found)
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
	stage6Pass("protected_settings_refusal", "registered synthetic settings route refused before approval and worker launch; only a correlated not_executed result reached the fake provider, with no protected bytes disclosed")
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
