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

package sandbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	rb "github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
	"github.com/ayeshLK/komposyn/internal/harness/runtime"
	"github.com/ayeshLK/komposyn/internal/harness/settings"
	"github.com/ayeshLK/komposyn/plugins/tools"
	"golang.org/x/sys/unix"
)

// The test binary is also the exact fixed worker image. Worker and actor modes
// run before testing initialization and never initialize credentials/providers.
func init() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--readbrokerstudy-bootstrap":
			handled, code := WorkerBootstrap(os.Args[1:])
			if handled {
				os.Exit(code)
			}
		case "--readbrokerstudy-actor":
			handled, err := rb.RunNativeActorMode(os.Args[1:])
			if handled {
				if err != nil {
					os.Exit(2)
				}
				os.Exit(0)
			}
		}
	}
}

type nativeRecord struct {
	FirstUpdateCleanupPending               bool                  `json:"first_update_cleanup_pending"`
	FirstRefusalPreservedDestination        bool                  `json:"first_refusal_preserved_destination"`
	FreshRetrySucceeded                     bool                  `json:"fresh_retry_succeeded"`
	FreshRetryDistinctObject                bool                  `json:"fresh_retry_distinct_object"`
	RootTransitionCleanupPending            bool                  `json:"root_transition_cleanup_pending"`
	FreshReadAfterRootChange                bool                  `json:"fresh_read_after_root_change"`
	FailureKind                             string                `json:"failure_kind,omitempty"`
	EmptyReplacementObserved                bool                  `json:"empty_replacement_observed"`
	WorkerJoinedBeforeOwnerWrite            bool                  `json:"worker_joined_before_owner_write"`
	RetiredAliasRefused                     bool                  `json:"retired_alias_refused"`
	VerifiedReturnedBytes                   bool                  `json:"verified_returned_bytes"`
	CreationVerifiedBytes                   bool                  `json:"creation_verified_bytes,omitempty"`
	CreationSchemaRegistered                bool                  `json:"creation_schema_registered,omitempty"`
	CreationVerifiedMode                    bool                  `json:"creation_verified_mode,omitempty"`
	CreationStageAbsent                     bool                  `json:"creation_stage_absent,omitempty"`
	CreationLateCollisionRefused            bool                  `json:"creation_late_collision_refused,omitempty"`
	CreationLateStageAbsent                 bool                  `json:"creation_late_stage_absent,omitempty"`
	CreationFilesystem                      string                `json:"creation_filesystem,omitempty"`
	CreationMountID                         uint64                `json:"creation_mount_id,omitempty"`
	CreationFailureDetail                   string                `json:"creation_failure_detail,omitempty"`
	CreationParentMovePublished             bool                  `json:"creation_parent_move_published_to_retained_object,omitempty"`
	CreationParentMoveFilesystem            string                `json:"creation_parent_move_filesystem,omitempty"`
	CreationParentMoveMountID               uint64                `json:"creation_parent_move_mount_id,omitempty"`
	CreationActorPID                        int                   `json:"creation_actor_pid,omitempty"`
	CreationActorUID                        int                   `json:"creation_actor_uid,omitempty"`
	CreationActorExecution                  string                `json:"creation_actor_execution,omitempty"`
	CreationParentIdentitySHA256            string                `json:"creation_parent_identity_sha256,omitempty"`
	CreationMovedIdentitySHA256             string                `json:"creation_moved_identity_sha256,omitempty"`
	CreationSubstituteIdentitySHA256        string                `json:"creation_substitute_identity_sha256,omitempty"`
	CreationNestedPublished                 bool                  `json:"creation_nested_published,omitempty"`
	CreationNestedParentSubstitutionRefused bool                  `json:"creation_nested_parent_substitution_refused,omitempty"`
	CreationInvalidTargetMatrixRefused      bool                  `json:"creation_invalid_target_matrix_refused,omitempty"`
	CreationMountBoundaryRefused            bool                  `json:"creation_mount_boundary_refused,omitempty"`
	CreationMountBoundaryUnavailableReason  string                `json:"creation_mount_boundary_unavailable_reason,omitempty"`
	StartupMoveAfterReleasePublished        bool                  `json:"startup_move_after_release_published,omitempty"`
	StartupMoveAfterReleaseOutcome          string                `json:"startup_move_after_release_outcome,omitempty"`
	StartupMoveAfterReleaseActorPID         int                   `json:"startup_move_after_release_actor_pid,omitempty"`
	StartupMoveAfterReleaseActorUID         int                   `json:"startup_move_after_release_actor_uid,omitempty"`
	UnavailableReason                       string                `json:"unavailable_reason,omitempty"`
	CreationStartupFilesystem               string                `json:"creation_startup_filesystem,omitempty"`
	CreationStartupMountID                  uint64                `json:"creation_startup_mount_id,omitempty"`
	StartupMoveActorPID                     int                   `json:"startup_move_actor_pid,omitempty"`
	StartupMoveActorUID                     int                   `json:"startup_move_actor_uid,omitempty"`
	StartupMoveDetected                     bool                  `json:"startup_move_detected,omitempty"`
	StartupMoveTerminalAfterRestore         bool                  `json:"startup_move_terminal_after_restore,omitempty"`
	Case                                    string                `json:"case"`
	Status                                  string                `json:"status"`
	CleanupConfirmed                        bool                  `json:"cleanup_confirmed"`
	Backend                                 *rb.BackendInspection `json:"backend,omitempty"`
	Evidence                                []rb.ReadEvidence     `json:"evidence,omitempty"`
	Limitations                             []string              `json:"limitations"`
}

func nativeReport(t *testing.T) *nativeRecord {
	t.Helper()
	r := &nativeRecord{Case: t.Name(), Status: "measured_pass", Limitations: []string{"Test-only fixture rebinding of the deny-only authorizer; shipped generic reads remain refused.", "No provider network call; publication means the coordinator's private result sink.", "This run covers the reported bind mode and fixed worker only, not all descendants/platforms or natural unjoinable processes."}}
	t.Cleanup(func() {
		if t.Failed() {
			r.Status = "failed"
		} else if t.Skipped() && r.Status == "measured_pass" {
			r.Status = "skipped_without_disposition"
		}
		if dir := os.Getenv("KOMPOSYN_NATIVE_RECORDS"); dir != "" {
			b, err := json.MarshalIndent(r, "", "  ")
			if err == nil {
				err = os.WriteFile(filepath.Join(dir, t.Name()+".json"), append(b, '\n'), 0600)
			}
			if err != nil {
				t.Errorf("could not persist native evidence")
			}
		}
	})
	return r
}
func nativeContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// This process-level check uses the compiled acceptance test binary as the
// exact actor executable. It intentionally does not require Bubblewrap: it
// verifies early actor dispatch and runs with only the fixture TMPDIR set.
func TestNativeActorModeDispatch(t *testing.T) {
	r := nativeReport(t)
	root := filepath.Join(t.TempDir(), "actor-root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal("create private actor root", err)
	}
	parent := filepath.Join(root, "parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal("create actor source directory", err)
	}
	marker := []byte("native actor dispatch fixture\n")
	if err := os.WriteFile(filepath.Join(parent, "marker.fixture"), marker, 0o600); err != nil {
		t.Fatal("create actor marker", err)
	}
	before, err := os.Stat(parent)
	if err != nil {
		t.Fatal("stat actor source directory", err)
	}
	actor, err := rb.NewNativeActor(nativeContext(t), os.Args[0], root, rb.NativeActorMoveAndReplaceDirectory, "parent", "moved-parent")
	if err != nil {
		t.Fatal("start actor from exact test binary", err)
	}
	defer func() {
		if err := actor.Close(); err != nil {
			t.Errorf("join native actor: %v", err)
		}
		r.CleanupConfirmed = actor.Joined()
	}()
	if !actor.Ready() {
		t.Fatal("native actor did not report READY")
	}
	if err := actor.Run(nativeContext(t)); err != nil {
		t.Fatal("run native actor transition", err)
	}
	moved, err := os.Stat(filepath.Join(root, "moved-parent"))
	if err != nil || !os.SameFile(before, moved) {
		t.Fatalf("moved directory identity mismatch: moved=%v err=%v", moved, err)
	}
	replacement, err := os.Stat(parent)
	if err != nil || os.SameFile(before, replacement) || !actor.TransitionCompleted() || !actor.Joined() {
		t.Fatalf("actor transition incomplete: replacement=%v err=%v changed=%t joined=%t", replacement, err, actor.TransitionCompleted(), actor.Joined())
	}
	got, err := os.ReadFile(filepath.Join(root, "moved-parent", "marker.fixture"))
	if err != nil || !bytes.Equal(got, marker) {
		t.Fatalf("moved directory marker=%q err=%v", got, err)
	}
}

type nativeFixture struct {
	tracked      *nativeTrackedLauncher
	service      *Service
	impl         *linuxImplementation
	auth         auth.Store
	settings     settings.Store
	grants       *rb.OnceGrantFixture
	root, target string
}

func newNativeFixture(t *testing.T, r *nativeRecord) *nativeFixture {
	t.Helper()
	backend := os.Getenv("KOMPOSYN_NATIVE_BWRAP")
	if backend == "" {
		backend = "/usr/bin/bwrap"
	}
	inspection := rb.InspectBubblewrapForStage6(backend, rb.BindModeDescriptor)
	inspection.Path = "<configured-backend>"
	r.Backend = &inspection
	if !inspection.Trusted || inspection.Error != "" || inspection.BindMode != rb.BindModeDescriptor {
		r.Status = "unavailable"
		r.UnavailableReason = "configured Bubblewrap failed the trust check or bounded backend probe"
		if inspection.Trusted && inspection.Error == "" && inspection.BindMode != rb.BindModeDescriptor {
			r.UnavailableReason = "configured Bubblewrap and worker source do not support the measured descriptor bind mode"
		}
		r.CleanupConfirmed = true
		t.Skip("trusted native backend unavailable; see evidence")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal("could not identify test worker")
	}
	launcher, err := rb.NewWorkerLauncherForStage6(backend, exe, rb.BindModeDescriptor, true)
	if err != nil {
		r.Status = "unavailable"
		r.UnavailableReason = "trusted Bubblewrap worker launcher could not be created"
		r.CleanupConfirmed = true
		t.Skip("native launcher unavailable")
	}
	registry, err := rb.NewRegistry()
	if err != nil {
		launcher.Close()
		t.Fatal("registry creation failed")
	}
	tracked := &nativeTrackedLauncher{delegate: launcher}
	impl, err := composeLinuxImplementation(registry, tracked, nil)
	if err != nil {
		registry.Close()
		launcher.Close()
		t.Fatal("service composition failed")
	}
	// No read has been prepared/launched. Replace only the internal deny-only
	// coordinator in this test image with an explicitly once-approved fixture
	// coordinator. Both use the service's real registry and composition inventory.
	// The original idle coordinator owns no unique OS resources.
	grants := rb.NewOnceGrantFixture()
	session, err := rb.NewHostCoordinator(registry, rb.NewBroker(registry), tracked, grants, impl.inventory)
	if err != nil {
		impl.Close(context.Background())
		t.Fatal("fixture coordinator creation failed")
	}
	impl.session = session
	f := &nativeFixture{tracked: tracked, service: &Service{impl: impl}, impl: impl, grants: grants, root: t.TempDir()}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r.Evidence = session.Evidence()
		// Exclude raw device/inode identities and any unsanitized diagnostic strings.
		for i := range r.Evidence {
			r.Evidence[i].CapabilityIdentity = rb.FileIdentity{}
			r.Evidence[i].Probe.Details = nil
			r.Evidence[i].Effect = ""
			r.Evidence[i].Limitation = ""
		}
		err := f.service.Close(ctx)
		r.CleanupConfirmed = err == nil
		if err != nil {
			t.Errorf("native service cleanup unconfirmed")
		}
	})
	f.auth = auth.Store{Path: filepath.Join(f.root, "private-auth", "auth.json"), Protection: f.service}
	f.settings = settings.Store{Path: filepath.Join(f.root, "private-settings", "settings.json"), Protection: f.service}
	if err = f.auth.RegisterProtection(nativeContext(t)); err != nil {
		t.Fatal("auth protection registration failed")
	}
	if err = f.settings.RegisterProtection(nativeContext(t)); err != nil {
		t.Fatal("settings protection registration failed")
	}
	if err = f.auth.AddAPIKey("fixture", "openai", "fixture", "synthetic-native-key", nil); err != nil {
		t.Fatal("synthetic auth write failed")
	}
	if err = f.settings.SaveContext(nativeContext(t), settings.Settings{Plugin: settings.DefaultUI}); err != nil {
		t.Fatal("synthetic settings write failed")
	}
	f.target = filepath.Join(f.root, "ordinary.txt")
	if err = os.WriteFile(f.target, []byte("synthetic-native-read-control"), 0600); err != nil {
		t.Fatal("fixture write failed")
	}
	if err = session.SelectRoot(f.root); err != nil {
		t.Fatal("root selection failed")
	}
	return f
}

func setupNativeStartupScope(t *testing.T, f *nativeFixture, r *nativeRecord, conversation string) {
	setupNativeStartupScopeAt(t, f, r, conversation, f.root)
}

func setupNativeStartupScopeAt(t *testing.T, f *nativeFixture, r *nativeRecord, conversation, scopeRoot string) {
	t.Helper()
	f.service.captureStartupDirectory(scopeRoot)
	if f.service.CreationFoundationSnapshot().StartupScope.State != StartupScopeIdentityRetained {
		r.Status = "unavailable"
		r.UnavailableReason = "host cannot retain the startup-directory identity required by this profile"
		r.CleanupConfirmed = true
		t.Skip("the host cannot retain the native startup-directory identity")
	}
	scope, err := os.Open(scopeRoot)
	if err != nil {
		t.Fatal("open retained startup scope for filesystem evidence", err)
	}
	filesystem, mountID := creationFilesystemEvidence(t, scope)
	_ = scope.Close()
	r.CreationStartupFilesystem = filesystem
	r.CreationStartupMountID = mountID
	if filesystem != "ext4" {
		r.Status = "unavailable"
		r.UnavailableReason = "startup scope is not on the supported ext4 filesystem"
		r.CleanupConfirmed = true
		t.Skip(r.UnavailableReason)
	}
	if !f.service.CreationFoundationSnapshot().OwnerCoverageComplete {
		t.Fatal("actual auth/settings owner lifecycle coverage is incomplete")
	}
	// newNativeFixture preselects its temporary root at coordinator generation
	// 1. Selecting a distinct external mount-test scope must advance that
	// generation or the coordinator correctly rejects the same-generation
	// root replacement as a policy change.
	rootGeneration := uint64(1)
	if scopeRoot != f.root {
		rootGeneration = 2
	}
	if err := f.service.InvalidateFilesystemRoot(nativeContext(t), runtime.FilesystemRootTransition{ConversationID: conversation, Root: scopeRoot, Generation: rootGeneration, Reason: runtime.FilesystemRootSelected}); err != nil {
		t.Fatal("select fixed-scope relative lookup root", err)
	}
}

func nativeCreationInvocation(t *testing.T, conversation, callID, path, content string) tools.Invocation {
	t.Helper()
	arguments, err := json.Marshal(tools.CreateFileInput{Path: path, Content: content})
	if err != nil {
		t.Fatal("encode synthetic creation request", err)
	}
	return tools.Invocation{ConversationID: conversation, TurnID: "turn", ProviderCallID: callID, ToolID: tools.CreateFileID, Arguments: arguments}
}

func prepareNativeCreation(t *testing.T, f *nativeFixture, invocation tools.Invocation, workingDirectory string) permissions.Request {
	t.Helper()
	request, err := f.service.PrepareFilesystemCreation(nativeContext(t), invocation, "fixture/model/profile", workingDirectory)
	if err != nil {
		t.Fatalf("prepare native creation %q: %v", invocation.ProviderCallID, err)
	}
	return request
}

func assertNoNativeCreationStages(t *testing.T, roots ...string) {
	t.Helper()
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if strings.HasPrefix(entry.Name(), ".komposyn-stage-") {
				return fmt.Errorf("unexpected private creation stage remains")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("inspect fixture stage disposition: %v", err)
		}
	}
}

func (f *nativeFixture) read(t *testing.T, id, target string) rb.WorkerResult {
	t.Helper()
	if err := f.grants.ApproveOnce(id); err != nil {
		t.Fatal("once fixture approval failed")
	}
	r, err := f.impl.session.Read(nativeContext(t), f.root, target, id)
	if err != nil {
		t.Fatal("coordinator read call failed")
	}
	return r
}
func lastNativeEvidence(t *testing.T, f *nativeFixture) rb.ReadEvidence {
	t.Helper()
	e := f.impl.session.Evidence()
	if len(e) == 0 {
		t.Fatal("missing worker evidence")
	}
	return e[len(e)-1]
}
func TestNativeShippedGenericReadGate(t *testing.T) {
	r := nativeReport(t)
	s, err := NewHostService(os.Getenv("KOMPOSYN_NATIVE_BWRAP"))
	if err != nil {
		t.Fatal("host service creation failed")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r.CleanupConfirmed = s.Close(ctx) == nil
		if !r.CleanupConfirmed {
			t.Error("host service cleanup failed")
		}
	})
	if !errors.Is(s.PrepareGenericRead(nativeContext(t), t.TempDir(), "unclassified"), ErrUnknownRootData) {
		t.Fatal("shipped generic read gate opened")
	}
}
func TestNativeServicePositiveRead(t *testing.T) {
	r := nativeReport(t)
	f := newNativeFixture(t, r)
	result := f.read(t, "positive", f.target)
	e := lastNativeEvidence(t, f)
	if result.Outcome != rb.ReadSucceeded || !bytes.Equal(result.Bytes, []byte("synthetic-native-read-control")) || f.grants.ConsumptionCount("positive") != 1 || !e.WorkerStarted || !e.WorkerReady || !e.CapabilitySent || !e.CapabilityHeld || !e.ReadStarted || !e.WorkerJoined || !e.Published || len(f.impl.session.PublishedResults()) != 1 {
		t.Fatal("positive read lacked actual bytes or complete worker lifecycle")
	}
	r.VerifiedReturnedBytes = true
}

func TestNativeServiceBoundedCreation(t *testing.T) {
	r := nativeReport(t)
	f := newNativeFixture(t, r)
	installSyntheticCreationManifest(t, f.service, f.root)
	parent, err := os.Open(f.root)
	if err != nil {
		t.Fatal("could not open synthetic creation parent")
	}
	defer parent.Close()
	identity := identityFromInfo(t, f.root)
	filesystem, mountID := creationFilesystemEvidence(t, parent)
	r.CreationFilesystem = filesystem
	r.CreationMountID = mountID
	stageName, err := rb.NewCreationStageName()
	if err != nil {
		t.Fatal("could not generate private stage name")
	}
	content := []byte("synthetic native creation fixture\n")
	result, err := f.impl.executeBoundedCreation(nativeContext(t), parent, identity, stageName, "created.txt", content)
	if err != nil || result.Outcome != rb.CreationCreated || result.Bytes != uint32(len(content)) {
		detail := f.tracked.lastFailureDetail()
		if detail == "worker_missing" {
			detail = creationEligibilitySummary(f.impl, identity, filepath.Join(f.root, "created.txt"))
		}
		r.CreationFailureDetail = detail
		t.Fatalf("confined create worker result outcome=%q bytes=%d err=%v worker=%s", result.Outcome, result.Bytes, err, detail)
	}
	created := filepath.Join(f.root, "created.txt")
	actual, err := os.ReadFile(created)
	if err != nil || !bytes.Equal(actual, content) {
		t.Fatal("published fixture bytes did not match the approved bytes")
	}
	info, err := os.Stat(created)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("published fixture mode was not 0600")
	}
	r.CreationVerifiedBytes = true
	r.CreationVerifiedMode = true
	if _, err := os.Lstat(filepath.Join(f.root, stageName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("private stage remained after publication")
	}
	r.CreationStageAbsent = true
	if !f.tracked.allJoined() {
		t.Fatal("create worker was not joined")
	}
	// The primitive must refuse a collision without replacing its bytes.
	collisionName, err := rb.NewCreationStageName()
	if err != nil {
		t.Fatal("could not generate collision stage name")
	}
	incumbent := []byte("independent actor fixture\n")
	if err := os.WriteFile(filepath.Join(f.root, "collision.txt"), incumbent, 0600); err != nil {
		t.Fatal("could not prepare collision fixture")
	}
	collision, err := f.impl.executeBoundedCreation(nativeContext(t), parent, identity, collisionName, "collision.txt", content)
	if err != nil || collision.Outcome != rb.CreationConflict {
		t.Fatal("existing target did not produce a no-replace conflict")
	}
	after, err := os.ReadFile(filepath.Join(f.root, "collision.txt"))
	if err != nil || !bytes.Equal(after, incumbent) {
		t.Fatal("no-replace conflict changed the incumbent bytes")
	}
	lateStageName, err := rb.NewCreationStageName()
	if err != nil {
		t.Fatal("could not generate late-collision stage name")
	}
	lateIncumbent := []byte("independent actor after final observation\n")
	f.tracked.mu.Lock()
	f.tracked.beforeCreationPublish = func() error {
		return os.WriteFile(filepath.Join(f.root, "late-collision.txt"), lateIncumbent, 0600)
	}
	f.tracked.mu.Unlock()
	lateCollision, err := f.impl.executeBoundedCreation(nativeContext(t), parent, identity, lateStageName, "late-collision.txt", content)
	f.tracked.mu.Lock()
	f.tracked.beforeCreationPublish = nil
	f.tracked.mu.Unlock()
	if err != nil || lateCollision.Outcome != rb.CreationConflict {
		t.Fatal("no-replace publication did not classify the post-observation actor as a conflict")
	}
	lateActual, err := os.ReadFile(filepath.Join(f.root, "late-collision.txt"))
	if err != nil || !bytes.Equal(lateActual, lateIncumbent) {
		t.Fatal("publication-time collision changed the independent actor's file")
	}
	if _, err := os.Lstat(filepath.Join(f.root, lateStageName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stage remained after publication-time collision")
	}
	if !f.tracked.allJoined() {
		t.Fatal("late-collision worker was not joined")
	}
	r.CreationLateCollisionRefused = true
	r.CreationLateStageAbsent = true
}

func TestNativeServiceCreationUsesRetainedParentAfterIndependentMove(t *testing.T) {
	r := nativeReport(t)
	f := newNativeFixture(t, r)
	scope := filepath.Join(f.root, "creation-scope")
	if err := os.Mkdir(scope, 0o700); err != nil {
		t.Fatal("create synthetic creation scope", err)
	}
	installSyntheticCreationManifest(t, f.service, scope)
	parent, err := os.Open(scope)
	if err != nil {
		t.Fatal("open retained synthetic parent", err)
	}
	defer parent.Close()
	parentIdentity := identityFromInfo(t, scope)
	filesystem, mountID := creationFilesystemEvidence(t, parent)
	r.CreationParentMoveFilesystem = filesystem
	r.CreationParentMoveMountID = mountID
	r.CreationParentIdentitySHA256 = creationIdentityHash(t, scope)
	actor, err := rb.NewNativeActor(nativeContext(t), os.Args[0], f.root, rb.NativeActorMoveAndReplaceDirectory, "creation-scope", "moved-creation-scope")
	if err != nil {
		t.Fatal("start independent same-user parent actor", err)
	}
	defer func() {
		if err := actor.Close(); err != nil {
			t.Errorf("join parent actor: %v", err)
		}
	}()
	r.CreationActorPID = actor.PID()
	r.CreationActorUID = os.Geteuid()
	r.CreationActorExecution = "separate process; same effective UID; no shell; shared host mount namespace; fixed rename-and-mkdir action"
	stageName, err := rb.NewCreationStageName()
	if err != nil {
		t.Fatal("generate private stage name", err)
	}
	content := []byte("created through the retained parent after its pathname moved\n")
	f.tracked.mu.Lock()
	f.tracked.beforeCreationStage = func() error {
		return actor.Run(nativeContext(t))
	}
	f.tracked.mu.Unlock()
	result, createErr := f.impl.executeBoundedCreation(nativeContext(t), parent, parentIdentity, stageName, "retained-parent.txt", content)
	f.tracked.mu.Lock()
	f.tracked.beforeCreationStage = nil
	f.tracked.mu.Unlock()
	if createErr != nil || result.Outcome != rb.CreationCreated || !actor.TransitionCompleted() || !actor.Joined() {
		t.Fatalf("retained-parent creation result=%+v err=%v actor_changed=%t actor_joined=%t", result, createErr, actor.TransitionCompleted(), actor.Joined())
	}
	movedScope := filepath.Join(f.root, "moved-creation-scope")
	replacementInfo, err := os.Stat(scope)
	if err != nil {
		t.Fatal("stat substitute parent", err)
	}
	movedInfo, err := os.Stat(movedScope)
	if err != nil {
		t.Fatal("stat moved parent", err)
	}
	r.CreationMovedIdentitySHA256 = creationIdentityHash(t, movedScope)
	r.CreationSubstituteIdentitySHA256 = creationIdentityHash(t, scope)
	if !os.SameFile(parentInfo(t, parent), movedInfo) || os.SameFile(replacementInfo, movedInfo) {
		t.Fatal("independent actor did not preserve the retained parent identity while substituting its old route")
	}
	actual, err := os.ReadFile(filepath.Join(movedScope, "retained-parent.txt"))
	if err != nil || !bytes.Equal(actual, content) {
		t.Fatalf("creation did not publish to the retained parent object: bytes=%q err=%v", actual, err)
	}
	if _, err := os.Lstat(filepath.Join(scope, "retained-parent.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("creation redirected to substitute pathname: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(movedScope, stageName)); !errors.Is(err, os.ErrNotExist) || !f.tracked.allJoined() {
		t.Fatalf("parent-move stage or worker cleanup incomplete: stage_err=%v joined=%t", err, f.tracked.allJoined())
	}
	r.CreationParentMovePublished = true
}

func TestNativeServiceNestedCreationUsesStartupScope(t *testing.T) {
	r := nativeReport(t)
	f := newNativeFixture(t, r)
	nested := filepath.Join(f.root, "existing", "nested")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal("create existing nested parent", err)
	}
	setupNativeStartupScope(t, f, r, "native-nested")
	toolRuntime := runtime.BundledConversationToolRuntimeConfigWithFilesystemCreation(
		runtime.ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			return permissions.ApprovalDeny, "", nil
		}),
		runtime.ToolClarificationFunc(func(context.Context, runtime.ClarificationRequest) (runtime.ClarificationResponse, error) {
			return runtime.ClarificationResponse{}, errors.New("synthetic clarification is unavailable")
		}),
		f.service,
	)
	if toolRuntime.Registry == nil {
		t.Fatal("shipping runtime composition was unavailable")
	}
	if _, registered := toolRuntime.Registry.Lookup(tools.CreateFileID); !registered {
		t.Fatal("eligible trusted Service did not register apply_patch through shipping composition")
	}
	r.CreationSchemaRegistered = true
	parent, err := os.Open(nested)
	if err != nil {
		t.Fatal("open nested evidence parent", err)
	}
	filesystem, mountID := creationFilesystemEvidence(t, parent)
	_ = parent.Close()
	r.CreationStartupFilesystem = filesystem
	r.CreationStartupMountID = mountID
	input := tools.CreateFileInput{Path: "existing/nested/created.txt", Content: "startup scope nested creation\n"}
	arguments, err := json.Marshal(input)
	if err != nil {
		t.Fatal("encode synthetic creation request", err)
	}
	invocation := tools.Invocation{ConversationID: "native-nested", TurnID: "turn", ProviderCallID: "nested-create", ToolID: tools.CreateFileID, Arguments: arguments}
	request, err := f.service.PrepareFilesystemCreation(nativeContext(t), invocation, "fixture/model/profile", "")
	if err != nil {
		t.Fatal("prepare nested creation through service", err)
	}
	want := filepath.Join(f.root, filepath.FromSlash(input.Path))
	if request.Requirements[1].Resource == nil || request.Requirements[1].Resource.Target != want {
		t.Fatal("approval was not bound to the exact nested target")
	}
	prepared, err := f.service.PrepareApprovedFilesystemCreation(nativeContext(t), invocation, request, 1)
	if err != nil {
		t.Fatal("prepare approved nested creation", err)
	}
	result, executeErr := prepared.Execute(nativeContext(t))
	closeErr := prepared.Close(context.Background())
	if executeErr != nil || closeErr != nil || result.Outcome != tools.OutcomeSucceeded {
		t.Fatalf("nested creation outcome=%q execute=%v close=%v", result.Outcome, executeErr, closeErr)
	}
	actual, err := os.ReadFile(want)
	if err != nil || !bytes.Equal(actual, []byte(input.Content)) {
		t.Fatalf("nested file bytes=%q err=%v", actual, err)
	}
	if !f.tracked.allJoined() {
		t.Fatal("nested creation worker was not joined")
	}
	r.CreationNestedPublished = true
}

func TestNativeServiceStartupRootMovementIsTerminal(t *testing.T) {
	r := nativeReport(t)
	f := newNativeFixture(t, r)
	setupNativeStartupScope(t, f, r, "native-root-move")
	input := tools.CreateFileInput{Path: "never-created.txt", Content: "synthetic movement refusal"}
	arguments, err := json.Marshal(input)
	if err != nil {
		t.Fatal("encode movement request", err)
	}
	invocation := tools.Invocation{ConversationID: "native-root-move", TurnID: "turn", ProviderCallID: "move-call", ToolID: tools.CreateFileID, Arguments: arguments}
	request, err := f.service.PrepareFilesystemCreation(nativeContext(t), invocation, "fixture/model/profile", "")
	if err != nil {
		t.Fatal("prepare before independent root move", err)
	}
	actorRoot := filepath.Dir(f.root)
	sourceName, targetName := filepath.Base(f.root), "moved-startup-root"
	actor, err := rb.NewNativeActor(nativeContext(t), os.Args[0], actorRoot, rb.NativeActorMoveAndReplaceDirectory, sourceName, targetName)
	if err != nil {
		t.Fatal("start independent startup-root actor", err)
	}
	defer func() {
		if err := actor.Close(); err != nil {
			t.Errorf("join startup-root actor after test: %v", err)
		}
	}()
	r.StartupMoveActorPID = actor.PID()
	r.StartupMoveActorUID = os.Geteuid()
	if !actor.Ready() {
		t.Fatal("startup-root actor did not report READY")
	}
	if err := actor.Run(nativeContext(t)); err != nil {
		t.Fatal("move and replace startup root", err)
	}
	if err := actor.Close(); err != nil || !actor.Joined() {
		t.Fatalf("startup-root actor cleanup: err=%v joined=%t", err, actor.Joined())
	}
	prepared, prepareErr := f.service.PrepareApprovedFilesystemCreation(nativeContext(t), invocation, request, 1)
	if prepareErr == nil {
		_ = prepared.Close(context.Background())
		t.Fatal("startup-root replacement reused approved preparation")
	}
	r.StartupMoveDetected = errors.Is(prepareErr, ErrStartupScopeMoved)
	if !r.StartupMoveDetected || len(f.tracked.workers) != 0 {
		t.Fatal("startup-root movement was not detected before worker launch")
	}
	if err := os.Remove(f.root); err != nil {
		t.Fatal("remove substitute startup root", err)
	}
	if err := os.Rename(filepath.Join(actorRoot, targetName), f.root); err != nil {
		t.Fatal("restore original startup object", err)
	}
	invocation.ProviderCallID = "move-call-after-restore"
	invocation.Arguments, _ = json.Marshal(tools.CreateFileInput{Path: "after-restore.txt", Content: "no replay"})
	if _, err := f.service.PrepareFilesystemCreation(nativeContext(t), invocation, "fixture/model/profile", ""); !errors.Is(err, ErrStartupScopeMoved) {
		t.Fatalf("restored root cleared terminal movement state: %v", err)
	}
	r.StartupMoveTerminalAfterRestore = true
}

func TestNativeServiceNestedParentSubstitutionRefusesPreparedProposal(t *testing.T) {
	r := nativeReport(t)
	f := newNativeFixture(t, r)
	nested := filepath.Join(f.root, "existing", "nested")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal("create existing nested parent", err)
	}
	setupNativeStartupScope(t, f, r, "native-nested-substitution")
	invocation := nativeCreationInvocation(t, "native-nested-substitution", "stale-parent-call", "existing/nested/stale.txt", "must not publish")
	request := prepareNativeCreation(t, f, invocation, f.root)
	r.CreationParentIdentitySHA256 = creationIdentityHash(t, nested)
	actor, err := rb.NewNativeActor(nativeContext(t), os.Args[0], f.root, rb.NativeActorMoveAndReplaceDirectory, "existing/nested", "existing/moved")
	if err != nil {
		t.Fatal("start independent same-UID nested-parent actor", err)
	}
	defer func() {
		if err := actor.Close(); err != nil {
			t.Errorf("join nested-parent actor after test: %v", err)
		}
	}()
	r.CreationActorPID = actor.PID()
	r.CreationActorUID = os.Geteuid()
	r.CreationActorExecution = "separate same-UID process; fixed nested-directory move and replacement; no shell"
	if !actor.Ready() {
		t.Fatal("nested-parent actor did not report READY")
	}
	if err := actor.Run(nativeContext(t)); err != nil {
		t.Fatal("move retained nested parent and create substitute", err)
	}
	if err := actor.Close(); err != nil || !actor.Joined() {
		t.Fatalf("nested-parent actor cleanup: err=%v joined=%t", err, actor.Joined())
	}
	moved := filepath.Join(f.root, "existing", "moved")
	substitute := nested
	r.CreationMovedIdentitySHA256 = creationIdentityHash(t, moved)
	r.CreationSubstituteIdentitySHA256 = creationIdentityHash(t, substitute)
	prepared, err := f.service.PrepareApprovedFilesystemCreation(nativeContext(t), invocation, request, 1)
	if err == nil {
		_ = prepared.Close(context.Background())
		t.Fatal("changed nested parent reused the approved proposal")
	}
	if !errors.Is(err, runtime.ErrFilesystemRootStale) {
		t.Fatalf("nested-parent replacement returned %v; want stale proposal", err)
	}
	for _, target := range []string{filepath.Join(moved, "stale.txt"), filepath.Join(substitute, "stale.txt")} {
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale proposal created %q: %v", target, err)
		}
	}
	if f.tracked.workerCount() != 0 {
		t.Fatal("nested-parent conflict launched a worker before fresh preparation")
	}
	assertNoNativeCreationStages(t, f.root)
	if f.service.CreationFoundationSnapshot().StartupScope.State != StartupScopeIdentityRetained {
		t.Fatal("nested-parent conflict incorrectly made the whole startup scope terminal")
	}
	r.CreationNestedParentSubstitutionRefused = true
}

func TestNativeServiceScopeAndTargetRefusalsPreserveFixtures(t *testing.T) {
	r := nativeReport(t)
	f := newNativeFixture(t, r)
	nested := filepath.Join(f.root, "existing", "nested")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal("create ordinary nested project content", err)
	}
	outside := f.root + "-outside"
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal("create outside sentinel directory", err)
	}
	defer os.RemoveAll(outside)
	sentinel := []byte("outside sentinel must remain unchanged\n")
	sentinelPath := filepath.Join(outside, "sentinel.txt")
	if err := os.WriteFile(sentinelPath, sentinel, 0600); err != nil {
		t.Fatal("create outside sentinel", err)
	}
	if err := os.Symlink(outside, filepath.Join(f.root, "parent-link")); err != nil {
		t.Fatal("create synthetic symlinked parent", err)
	}
	if err := os.Symlink(sentinelPath, filepath.Join(f.root, "leaf-link")); err != nil {
		t.Fatal("create synthetic symlinked leaf", err)
	}
	authPath, err := f.auth.EffectivePath()
	if err != nil {
		t.Fatal("resolve synthetic protected auth route", err)
	}
	settingsPath, err := f.settings.EffectivePath()
	if err != nil {
		t.Fatal("resolve synthetic protected settings route", err)
	}
	authAlias := filepath.Join(f.root, "auth-hardlink-alias")
	settingsAlias := filepath.Join(f.root, "settings-hardlink-alias")
	if err := os.Link(authPath, authAlias); err != nil {
		t.Fatal("create synthetic auth hard-link alias", err)
	}
	if err := os.Link(settingsPath, settingsAlias); err != nil {
		t.Fatal("create synthetic settings hard-link alias", err)
	}
	authAliasBefore, err := os.ReadFile(authAlias)
	if err != nil {
		t.Fatal("read synthetic auth alias for unchanged-byte comparison", err)
	}
	settingsAliasBefore, err := os.ReadFile(settingsAlias)
	if err != nil {
		t.Fatal("read synthetic settings alias for unchanged-byte comparison", err)
	}
	if err := os.Remove(authPath); err != nil {
		t.Fatal("make protected auth route absent while retaining its owner registration", err)
	}
	protectedRelative, err := filepath.Rel(f.root, authPath)
	if err != nil || filepath.IsAbs(protectedRelative) || strings.HasPrefix(protectedRelative, ".."+string(filepath.Separator)) {
		t.Fatal("synthetic protected route is not inside the disposable startup scope")
	}
	setupNativeStartupScope(t, f, r, "native-invalid-targets")

	// A valid nested operation establishes that unrelated existing project
	// content does not disable the scope before refusal cases are attempted.
	baseline := nativeCreationInvocation(t, "native-invalid-targets", "valid-baseline", "existing/nested/baseline.txt", "valid nested baseline\n")
	request := prepareNativeCreation(t, f, baseline, f.root)
	prepared, err := f.service.PrepareApprovedFilesystemCreation(nativeContext(t), baseline, request, 1)
	if err != nil {
		t.Fatal("prepare valid nested baseline", err)
	}
	result, executeErr := prepared.Execute(nativeContext(t))
	closeErr := prepared.Close(context.Background())
	if executeErr != nil || closeErr != nil || result.Outcome != tools.OutcomeSucceeded {
		t.Fatalf("valid nested baseline outcome=%q execute=%v close=%v", result.Outcome, executeErr, closeErr)
	}
	if got, err := os.ReadFile(filepath.Join(nested, "baseline.txt")); err != nil || string(got) != "valid nested baseline\n" {
		t.Fatalf("ordinary-content baseline bytes=%q err=%v", got, err)
	}
	workersBeforeRefusals := f.tracked.workerCount()

	cases := []struct {
		name, path, workingDirectory string
	}{
		{name: "direct scope escape", path: "../" + filepath.Base(outside) + "/direct.txt", workingDirectory: f.root},
		{name: "nested scope escape", path: "existing/nested/../../../" + filepath.Base(outside) + "/nested.txt", workingDirectory: f.root},
		{name: "navigation outside fixed scope", path: "navigated-outside.txt", workingDirectory: outside},
		{name: "missing nested parent", path: "missing/new.txt", workingDirectory: f.root},
		{name: "non-directory nested parent", path: "ordinary.txt/new.txt", workingDirectory: f.root},
		{name: "symlinked parent", path: "parent-link/new.txt", workingDirectory: f.root},
		{name: "symlinked leaf", path: "leaf-link", workingDirectory: f.root},
		{name: "existing file collision", path: "ordinary.txt", workingDirectory: f.root},
		{name: "existing directory collision", path: "existing", workingDirectory: f.root},
		{name: "auth hard-link alias collision", path: filepath.Base(authAlias), workingDirectory: f.root},
		{name: "settings hard-link alias collision", path: filepath.Base(settingsAlias), workingDirectory: f.root},
		{name: "absent protected owner route", path: filepath.ToSlash(protectedRelative), workingDirectory: f.root},
	}
	for i, test := range cases {
		invocation := nativeCreationInvocation(t, "native-invalid-targets", fmt.Sprintf("refusal-%d", i), test.path, "must remain unapplied")
		_, err := f.service.PrepareFilesystemCreation(nativeContext(t), invocation, "fixture/model/profile", test.workingDirectory)
		if err == nil {
			if discardErr := f.service.DiscardFilesystemCreationProposal(context.Background(), invocation); discardErr != nil {
				t.Fatalf("discard unexpectedly eligible %s proposal: %v", test.name, discardErr)
			}
			t.Errorf("%s was offered for approval", test.name)
		}
	}
	if f.tracked.workerCount() != workersBeforeRefusals {
		t.Fatal("a refused scope/target launched a worker")
	}
	if got, err := os.ReadFile(sentinelPath); err != nil || !bytes.Equal(got, sentinel) {
		t.Fatalf("outside symlink sentinel changed: %q err=%v", got, err)
	}
	if got, err := os.ReadFile(authAlias); err != nil || !bytes.Equal(got, authAliasBefore) {
		t.Fatal("auth hard-link alias changed during refused creation")
	}
	if got, err := os.ReadFile(settingsAlias); err != nil || !bytes.Equal(got, settingsAliasBefore) {
		t.Fatal("settings hard-link alias changed during refused creation")
	}
	if _, err := os.Lstat(filepath.Join(outside, "direct.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("direct scope escape created an outside target: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "nested.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nested scope escape created an outside target: %v", err)
	}
	assertNoNativeCreationStages(t, f.root, outside)
	if !f.tracked.allJoined() {
		t.Fatal("baseline creation worker did not join")
	}
	r.CreationInvalidTargetMatrixRefused = true
}

func TestNativeServiceMountBoundaryRefusal(t *testing.T) {
	r := nativeReport(t)
	f := newNativeFixture(t, r)
	scopeRoot := os.Getenv("KOMPOSYN_NATIVE_MOUNT_SCOPE")
	if scopeRoot == "" {
		r.Status = "unavailable"
		r.CreationMountBoundaryUnavailableReason = "no operator-designated private scratch directory with an already-existing nested mount was supplied; creating or changing mounts is outside this task"
		r.UnavailableReason = r.CreationMountBoundaryUnavailableReason
		r.CleanupConfirmed = true
		t.Skip(r.CreationMountBoundaryUnavailableReason)
	}
	scopeRoot, err := filepath.Abs(scopeRoot)
	if err != nil || filepath.Clean(scopeRoot) != scopeRoot {
		t.Fatal("configured mount-scope path must be absolute and clean")
	}
	info, err := os.Lstat(scopeRoot)
	var stat *syscall.Stat_t
	statOK := false
	if info != nil {
		stat, statOK = info.Sys().(*syscall.Stat_t)
	}
	if err != nil || info == nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 || !statOK || int(stat.Uid) != os.Geteuid() {
		t.Fatal("configured mount scope must be an existing private real directory owned by the current user")
	}
	setupNativeStartupScopeAt(t, f, r, "native-mount-boundary", scopeRoot)
	scope, err := os.Open(scopeRoot)
	if err != nil {
		t.Fatal("open configured startup scope")
	}
	filesystem, scopeMountID := creationFilesystemEvidence(t, scope)
	_ = scope.Close()
	r.CreationStartupFilesystem = filesystem
	r.CreationStartupMountID = scopeMountID
	if filesystem != "ext4" {
		r.Status = "unavailable"
		r.CreationMountBoundaryUnavailableReason = "operator-designated scope is not on the supported ext4 filesystem"
		r.UnavailableReason = r.CreationMountBoundaryUnavailableReason
		r.CleanupConfirmed = true
		t.Skip(r.CreationMountBoundaryUnavailableReason)
	}
	mountID, relative, found := mountBoundaryBelow(t, scopeRoot)
	if !found {
		r.Status = "unavailable"
		r.CreationMountBoundaryUnavailableReason = "operator-designated startup scope contains no pre-existing nested mount; creating or changing a mount is outside this task"
		r.UnavailableReason = r.CreationMountBoundaryUnavailableReason
		r.CleanupConfirmed = true
		t.Skip(r.CreationMountBoundaryUnavailableReason)
	}
	if mountID == scopeMountID {
		t.Fatal("nested mount evidence did not identify a distinct mount ID")
	}
	r.CreationMountID = mountID
	// A valid direct-child proposal proves this exact scope and owner profile
	// are eligible before the real mount-crossing path is attempted.
	baseline := nativeCreationInvocation(t, "native-mount-boundary", "mount-baseline", "native-baseline-not-written.txt", "synthetic eligible baseline")
	_ = prepareNativeCreation(t, f, baseline, scopeRoot)
	if err := f.service.DiscardFilesystemCreationProposal(context.Background(), baseline); err != nil {
		t.Fatal("discard no-effect mount-profile baseline proposal", err)
	}
	invocation := nativeCreationInvocation(t, "native-mount-boundary", "mount-crossing", filepath.ToSlash(filepath.Join(relative, "must-not-exist.txt")), "must not cross a mount")
	if _, err := f.service.PrepareFilesystemCreation(nativeContext(t), invocation, "fixture/model/profile", scopeRoot); err == nil {
		t.Fatal("target beneath an existing nested mount was offered for approval")
	}
	if f.tracked.workerCount() != 0 {
		t.Fatal("mount-crossing refusal launched a worker")
	}
	target := filepath.Join(scopeRoot, relative, "must-not-exist.txt")
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mount-crossing refusal created a target: %v", err)
	}
	assertNoNativeDirectStages(t, scopeRoot)
	r.CreationMountBoundaryRefused = true
}

func assertNoNativeDirectStages(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal("inspect direct disposable scope entries", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".komposyn-stage-") {
			t.Fatal("unexpected private creation stage remains in the scope root")
		}
	}
}

func mountBoundaryBelow(t *testing.T, root string) (uint64, string, bool) {
	t.Helper()
	mountInfo, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		t.Fatal("read kernel mount table for fixture boundary", err)
	}
	type candidate struct {
		id       uint64
		relative string
		depth    int
	}
	var candidates []candidate
	for _, line := range strings.Split(string(mountInfo), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		var id uint64
		if _, err := fmt.Sscan(fields[0], &id); err != nil || id == 0 {
			continue
		}
		mountPath := strings.NewReplacer("\\040", " ", "\\011", "\t", "\\012", "\n", "\\134", "\\").Replace(fields[4])
		relative, err := filepath.Rel(root, mountPath)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			continue
		}
		info, err := os.Stat(mountPath)
		if err != nil || !info.IsDir() {
			continue
		}
		candidates = append(candidates, candidate{id: id, relative: relative, depth: strings.Count(relative, string(filepath.Separator))})
	}
	if len(candidates) == 0 {
		return 0, "", false
	}
	slices.SortFunc(candidates, func(a, b candidate) int { return a.depth - b.depth })
	return candidates[0].id, candidates[0].relative, true
}

func TestNativeServiceStartupRootMovementAfterPublicationRelease(t *testing.T) {
	r := nativeReport(t)
	f := newNativeFixture(t, r)
	setupNativeStartupScope(t, f, r, "native-root-after-release")
	invocation := nativeCreationInvocation(t, "native-root-after-release", "after-release-call", "after-release.txt", "published after final observation\n")
	request := prepareNativeCreation(t, f, invocation, f.root)
	prepared, err := f.service.PrepareApprovedFilesystemCreation(nativeContext(t), invocation, request, 1)
	if err != nil {
		t.Fatal("prepare once-approved startup-scope creation", err)
	}
	actorRoot := filepath.Dir(f.root)
	movedName := "moved-" + filepath.Base(f.root)
	movedRoot := filepath.Join(actorRoot, movedName)
	actor, err := rb.NewNativeActor(nativeContext(t), os.Args[0], actorRoot, rb.NativeActorMoveAndReplaceDirectory, filepath.Base(f.root), movedName)
	if err != nil {
		t.Fatal("start independent startup-root actor", err)
	}
	defer func() {
		if err := actor.Close(); err != nil {
			t.Errorf("join post-release startup actor: %v", err)
		}
		if _, err := os.Lstat(movedRoot); err == nil {
			_ = os.RemoveAll(movedRoot)
		}
	}()
	r.StartupMoveAfterReleaseActorPID = actor.PID()
	r.StartupMoveAfterReleaseActorUID = os.Geteuid()
	if !actor.Ready() {
		t.Fatal("post-release startup actor did not report READY")
	}
	f.tracked.mu.Lock()
	f.tracked.beforeCreationPublish = func() error { return actor.Run(nativeContext(t)) }
	f.tracked.mu.Unlock()
	result, executeErr := prepared.Execute(nativeContext(t))
	f.tracked.mu.Lock()
	f.tracked.beforeCreationPublish = nil
	f.tracked.mu.Unlock()
	closeErr := prepared.Close(context.Background())
	if executeErr != nil || closeErr != nil || result.Outcome != tools.OutcomeSucceeded || !actor.TransitionCompleted() || !actor.Joined() || !f.tracked.allJoined() {
		t.Fatalf("after-release result=%q execute=%v close=%v actor_changed=%t actor_joined=%t workers_joined=%t", result.Outcome, executeErr, closeErr, actor.TransitionCompleted(), actor.Joined(), f.tracked.allJoined())
	}
	r.StartupMoveAfterReleaseOutcome = string(result.Outcome)
	if got, err := os.ReadFile(filepath.Join(movedRoot, "after-release.txt")); err != nil || string(got) != "published after final observation\n" {
		t.Fatalf("retained-object effect bytes=%q err=%v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(f.root, "after-release.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("effect was redirected to startup-path substitute: %v", err)
	}
	// Restore the retained object to its original pathname, then confirm that
	// restoration cannot clear the terminal movement latch.
	if err := os.Remove(f.root); err != nil {
		t.Fatal("remove empty startup-path substitute", err)
	}
	if err := os.Rename(movedRoot, f.root); err != nil {
		t.Fatal("restore original startup directory identity", err)
	}
	if got, err := os.ReadFile(filepath.Join(f.root, "after-release.txt")); err != nil || string(got) != "published after final observation\n" {
		t.Fatalf("restored retained object effect bytes=%q err=%v", got, err)
	}
	if f.service.CreationFoundationSnapshot().StartupScope.State != StartupScopeMoved {
		t.Fatal("startup movement after release was not latched")
	}
	future := nativeCreationInvocation(t, "native-root-after-release", "future-after-restore", "future.txt", "no replay")
	if _, err := f.service.PrepareFilesystemCreation(nativeContext(t), future, "fixture/model/profile", f.root); !errors.Is(err, ErrStartupScopeMoved) {
		t.Fatalf("restored startup identity allowed future creation: %v", err)
	}
	assertNoNativeCreationStages(t, f.root)
	r.StartupMoveAfterReleasePublished = true
}

func parentInfo(t *testing.T, parent *os.File) os.FileInfo {
	t.Helper()
	info, err := parent.Stat()
	if err != nil {
		t.Fatal("stat retained parent", err)
	}
	return info
}

func creationIdentityHash(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal("stat synthetic identity", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("synthetic object identity unavailable")
	}
	encoded := fmt.Sprintf("%d:%d:%d", stat.Dev, stat.Ino, stat.Mode&syscall.S_IFMT)
	digest := sha256.Sum256([]byte(encoded))
	return hex.EncodeToString(digest[:])
}

func creationEligibilitySummary(impl *linuxImplementation, parent rb.FileIdentity, target string) string {
	impl.mu.Lock()
	manifest := impl.creationManifest
	closed := impl.closed
	blocked := make([]string, 0, len(impl.blocked))
	for owner := range impl.blocked {
		blocked = append(blocked, owner)
	}
	barrierActive := impl.creationBarrier != nil
	owners := map[string]OwnerState{
		"auth":        impl.owners[auth.SensitiveOwnerAuth].State,
		"settings":    impl.owners[auth.SensitiveOwnerSettings].State,
		"tokenlogger": impl.owners["tokenlogger-store"].State,
		"provider":    impl.owners["provider-source"].State,
	}
	impl.mu.Unlock()
	coverage := impl.inventory.Snapshot()
	policy := impl.registry.View()
	if manifest == nil {
		return fmt.Sprintf("worker_missing; manifest=missing closed=%t blocked=%v barrier_active=%t", closed, blocked, barrierActive)
	}
	ownerMatches := make(map[string]int, len(manifest.owners))
	for owner, manifestOwner := range manifest.owners {
		for route, identity := range manifestOwner.registrations {
			for _, entry := range policy.Entries {
				if entry.Owner == owner && entry.Class == manifestOwner.class && entry.Path == route && entry.Identity == identity && !entry.Directory && identity.Inode != 0 {
					ownerMatches[owner]++
					break
				}
			}
		}
	}
	reasons := creationEligibilityReasons(impl, parent)
	return fmt.Sprintf("worker_missing; eligible=%t reasons=%v target_unprotected=%t closed=%t blocked=%v barrier_active=%t scope_complete=%t scope_matches=%t coverage_complete=%t coverage_revision_matches=%t policy_generation_matches=%t owner_states=%v manifest_owner_counts=%d/%d/%d/%d registry_owner_matches=%v token_db_suffix_valid=%t",
		impl.syntheticCreationManifestEligible(parent), reasons, impl.creationTargetUnprotected(target), closed, blocked, barrierActive,
		manifest.scopeComplete, manifest.scopeParent == parent,
		coverage.Complete, coverage.Revision == manifest.coverageRevision, policy.Generation == manifest.policyRevision,
		owners, len(manifest.owners[auth.SensitiveOwnerAuth].registrations), len(manifest.owners[auth.SensitiveOwnerSettings].registrations), len(manifest.owners["tokenlogger-store"].registrations), len(manifest.owners["provider-source"].registrations), ownerMatches,
		strings.HasSuffix(manifest.tokenDatabasePath, ".db"))
}

func creationEligibilityReasons(impl *linuxImplementation, parent rb.FileIdentity) []string {
	impl.mu.Lock()
	manifest := impl.creationManifest
	closed := impl.closed
	owners := make(map[string]OwnerCoverage, len(impl.owners))
	for name, coverage := range impl.owners {
		owners[name] = coverage
	}
	registry, inventory := impl.registry, impl.inventory
	impl.mu.Unlock()
	var reasons []string
	if closed {
		reasons = append(reasons, "service_closed")
	}
	if manifest == nil {
		return append(reasons, "manifest_missing")
	}
	if !manifest.scopeComplete {
		reasons = append(reasons, "scope_incomplete")
	}
	if manifest.scopeParent != parent {
		reasons = append(reasons, "scope_parent_mismatch")
	}
	if registry == nil || inventory == nil {
		return append(reasons, "registry_or_inventory_missing")
	}
	policy := registry.View()
	coverage := inventory.Snapshot()
	if !coverage.Complete {
		reasons = append(reasons, "inventory_incomplete")
	}
	if policy.Generation != manifest.policyRevision {
		reasons = append(reasons, "policy_generation_changed")
	}
	if coverage.Revision != manifest.coverageRevision {
		reasons = append(reasons, "coverage_revision_changed")
	}
	if len(manifest.owners) != 4 {
		reasons = append(reasons, "owner_set_size")
	}
	required := map[string]rb.ResourceClass{
		auth.SensitiveOwnerAuth:     rb.ClassCredential,
		auth.SensitiveOwnerSettings: rb.ClassSettings,
		"tokenlogger-store":         rb.ClassTokenDatabase,
		"provider-source":           rb.ClassProvider,
	}
	for owner, class := range required {
		entry, exists := manifest.owners[owner]
		if !exists {
			reasons = append(reasons, "owner_missing:"+owner)
			continue
		}
		if entry.class != class {
			reasons = append(reasons, "owner_class:"+owner)
		}
		if len(entry.registrations) == 0 {
			reasons = append(reasons, "owner_routes_empty:"+owner)
		}
		state, exists := owners[owner]
		if !exists || state.State != OwnerRegistered {
			reasons = append(reasons, "owner_state:"+owner)
		}
		minimum := 1
		if owner == "tokenlogger-store" {
			minimum = 5
		}
		if len(entry.registrations) < minimum {
			reasons = append(reasons, "owner_identity_count:"+owner)
		}
		for route, identity := range entry.registrations {
			found := false
			for _, candidate := range policy.Entries {
				if candidate.Owner == owner && candidate.Class == class && candidate.Path == route && candidate.Identity == identity && !candidate.Directory && identity.Inode != 0 {
					found = true
					break
				}
			}
			if !found {
				reasons = append(reasons, "owner_registration_mismatch:"+owner)
				break
			}
		}
	}
	tokenDB, ok := manifest.owners["tokenlogger-store"]
	if !ok || !strings.HasSuffix(manifest.tokenDatabasePath, ".db") {
		reasons = append(reasons, "token_database_path_invalid")
	}
	if ok {
		for _, suffix := range []string{"", "-wal", "-shm", "-journal", ".backup"} {
			if _, exists := tokenDB.registrations[manifest.tokenDatabasePath+suffix]; !exists {
				reasons = append(reasons, "token_database_identity_missing")
				break
			}
		}
	}
	return reasons
}

func creationFilesystemEvidence(t *testing.T, directory *os.File) (string, uint64) {
	t.Helper()
	fdInfo, err := os.ReadFile(filepath.Join("/proc/self/fdinfo", fmt.Sprint(directory.Fd())))
	if err != nil {
		t.Fatal("could not inspect retained parent mount ID")
	}
	var mountID uint64
	for _, line := range strings.Split(string(fdInfo), "\n") {
		if _, err := fmt.Sscanf(line, "mnt_id:\t%d", &mountID); err == nil && mountID != 0 {
			break
		}
	}
	if mountID == 0 {
		t.Fatal("retained parent mount ID was unavailable")
	}
	mountInfo, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		t.Fatal("could not inspect mount table")
	}
	for _, line := range strings.Split(string(mountInfo), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 10 && fields[0] == fmt.Sprint(mountID) {
			for i, field := range fields {
				if field == "-" && i+1 < len(fields) {
					return fields[i+1], mountID
				}
			}
		}
	}
	t.Fatal("retained parent mount type was unavailable")
	return "", 0
}
func TestNativeServiceProtectedRoutesAndAliases(t *testing.T) {
	r := nativeReport(t)
	f := newNativeFixture(t, r)
	authPath, _ := f.auth.EffectivePath()
	settingsPath, _ := f.settings.EffectivePath()
	alias := filepath.Join(f.root, "auth-alias")
	if err := os.Link(authPath, alias); err != nil {
		t.Fatal("alias fixture creation failed")
	}
	for i, target := range []string{authPath, settingsPath, alias} {
		id := []string{"auth-route", "settings-route", "auth-alias"}[i]
		result := f.read(t, id, target)
		if result.Outcome != rb.ReadRefused || result.Detail != rb.ErrProtected.Error() || len(result.Bytes) > 0 || len(f.impl.session.Evidence()) != 0 || f.grants.ConsumptionCount(id) != 0 {
			t.Fatal("protected target crossed refusal boundary")
		}
	}
}
func TestNativeServiceRotationJoinsHeldWorker(t *testing.T)     { nativeRotation(t, false) }
func TestNativeServiceAuthRotationJoinsHeldWorker(t *testing.T) { nativeRotation(t, true) }
func nativeRotation(t *testing.T, rotateAuth bool) {
	r := nativeReport(t)
	f := newNativeFixture(t, r)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.impl.session.SetCheckpointHook(func(p rb.Checkpoint) error {
		if p == rb.CheckpointAfterCapabilityHeld {
			once.Do(func() { close(entered) })
			select {
			case <-release:
			case <-time.After(15 * time.Second):
				return context.DeadlineExceeded
			}
		}
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := f.grants.ApproveOnce("rotation-held"); err != nil {
		t.Fatal("fixture approval failed")
	}
	done := make(chan rb.WorkerResult, 1)
	go func() { v, _ := f.impl.session.Read(ctx, f.root, f.target, "rotation-held"); done <- v }()
	released := false
	defer func() {
		if !released {
			close(release)
		}
		cancel()
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("worker never confirmed held capability")
	}
	// Keep an alias of the previously registered settings object.
	settingsPath, _ := f.settings.EffectivePath()
	if rotateAuth {
		settingsPath, _ = f.auth.EffectivePath()
	}
	retiredAlias := filepath.Join(f.root, "retired-settings-alias")
	if err := os.Link(settingsPath, retiredAlias); err != nil {
		t.Fatal("retired-alias fixture failed")
	}
	// Observe the actual service protector before the real store writes bytes.
	beforeBytes, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal("could not inspect synthetic destination before first attempt")
	}
	store := f.settings
	observer := &nativeObservedProtector{service: f.service, tracked: f.tracked, record: r}
	defer func() {
		if err := observer.Close(); err != nil {
			t.Errorf("close retained owner-test descriptors: %v", err)
		}
	}()
	store.Protection = observer
	update := func() error {
		if rotateAuth {
			authStore := f.auth
			authStore.Protection = observer
			return authStore.AddAPIKey("rotation", "openai", "fixture-rotation", "synthetic-rotated-value", nil)
		}
		return store.SaveContext(ctx, settings.Settings{Plugin: settings.DefaultUI})
	}
	firstErr := update()
	r.FirstUpdateCleanupPending = errors.Is(firstErr, rb.ErrCleanupPending)
	afterRefusal, readErr := os.ReadFile(settingsPath)
	r.FirstRefusalPreservedDestination = readErr == nil && bytes.Equal(beforeBytes, afterRefusal)
	close(release)
	released = true
	var result rb.WorkerResult
	select {
	case result = <-done:
	case <-ctx.Done():
		t.Fatal("invalidated read did not join")
	}
	if !r.FirstUpdateCleanupPending || !r.FirstRefusalPreservedDestination {
		r.FailureKind = nativeErrorKind(firstErr)
		t.Fatal("first held-worker update did not produce cleanup-pending refusal with unchanged destination")
	}
	if !f.tracked.allJoined() {
		t.Fatal("worker remained unjoined before fresh owner retry")
	}
	if err = update(); err != nil {
		r.FailureKind = nativeErrorKind(err)
		t.Fatal("fresh owner update failed after confirmed join")
	}
	r.FreshRetrySucceeded = true
	if len(observer.opened) != 2 || os.SameFile(observer.opened[0], observer.opened[1]) {
		t.Fatal("retry did not register a distinct newly opened replacement")
	}
	r.FreshRetryDistinctObject = true
	e := lastNativeEvidence(t, f)
	if !e.CapabilityHeld || !e.WorkerJoined || e.ReadStarted || e.Published || len(result.Bytes) > 0 || len(f.impl.session.PublishedResults()) > 0 {
		t.Fatal("held read was released or stale data published during rotation")
	}
	if !r.EmptyReplacementObserved || !r.WorkerJoinedBeforeOwnerWrite {
		t.Fatal("missing empty-object or join-before-write observation")
	}
	result = f.read(t, "retired-settings", retiredAlias)
	if result.Outcome != rb.ReadRefused || result.Detail != rb.ErrProtected.Error() || f.grants.ConsumptionCount("retired-settings") != 0 {
		t.Fatal("retired settings alias lost protection")
	}
	r.RetiredAliasRefused = true

}
func TestNativeServicePostCaptureRevocation(t *testing.T) {
	r := nativeReport(t)
	f := newNativeFixture(t, r)
	f.impl.session.SetCheckpointHook(func(p rb.Checkpoint) error {
		if p == rb.CheckpointAfterCapture {
			return f.impl.session.Revoke("captured")
		}
		return nil
	})
	result := f.read(t, "captured", f.target)
	e := lastNativeEvidence(t, f)
	if !e.ReadStarted || !e.WorkerJoined || e.Published || len(result.Bytes) > 0 || len(f.impl.session.PublishedResults()) > 0 {
		t.Fatal("captured result escaped revocation publication gate")
	}
}

// This wrapper records real worker state; it never replaces worker behavior.
type nativeTrackedLauncher struct {
	mu                    sync.Mutex
	delegate              rb.WorkerLauncher
	workers               []rb.SandboxWorker
	beforeCreationStage   func() error
	beforeCreationPublish func() error
}

func (l *nativeTrackedLauncher) Start(ctx context.Context) (rb.SandboxWorker, error) {
	w, e := l.delegate.Start(ctx)
	if w != nil {
		l.mu.Lock()
		l.workers = append(l.workers, w)
		l.mu.Unlock()
	}
	return w, e
}
func (l *nativeTrackedLauncher) StartForOperation(ctx context.Context, operation rb.AdmissionOperation) (rb.SandboxWorker, error) {
	launcher, ok := l.delegate.(rb.OperationWorkerLauncher)
	if !ok {
		return nil, rb.ErrUnavailable
	}
	w, e := launcher.StartForOperation(ctx, operation)
	if w != nil {
		l.mu.Lock()
		if operation == rb.AdmissionCreateFile {
			tracked := &nativeTrackedWorker{SandboxWorker: w, beforeStage: l.beforeCreationStage, beforePublish: l.beforeCreationPublish}
			l.workers = append(l.workers, tracked)
			l.mu.Unlock()
			return tracked, e
		}
		l.workers = append(l.workers, w)
		l.mu.Unlock()
	}
	return w, e
}
func (l *nativeTrackedLauncher) CreationProfileAvailable() bool {
	profile, ok := l.delegate.(interface{ CreationProfileAvailable() bool })
	return ok && profile.CreationProfileAvailable()
}

type nativeTrackedWorker struct {
	rb.SandboxWorker
	beforeStage   func() error
	beforePublish func() error
}

func (w *nativeTrackedWorker) WorkerContext() context.Context {
	creation, ok := w.SandboxWorker.(rb.BoundedCreationWorker)
	if !ok {
		return nil
	}
	return creation.WorkerContext()
}

func (w *nativeTrackedWorker) FailureDetail() string {
	if worker, ok := w.SandboxWorker.(interface{ FailureDetail() string }); ok {
		return worker.FailureDetail()
	}
	return "worker_diagnostic_unavailable"
}

func (w *nativeTrackedWorker) StageCreation(ctx context.Context, parent *os.File, identity rb.FileIdentity, stageName, leaf string, content []byte) (rb.CreationStage, error) {
	if w.beforeStage != nil {
		if err := w.beforeStage(); err != nil {
			return rb.CreationStage{}, &rb.CreationOperationError{Result: rb.CreationResult{Outcome: rb.CreationNotCreated}, Cause: err}
		}
	}
	creation, ok := w.SandboxWorker.(rb.BoundedCreationWorker)
	if !ok {
		return rb.CreationStage{}, rb.ErrProtocol
	}
	return creation.StageCreation(ctx, parent, identity, stageName, leaf, content)
}

func (w *nativeTrackedWorker) PublishCreation(ctx context.Context, stage rb.CreationStage) (rb.CreationResult, error) {
	if w.beforePublish != nil {
		if err := w.beforePublish(); err != nil {
			return rb.CreationResult{Outcome: rb.CreationUnknown}, err
		}
	}
	creation, ok := w.SandboxWorker.(rb.BoundedCreationWorker)
	if !ok {
		return rb.CreationResult{Outcome: rb.CreationUnknown}, rb.ErrProtocol
	}
	return creation.PublishCreation(ctx, stage)
}

func (w *nativeTrackedWorker) AbortCreation(ctx context.Context, stage rb.CreationStage) (rb.CreationResult, error) {
	creation, ok := w.SandboxWorker.(rb.BoundedCreationWorker)
	if !ok {
		return rb.CreationResult{Outcome: rb.CreationUnknown}, rb.ErrProtocol
	}
	return creation.AbortCreation(ctx, stage)
}
func (l *nativeTrackedLauncher) Close() error { return l.delegate.Close() }
func (l *nativeTrackedLauncher) allJoined() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.workers) == 0 {
		return false
	}
	for _, w := range l.workers {
		if !w.Joined() {
			return false
		}
	}
	return true
}

func (l *nativeTrackedLauncher) workerCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.workers)
}

func (l *nativeTrackedLauncher) lastFailureDetail() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.workers) == 0 {
		return "worker_missing"
	}
	worker, ok := l.workers[len(l.workers)-1].(interface{ FailureDetail() string })
	if !ok {
		return "worker_diagnostic_unavailable"
	}
	return worker.FailureDetail()
}

type nativeObservedProtector struct {
	opened      []os.FileInfo
	openedFiles []*os.File
	service     *Service
	tracked     *nativeTrackedLauncher
	record      *nativeRecord
}

func (p *nativeObservedProtector) Protect(ctx context.Context, owner, class, route string, object *os.File) error {
	if object == nil {
		return errors.New("rotation test did not supply an opened replacement")
	}
	fd, e := unix.FcntlInt(object.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if e != nil {
		return errors.New("could not retain replacement identity for observation")
	}
	retained := os.NewFile(uintptr(fd), "native-observed-replacement")
	if retained == nil {
		_ = unix.Close(fd)
		return errors.New("could not retain replacement descriptor")
	}
	p.openedFiles = append(p.openedFiles, retained)
	st, e := retained.Stat()
	if e != nil || st.Size() != 0 {
		return errors.New("replacement was not empty before registration")
	}
	p.opened = append(p.opened, st)
	if e = p.service.Protect(ctx, owner, class, route, object); e != nil {
		return e
	}
	st, e = object.Stat()
	if e != nil || st.Size() != 0 {
		return errors.New("replacement acquired bytes before registration returned")
	}
	if !p.tracked.allJoined() {
		return errors.New("actual worker was not joined before owner write")
	}
	p.record.EmptyReplacementObserved = true
	p.record.WorkerJoinedBeforeOwnerWrite = true
	return nil
}

func (p *nativeObservedProtector) Close() error {
	var closeErr error
	for _, file := range p.openedFiles {
		closeErr = errors.Join(closeErr, file.Close())
	}
	p.openedFiles = nil
	return closeErr
}

func TestNativeServiceRootChangeWhileHeld(t *testing.T)    { nativeInterruptedHold(t, "root") }
func TestNativeServiceCancellationWhileHeld(t *testing.T)  { nativeInterruptedHold(t, "cancel") }
func TestNativeServiceShutdownRetryWhileHeld(t *testing.T) { nativeInterruptedHold(t, "shutdown") }
func nativeInterruptedHold(t *testing.T, action string) {
	r := nativeReport(t)
	f := newNativeFixture(t, r)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f.impl.session.SetCheckpointHook(func(p rb.Checkpoint) error {
		if p == rb.CheckpointAfterCapabilityHeld {
			once.Do(func() { close(entered) })
			select {
			case <-release:
			case <-time.After(15 * time.Second):
				return context.DeadlineExceeded
			}
		}
		return nil
	})
	if err := f.grants.ApproveOnce("held-lifecycle"); err != nil {
		t.Fatal("fixture approval failed")
	}
	done := make(chan rb.WorkerResult, 1)
	go func() { v, _ := f.impl.session.Read(ctx, f.root, f.target, "held-lifecycle"); done <- v }()
	released := false
	defer func() {
		if !released {
			close(release)
		}
		cancel()
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("worker did not reach held capability checkpoint")
	}
	var nextRoot string
	switch action {
	case "root":
		nextRoot = t.TempDir()
		rootErr := f.impl.session.SelectRoot(nextRoot)
		r.RootTransitionCleanupPending = errors.Is(rootErr, rb.ErrCleanupPending)
		if !r.RootTransitionCleanupPending {
			r.FailureKind = nativeErrorKind(rootErr)
			t.Fatal("held root transition did not report cleanup pending")
		}
	case "cancel":
		cancel()
	case "shutdown":
		stopped, stop := context.WithCancel(context.Background())
		stop()
		if err := f.service.Close(stopped); err == nil {
			t.Fatal("shutdown unexpectedly claimed cleanup before held operation settled")
		}
	}
	close(release)
	released = true
	var result rb.WorkerResult
	select {
	case result = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("interrupted held read did not settle")
	}
	e := lastNativeEvidence(t, f)
	if !e.CapabilityHeld || !e.WorkerJoined || e.ReadStarted || e.Published || len(result.Bytes) > 0 || len(f.impl.session.PublishedResults()) > 0 {
		t.Fatal("interrupted held worker read or published data, or remained unjoined")
	}
	if action == "root" {
		// The root transition already advanced the generation. Its pending barrier
		// clears only when the old operation actually finishes and the child joins.
		if !f.tracked.allJoined() || f.impl.session.ActiveOperationCount() != 0 {
			t.Fatal("root transition did not settle its old operation")
		}
		f.root = nextRoot
		f.target = filepath.Join(nextRoot, "ordinary-fresh-root.txt")
		if err := os.WriteFile(f.target, []byte("synthetic-native-read-control"), 0600); err != nil {
			t.Fatal("fresh-root fixture write failed")
		}
		result = f.read(t, "fresh-root-read", f.target)
		fresh := lastNativeEvidence(t, f)
		if result.Outcome != rb.ReadSucceeded || !bytes.Equal(result.Bytes, []byte("synthetic-native-read-control")) || !fresh.WorkerJoined || !fresh.Published {
			t.Fatal("fresh invocation after root transition did not succeed")
		}
		r.FreshReadAfterRootChange = true
	}
	if action == "shutdown" {
		cleanup, cancelCleanup := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelCleanup()
		if err := f.service.Close(cleanup); err != nil {
			t.Fatal("fresh shutdown retry failed to drain service")
		}
	}
}

func nativeErrorKind(err error) string {
	if err == nil {
		return "none"
	}
	switch {
	case errors.Is(err, rb.ErrCleanupPending):
		return "cleanup_pending"
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, rb.ErrInventoryIncomplete):
		return "inventory_incomplete"
	default:
		return "unexpected_error"
	}
}
