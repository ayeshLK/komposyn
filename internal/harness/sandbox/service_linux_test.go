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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	"github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
	"github.com/ayeshLK/komposyn/internal/harness/runtime"
	"github.com/ayeshLK/komposyn/internal/harness/settings"
	provider "github.com/ayeshLK/komposyn/plugins/model-provider"
	"github.com/ayeshLK/komposyn/plugins/tools"
	"golang.org/x/sys/unix"
)

func TestHostCompositionRegistersIndependentAuthAndSettingsRoutes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	defaultAuth, err := auth.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	defaultSettings, err := settings.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name         string
		authPath     string
		settingsPath string
	}{
		{
			name:     "auth-only override leaves default settings independent",
			authPath: filepath.Join(t.TempDir(), "auth", "auth.json"),
		},
		{
			name:         "settings-only override leaves default auth independent",
			settingsPath: filepath.Join(t.TempDir(), "settings", "settings.json"),
		},
		{
			name:         "both overrides may use unrelated directories",
			authPath:     filepath.Join(t.TempDir(), "auth-side", "auth.json"),
			settingsPath: filepath.Join(t.TempDir(), "settings-side", "settings.json"),
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service, err := newTestHostService()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := service.Close(context.Background()); err != nil {
					t.Errorf("close service: %v", err)
				}
			})

			authStore := auth.Store{Path: test.authPath, Protection: service}
			settingsStore := settings.Store{Path: test.settingsPath, Protection: service}
			wantAuth, wantSettings := test.authPath, test.settingsPath
			if wantAuth == "" {
				wantAuth = defaultAuth
			}
			if wantSettings == "" {
				wantSettings = defaultSettings
			}
			gotAuth, err := authStore.EffectivePath()
			if err != nil || gotAuth != wantAuth {
				t.Fatalf("auth effective path=%q want=%q err=%v", gotAuth, wantAuth, err)
			}
			gotSettings, err := settingsStore.EffectivePath()
			if err != nil || gotSettings != wantSettings {
				t.Fatalf("settings effective path=%q want=%q err=%v", gotSettings, wantSettings, err)
			}
			if err := authStore.RegisterProtection(context.Background()); err != nil {
				t.Fatalf("register auth path: %v", err)
			}
			if err := settingsStore.RegisterProtection(context.Background()); err != nil {
				t.Fatalf("register settings path: %v", err)
			}
			if err := settingsStore.SaveContext(context.Background(), settings.Settings{Plugin: settings.DefaultUI}); err != nil {
				t.Fatalf("write protected settings replacement: %v", err)
			}
			if err := authStore.AddAPIKey("fixture", "openai", "synthetic", "synthetic-test-value", nil); err != nil {
				t.Fatalf("write protected auth replacement: %v", err)
			}

			snapshot := service.CoverageSnapshot()
			if !snapshot.UnknownRootDataGate {
				t.Fatal("generic-read coverage gate opened")
			}
			for _, owner := range snapshot.Owners {
				switch owner.Owner {
				case auth.SensitiveOwnerAuth, auth.SensitiveOwnerSettings:
					if owner.State != OwnerRegistered || owner.Routes == 0 || owner.Identities == 0 {
						t.Errorf("owner protection incomplete: %+v", owner)
					}
				case "tokenlogger-store":
					if owner.State != OwnerAbsent || owner.Reason == "" {
						t.Errorf("Token Logger absence is not source-scoped: %+v", owner)
					}
				case "provider-source", "unknown-root-data":
					if owner.State != OwnerUnsupported {
						t.Errorf("unknown owner was treated as covered: %+v", owner)
					}
				}
			}
			if err := service.PrepareGenericRead(context.Background(), filepath.Dir(gotAuth), gotAuth); !errors.Is(err, ErrUnknownRootData) {
				t.Fatalf("generic read error=%v; want unknown-root refusal", err)
			}
		})
	}
}

func TestHostCompositionCannotActivateDeferredOwnerOrBypassUnknownGate(t *testing.T) {
	service, err := newTestHostService()
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(context.Background())
	if err := service.Protect(context.Background(), "tokenlogger-store", "token_database", filepath.Join(t.TempDir(), "events.db"), nil); err == nil {
		t.Fatal("unwired Token Logger owner was accepted")
	}
	root := t.TempDir()
	if err := service.PrepareGenericRead(context.Background(), root, filepath.Join(root, "fixture.txt")); !errors.Is(err, ErrUnknownRootData) {
		t.Fatalf("caller-selected path changed unknown-data policy: %v", err)
	}
	if !service.Prepares(tools.ID("read_file")) {
		t.Fatal("reserved read_file ID is not guarded by the pre-consumption preparer")
	}
	if _, ok := runtime.BundledConversationToolRuntimeConfig(nil, nil).Registry.Lookup("read_file"); ok {
		t.Fatal("read_file was registered in the shipped conversation tool bundle")
	}
	prepared, err := service.PrepareTool(context.Background(), nil, tools.Invocation{ToolID: "read_file"}, permissions.Request{}, 0)
	if prepared != nil || !errors.Is(err, ErrUnknownRootData) {
		t.Fatalf("host preparation=%v, err=%v; want unknown-root refusal without a capability", prepared, err)
	}
	backendService, err := NewHostService(filepath.Join(t.TempDir(), "no-bubblewrap"))
	if err != nil {
		t.Fatal(err)
	}
	defer backendService.Close(context.Background())
	if available, _ := backendService.BackendStatus(); available {
		t.Fatal("untrusted/missing Bubblewrap path was treated as an available backend")
	}
	if impl := backendService.impl.(*linuxImplementation); impl.DirectoryListingAvailable() {
		t.Fatal("directory listing remained available without a trusted backend")
	}
}

func TestDirectoryListingAvailabilityTracksTrustedServiceLifecycle(t *testing.T) {
	service, err := newTestHostServiceWithLauncher(profileTestWorkerLauncher{mode: readbrokerstudy.BindModeDescriptor})
	if err != nil {
		t.Fatal(err)
	}
	impl := service.impl.(*linuxImplementation)
	if !impl.DirectoryListingAvailable() {
		t.Fatal("composed trusted worker service did not enable directory listing")
	}
	if service.SearchFilesAvailable() {
		t.Fatal("search inherited availability without search-specific worker evidence")
	}
	wantOptions := goruntime.GOARCH == "amd64"
	if got := service.DirectoryListingOptionsAvailable(); got != wantOptions {
		t.Fatalf("public service profile availability=%t, want %t for %s", got, wantOptions, goruntime.GOARCH)
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if impl.DirectoryListingAvailable() {
		t.Fatal("closed worker service kept directory listing available")
	}
	if service.DirectoryListingOptionsAvailable() {
		t.Fatal("closed worker service kept advanced listing options available")
	}
	if service.SearchFilesAvailable() {
		t.Fatal("closed worker service kept search available")
	}
}

func TestRuntimeFilesystemToolRegistrationRequiresEligibleHostProfile(t *testing.T) {
	approval := runtime.ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		return permissions.ApprovalOnce, "", nil
	})
	clarification := runtime.ToolClarificationFunc(func(context.Context, runtime.ClarificationRequest) (runtime.ClarificationResponse, error) {
		return runtime.ClarificationResponse{}, nil
	})
	missingBackend, err := NewHostService(filepath.Join(t.TempDir(), "no-bubblewrap"))
	if err != nil {
		t.Fatal(err)
	}
	defer missingBackend.Close(context.Background())
	config := runtime.BundledConversationToolRuntimeConfigWithFilesystemRead(approval, clarification, missingBackend)
	if _, registered := config.Registry.Lookup(tools.ListFilesID); registered {
		t.Fatal("list_files registered without a verified backend")
	}
	if _, registered := config.Registry.Lookup(tools.SearchFilesID); registered {
		t.Fatal("search_files registered without a verified backend")
	}

	availableBackend, err := newTestHostServiceWithLauncher(profileTestWorkerLauncher{mode: readbrokerstudy.BindModeDescriptor})
	if err != nil {
		t.Fatal(err)
	}
	defer availableBackend.Close(context.Background())
	wantOptions := goruntime.GOARCH == "amd64"
	if got := availableBackend.DirectoryListingOptionsAvailable(); got != wantOptions {
		t.Fatalf("public service profile availability=%t, want %t for %s", got, wantOptions, goruntime.GOARCH)
	}
	config = runtime.BundledConversationToolRuntimeConfigWithFilesystemRead(approval, clarification, availableBackend)
	if _, registered := config.Registry.Lookup(tools.SearchFilesID); registered {
		t.Fatal("search_files inherited the listing evidence gate")
	}
	searchRoot := t.TempDir()
	if err := availableBackend.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, filepath.Join(t.TempDir(), "auth.json"), nil); err != nil {
		t.Fatal(err)
	}
	if err := availableBackend.Protect(context.Background(), auth.SensitiveOwnerSettings, auth.SensitiveClassSettings, filepath.Join(t.TempDir(), "settings.json"), nil); err != nil {
		t.Fatal(err)
	}
	if err := availableBackend.InvalidateFilesystemRoot(context.Background(), runtime.FilesystemRootTransition{ConversationID: "conversation", Root: searchRoot, Generation: 1}); err != nil {
		t.Fatal(err)
	}
	arguments, err := json.Marshal(tools.SearchFilesInput{Path: searchRoot, Query: "needle", Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := availableBackend.AdmitFilesystemRead(context.Background(), tools.Invocation{ConversationID: "conversation", TurnID: "turn", ProviderCallID: "call", ToolID: tools.SearchFilesID, Arguments: arguments}, "openai/model/profile"); !errors.Is(err, readbrokerstudy.ErrUnavailable) {
		t.Fatalf("search arguments on a listing-only profile error=%v, want unavailable", err)
	}
	definition, registered := config.Registry.Lookup(tools.ListFilesID)
	if !registered {
		t.Fatal("list_files not registered when the Linux host service reports an available backend")
	}
	if got := strings.Contains(string(definition.Definition().InputSchema), "recursive"); got != wantOptions {
		t.Fatalf("runtime schema advanced fields=%t, want %t for %s", got, wantOptions, goruntime.GOARCH)
	}
	searchBackend, err := newTestHostServiceWithLauncher(searchProfileTestWorkerLauncher{profileTestWorkerLauncher{mode: readbrokerstudy.BindModeDescriptor}})
	if err != nil {
		t.Fatal(err)
	}
	defer searchBackend.Close(context.Background())
	config = runtime.BundledConversationToolRuntimeConfigWithFilesystemRead(approval, clarification, searchBackend)
	searchDefinition, registered := config.Registry.Lookup(tools.SearchFilesID)
	if registered != searchBackend.SearchFilesAvailable() {
		t.Fatalf("search_files registration=%t, profile availability=%t", registered, searchBackend.SearchFilesAvailable())
	}
	if !registered || !strings.Contains(strings.ToLower(searchDefinition.Definition().Description), "ext4") {
		t.Fatalf("eligible runtime did not advertise the ext4 search requirement: registered=%t definition=%+v", registered, searchDefinition)
	}
}

func TestHostAdmissionRetainsOneObjectThroughPreparationWithoutWorker(t *testing.T) {
	service, err := newTestHostService()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := service.Close(context.Background()); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	if err := service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, filepath.Join(t.TempDir(), "auth.json"), nil); err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerSettings, auth.SensitiveClassSettings, filepath.Join(t.TempDir(), "settings.json"), nil); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	target := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(target, []byte("synthetic admitted contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := service.InvalidateFilesystemRoot(context.Background(), runtime.FilesystemRootTransition{ConversationID: "conversation-fixture", Root: root, Generation: 1, Reason: runtime.FilesystemRootSelected}); err != nil {
		t.Fatal(err)
	}
	invocation := tools.Invocation{ConversationID: "conversation-fixture", TurnID: "turn-fixture", ProviderCallID: "call-fixture", ToolID: tools.ID("read_file"), Arguments: []byte(`{"path":"` + target + `"}`)}
	destination := "openai/gpt-test/profile-revision"
	admission, err := service.AdmitFilesystemRead(context.Background(), invocation, destination)
	if err != nil {
		t.Fatalf("admit synthetic regular file: %v", err)
	}
	if admission.Class != string(readbrokerstudy.ClassOrdinary) || admission.Identity == "" || admission.AdmissionBinding == "" || admission.Reference == nil || admission.RootGeneration != 1 {
		t.Fatalf("detached admission metadata is incomplete: %+v", admission)
	}
	originalIdentity := admission.Identity
	moved := filepath.Join(root, "moved-original.txt")
	if err := os.Rename(target, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("synthetic replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	preparation, err := service.PrepareAdmittedFilesystemRead(context.Background(), admission, 1)
	if err != nil {
		t.Fatalf("prepare same admitted object: %v", err)
	}
	prepared := preparation.Metadata()
	if prepared.Identity != originalIdentity || prepared.Target != target || prepared.AdmissionBinding != admission.AdmissionBinding {
		t.Fatalf("preparation reopened or rebound the target: %+v", prepared)
	}
	committed := false
	if err := preparation.WithAuthorityCommit(context.Background(), func() error { committed = true; return nil }); err != nil {
		t.Fatalf("current preparation commit gate: %v", err)
	}
	if !committed {
		t.Fatal("host authority commit callback was not invoked")
	}
	if err := preparation.Close(context.Background()); err != nil {
		t.Fatalf("close preparation: %v", err)
	}
	impl := service.impl.(*linuxImplementation)
	if impl.session.ActiveOperationCount() != 0 {
		t.Fatal("host-only admission appeared as a worker operation")
	}
}

func TestHostAdmissionAcceptsBoundedDirectoryListing(t *testing.T) {
	service, err := newTestHostService()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(context.Background()); err != nil {
			t.Errorf("close service: %v", err)
		}
	})
	root := t.TempDir()
	listingPath := filepath.Join(root, "listing-scope")
	if err := os.Mkdir(listingPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, filepath.Join(t.TempDir(), "auth.json"), nil); err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerSettings, auth.SensitiveClassSettings, filepath.Join(t.TempDir(), "settings.json"), nil); err != nil {
		t.Fatal(err)
	}
	if err := service.InvalidateFilesystemRoot(context.Background(), runtime.FilesystemRootTransition{ConversationID: "listing-fixture", Root: root, Generation: 1}); err != nil {
		t.Fatal(err)
	}
	arguments, err := json.Marshal(map[string]string{"path": listingPath})
	if err != nil {
		t.Fatal(err)
	}
	invocation := tools.Invocation{ConversationID: "listing-fixture", TurnID: "turn", ProviderCallID: "listing-call", ToolID: tools.ListFilesID, Arguments: arguments}
	admission, err := service.AdmitFilesystemRead(context.Background(), invocation, "openai/gpt-test/profile")
	if err != nil {
		t.Fatalf("admit synthetic directory listing through public Service: %v", err)
	}
	if admission.Operation != string(tools.ListFilesID) || admission.Target != listingPath || admission.Reference == nil || admission.AdmissionBinding == "" {
		t.Fatalf("detached listing admission metadata is incomplete: %+v", admission)
	}
	if err := service.CloseFilesystemReadAdmission(context.Background(), admission); err != nil {
		t.Fatalf("retire listing admission: %v", err)
	}
}

func TestCreationBarrierClosesAndReopensReadAdmissionAroundDrain(t *testing.T) {
	service, err := newTestHostService()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(context.Background()); err != nil {
			t.Errorf("close service: %v", err)
		}
	})
	if err := service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, filepath.Join(t.TempDir(), "auth.json"), nil); err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerSettings, auth.SensitiveClassSettings, filepath.Join(t.TempDir(), "settings.json"), nil); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	target := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(target, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := service.InvalidateFilesystemRoot(context.Background(), runtime.FilesystemRootTransition{ConversationID: "barrier-fixture", Root: root, Generation: 1, Reason: runtime.FilesystemRootSelected}); err != nil {
		t.Fatal(err)
	}
	invocation := tools.Invocation{
		ConversationID: "barrier-fixture", TurnID: "turn", ProviderCallID: "call",
		ToolID: tools.ReadFileID, Arguments: []byte(`{"path":"` + target + `"}`),
	}
	admission, err := service.AdmitFilesystemRead(context.Background(), invocation, "openai/gpt-test/profile")
	if err != nil {
		t.Fatalf("initial read admission: %v", err)
	}
	preparation, err := service.PrepareAdmittedFilesystemRead(context.Background(), admission, 1)
	if err != nil {
		t.Fatalf("prepare before barrier: %v", err)
	}

	impl := service.impl.(*linuxImplementation)
	lease, err := impl.beginCreationBarrier(context.Background())
	if err != nil {
		t.Fatalf("begin barrier and drain: %v", err)
	}
	if lease.state != creationBarrierActive {
		t.Fatalf("barrier state=%d, want active", lease.state)
	}
	if _, err := service.AdmitFilesystemRead(context.Background(), invocation, "openai/gpt-test/profile"); !errors.Is(err, ErrCreationBarrier) {
		t.Fatalf("new read admission passed the closed barrier: %v", err)
	}
	if _, err := service.PrepareAdmittedFilesystemRead(context.Background(), admission, 1); !errors.Is(err, readbrokerstudy.ErrAdmissionStale) {
		t.Fatalf("pre-barrier admission survived the drain: %v", err)
	}
	commitCalled := false
	if err := preparation.WithAuthorityCommit(context.Background(), func() error { commitCalled = true; return nil }); !errors.Is(err, readbrokerstudy.ErrAdmissionStale) || commitCalled {
		t.Fatalf("pre-barrier prepared read retained its publication authority: called=%t err=%v", commitCalled, err)
	}
	if err := preparation.Close(context.Background()); err != nil {
		t.Fatalf("close invalidated preparation: %v", err)
	}
	if err := impl.finishCreationBarrier(lease, false); !errors.Is(err, ErrCreationBarrier) {
		t.Fatalf("incomplete cleanup released the barrier: %v", err)
	}
	if _, err := service.AdmitFilesystemRead(context.Background(), invocation, "openai/gpt-test/profile"); !errors.Is(err, ErrCreationBarrier) {
		t.Fatalf("read admission reopened after incomplete cleanup: %v", err)
	}
	if err := impl.finishCreationBarrier(lease, true); err != nil {
		t.Fatalf("confirmed retirement: %v", err)
	}
	if _, err := service.AdmitFilesystemRead(context.Background(), invocation, "openai/gpt-test/profile"); err != nil {
		t.Fatalf("read admission did not reopen after confirmed retirement: %v", err)
	}
	if err := service.CloseFilesystemReadAdmission(context.Background(), admission); err != nil {
		t.Fatalf("retire stale admission: %v", err)
	}
}

func TestCreationBarrierWaitsForActiveReadWorkerToJoin(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "active-read.txt")
	if err := os.WriteFile(target, []byte("must not overlap creation staging"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := &stage4WorkerLauncher{}
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	inventory := readbrokerstudy.NewChatCompositionInventory()
	grants := readbrokerstudy.NewOnceGrantFixture()
	session, err := readbrokerstudy.NewHostCoordinator(registry, readbrokerstudy.NewBroker(registry), launcher, grants, inventory)
	if err != nil {
		t.Fatal(err)
	}
	for _, requirement := range readbrokerstudy.ChatCompositionRequirements() {
		if err := session.DeclareCoverageOwner(requirement); err != nil {
			t.Fatal(err)
		}
	}
	impl := &linuxImplementation{registry: registry, launcher: launcher, session: session, inventory: inventory, revision: 1,
		owners: map[string]OwnerCoverage{
			auth.SensitiveOwnerAuth:     {Owner: auth.SensitiveOwnerAuth, Class: auth.SensitiveClassAuth, State: OwnerRequired},
			auth.SensitiveOwnerSettings: {Owner: auth.SensitiveOwnerSettings, Class: auth.SensitiveClassSettings, State: OwnerRequired},
		}, blocked: make(map[string]string)}
	service := &Service{impl: impl}
	for owner, class := range map[string]string{auth.SensitiveOwnerAuth: auth.SensitiveClassAuth, auth.SensitiveOwnerSettings: auth.SensitiveClassSettings} {
		path := filepath.Join(t.TempDir(), owner+".json")
		if err := service.Protect(context.Background(), owner, class, path, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.InvalidateFilesystemRoot(context.Background(), runtime.FilesystemRootTransition{ConversationID: "active-read-barrier", Root: root, Generation: 1}); err != nil {
		t.Fatal(err)
	}
	if err := grants.ApproveOnce("active-read"); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	session.SetCheckpointHook(func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint == readbrokerstudy.CheckpointAfterCapabilityHeld {
			close(entered)
			<-release
			return context.Canceled
		}
		return nil
	})
	readDone := make(chan struct {
		result readbrokerstudy.WorkerResult
		err    error
	}, 1)
	go func() {
		result, readErr := session.Read(context.Background(), root, target, "active-read")
		readDone <- struct {
			result readbrokerstudy.WorkerResult
			err    error
		}{result, readErr}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("read worker did not reach held-capability checkpoint")
	}
	generationBeforeDrain := session.GenerationSnapshot()
	barrierDone := make(chan struct {
		lease *creationBarrierLease
		err   error
	}, 1)
	go func() {
		lease, beginErr := impl.beginCreationBarrier(context.Background())
		barrierDone <- struct {
			lease *creationBarrierLease
			err   error
		}{lease, beginErr}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		impl.mu.Lock()
		closed := impl.creationBarrier != nil
		impl.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("creation did not close admissions before drain")
		}
		time.Sleep(time.Millisecond)
	}
	arguments, err := json.Marshal(map[string]string{"path": target})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AdmitFilesystemRead(context.Background(), tools.Invocation{ConversationID: "active-read-barrier", TurnID: "new", ProviderCallID: "new-read", ToolID: tools.ReadFileID, Arguments: arguments}, "openai/model/profile"); !errors.Is(err, ErrCreationBarrier) {
		close(release)
		t.Fatalf("new admission passed before active-worker drain: %v", err)
	}
	select {
	case <-barrierDone:
		t.Fatal("barrier completed before active read worker joined")
	default:
	}
	close(release)
	var barrier struct {
		lease *creationBarrierLease
		err   error
	}
	select {
	case barrier = <-barrierDone:
	case <-time.After(3 * time.Second):
		t.Fatal("barrier did not finish after active worker settled")
	}
	if barrier.err != nil || barrier.lease == nil || barrier.lease.state != creationBarrierActive {
		t.Fatalf("barrier drain result lease=%v err=%v", barrier.lease, barrier.err)
	}
	var read struct {
		result readbrokerstudy.WorkerResult
		err    error
	}
	select {
	case read = <-readDone:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled active read did not settle")
	}
	if read.result.Outcome != readbrokerstudy.ReadCancelled || len(read.result.Bytes) != 0 || launcher.starts.Load() != 1 || session.ActiveOperationCount() != 0 {
		t.Fatalf("active read crossed barrier or failed to join: result=%+v err=%v starts=%d active=%d", read.result, read.err, launcher.starts.Load(), session.ActiveOperationCount())
	}
	if got := session.GenerationSnapshot(); got != generationBeforeDrain {
		t.Fatalf("transient creation drain changed read authorization generations: before=%+v after=%+v", generationBeforeDrain, got)
	}
	if err := impl.finishCreationBarrier(barrier.lease, true); err != nil {
		t.Fatalf("retire barrier after confirmed worker join: %v", err)
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatalf("close service after joined read: %v", err)
	}
}

func TestCreationBarrierOrdersOwnerPendingAgainstPublicationRelease(t *testing.T) {
	for _, test := range []struct {
		name             string
		publicationFirst bool
	}{
		{name: "owner pending wins before publication release"},
		{name: "publication release wins and owner waits", publicationFirst: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, err := newTestHostService()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := service.Close(context.Background()); err != nil {
					t.Errorf("close service: %v", err)
				}
			})
			if err := service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, filepath.Join(t.TempDir(), "auth.json"), nil); err != nil {
				t.Fatal(err)
			}
			if err := service.Protect(context.Background(), auth.SensitiveOwnerSettings, auth.SensitiveClassSettings, filepath.Join(t.TempDir(), "settings.json"), nil); err != nil {
				t.Fatal(err)
			}
			impl := service.impl.(*linuxImplementation)
			lease, err := impl.beginCreationBarrier(context.Background())
			if err != nil {
				t.Fatalf("begin barrier: %v", err)
			}
			if test.publicationFirst {
				if err := impl.admitCreationStage(lease); err != nil {
					t.Fatalf("admit stage: %v", err)
				}
				if err := impl.releaseCreationPublication(lease); err != nil {
					t.Fatalf("release publication: %v", err)
				}
				if err := impl.releaseCreationPublication(lease); !errors.Is(err, ErrCoverageMissing) {
					t.Fatalf("publication permit was reusable: %v", err)
				}
			}

			rotationDone := make(chan error, 1)
			go func() {
				rotationDone <- service.Protect(context.Background(), auth.SensitiveOwnerSettings, auth.SensitiveClassSettings, filepath.Join(t.TempDir(), "settings-rotated.json"), nil)
			}()
			deadline := time.Now().Add(2 * time.Second)
			for {
				impl.mu.Lock()
				pending := impl.blocked[auth.SensitiveOwnerSettings] != ""
				impl.mu.Unlock()
				if pending {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("owner transition did not enter pending state")
				}
				time.Sleep(time.Millisecond)
			}
			if lease.ctx.Err() == nil {
				t.Fatal("owner transition did not cancel the active effect context")
			}
			if !test.publicationFirst {
				if err := impl.admitCreationStage(lease); !errors.Is(err, ErrCoverageMissing) {
					t.Fatalf("stage permit passed after owner pending won: %v", err)
				}
				if err := impl.releaseCreationPublication(lease); !errors.Is(err, ErrCoverageMissing) {
					t.Fatalf("publication passed after owner pending won: %v", err)
				}
			}
			select {
			case err := <-rotationDone:
				t.Fatalf("owner transition completed before effect retirement: %v", err)
			default:
			}
			if err := impl.finishCreationBarrier(lease, true); err != nil {
				t.Fatalf("finish cancelled effect: %v", err)
			}
			select {
			case err := <-rotationDone:
				if err != nil {
					t.Fatalf("owner transition after effect retirement: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("owner transition did not resume after effect retirement")
			}
		})
	}
}

func TestOwnerTransitionAfterPublicationReleaseWaitsForCreationClassification(t *testing.T) {
	root := t.TempDir()
	launcher := &filesystemCreationFixtureLauncher{publishEntered: make(chan struct{}), releasePublish: make(chan struct{})}
	service, err := newTestHostServiceWithLauncher(launcher)
	if err != nil {
		t.Fatal(err)
	}
	if err := registerCreationFixtureOwners(t, service, root, "owner-release-race"); err != nil {
		t.Fatal(err)
	}
	impl := service.impl.(*linuxImplementation)
	parent, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	parentIdentity := identityFromInfo(t, root)
	stageName, err := readbrokerstudy.NewCreationStageName()
	if err != nil {
		_ = parent.Close()
		t.Fatal(err)
	}
	creationDone := make(chan struct {
		result readbrokerstudy.CreationResult
		err    error
	}, 1)
	go func() {
		result, createErr := impl.executeBoundedCreation(context.Background(), parent, parentIdentity, stageName, "release-winner.txt", []byte("publication release won"))
		creationDone <- struct {
			result readbrokerstudy.CreationResult
			err    error
		}{result, createErr}
	}()
	select {
	case <-launcher.publishEntered:
	case <-time.After(3 * time.Second):
		close(launcher.releasePublish)
		_ = parent.Close()
		t.Fatal("create worker did not pause after publication release")
	}
	impl.mu.Lock()
	lease := impl.creationBarrier
	released := lease != nil && lease.publicationReleased
	impl.mu.Unlock()
	if !released {
		close(launcher.releasePublish)
		t.Fatal("publication release did not precede the worker effect window")
	}
	aliasRoot := t.TempDir()
	aliasPath := filepath.Join(aliasRoot, "parent-alias")
	if err := os.Symlink(root, aliasPath); err != nil {
		close(launcher.releasePublish)
		t.Fatal("create synthetic parent alias", err)
	}
	aliasParent, err := os.Open(aliasPath)
	if err != nil {
		close(launcher.releasePublish)
		t.Fatal("open synthetic parent alias", err)
	}
	aliasStage, err := readbrokerstudy.NewCreationStageName()
	if err != nil {
		_ = aliasParent.Close()
		close(launcher.releasePublish)
		t.Fatal(err)
	}
	duplicate, duplicateErr := impl.executeBoundedCreation(context.Background(), aliasParent, parentIdentity, aliasStage, "release-winner.txt", []byte("duplicate through alias"))
	_ = aliasParent.Close()
	if !errors.Is(duplicateErr, ErrCreationBarrier) || duplicate.Outcome != readbrokerstudy.CreationNotCreated || len(launcher.workers) != 1 {
		close(launcher.releasePublish)
		t.Fatalf("concurrent aliased proposal did not fail closed: result=%+v err=%v workers=%d", duplicate, duplicateErr, len(launcher.workers))
	}
	ownerDone := make(chan error, 1)
	go func() {
		ownerDone <- service.Protect(context.Background(), auth.SensitiveOwnerSettings, auth.SensitiveClassSettings, filepath.Join(t.TempDir(), "rotated-settings.json"), nil)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		impl.mu.Lock()
		pending := impl.blocked[auth.SensitiveOwnerSettings] != ""
		impl.mu.Unlock()
		if pending {
			break
		}
		if time.Now().After(deadline) {
			close(launcher.releasePublish)
			t.Fatal("owner transition did not win the pending-state coordinator")
		}
		time.Sleep(time.Millisecond)
	}
	if lease.ctx.Err() == nil {
		close(launcher.releasePublish)
		t.Fatal("owner transition did not cancel the in-flight creation context")
	}
	select {
	case err := <-ownerDone:
		close(launcher.releasePublish)
		t.Fatalf("owner transition completed before the released effect was classified: %v", err)
	default:
	}
	close(launcher.releasePublish)
	var completed struct {
		result readbrokerstudy.CreationResult
		err    error
	}
	select {
	case completed = <-creationDone:
	case <-time.After(3 * time.Second):
		t.Fatal("released creation did not settle after actor release")
	}
	if completed.err != nil || completed.result.Outcome != readbrokerstudy.CreationCreated {
		t.Fatalf("release-winner effect was not truthfully classified: result=%+v err=%v", completed.result, completed.err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "release-winner.txt")); err != nil || string(got) != "publication release won" {
		t.Fatalf("released publication result bytes=%q err=%v", got, err)
	}
	select {
	case err := <-ownerDone:
		if err != nil {
			t.Fatalf("owner transition after effect classification: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owner transition did not resume after creation retirement")
	}
	if err := parent.Close(); err != nil {
		t.Errorf("close retained parent: %v", err)
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatalf("close service after owner/effect race: %v", err)
	}
}

func TestServiceCloseWaitsForCreationBarrierRetirement(t *testing.T) {
	service, err := newTestHostService()
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, filepath.Join(t.TempDir(), "auth.json"), nil); err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerSettings, auth.SensitiveClassSettings, filepath.Join(t.TempDir(), "settings.json"), nil); err != nil {
		t.Fatal(err)
	}
	impl := service.impl.(*linuxImplementation)
	lease, err := impl.beginCreationBarrier(context.Background())
	if err != nil {
		t.Fatalf("begin barrier: %v", err)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := service.Close(closeCtx); !errors.Is(err, readbrokerstudy.ErrCleanupPending) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close did not report incomplete barrier cleanup: %v", err)
	}
	if lease.ctx.Err() == nil {
		t.Fatal("service close did not cancel the effect context")
	}
	if err := impl.finishCreationBarrier(lease, true); err != nil {
		t.Fatalf("retire barrier after effect cleanup: %v", err)
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatalf("retry service close after cleanup: %v", err)
	}
}

func TestServiceCreationPathHoldsBarrierThroughWorkerRetirement(t *testing.T) {
	launcher := &testCreationWorkerLauncher{}
	service, err := newTestHostServiceWithLauncher(launcher)
	if err != nil {
		t.Fatal(err)
	}
	launcher.service = service
	t.Cleanup(func() {
		if err := service.Close(context.Background()); err != nil {
			t.Errorf("close service: %v", err)
		}
	})
	root := t.TempDir()
	authPath := filepath.Join(root, ".fixture-auth.json")
	settingsPath := filepath.Join(root, ".fixture-settings.json")
	for _, path := range []string{authPath, settingsPath} {
		if err := os.WriteFile(path, []byte("synthetic protected owner"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	authFile, err := os.Open(authPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, authPath, authFile); err != nil {
		_ = authFile.Close()
		t.Fatal(err)
	}
	if err := authFile.Close(); err != nil {
		t.Fatal(err)
	}
	settingsFile, err := os.Open(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerSettings, auth.SensitiveClassSettings, settingsPath, settingsFile); err != nil {
		_ = settingsFile.Close()
		t.Fatal(err)
	}
	if err := settingsFile.Close(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(target, []byte("ordinary fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := service.InvalidateFilesystemRoot(context.Background(), runtime.FilesystemRootTransition{ConversationID: "creation-service-fixture", Root: root, Generation: 1, Reason: runtime.FilesystemRootSelected}); err != nil {
		t.Fatal(err)
	}
	installSyntheticCreationManifest(t, service, root)
	launcher.invocation = tools.Invocation{ConversationID: "creation-service-fixture", TurnID: "turn", ProviderCallID: "call", ToolID: tools.ReadFileID, Arguments: []byte(`{"path":"` + target + `"}`)}
	parent, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	parentInfo, err := parent.Stat()
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := parentInfo.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("parent identity unavailable")
	}
	identity := readbrokerstudy.FileIdentity{Device: uint64(stat.Dev), Inode: stat.Ino, Type: uint32(stat.Mode & syscall.S_IFMT)}
	stageName, err := readbrokerstudy.NewCreationStageName()
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.impl.(*linuxImplementation).executeBoundedCreation(context.Background(), parent, identity, stageName, "new.txt", []byte("approved fixture"))
	if err != nil || result.Outcome != readbrokerstudy.CreationCreated {
		t.Fatalf("creation result=%+v err=%v", result, err)
	}
	if !launcher.readsBlockedDuringStage || !launcher.readsBlockedDuringPublish || !launcher.workerJoined {
		t.Fatalf("barrier/worker lifecycle: stage_blocked=%t publish_blocked=%t joined=%t events=%v", launcher.readsBlockedDuringStage, launcher.readsBlockedDuringPublish, launcher.workerJoined, launcher.events)
	}
	if got := strings.Join(launcher.events, ","); got != "launch,ready,stage,publish,wait" {
		t.Fatalf("creation worker order=%q", got)
	}
	admission, err := service.AdmitFilesystemRead(context.Background(), launcher.invocation, "openai/gpt-test/profile")
	if err != nil {
		t.Fatalf("read barrier did not reopen after worker retirement: %v", err)
	}
	if err := service.CloseFilesystemReadAdmission(context.Background(), admission); err != nil {
		t.Fatalf("close post-creation read admission: %v", err)
	}
	beforeConflict := len(launcher.events)
	conflictName, err := readbrokerstudy.NewCreationStageName()
	if err != nil {
		t.Fatal(err)
	}
	conflict, err := service.impl.(*linuxImplementation).executeBoundedCreation(context.Background(), parent, identity, conflictName, "existing.txt", []byte("must not replace"))
	if err != nil || conflict.Outcome != readbrokerstudy.CreationConflict {
		t.Fatalf("existing target outcome=%+v err=%v", conflict, err)
	}
	if len(launcher.events) != beforeConflict {
		t.Fatalf("existing-target conflict launched worker: events=%v", launcher.events[beforeConflict:])
	}
	incumbent, err := os.ReadFile(target)
	if err != nil || string(incumbent) != "ordinary fixture" {
		t.Fatalf("existing destination changed: bytes=%q err=%v", incumbent, err)
	}
}

func TestCreationCleanupFailureRetainsBarrierAndDoesNotRetry(t *testing.T) {
	root := t.TempDir()
	launcher := &incompleteCleanupCreationLauncher{stageStarted: make(chan struct{}), continueStage: make(chan struct{})}
	service, err := newTestHostServiceWithLauncher(launcher)
	if err != nil {
		t.Fatal(err)
	}
	if err := registerCreationFixtureOwners(t, service, root, "creation-cleanup-failure"); err != nil {
		t.Fatal(err)
	}
	impl := service.impl.(*linuxImplementation)
	parent, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	parentIdentity := identityFromInfo(t, root)
	stageName, err := readbrokerstudy.NewCreationStageName()
	if err != nil {
		_ = parent.Close()
		t.Fatal(err)
	}
	finished := make(chan struct {
		result readbrokerstudy.CreationResult
		err    error
	}, 1)
	go func() {
		result, executeErr := impl.executeBoundedCreation(context.Background(), parent, parentIdentity, stageName, "uncertain.txt", []byte("synthetic stage"))
		finished <- struct {
			result readbrokerstudy.CreationResult
			err    error
		}{result, executeErr}
	}()
	select {
	case <-launcher.stageStarted:
	case <-time.After(3 * time.Second):
		_ = parent.Close()
		t.Fatal("stage worker did not reach deterministic cleanup checkpoint")
	}
	impl.mu.Lock()
	lease := impl.creationBarrier
	if lease == nil {
		impl.mu.Unlock()
		close(launcher.continueStage)
		_ = parent.Close()
		t.Fatal("creation barrier was not retained during staging")
	}
	lease.cancel()
	impl.mu.Unlock()
	close(launcher.continueStage)
	var completed struct {
		result readbrokerstudy.CreationResult
		err    error
	}
	select {
	case completed = <-finished:
	case <-time.After(3 * time.Second):
		_ = parent.Close()
		t.Fatal("cancelled creation did not classify and join")
	}
	if completed.result.Outcome != readbrokerstudy.CreationUnknown || completed.err == nil || !launcher.worker.joined || launcher.worker.abortCalls != 1 {
		t.Fatalf("uncertain cleanup was reported as success/retried: result=%+v err=%v joined=%t aborts=%d", completed.result, completed.err, launcher.worker.joined, launcher.worker.abortCalls)
	}
	if _, err := os.Lstat(filepath.Join(root, stageName)); err != nil {
		t.Fatalf("injected cleanup failure did not retain its stage evidence: %v", err)
	}
	secondStage, err := readbrokerstudy.NewCreationStageName()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := impl.executeBoundedCreation(context.Background(), parent, parentIdentity, secondStage, "second.txt", []byte("must not retry")); !errors.Is(err, ErrCreationBarrier) {
		t.Fatalf("uncertain effect was automatically retried: %v", err)
	}
	readArguments, err := json.Marshal(map[string]string{"path": filepath.Join(root, "uncertain.txt")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AdmitFilesystemRead(context.Background(), tools.Invocation{ConversationID: "creation-cleanup-failure", TurnID: "read", ProviderCallID: "after-failure", ToolID: tools.ReadFileID, Arguments: readArguments}, "openai/model/profile"); !errors.Is(err, ErrCreationBarrier) {
		t.Fatalf("read admission reopened despite retained cleanup ownership: %v", err)
	}
	impl.mu.Lock()
	retained := impl.creationBarrier == lease && lease.state == creationBarrierBlocked
	impl.mu.Unlock()
	if !retained {
		t.Fatal("blocked barrier ownership was discarded after uncertain cleanup")
	}
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 25*time.Millisecond)
	closeErr := service.Close(closeCtx)
	cancelClose()
	if !errors.Is(closeErr, readbrokerstudy.ErrCleanupPending) {
		t.Fatalf("service shutdown did not report retained cleanup ownership: %v", closeErr)
	}
	// Production has no automatic recovery API. Test teardown removes only its
	// synthetic stage and directly closes the already-joined fixture resources;
	// this does not reopen the service or count as supported recovery.
	if err := parent.Close(); err != nil {
		t.Errorf("close retained parent: %v", err)
	}
	if err := os.Remove(filepath.Join(root, stageName)); err != nil {
		t.Fatalf("remove test-owned stage after asserting retained ownership: %v", err)
	}
	if err := impl.session.Close(context.Background()); err != nil {
		t.Errorf("close joined fixture coordinator: %v", err)
	}
	if err := impl.launcher.Close(); err != nil {
		t.Errorf("close fixture launcher: %v", err)
	}
	if err := impl.registry.Close(); err != nil {
		t.Errorf("close fixture registry: %v", err)
	}
}

func TestCreationDelayedJoinReturnsUnknownAndKeepsAdmissionBlocked(t *testing.T) {
	root := t.TempDir()
	launcher := &delayedJoinCreationLauncher{stageStarted: make(chan struct{}), continueStage: make(chan struct{}), waitStarted: make(chan struct{}), releaseWait: make(chan struct{})}
	service, err := newTestHostServiceWithLauncher(launcher)
	if err != nil {
		t.Fatal(err)
	}
	if err := registerCreationFixtureOwners(t, service, root, "creation-delayed-join"); err != nil {
		t.Fatal(err)
	}
	impl := service.impl.(*linuxImplementation)
	parent, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	parentIdentity := identityFromInfo(t, root)
	stageName, err := readbrokerstudy.NewCreationStageName()
	if err != nil {
		_ = parent.Close()
		t.Fatal(err)
	}
	done := make(chan struct {
		result readbrokerstudy.CreationResult
		err    error
	}, 1)
	go func() {
		result, executeErr := impl.executeBoundedCreation(context.Background(), parent, parentIdentity, stageName, "delayed.txt", []byte("synthetic"))
		done <- struct {
			result readbrokerstudy.CreationResult
			err    error
		}{result, executeErr}
	}()
	select {
	case <-launcher.stageStarted:
	case <-time.After(3 * time.Second):
		_ = parent.Close()
		t.Fatal("stage did not reach deterministic delayed-join checkpoint")
	}
	impl.mu.Lock()
	lease := impl.creationBarrier
	if lease == nil {
		impl.mu.Unlock()
		t.Fatal("creation barrier missing during active worker")
	}
	lease.cancel()
	impl.mu.Unlock()
	close(launcher.continueStage)
	select {
	case <-launcher.waitStarted:
	case <-time.After(3 * time.Second):
		_ = parent.Close()
		t.Fatal("worker did not enter delayed Wait")
	}
	var completed struct {
		result readbrokerstudy.CreationResult
		err    error
	}
	select {
	case completed = <-done:
	case <-time.After(3 * time.Second):
		_ = parent.Close()
		t.Fatal("bounded worker join did not return after its deadline")
	}
	if completed.result.Outcome != readbrokerstudy.CreationUnknown || completed.err == nil || launcher.worker.Joined() {
		t.Fatalf("unconfirmed worker join was classified as complete: result=%+v err=%v joined=%t", completed.result, completed.err, launcher.worker.Joined())
	}
	if _, err := os.Lstat(filepath.Join(root, stageName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("confirmed stage abort did not remove its file: %v", err)
	}
	readArguments, err := json.Marshal(map[string]string{"path": filepath.Join(root, "delayed.txt")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AdmitFilesystemRead(context.Background(), tools.Invocation{ConversationID: "creation-delayed-join", TurnID: "read", ProviderCallID: "blocked", ToolID: tools.ReadFileID, Arguments: readArguments}, "openai/model/profile"); !errors.Is(err, ErrCreationBarrier) {
		t.Fatalf("admission reopened before worker join was confirmed: %v", err)
	}
	impl.mu.Lock()
	retained := impl.creationBarrier == lease && lease.state == creationBarrierBlocked && lease.worker == launcher.worker
	impl.mu.Unlock()
	if !retained {
		t.Fatal("unjoined worker ownership was not retained in the blocked barrier")
	}
	close(launcher.releaseWait)
	deadline := time.Now().Add(3 * time.Second)
	for !launcher.worker.Joined() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !launcher.worker.Joined() {
		t.Fatal("delayed worker did not eventually join")
	}
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 25*time.Millisecond)
	closeErr := service.Close(closeCtx)
	cancelClose()
	if !errors.Is(closeErr, readbrokerstudy.ErrCleanupPending) {
		t.Fatalf("unsupported automatic recovery did not remain explicit at close: %v", closeErr)
	}
	if err := parent.Close(); err != nil {
		t.Errorf("close retained parent: %v", err)
	}
	if err := impl.session.Close(context.Background()); err != nil {
		t.Errorf("close joined fixture coordinator: %v", err)
	}
	if err := impl.launcher.Close(); err != nil {
		t.Errorf("close fixture launcher: %v", err)
	}
	if err := impl.registry.Close(); err != nil {
		t.Errorf("close fixture registry: %v", err)
	}
}

func TestCreationCompletionLossAfterPublicationIsUnknownAndNeverReplayed(t *testing.T) {
	root := t.TempDir()
	launcher := &filesystemCreationFixtureLauncher{waitErr: errors.New("injected worker completion loss")}
	service, err := newTestHostServiceWithLauncher(launcher)
	if err != nil {
		t.Fatal(err)
	}
	if err := registerCreationFixtureOwners(t, service, root, "creation-completion-loss"); err != nil {
		t.Fatal(err)
	}
	impl := service.impl.(*linuxImplementation)
	parent, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	parentIdentity := identityFromInfo(t, root)
	stageName, err := readbrokerstudy.NewCreationStageName()
	if err != nil {
		_ = parent.Close()
		t.Fatal(err)
	}
	content := []byte("effect may have completed but response was lost\n")
	result, executeErr := impl.executeBoundedCreation(context.Background(), parent, parentIdentity, stageName, "completion-unknown.txt", content)
	if result.Outcome != readbrokerstudy.CreationUnknown || executeErr == nil || len(launcher.workers) != 1 || !launcher.workers[0].joined {
		t.Fatalf("lost completion was reported as success/replayed: result=%+v err=%v workers=%d joined=%t", result, executeErr, len(launcher.workers), launcher.workers[0].joined)
	}
	actual, err := os.ReadFile(filepath.Join(root, "completion-unknown.txt"))
	if err != nil || string(actual) != string(content) {
		t.Fatalf("uncertain result rolled back or corrupted a possibly completed effect: bytes=%q err=%v", actual, err)
	}
	impl.mu.Lock()
	lease := impl.creationBarrier
	retained := lease != nil && lease.state == creationBarrierBlocked && lease.worker != nil
	impl.mu.Unlock()
	if !retained {
		t.Fatal("worker completion uncertainty did not retain barrier ownership")
	}
	nextStage, err := readbrokerstudy.NewCreationStageName()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := impl.executeBoundedCreation(context.Background(), parent, parentIdentity, nextStage, "retry.txt", content); !errors.Is(err, ErrCreationBarrier) {
		t.Fatalf("uncertain publication was replayed automatically: %v", err)
	}
	readArguments, err := json.Marshal(map[string]string{"path": filepath.Join(root, "completion-unknown.txt")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AdmitFilesystemRead(context.Background(), tools.Invocation{ConversationID: "creation-completion-loss", TurnID: "read", ProviderCallID: "blocked", ToolID: tools.ReadFileID, Arguments: readArguments}, "openai/model/profile"); !errors.Is(err, ErrCreationBarrier) {
		t.Fatalf("read gate reopened after completion uncertainty: %v", err)
	}
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 25*time.Millisecond)
	closeErr := service.Close(closeCtx)
	cancelClose()
	if !errors.Is(closeErr, readbrokerstudy.ErrCleanupPending) {
		t.Fatalf("unsupported recovery was hidden during close: %v", closeErr)
	}
	if err := parent.Close(); err != nil {
		t.Errorf("close retained parent: %v", err)
	}
	if err := impl.session.Close(context.Background()); err != nil {
		t.Errorf("close joined fixture coordinator: %v", err)
	}
	if err := impl.launcher.Close(); err != nil {
		t.Errorf("close fixture launcher: %v", err)
	}
	if err := impl.registry.Close(); err != nil {
		t.Errorf("close fixture registry: %v", err)
	}
}

func registerCreationFixtureOwners(t *testing.T, service *Service, root, conversation string) error {
	t.Helper()
	for owner, class := range map[string]string{auth.SensitiveOwnerAuth: auth.SensitiveClassAuth, auth.SensitiveOwnerSettings: auth.SensitiveClassSettings} {
		path := filepath.Join(root, "."+owner+".json")
		if err := os.WriteFile(path, []byte("synthetic protected owner"), 0o600); err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		if err := service.Protect(context.Background(), owner, class, path, file); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	if err := service.InvalidateFilesystemRoot(context.Background(), runtime.FilesystemRootTransition{ConversationID: conversation, Root: root, Generation: 1}); err != nil {
		return err
	}
	installSyntheticCreationManifest(t, service, root)
	return nil
}

func TestRootTransitionCancelsAndJoinsCreationBeforeChangingScope(t *testing.T) {
	launcher := &blockingCreationWorkerLauncher{stageStarted: make(chan struct{})}
	service, err := newTestHostServiceWithLauncher(launcher)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(context.Background()); err != nil {
			t.Errorf("close service: %v", err)
		}
	})
	root := t.TempDir()
	for owner, class := range map[string]string{auth.SensitiveOwnerAuth: auth.SensitiveClassAuth, auth.SensitiveOwnerSettings: auth.SensitiveClassSettings} {
		path := filepath.Join(root, "."+owner+".json")
		if err := os.WriteFile(path, []byte("synthetic protected owner"), 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Protect(context.Background(), owner, class, path, file); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	conversation := "root-transition-fixture"
	if err := service.InvalidateFilesystemRoot(context.Background(), runtime.FilesystemRootTransition{ConversationID: conversation, Root: root, Generation: 1, Reason: runtime.FilesystemRootSelected}); err != nil {
		t.Fatal(err)
	}
	installSyntheticCreationManifest(t, service, root)
	parent, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	identity := identityFromInfo(t, root)
	stageName, err := readbrokerstudy.NewCreationStageName()
	if err != nil {
		t.Fatal(err)
	}
	creationDone := make(chan error, 1)
	go func() {
		result, createErr := service.impl.(*linuxImplementation).executeBoundedCreation(context.Background(), parent, identity, stageName, "cancelled.txt", []byte("never published"), creationRootBinding{root: root, conversation: conversation, generation: 1})
		if result.Outcome != readbrokerstudy.CreationNotCreated {
			creationDone <- fmt.Errorf("cancelled creation outcome=%s", result.Outcome)
			return
		}
		creationDone <- createErr
	}()
	select {
	case <-launcher.stageStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("creation did not reach stage admission")
	}
	transitionDone := make(chan error, 1)
	go func() {
		transitionDone <- service.InvalidateFilesystemRoot(context.Background(), runtime.FilesystemRootTransition{ConversationID: conversation, Root: root, Generation: 2, Reason: runtime.FilesystemRootSelected})
	}()
	select {
	case err := <-transitionDone:
		if err != nil {
			t.Fatalf("root transition: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("root transition did not wait for creation cleanup")
	}
	if err := <-creationDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("creation cancellation result=%v", err)
	}
	if !launcher.joined {
		t.Fatal("root transition completed before worker join")
	}
	if _, err := os.Lstat(filepath.Join(root, "cancelled.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled creation published target: %v", err)
	}
	if service.rootGeneration != 2 || service.rootPending {
		t.Fatalf("root state after joined cancellation: generation=%d pending=%t", service.rootGeneration, service.rootPending)
	}
}

func TestServiceCreationProposalBindsScopeContentDestinationAndConsumesOnce(t *testing.T) {
	service, err := newTestHostServiceWithLauncher(&testCreationWorkerLauncher{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(context.Background()); err != nil {
			t.Errorf("close service: %v", err)
		}
	})
	root := t.TempDir()
	for owner, class := range map[string]string{auth.SensitiveOwnerAuth: auth.SensitiveClassAuth, auth.SensitiveOwnerSettings: auth.SensitiveClassSettings} {
		path := filepath.Join(root, "."+owner+".json")
		if err := os.WriteFile(path, []byte("synthetic protected owner"), 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Protect(context.Background(), owner, class, path, file); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.InvalidateFilesystemRoot(context.Background(), runtime.FilesystemRootTransition{ConversationID: "create-proposal", Root: root, Generation: 1, Reason: runtime.FilesystemRootSelected}); err != nil {
		t.Fatal(err)
	}
	installSyntheticCreationManifest(t, service, root)
	input := tools.CreateFileInput{Path: "approved.txt", Content: "exact approved bytes\nsecond line\n"}
	arguments, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	invocation := tools.Invocation{ConversationID: "create-proposal", TurnID: "turn-1", ProviderCallID: "call-1", ToolID: tools.CreateFileID, Arguments: arguments}
	destination := "openai/model-a/profile"
	request, err := service.PrepareFilesystemCreation(context.Background(), invocation, destination)
	if err != nil {
		t.Fatalf("prepare proposal: %v", err)
	}
	if request.Destination != destination || request.AdmissionBinding == "" || len(request.Requirements) != 2 || len(request.RequireOnceKeys) != 2 || request.RequireOnceKeys[0] != "create" || request.RequireOnceKeys[1] != "target" {
		t.Fatalf("incomplete once approval request: %+v", request)
	}
	target := filepath.Join(root, input.Path)
	if request.Requirements[0].Description == "" || !strings.Contains(request.Requirements[0].Description, target) || !strings.Contains(request.Requirements[0].Description, "+exact approved bytes\n+second line\n") || !strings.Contains(request.Requirements[0].Description, destination) {
		t.Fatalf("approval omits exact target/content/destination: %+v", request.Requirements[0])
	}
	changed := invocation
	changed.Arguments = []byte(`{"path":"approved.txt","content":"changed"}`)
	if _, err := service.PrepareApprovedFilesystemCreation(context.Background(), changed, request, 1); !errors.Is(err, runtime.ErrFilesystemRootStale) {
		t.Fatalf("changed arguments reused proposal: %v", err)
	}
	changed = invocation
	changed.TurnID = "different-turn"
	if _, err := service.PrepareApprovedFilesystemCreation(context.Background(), changed, request, 1); !errors.Is(err, runtime.ErrFilesystemRootStale) {
		t.Fatalf("changed turn reused proposal: %v", err)
	}
	prepared, err := service.PrepareApprovedFilesystemCreation(context.Background(), invocation, request, 1)
	if err != nil {
		t.Fatalf("prepare exact approved proposal: %v", err)
	}
	if _, err := service.PrepareApprovedFilesystemCreation(context.Background(), invocation, request, 1); !errors.Is(err, runtime.ErrFilesystemRootStale) {
		t.Fatalf("proposal was not consumed once: %v", err)
	}
	if err := prepared.Close(context.Background()); err != nil {
		t.Fatalf("close retained parent: %v", err)
	}
	if err := os.WriteFile(target, []byte("incumbent"), 0o600); err != nil {
		t.Fatal(err)
	}
	invocation.ProviderCallID = "call-2"
	if _, err := service.PrepareFilesystemCreation(context.Background(), invocation, destination); err == nil {
		t.Fatal("existing destination was offered for approval")
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "incumbent" {
		t.Fatalf("existing file changed: %q err=%v", got, err)
	}
	authRoute := filepath.Join(root, "."+auth.SensitiveOwnerAuth+".json")
	if err := os.Remove(authRoute); err != nil {
		t.Fatal(err)
	}
	invocation.ProviderCallID = "call-3"
	invocation.Arguments, err = json.Marshal(tools.CreateFileInput{Path: filepath.Base(authRoute), Content: "must not claim a protected route"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PrepareFilesystemCreation(context.Background(), invocation, destination); !errors.Is(err, ErrCoverageMissing) {
		t.Fatalf("absent reserved protected route was offered: %v", err)
	}
}

func TestCreationRuntimeServiceCompositionReachesBoundedWorkerWithoutPublishingOnRefusal(t *testing.T) {
	root := t.TempDir()
	launcher := &refusingCreationWorkerLauncher{}
	service, err := newTestHostServiceWithLauncher(launcher)
	if err != nil {
		t.Fatal(err)
	}
	for owner, class := range map[string]string{auth.SensitiveOwnerAuth: auth.SensitiveClassAuth, auth.SensitiveOwnerSettings: auth.SensitiveClassSettings} {
		path := filepath.Join(root, "."+owner+".json")
		if err := os.WriteFile(path, []byte("synthetic protected owner"), 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Protect(context.Background(), owner, class, path, file); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	var approval permissions.PendingApproval
	base := runtime.BundledConversationToolRuntimeConfig(runtime.ToolApprovalFunc(func(_ context.Context, pending permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		approval = pending
		return permissions.ApprovalOnce, "", nil
	}), runtime.ToolClarificationFunc(func(context.Context, runtime.ClarificationRequest) (runtime.ClarificationResponse, error) {
		return runtime.ClarificationResponse{}, errors.New("unexpected clarification")
	}))
	config := runtime.WithBoundedCreationPrototype(base, service)
	config.ReadAdmission = service
	config.RootHost = service
	fp := &creationServiceProvider{}
	providers, err := model.NewRegistry(fp)
	if err != nil {
		t.Fatal(err)
	}
	chat := runtime.NewChatWithToolRuntime(providers, auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}, nil, config)
	if err := chat.AddProfile(context.Background(), "profile", string(provider.OpenAI), "Fixture", "synthetic credential", nil); err != nil {
		t.Fatal(err)
	}
	selection := chat.Discover(context.Background())
	if len(selection.Models) == 0 {
		t.Fatalf("provider discovery=%+v", selection)
	}
	if err := chat.Select(context.Background(), selection.Models[0].Key(), false); err != nil {
		t.Fatal(err)
	}
	if err := chat.SelectFilesystemRoot(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	installSyntheticCreationManifest(t, service, root)
	t.Cleanup(func() {
		chat.Close()
		if err := service.Close(context.Background()); err != nil {
			t.Errorf("close service: %v", err)
		}
	})
	input := tools.CreateFileInput{Path: "runtime-created.txt", Content: "exact approved bytes\n"}
	fp.input = input
	turn, err := chat.Send(context.Background(), "create the approved synthetic file", nil)
	if err != nil {
		t.Fatalf("runtime turn: %v", err)
	}
	if turn.Outcome != provider.OutcomeCompleted || len(turn.ToolActivities) != 1 || turn.ToolActivities[0].Outcome != tools.OutcomeFailed || fp.calls != 2 {
		t.Fatalf("unexpected runtime turn after injected pre-stage refusal: %+v", turn)
	}
	if approval.Request.Destination == "" || len(approval.Request.Requirements) != 2 || !strings.Contains(approval.Request.Requirements[0].Description, filepath.Join(root, input.Path)) || !strings.Contains(approval.Request.Requirements[0].Description, input.Content) {
		t.Fatalf("real service approval omitted exact target/content/destination: %+v", approval.Request)
	}
	if _, err := os.Lstat(filepath.Join(root, input.Path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused operation published a file: err=%v", err)
	}
	if !launcher.joined || strings.Join(launcher.events, ",") != "launch,ready,stage,terminate,wait" {
		t.Fatalf("worker retirement/order: joined=%t events=%v", launcher.joined, launcher.events)
	}
}

func TestComposedCreationSuccessAndContinuationFailureDoNotReplay(t *testing.T) {
	root := t.TempDir()
	launcher := &filesystemCreationFixtureLauncher{}
	service, err := newTestHostServiceWithLauncher(launcher)
	if err != nil {
		t.Fatal(err)
	}
	launcher.service = service
	for owner, class := range map[string]string{auth.SensitiveOwnerAuth: auth.SensitiveClassAuth, auth.SensitiveOwnerSettings: auth.SensitiveClassSettings} {
		path := filepath.Join(root, "."+owner+".json")
		if err := os.WriteFile(path, []byte("synthetic protected owner"), 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Protect(context.Background(), owner, class, path, file); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.InvalidateFilesystemRoot(context.Background(), runtime.FilesystemRootTransition{ConversationID: "creation-success-fixture", Root: root, Generation: 1}); err != nil {
		t.Fatal(err)
	}
	installSyntheticCreationManifest(t, service, root)
	var approvals []permissions.PendingApproval
	base := runtime.BundledConversationToolRuntimeConfig(runtime.ToolApprovalFunc(func(_ context.Context, pending permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		approvals = append(approvals, pending)
		return permissions.ApprovalOnce, "", nil
	}), runtime.ToolClarificationFunc(func(context.Context, runtime.ClarificationRequest) (runtime.ClarificationResponse, error) {
		return runtime.ClarificationResponse{}, errors.New("unexpected clarification")
	}))
	config := runtime.WithBoundedCreationPrototype(base, service)
	config.ReadAdmission = service
	config.RootHost = service
	fp := &creationServiceProvider{}
	providers, err := model.NewRegistry(fp)
	if err != nil {
		t.Fatal(err)
	}
	chat := runtime.NewChatWithToolRuntime(providers, auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}, nil, config)
	if err := chat.AddProfile(context.Background(), "profile", string(provider.OpenAI), "Fixture", "synthetic credential", nil); err != nil {
		t.Fatal(err)
	}
	selection := chat.Discover(context.Background())
	if len(selection.Models) == 0 {
		t.Fatal("provider discovery returned no model")
	}
	if err := chat.Select(context.Background(), selection.Models[0].Key(), false); err != nil {
		t.Fatal(err)
	}
	if err := chat.SelectFilesystemRoot(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	fp.script = func(index int) (tools.CreateFileInput, error) {
		switch index {
		case 0:
			return tools.CreateFileInput{Path: "first.txt", Content: "approved first bytes\n"}, nil
		case 2:
			return tools.CreateFileInput{Path: "second.txt", Content: "new turn bytes\n"}, nil
		default:
			return tools.CreateFileInput{}, nil
		}
	}
	fp.continuationFailureAt = 1
	t.Cleanup(func() {
		chat.Close()
		if err := service.Close(context.Background()); err != nil {
			t.Errorf("close creation service: %v", err)
		}
	})

	first, firstErr := chat.Send(context.Background(), "create first fixture", nil)
	if firstErr == nil || first.Outcome != provider.OutcomeService || len(first.ToolActivities) != 1 || first.ToolActivities[0].Outcome != tools.OutcomeSucceeded {
		t.Fatalf("first effect/failed continuation truth: turn=%+v err=%v", first, firstErr)
	}
	firstBytes, err := os.ReadFile(filepath.Join(root, "first.txt"))
	if err != nil || string(firstBytes) != "approved first bytes\n" {
		t.Fatalf("confirmed creation missing after continuation failure: bytes=%q err=%v", firstBytes, err)
	}
	second, secondErr := chat.Send(context.Background(), "create a different file", nil)
	if secondErr != nil || second.Outcome != provider.OutcomeCompleted || len(second.ToolActivities) != 1 || second.ToolActivities[0].Outcome != tools.OutcomeSucceeded {
		t.Fatalf("new turn did not complete independently: turn=%+v err=%v", second, secondErr)
	}
	secondBytes, err := os.ReadFile(filepath.Join(root, "second.txt"))
	if err != nil || string(secondBytes) != "new turn bytes\n" {
		t.Fatalf("new proposal was not created exactly: bytes=%q err=%v", secondBytes, err)
	}
	if len(approvals) != 2 || len(launcher.workers) != 2 || len(fp.callIDs) != 2 || fp.callIDs[0] == fp.callIDs[1] {
		t.Fatalf("new turn did not require its own approval and call identity: approvals=%d workers=%d calls=%v", len(approvals), len(launcher.workers), fp.callIDs)
	}
	if launcher.workers[0].joined == false || launcher.workers[1].joined == false || strings.Join(launcher.events, ",") != "launch,ready,stage,publish,wait,launch,ready,stage,publish,wait" {
		t.Fatalf("creation workers were not joined in order: joined=%v events=%v", []bool{launcher.workers[0].joined, launcher.workers[1].joined}, launcher.events)
	}
	if _, err := service.AdmitFilesystemRead(context.Background(), tools.Invocation{ConversationID: "creation-success-fixture", TurnID: "read", ProviderCallID: "read-after", ToolID: tools.ReadFileID, Arguments: []byte(`{"path":"first.txt"}`)}, "openai/model/profile"); err == nil {
		// A test launcher cannot confer read capability; it can only prove that
		// the create-owned barrier was retired. Admission gets past that gate and
		// fails later on the deliberately absent selected-root read coordinator.
	} else if errors.Is(err, ErrCreationBarrier) {
		t.Fatalf("creation barrier remained closed after confirmed worker retirement: %v", err)
	}
}

func TestCompletedReadCaptureSurvivesTransientCreationBarrierBeforeProviderHandoff(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "capture.txt")
	if err := os.WriteFile(target, []byte("synthetic completed capture"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	inventory := readbrokerstudy.NewChatCompositionInventory()
	launcher := &stage4WorkerLauncher{}
	grants := readbrokerstudy.NewOnceGrantFixture()
	session, err := readbrokerstudy.NewHostCoordinator(registry, readbrokerstudy.NewBroker(registry), launcher, grants, inventory)
	if err != nil {
		t.Fatal(err)
	}
	for _, requirement := range readbrokerstudy.ChatCompositionRequirements() {
		if err := session.DeclareCoverageOwner(requirement); err != nil {
			t.Fatal(err)
		}
	}
	impl := &linuxImplementation{registry: registry, launcher: launcher, session: session, inventory: inventory, revision: 1,
		owners: map[string]OwnerCoverage{
			auth.SensitiveOwnerAuth:     {Owner: auth.SensitiveOwnerAuth, Class: auth.SensitiveClassAuth, State: OwnerRequired},
			auth.SensitiveOwnerSettings: {Owner: auth.SensitiveOwnerSettings, Class: auth.SensitiveClassSettings, State: OwnerRequired},
		}, blocked: make(map[string]string)}
	service := &Service{impl: impl}
	for owner, class := range map[string]string{auth.SensitiveOwnerAuth: auth.SensitiveClassAuth, auth.SensitiveOwnerSettings: auth.SensitiveClassSettings} {
		path := filepath.Join(t.TempDir(), owner+".json")
		if err := service.Protect(context.Background(), owner, class, path, nil); err != nil {
			t.Fatal(err)
		}
	}
	callID := "completed-read-capture"
	if err := grants.ApproveOnce(callID); err != nil {
		t.Fatal(err)
	}
	fp := &creationBarrierCaptureProvider{service: service, impl: impl, path: target, callID: callID, want: "synthetic completed capture"}
	providers, err := model.NewRegistry(fp)
	if err != nil {
		t.Fatal(err)
	}
	var approvalCount int
	config := runtime.BundledConversationToolRuntimeConfigWithFilesystemRead(runtime.ToolApprovalFunc(func(_ context.Context, pending permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		approvalCount++
		if len(pending.Request.Requirements) != 2 || pending.Request.Destination == "" {
			t.Fatalf("completed read approval was incomplete: %+v", pending.Request)
		}
		return permissions.ApprovalOnce, "", nil
	}), runtime.ToolClarificationFunc(func(context.Context, runtime.ClarificationRequest) (runtime.ClarificationResponse, error) {
		return runtime.ClarificationResponse{}, errors.New("unexpected clarification")
	}), service)
	chat := runtime.NewChatWithToolRuntime(providers, auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}, nil, config)
	if err := chat.AddProfile(context.Background(), "profile", string(provider.OpenAI), "Fixture", "synthetic credential", nil); err != nil {
		t.Fatal(err)
	}
	selection := chat.Discover(context.Background())
	if len(selection.Models) == 0 {
		t.Fatal("provider discovery returned no model")
	}
	if err := chat.Select(context.Background(), selection.Models[0].Key(), false); err != nil {
		t.Fatal(err)
	}
	if err := chat.SelectFilesystemRoot(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		chat.Close()
		if err := service.Close(context.Background()); err != nil {
			t.Errorf("close capture/barrier service: %v", err)
		}
	})
	turn, err := chat.Send(context.Background(), "read the approved synthetic fixture", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted || len(turn.ToolActivities) != 1 || turn.ToolActivities[0].Outcome != tools.OutcomeSucceeded {
		t.Fatalf("completed capture/provider continuation: turn=%+v err=%v", turn, err)
	}
	if approvalCount != 1 || !fp.capturePublished || !fp.barrierRetired || !launcher.lastWorker.Joined() {
		t.Fatalf("capture lifecycle approvals=%d published=%t barrier_retired=%t joined=%t", approvalCount, fp.capturePublished, fp.barrierRetired, launcher.lastWorker.Joined())
	}
}

type creationBarrierCaptureProvider struct {
	service          *Service
	impl             *linuxImplementation
	path, callID     string
	want             string
	requests         int
	capturePublished bool
	barrierRetired   bool
}

func (*creationBarrierCaptureProvider) ID() provider.ID { return provider.OpenAI }
func (*creationBarrierCaptureProvider) AuthMethods() []provider.AuthMethod {
	return []provider.AuthMethod{provider.AuthAPIKey}
}
func (*creationBarrierCaptureProvider) Authenticate(context.Context, provider.Credential) (provider.AuthResult, error) {
	return provider.AuthResult{Provider: provider.OpenAI}, nil
}
func (*creationBarrierCaptureProvider) ListModels(context.Context, provider.Credential) ([]provider.Model, error) {
	return []provider.Model{{ID: "capture-model", DisplayName: "Capture Model", Compatibility: provider.CompatibilitySupported}}, nil
}
func (*creationBarrierCaptureProvider) RouteCapabilities(provider.AuthMethod) provider.RouteCapabilities {
	return provider.RouteCapabilities{ClientTools: provider.SupportSupported}
}
func (p *creationBarrierCaptureProvider) StreamChat(ctx context.Context, request provider.ChatRequest, _ func(string)) (provider.Result, error) {
	if p.requests == 0 {
		p.requests++
		arguments, _ := json.Marshal(map[string]string{"path": p.path})
		callRaw, _ := json.Marshal(map[string]any{"type": "function_call", "id": "item-" + p.callID, "call_id": p.callID, "name": string(tools.ReadFileID), "arguments": string(arguments), "status": "completed"})
		return provider.Result{Outcome: provider.OutcomeCompleted, Completion: provider.CompletionToolCalls, Text: "read", Native: []json.RawMessage{callRaw}, ToolCalls: []provider.ToolCall{{CallID: p.callID, ToolID: tools.ReadFileID, Arguments: arguments}}}, nil
	}
	p.requests++
	for _, message := range request.Messages {
		if message.ToolResult != nil && message.ToolResult.CallID == p.callID && strings.Contains(message.ToolResult.Content, p.want) && !message.ToolResult.IsError {
			p.capturePublished = true
		}
	}
	if !p.capturePublished {
		return provider.Result{}, errors.New("completed read capture was not present at provider continuation")
	}
	generation := p.impl.session.GenerationSnapshot()
	lease, err := p.impl.beginCreationBarrier(ctx)
	if err != nil {
		return provider.Result{}, err
	}
	arguments, err := json.Marshal(map[string]string{"path": p.path})
	if err != nil {
		return provider.Result{}, err
	}
	if _, err := p.service.AdmitFilesystemRead(ctx, tools.Invocation{ConversationID: p.service.rootConversationID, TurnID: "after-capture", ProviderCallID: "new-read", ToolID: tools.ReadFileID, Arguments: arguments}, "openai/model/profile"); !errors.Is(err, ErrCreationBarrier) {
		return provider.Result{}, fmt.Errorf("new read was not blocked while stage could become visible: %w", err)
	}
	if err := p.impl.finishCreationBarrier(lease, true); err != nil {
		return provider.Result{}, err
	}
	p.barrierRetired = true
	if after := p.impl.session.GenerationSnapshot(); after != generation {
		return provider.Result{}, fmt.Errorf("creation barrier changed read generations: before=%+v after=%+v", generation, after)
	}
	message, _ := json.Marshal(map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "continued"}}})
	return provider.Result{Outcome: provider.OutcomeCompleted, Completion: provider.CompletionFinal, Text: "continued", Native: []json.RawMessage{message}}, nil
}

type filesystemCreationFixtureLauncher struct {
	service        *Service
	events         []string
	workers        []*filesystemCreationFixtureWorker
	waitErr        error
	publishEntered chan struct{}
	releasePublish chan struct{}
}

func (*filesystemCreationFixtureLauncher) Start(context.Context) (readbrokerstudy.SandboxWorker, error) {
	return nil, readbrokerstudy.ErrUnavailable
}
func (l *filesystemCreationFixtureLauncher) StartForOperation(_ context.Context, operation readbrokerstudy.AdmissionOperation) (readbrokerstudy.SandboxWorker, error) {
	if operation != readbrokerstudy.AdmissionCreateFile {
		return nil, readbrokerstudy.ErrProtocol
	}
	l.events = append(l.events, "launch")
	w := &filesystemCreationFixtureWorker{launcher: l}
	l.workers = append(l.workers, w)
	return w, nil
}
func (*filesystemCreationFixtureLauncher) Close() error { return nil }

type filesystemCreationFixtureWorker struct {
	launcher *filesystemCreationFixtureLauncher
	parent   *os.File
	stage    string
	leaf     string
	content  []byte
	joined   bool
	identity readbrokerstudy.FileIdentity
}

func (w *filesystemCreationFixtureWorker) Ready(context.Context) error {
	w.launcher.events = append(w.launcher.events, "ready")
	return nil
}
func (*filesystemCreationFixtureWorker) Probe() readbrokerstudy.WorkerProbe {
	return readbrokerstudy.WorkerProbe{}
}
func (*filesystemCreationFixtureWorker) TransferCapability(context.Context, *os.File, readbrokerstudy.FileIdentity) error {
	return readbrokerstudy.ErrProtocol
}
func (*filesystemCreationFixtureWorker) AwaitReadStarted(context.Context) error {
	return readbrokerstudy.ErrProtocol
}
func (*filesystemCreationFixtureWorker) ReadResult(context.Context) (readbrokerstudy.WorkerResult, error) {
	return readbrokerstudy.WorkerResult{}, readbrokerstudy.ErrProtocol
}
func (w *filesystemCreationFixtureWorker) StageCreation(_ context.Context, parent *os.File, expected readbrokerstudy.FileIdentity, stageName, leaf string, content []byte) (readbrokerstudy.CreationStage, error) {
	w.launcher.events = append(w.launcher.events, "stage")
	var stat unix.Stat_t
	if err := unix.Fstat(int(parent.Fd()), &stat); err != nil || identityFromUnixStat(&stat) != expected {
		return readbrokerstudy.CreationStage{}, readbrokerstudy.ErrStaleReceipt
	}
	fd, err := unix.Openat(int(parent.Fd()), stageName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return readbrokerstudy.CreationStage{}, &readbrokerstudy.CreationOperationError{Result: readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, Cause: err}
	}
	if len(content) != 0 {
		n, writeErr := unix.Write(fd, content)
		if writeErr != nil || n != len(content) {
			_ = unix.Close(fd)
			return readbrokerstudy.CreationStage{}, &readbrokerstudy.CreationOperationError{Result: readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, Cause: errors.Join(writeErr, io.ErrShortWrite)}
		}
	}
	if err := unix.Fsync(fd); err != nil {
		_ = unix.Close(fd)
		return readbrokerstudy.CreationStage{}, &readbrokerstudy.CreationOperationError{Result: readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, Cause: err}
	}
	var staged unix.Stat_t
	if err := unix.Fstat(fd, &staged); err != nil {
		_ = unix.Close(fd)
		return readbrokerstudy.CreationStage{}, &readbrokerstudy.CreationOperationError{Result: readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationUnknown}, Cause: err}
	}
	dup, err := unix.FcntlInt(parent.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		_ = unix.Close(fd)
		return readbrokerstudy.CreationStage{}, &readbrokerstudy.CreationOperationError{Result: readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationUnknown}, Cause: err}
	}
	w.parent = os.NewFile(uintptr(dup), "creation-fixture-parent")
	if w.parent == nil {
		_ = unix.Close(dup)
		_ = unix.Close(fd)
		return readbrokerstudy.CreationStage{}, readbrokerstudy.ErrProtocol
	}
	if err := unix.Close(fd); err != nil {
		return readbrokerstudy.CreationStage{}, &readbrokerstudy.CreationOperationError{Result: readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationUnknown}, Cause: err}
	}
	w.stage, w.leaf, w.content = stageName, leaf, append([]byte(nil), content...)
	w.identity = readbrokerstudy.FileIdentity{Device: uint64(staged.Dev), Inode: staged.Ino, Type: uint32(staged.Mode & unix.S_IFMT)}
	return readbrokerstudy.CreationStage{}, nil
}
func (w *filesystemCreationFixtureWorker) PublishCreation(_ context.Context, stage readbrokerstudy.CreationStage) (readbrokerstudy.CreationResult, error) {
	w.launcher.events = append(w.launcher.events, "publish")
	if w.launcher.publishEntered != nil {
		close(w.launcher.publishEntered)
		<-w.launcher.releasePublish
	}
	if stage.StageName() != "" || w.parent == nil || w.stage == "" {
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationUnknown}, readbrokerstudy.ErrProtocol
	}
	if err := unix.Renameat2(int(w.parent.Fd()), w.stage, int(w.parent.Fd()), w.leaf, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationConflict}, nil
		}
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationUnknown}, err
	}
	var st unix.Stat_t
	if err := unix.Fstatat(int(w.parent.Fd()), w.leaf, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationUnknown}, err
	}
	identity := readbrokerstudy.FileIdentity{Device: uint64(st.Dev), Inode: st.Ino, Type: uint32(st.Mode & unix.S_IFMT)}
	if identity != w.identity || st.Mode&0o777 != 0o600 {
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationUnknown}, readbrokerstudy.ErrStaleReceipt
	}
	if err := unix.Fsync(int(w.parent.Fd())); err != nil {
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationUnknown}, err
	}
	return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationCreated, Identity: identity, Bytes: uint32(len(w.content))}, nil
}
func (w *filesystemCreationFixtureWorker) AbortCreation(_ context.Context, _ readbrokerstudy.CreationStage) (readbrokerstudy.CreationResult, error) {
	if w.parent != nil && w.stage != "" {
		_ = unix.Unlinkat(int(w.parent.Fd()), w.stage, 0)
	}
	return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, nil
}
func (w *filesystemCreationFixtureWorker) Terminate() error { return nil }
func (w *filesystemCreationFixtureWorker) Wait() error {
	w.launcher.events = append(w.launcher.events, "wait")
	if w.parent != nil {
		_ = w.parent.Close()
		w.parent = nil
	}
	w.joined = true
	return w.launcher.waitErr
}
func (w *filesystemCreationFixtureWorker) Joined() bool                 { return w.joined }
func (*filesystemCreationFixtureWorker) WorkerContext() context.Context { return context.Background() }

type incompleteCleanupCreationLauncher struct {
	inertWorkerLauncher
	stageStarted  chan struct{}
	continueStage chan struct{}
	worker        *incompleteCleanupCreationWorker
}

func (l *incompleteCleanupCreationLauncher) StartForOperation(_ context.Context, operation readbrokerstudy.AdmissionOperation) (readbrokerstudy.SandboxWorker, error) {
	if operation != readbrokerstudy.AdmissionCreateFile {
		return nil, readbrokerstudy.ErrProtocol
	}
	l.worker = &incompleteCleanupCreationWorker{launcher: l}
	return l.worker, nil
}

type incompleteCleanupCreationWorker struct {
	launcher   *incompleteCleanupCreationLauncher
	parent     *os.File
	stage      string
	joined     bool
	abortCalls int
}

func (*incompleteCleanupCreationWorker) Ready(context.Context) error { return nil }
func (*incompleteCleanupCreationWorker) Probe() readbrokerstudy.WorkerProbe {
	return readbrokerstudy.WorkerProbe{}
}
func (*incompleteCleanupCreationWorker) TransferCapability(context.Context, *os.File, readbrokerstudy.FileIdentity) error {
	return readbrokerstudy.ErrProtocol
}
func (*incompleteCleanupCreationWorker) AwaitReadStarted(context.Context) error {
	return readbrokerstudy.ErrProtocol
}
func (*incompleteCleanupCreationWorker) ReadResult(context.Context) (readbrokerstudy.WorkerResult, error) {
	return readbrokerstudy.WorkerResult{}, readbrokerstudy.ErrProtocol
}
func (w *incompleteCleanupCreationWorker) StageCreation(_ context.Context, parent *os.File, _ readbrokerstudy.FileIdentity, stageName, _ string, _ []byte) (readbrokerstudy.CreationStage, error) {
	fd, err := unix.Openat(int(parent.Fd()), stageName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return readbrokerstudy.CreationStage{}, &readbrokerstudy.CreationOperationError{Result: readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, Cause: err}
	}
	if err := unix.Close(fd); err != nil {
		return readbrokerstudy.CreationStage{}, &readbrokerstudy.CreationOperationError{Result: readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationUnknown}, Cause: err}
	}
	dup, err := unix.FcntlInt(parent.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return readbrokerstudy.CreationStage{}, &readbrokerstudy.CreationOperationError{Result: readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationUnknown}, Cause: err}
	}
	w.parent, w.stage = os.NewFile(uintptr(dup), "incomplete-cleanup-parent"), stageName
	close(w.launcher.stageStarted)
	<-w.launcher.continueStage
	return readbrokerstudy.CreationStage{}, nil
}
func (*incompleteCleanupCreationWorker) PublishCreation(context.Context, readbrokerstudy.CreationStage) (readbrokerstudy.CreationResult, error) {
	return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationUnknown}, readbrokerstudy.ErrProtocol
}
func (w *incompleteCleanupCreationWorker) AbortCreation(context.Context, readbrokerstudy.CreationStage) (readbrokerstudy.CreationResult, error) {
	w.abortCalls++
	return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationUnknown}, errors.New("injected stage cleanup failure")
}
func (*incompleteCleanupCreationWorker) Terminate() error { return nil }
func (w *incompleteCleanupCreationWorker) Wait() error {
	w.joined = true
	if w.parent != nil {
		_ = w.parent.Close()
		w.parent = nil
	}
	return nil
}
func (w *incompleteCleanupCreationWorker) Joined() bool                 { return w.joined }
func (*incompleteCleanupCreationWorker) WorkerContext() context.Context { return context.Background() }

type delayedJoinCreationLauncher struct {
	inertWorkerLauncher
	stageStarted  chan struct{}
	continueStage chan struct{}
	waitStarted   chan struct{}
	releaseWait   chan struct{}
	worker        *delayedJoinCreationWorker
}

func (l *delayedJoinCreationLauncher) StartForOperation(_ context.Context, operation readbrokerstudy.AdmissionOperation) (readbrokerstudy.SandboxWorker, error) {
	if operation != readbrokerstudy.AdmissionCreateFile {
		return nil, readbrokerstudy.ErrProtocol
	}
	l.worker = &delayedJoinCreationWorker{launcher: l}
	return l.worker, nil
}

type delayedJoinCreationWorker struct {
	launcher *delayedJoinCreationLauncher
	parent   *os.File
	stage    string
	joined   atomic.Bool
}

func (*delayedJoinCreationWorker) Ready(context.Context) error { return nil }
func (*delayedJoinCreationWorker) Probe() readbrokerstudy.WorkerProbe {
	return readbrokerstudy.WorkerProbe{}
}
func (*delayedJoinCreationWorker) TransferCapability(context.Context, *os.File, readbrokerstudy.FileIdentity) error {
	return readbrokerstudy.ErrProtocol
}
func (*delayedJoinCreationWorker) AwaitReadStarted(context.Context) error {
	return readbrokerstudy.ErrProtocol
}
func (*delayedJoinCreationWorker) ReadResult(context.Context) (readbrokerstudy.WorkerResult, error) {
	return readbrokerstudy.WorkerResult{}, readbrokerstudy.ErrProtocol
}
func (w *delayedJoinCreationWorker) StageCreation(_ context.Context, parent *os.File, _ readbrokerstudy.FileIdentity, stageName, _ string, _ []byte) (readbrokerstudy.CreationStage, error) {
	fd, err := unix.Openat(int(parent.Fd()), stageName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return readbrokerstudy.CreationStage{}, &readbrokerstudy.CreationOperationError{Result: readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, Cause: err}
	}
	if err := unix.Close(fd); err != nil {
		return readbrokerstudy.CreationStage{}, &readbrokerstudy.CreationOperationError{Result: readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationUnknown}, Cause: err}
	}
	dup, err := unix.FcntlInt(parent.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return readbrokerstudy.CreationStage{}, &readbrokerstudy.CreationOperationError{Result: readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationUnknown}, Cause: err}
	}
	w.parent, w.stage = os.NewFile(uintptr(dup), "delayed-join-parent"), stageName
	close(w.launcher.stageStarted)
	<-w.launcher.continueStage
	return readbrokerstudy.CreationStage{}, nil
}
func (w *delayedJoinCreationWorker) PublishCreation(context.Context, readbrokerstudy.CreationStage) (readbrokerstudy.CreationResult, error) {
	return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationUnknown}, readbrokerstudy.ErrProtocol
}
func (w *delayedJoinCreationWorker) AbortCreation(context.Context, readbrokerstudy.CreationStage) (readbrokerstudy.CreationResult, error) {
	if err := unix.Unlinkat(int(w.parent.Fd()), w.stage, 0); err != nil {
		return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationUnknown}, err
	}
	return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, nil
}
func (*delayedJoinCreationWorker) Terminate() error { return nil }
func (w *delayedJoinCreationWorker) Wait() error {
	close(w.launcher.waitStarted)
	<-w.launcher.releaseWait
	w.joined.Store(true)
	if w.parent != nil {
		_ = w.parent.Close()
		w.parent = nil
	}
	return nil
}
func (w *delayedJoinCreationWorker) Joined() bool                 { return w.joined.Load() }
func (*delayedJoinCreationWorker) WorkerContext() context.Context { return context.Background() }

type creationServiceProvider struct {
	input                 tools.CreateFileInput
	calls                 int
	callIDs               []string
	continuationFailureAt int
	script                func(int) (tools.CreateFileInput, error)
}

func (*creationServiceProvider) ID() provider.ID { return provider.OpenAI }
func (*creationServiceProvider) AuthMethods() []provider.AuthMethod {
	return []provider.AuthMethod{provider.AuthAPIKey}
}
func (*creationServiceProvider) Authenticate(context.Context, provider.Credential) (provider.AuthResult, error) {
	return provider.AuthResult{Provider: provider.OpenAI}, nil
}
func (*creationServiceProvider) ListModels(context.Context, provider.Credential) ([]provider.Model, error) {
	return []provider.Model{{ID: "model-a", DisplayName: "Model A", Compatibility: provider.CompatibilitySupported}}, nil
}
func (*creationServiceProvider) RouteCapabilities(provider.AuthMethod) provider.RouteCapabilities {
	return provider.RouteCapabilities{ClientTools: provider.SupportSupported}
}
func (p *creationServiceProvider) StreamChat(_ context.Context, _ provider.ChatRequest, _ func(string)) (provider.Result, error) {
	callIndex := p.calls
	p.calls++
	if callIndex == p.continuationFailureAt && p.continuationFailureAt > 0 {
		return provider.Result{}, &provider.Error{Outcome: provider.OutcomeService}
	}
	if p.script == nil && p.calls > 1 || p.script != nil && (callIndex == 1 || callIndex == 3) {
		message, _ := json.Marshal(map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "done"}}})
		return provider.Result{Outcome: provider.OutcomeCompleted, Completion: provider.CompletionFinal, Text: "done", Native: []json.RawMessage{message}}, nil
	}
	input := p.input
	if p.script != nil {
		var err error
		input, err = p.script(callIndex)
		if err != nil {
			return provider.Result{}, err
		}
	}
	callID := fmt.Sprintf("create-call-%d", callIndex)
	p.callIDs = append(p.callIDs, callID)
	arguments, _ := json.Marshal(input)
	callRaw, _ := json.Marshal(map[string]any{"type": "function_call", "id": "item-" + callID, "call_id": callID, "name": string(tools.CreateFileID), "arguments": string(arguments), "status": "completed"})
	return provider.Result{Outcome: provider.OutcomeCompleted, Completion: provider.CompletionToolCalls, Text: "create", Native: []json.RawMessage{callRaw}, ToolCalls: []provider.ToolCall{{CallID: callID, ToolID: tools.CreateFileID, Arguments: arguments}}}, nil
}

type refusingCreationWorkerLauncher struct {
	events []string
	joined bool
}

func (*refusingCreationWorkerLauncher) Start(context.Context) (readbrokerstudy.SandboxWorker, error) {
	return nil, readbrokerstudy.ErrUnavailable
}
func (l *refusingCreationWorkerLauncher) StartForOperation(_ context.Context, operation readbrokerstudy.AdmissionOperation) (readbrokerstudy.SandboxWorker, error) {
	if operation != readbrokerstudy.AdmissionCreateFile {
		return nil, readbrokerstudy.ErrProtocol
	}
	l.events = append(l.events, "launch")
	return &refusingCreationSandboxWorker{launcher: l}, nil
}
func (*refusingCreationWorkerLauncher) Close() error { return nil }

type refusingCreationSandboxWorker struct {
	launcher *refusingCreationWorkerLauncher
	joined   bool
}

func (w *refusingCreationSandboxWorker) Ready(context.Context) error {
	w.launcher.events = append(w.launcher.events, "ready")
	return nil
}
func (*refusingCreationSandboxWorker) Probe() readbrokerstudy.WorkerProbe {
	return readbrokerstudy.WorkerProbe{}
}
func (*refusingCreationSandboxWorker) TransferCapability(context.Context, *os.File, readbrokerstudy.FileIdentity) error {
	return readbrokerstudy.ErrProtocol
}
func (*refusingCreationSandboxWorker) AwaitReadStarted(context.Context) error {
	return readbrokerstudy.ErrProtocol
}
func (*refusingCreationSandboxWorker) ReadResult(context.Context) (readbrokerstudy.WorkerResult, error) {
	return readbrokerstudy.WorkerResult{}, readbrokerstudy.ErrProtocol
}
func (w *refusingCreationSandboxWorker) StageCreation(context.Context, *os.File, readbrokerstudy.FileIdentity, string, string, []byte) (readbrokerstudy.CreationStage, error) {
	w.launcher.events = append(w.launcher.events, "stage")
	return readbrokerstudy.CreationStage{}, &readbrokerstudy.CreationOperationError{Result: readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, Cause: errors.New("injected stage refusal")}
}
func (*refusingCreationSandboxWorker) PublishCreation(context.Context, readbrokerstudy.CreationStage) (readbrokerstudy.CreationResult, error) {
	return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationUnknown}, readbrokerstudy.ErrProtocol
}
func (*refusingCreationSandboxWorker) AbortCreation(context.Context, readbrokerstudy.CreationStage) (readbrokerstudy.CreationResult, error) {
	return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, nil
}
func (w *refusingCreationSandboxWorker) Terminate() error {
	w.launcher.events = append(w.launcher.events, "terminate")
	return nil
}
func (w *refusingCreationSandboxWorker) Wait() error {
	w.launcher.events = append(w.launcher.events, "wait")
	w.joined = true
	w.launcher.joined = true
	return nil
}
func (w *refusingCreationSandboxWorker) Joined() bool                 { return w.joined }
func (*refusingCreationSandboxWorker) WorkerContext() context.Context { return context.Background() }

func installSyntheticCreationManifest(t *testing.T, service *Service, root string) {
	t.Helper()
	impl := service.impl.(*linuxImplementation)
	ownerDir := filepath.Join(root, ".synthetic-owned")
	if err := os.Mkdir(ownerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	tokenDB := filepath.Join(ownerDir, "events.db")
	paths := []string{tokenDB, tokenDB + "-wal", tokenDB + "-shm", tokenDB + "-journal", tokenDB + ".backup"}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("synthetic protected identity"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := impl.registry.Register(readbrokerstudy.OwnerRegistration{Owner: "tokenlogger-store", Class: readbrokerstudy.ClassTokenDatabase, Path: path}); err != nil {
			t.Fatalf("register Token Logger fixture identity %s: %v", filepath.Base(path), err)
		}
	}
	providerPath := filepath.Join(ownerDir, "provider-source.snapshot")
	if err := os.WriteFile(providerPath, []byte("synthetic provider source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := impl.registry.Register(readbrokerstudy.OwnerRegistration{Owner: "provider-source", Class: readbrokerstudy.ClassProvider, Path: providerPath}); err != nil {
		t.Fatal(err)
	}
	view := impl.registry.View()
	entries := make(map[string]syntheticCreationOwner)
	for _, owner := range []struct {
		name  string
		class readbrokerstudy.ResourceClass
	}{
		{auth.SensitiveOwnerAuth, readbrokerstudy.ClassCredential},
		{auth.SensitiveOwnerSettings, readbrokerstudy.ClassSettings},
		{"tokenlogger-store", readbrokerstudy.ClassTokenDatabase},
		{"provider-source", readbrokerstudy.ClassProvider},
	} {
		manifest := syntheticCreationOwner{class: owner.class, registrations: make(map[string]readbrokerstudy.FileIdentity)}
		for _, entry := range view.Entries {
			// Route-only records have no pinned inode and do not belong in the identity manifest.
			if entry.Owner == owner.name && entry.Class == owner.class && !entry.Directory && entry.Identity.Inode != 0 {
				manifest.registrations[entry.Path] = entry.Identity
			}
		}
		entries[owner.name] = manifest
	}
	impl.mu.Lock()
	impl.owners["tokenlogger-store"] = OwnerCoverage{Owner: "tokenlogger-store", Class: "token_database", State: OwnerRegistered, Routes: 5, Identities: 5, Evidence: "synthetic creation fixture"}
	impl.owners["provider-source"] = OwnerCoverage{Owner: "provider-source", Class: "provider_source", State: OwnerRegistered, Routes: 1, Identities: 1, Evidence: "synthetic creation fixture"}
	impl.creationManifest = &syntheticCreationOwnerManifest{
		scopeParent:       identityFromInfo(t, root),
		scopeComplete:     true,
		policyRevision:    view.Generation,
		coverageRevision:  impl.inventory.Snapshot().Revision,
		tokenDatabasePath: tokenDB,
		owners:            entries,
	}
	impl.mu.Unlock()
}

func identityFromInfo(t *testing.T, path string) readbrokerstudy.FileIdentity {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("fixture identity unavailable")
	}
	return readbrokerstudy.FileIdentity{Device: uint64(stat.Dev), Inode: stat.Ino, Type: uint32(stat.Mode & syscall.S_IFMT)}
}

type testCreationWorkerLauncher struct {
	service                   *Service
	invocation                tools.Invocation
	events                    []string
	readsBlockedDuringStage   bool
	readsBlockedDuringPublish bool
	workerJoined              bool
}

func (l *testCreationWorkerLauncher) Start(context.Context) (readbrokerstudy.SandboxWorker, error) {
	return nil, readbrokerstudy.ErrUnavailable
}

func (l *testCreationWorkerLauncher) StartForOperation(_ context.Context, operation readbrokerstudy.AdmissionOperation) (readbrokerstudy.SandboxWorker, error) {
	if operation != readbrokerstudy.AdmissionCreateFile {
		return nil, readbrokerstudy.ErrProtocol
	}
	l.events = append(l.events, "launch")
	return &testCreationSandboxWorker{launcher: l}, nil
}

func (*testCreationWorkerLauncher) Close() error { return nil }

type testCreationSandboxWorker struct {
	launcher *testCreationWorkerLauncher
	joined   bool
}

type blockingCreationWorkerLauncher struct {
	inertWorkerLauncher
	stageStarted chan struct{}
	joined       bool
}

func (l *blockingCreationWorkerLauncher) StartForOperation(_ context.Context, operation readbrokerstudy.AdmissionOperation) (readbrokerstudy.SandboxWorker, error) {
	if operation != readbrokerstudy.AdmissionCreateFile {
		return nil, readbrokerstudy.ErrProtocol
	}
	return &blockingCreationSandboxWorker{launcher: l}, nil
}

type blockingCreationSandboxWorker struct {
	launcher *blockingCreationWorkerLauncher
	joined   bool
}

func (*blockingCreationSandboxWorker) Ready(context.Context) error { return nil }
func (*blockingCreationSandboxWorker) Probe() readbrokerstudy.WorkerProbe {
	return readbrokerstudy.WorkerProbe{}
}
func (*blockingCreationSandboxWorker) TransferCapability(context.Context, *os.File, readbrokerstudy.FileIdentity) error {
	return readbrokerstudy.ErrProtocol
}
func (*blockingCreationSandboxWorker) AwaitReadStarted(context.Context) error {
	return readbrokerstudy.ErrProtocol
}
func (*blockingCreationSandboxWorker) ReadResult(context.Context) (readbrokerstudy.WorkerResult, error) {
	return readbrokerstudy.WorkerResult{}, readbrokerstudy.ErrProtocol
}
func (w *blockingCreationSandboxWorker) StageCreation(ctx context.Context, _ *os.File, _ readbrokerstudy.FileIdentity, _ string, _ string, _ []byte) (readbrokerstudy.CreationStage, error) {
	close(w.launcher.stageStarted)
	<-ctx.Done()
	return readbrokerstudy.CreationStage{}, &readbrokerstudy.CreationOperationError{Result: readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, Cause: ctx.Err()}
}
func (*blockingCreationSandboxWorker) PublishCreation(context.Context, readbrokerstudy.CreationStage) (readbrokerstudy.CreationResult, error) {
	return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationUnknown}, readbrokerstudy.ErrProtocol
}
func (*blockingCreationSandboxWorker) AbortCreation(context.Context, readbrokerstudy.CreationStage) (readbrokerstudy.CreationResult, error) {
	return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, nil
}
func (*blockingCreationSandboxWorker) Terminate() error { return nil }
func (w *blockingCreationSandboxWorker) Wait() error {
	w.joined = true
	w.launcher.joined = true
	return nil
}
func (w *blockingCreationSandboxWorker) Joined() bool                 { return w.joined }
func (*blockingCreationSandboxWorker) WorkerContext() context.Context { return context.Background() }

func (w *testCreationSandboxWorker) Ready(context.Context) error {
	w.launcher.events = append(w.launcher.events, "ready")
	return nil
}
func (*testCreationSandboxWorker) Probe() readbrokerstudy.WorkerProbe {
	return readbrokerstudy.WorkerProbe{}
}
func (*testCreationSandboxWorker) TransferCapability(context.Context, *os.File, readbrokerstudy.FileIdentity) error {
	return readbrokerstudy.ErrProtocol
}
func (*testCreationSandboxWorker) AwaitReadStarted(context.Context) error {
	return readbrokerstudy.ErrProtocol
}
func (*testCreationSandboxWorker) ReadResult(context.Context) (readbrokerstudy.WorkerResult, error) {
	return readbrokerstudy.WorkerResult{}, readbrokerstudy.ErrProtocol
}
func (*testCreationSandboxWorker) Terminate() error { return nil }
func (w *testCreationSandboxWorker) Wait() error {
	w.launcher.events = append(w.launcher.events, "wait")
	w.joined = true
	w.launcher.workerJoined = true
	return nil
}
func (w *testCreationSandboxWorker) Joined() bool                 { return w.joined }
func (*testCreationSandboxWorker) WorkerContext() context.Context { return context.Background() }
func (w *testCreationSandboxWorker) StageCreation(_ context.Context, _ *os.File, _ readbrokerstudy.FileIdentity, stageName, leaf string, content []byte) (readbrokerstudy.CreationStage, error) {
	w.launcher.events = append(w.launcher.events, "stage")
	if stageName == "" || leaf != "new.txt" || string(content) != "approved fixture" {
		return readbrokerstudy.CreationStage{}, readbrokerstudy.ErrProtocol
	}
	_, err := w.launcher.service.AdmitFilesystemRead(context.Background(), w.launcher.invocation, "openai/gpt-test/profile")
	w.launcher.readsBlockedDuringStage = errors.Is(err, ErrCreationBarrier)
	return readbrokerstudy.CreationStage{}, nil
}
func (w *testCreationSandboxWorker) PublishCreation(context.Context, readbrokerstudy.CreationStage) (readbrokerstudy.CreationResult, error) {
	w.launcher.events = append(w.launcher.events, "publish")
	_, err := w.launcher.service.AdmitFilesystemRead(context.Background(), w.launcher.invocation, "openai/gpt-test/profile")
	w.launcher.readsBlockedDuringPublish = errors.Is(err, ErrCreationBarrier)
	return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationCreated, Bytes: uint32(len("approved fixture"))}, nil
}
func (*testCreationSandboxWorker) AbortCreation(context.Context, readbrokerstudy.CreationStage) (readbrokerstudy.CreationResult, error) {
	return readbrokerstudy.CreationResult{Outcome: readbrokerstudy.CreationNotCreated}, nil
}

func TestHostSearchAdmissionRequiresMeasuredProfileAndExt4(t *testing.T) {
	launcher := searchProfileTestWorkerLauncher{profileTestWorkerLauncher{mode: readbrokerstudy.BindModeDescriptor}}
	service, err := newTestHostServiceWithLauncher(launcher)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(context.Background()); err != nil {
			t.Errorf("close service: %v", err)
		}
	})
	root := t.TempDir()
	searchPath := filepath.Join(root, "search-scope")
	if err := os.Mkdir(searchPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, filepath.Join(t.TempDir(), "auth.json"), nil); err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerSettings, auth.SensitiveClassSettings, filepath.Join(t.TempDir(), "settings.json"), nil); err != nil {
		t.Fatal(err)
	}
	if err := service.InvalidateFilesystemRoot(context.Background(), runtime.FilesystemRootTransition{ConversationID: "search-fixture", Root: root, Generation: 1}); err != nil {
		t.Fatal(err)
	}
	arguments, err := json.Marshal(tools.SearchFilesInput{
		Path: searchPath, Query: "synthetic-query", Recursive: true,
		IncludeHidden: true, IncludeIgnored: true, NameGlobs: []string{"*.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	invocation := tools.Invocation{
		ConversationID: "search-fixture", TurnID: "turn", ProviderCallID: "search-call",
		ToolID: tools.SearchFilesID, Arguments: arguments,
	}
	if !service.SearchFilesAvailable() {
		t.Fatal("measured search worker profile was not exposed by the composed service")
	}
	unsupportedArguments, err := json.Marshal(tools.SearchFilesInput{Path: "/proc", Query: "synthetic-query"})
	if err != nil {
		t.Fatal(err)
	}
	unsupportedInvocation := invocation
	unsupportedInvocation.Arguments = unsupportedArguments
	if _, err := service.AdmitFilesystemRead(context.Background(), unsupportedInvocation, "openai/gpt-test/profile"); !errors.Is(err, readbrokerstudy.ErrUnsupportedFilesystem) {
		t.Fatalf("non-ext4 search admission error=%v, want unsupported filesystem", err)
	}
	var filesystem unix.Statfs_t
	if err := unix.Statfs(searchPath, &filesystem); err != nil {
		t.Fatal(err)
	}
	if uint64(filesystem.Type) != 0xef53 {
		t.Skipf("positive composed-service search admission requires ext4, got filesystem magic %#x", uint64(filesystem.Type))
	}
	admission, err := service.AdmitFilesystemRead(context.Background(), invocation, "openai/gpt-test/profile")
	if err != nil {
		t.Fatalf("admit synthetic search through public Service on ext4: %v", err)
	}
	if admission.Operation != string(tools.SearchFilesID) || admission.Target != searchPath || admission.Search.Query != "synthetic-query" || !admission.Search.Recursive || !admission.Search.IncludeHidden || !admission.Search.IncludeIgnored || len(admission.Search.NameGlobs) != 1 || admission.Reference == nil || admission.AdmissionBinding == "" {
		t.Fatalf("detached search admission metadata is incomplete: %+v", admission)
	}
	if err := service.CloseFilesystemReadAdmission(context.Background(), admission); err != nil {
		t.Fatalf("retire search admission: %v", err)
	}
}

func TestHostAdmissionRefusesRegisteredOwnerRoutesAndAliases(t *testing.T) {
	service, err := newTestHostService()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := service.Close(context.Background()); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	root := t.TempDir()
	authPath := filepath.Join(root, "auth.json")
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	authStore := auth.Store{Path: authPath, Protection: service}
	settingsStore := settings.Store{Path: settingsPath, Protection: service}
	if err := authStore.RegisterProtection(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := authStore.AddAPIKey("fixture", "openai", "synthetic", "synthetic-secret", nil); err != nil {
		t.Fatal(err)
	}
	if err := settingsStore.RegisterProtection(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := settingsStore.SaveContext(context.Background(), settings.Settings{Plugin: settings.DefaultUI}); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "ordinary-looking.json")
	if err := os.Link(authPath, alias); err != nil {
		t.Fatal(err)
	}
	if err := service.InvalidateFilesystemRoot(context.Background(), runtime.FilesystemRootTransition{ConversationID: "protected-fixture", Root: root, Generation: 1}); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{authPath, alias} {
		t.Run(filepath.Base(target), func(t *testing.T) {
			invocation := tools.Invocation{ConversationID: "protected-fixture", TurnID: "turn", ProviderCallID: "call-" + filepath.Base(target), ToolID: tools.ID("read_file"), Arguments: []byte(`{"path":"` + target + `"}`)}
			if _, err := service.AdmitFilesystemRead(context.Background(), invocation, "openai/gpt-test/profile"); err == nil {
				t.Fatal("registered auth route or alias was admitted for ordinary approval")
			}
		})
	}
	impl := service.impl.(*linuxImplementation)
	if len(impl.session.AdmissionConversations()) != 0 {
		t.Fatal("protected refusal retained an admission")
	}
}

func TestHostAdmissionCancellationReturnsWhileLookupCleanupRemainsOwned(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.txt")
	if err := os.WriteFile(target, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	inventory := readbrokerstudy.NewChatCompositionInventory()
	blocking := &serviceAdmissionBlockingBroker{base: readbrokerstudy.NewBroker(registry), entered: make(chan struct{}), release: make(chan struct{})}
	launcher := inertWorkerLauncher{}
	session, err := readbrokerstudy.NewHostCoordinator(registry, blocking, launcher, hostAuthorizer{}, inventory)
	if err != nil {
		t.Fatal(err)
	}
	for _, requirement := range readbrokerstudy.ChatCompositionRequirements() {
		if err := session.DeclareCoverageOwner(requirement); err != nil {
			t.Fatal(err)
		}
	}
	impl := &linuxImplementation{
		registry: registry, launcher: launcher, session: session, inventory: inventory,
		revision: 1, owners: map[string]OwnerCoverage{
			auth.SensitiveOwnerAuth:     {Owner: auth.SensitiveOwnerAuth, Class: auth.SensitiveClassAuth, State: OwnerRequired},
			auth.SensitiveOwnerSettings: {Owner: auth.SensitiveOwnerSettings, Class: auth.SensitiveClassSettings, State: OwnerRequired},
		}, blocked: make(map[string]string),
	}
	service := &Service{impl: impl}
	defer func() {
		blocking.unblock()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil && !errors.Is(err, readbrokerstudy.ErrCleanupPending) {
			t.Errorf("close service: %v", err)
		}
	}()
	if err := service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, filepath.Join(t.TempDir(), "auth.json"), nil); err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerSettings, auth.SensitiveClassSettings, filepath.Join(t.TempDir(), "settings.json"), nil); err != nil {
		t.Fatal(err)
	}
	if err := service.InvalidateFilesystemRoot(context.Background(), runtime.FilesystemRootTransition{ConversationID: "cancel-fixture", Root: root, Generation: 1}); err != nil {
		t.Fatal(err)
	}
	invocation := tools.Invocation{ConversationID: "cancel-fixture", TurnID: "turn", ProviderCallID: "call", ToolID: tools.ID("read_file"), Arguments: []byte(`{"path":"` + target + `"}`)}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := service.AdmitFilesystemRead(ctx, invocation, "openai/gpt-test/profile")
		result <- err
	}()
	select {
	case <-blocking.entered:
	case <-time.After(time.Second):
		t.Fatal("constrained lookup did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled admission error=%v, want context cancellation", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("service admission blocked in cleanup after caller cancellation")
	}
	if conversations := session.AdmissionConversations(); len(conversations) != 1 {
		t.Fatalf("opening cleanup obligation was not retained: %v", conversations)
	}
	blocking.unblock()
	deadline := time.Now().Add(time.Second)
	for len(session.AdmissionConversations()) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if conversations := session.AdmissionConversations(); len(conversations) != 0 {
		t.Fatalf("late lookup did not acknowledge and retire cleanup: %v", conversations)
	}
}

type serviceAdmissionBlockingBroker struct {
	base        readbrokerstudy.ReadBroker
	entered     chan struct{}
	release     chan struct{}
	once        sync.Once
	releaseOnce sync.Once
}

func (b *serviceAdmissionBlockingBroker) PrepareRead(root, target string, generation readbrokerstudy.GenerationSet) (readbrokerstudy.PreparedRead, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return b.base.PrepareRead(root, target, generation)
}

func (b *serviceAdmissionBlockingBroker) Revalidate(prepared readbrokerstudy.PreparedRead) error {
	return b.base.Revalidate(prepared)
}

func (b *serviceAdmissionBlockingBroker) unblock() {
	b.releaseOnce.Do(func() { close(b.release) })
}

func TestPublicationGateSerializesProtectedOwnerRotation(t *testing.T) {
	service, err := newTestHostService()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(context.Background()); err != nil {
			t.Errorf("close service: %v", err)
		}
	})
	for _, owner := range []struct{ name, class string }{{auth.SensitiveOwnerAuth, auth.SensitiveClassAuth}, {auth.SensitiveOwnerSettings, auth.SensitiveClassSettings}} {
		if err := service.Protect(context.Background(), owner.name, owner.class, filepath.Join(t.TempDir(), owner.name+".json"), nil); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	conversation := "publication-gate-fixture"
	if err := service.InvalidateFilesystemRoot(context.Background(), runtime.FilesystemRootTransition{ConversationID: conversation, Root: root, Generation: 1}); err != nil {
		t.Fatal(err)
	}
	impl := service.impl.(*linuxImplementation)
	policy, coverage := impl.registry.View().Generation, impl.inventory.Snapshot().Revision
	if policy == 0 || coverage == 0 {
		t.Fatalf("fixture generations unavailable: policy=%d coverage=%d", policy, coverage)
	}

	entered, release := make(chan struct{}), make(chan struct{})
	handoff := make(chan error, 1)
	go func() {
		handoff <- service.WithFilesystemReadPublicationCurrent(context.Background(), conversation, root, 1, policy, coverage, func() error {
			close(entered)
			<-release
			return nil // deterministic synthetic dispatcher acceptance point
		})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("publication gate did not enter its commit callback")
	}
	if impl.ownerUpdateMu.TryLock() {
		impl.ownerUpdateMu.Unlock()
		t.Fatal("publication callback did not retain the owner-transition ordering gate")
	}
	rotation := make(chan error, 1)
	go func() {
		rotation <- service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, filepath.Join(t.TempDir(), "auth-after-handoff.json"), nil)
	}()
	close(release)
	select {
	case err := <-handoff:
		if err != nil {
			t.Fatalf("handoff: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("publication gate did not release")
	}
	select {
	case err := <-rotation:
		if err != nil {
			t.Fatalf("owner rotation after handoff: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owner rotation did not proceed after handoff")
	}
	called := false
	if err := service.WithFilesystemReadPublicationCurrent(context.Background(), conversation, root, 1, policy, coverage, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("replay with pre-rotation policy/coverage generations crossed the host gate")
	}
}

func TestPublicationGateRejectsOwnerRotationCompletedBeforeHandoff(t *testing.T) {
	service, err := newTestHostService()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(context.Background()); err != nil {
			t.Errorf("close service: %v", err)
		}
	})
	for _, owner := range []struct{ name, class string }{{auth.SensitiveOwnerAuth, auth.SensitiveClassAuth}, {auth.SensitiveOwnerSettings, auth.SensitiveClassSettings}} {
		if err := service.Protect(context.Background(), owner.name, owner.class, filepath.Join(t.TempDir(), owner.name+".json"), nil); err != nil {
			t.Fatal(err)
		}
	}
	root, conversation := t.TempDir(), "mutation-before-handoff"
	if err := service.InvalidateFilesystemRoot(context.Background(), runtime.FilesystemRootTransition{ConversationID: conversation, Root: root, Generation: 1}); err != nil {
		t.Fatal(err)
	}
	impl := service.impl.(*linuxImplementation)
	policy, coverage := impl.registry.View().Generation, impl.inventory.Snapshot().Revision
	if err := service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, filepath.Join(t.TempDir(), "auth-rotated.json"), nil); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := service.WithFilesystemReadPublicationCurrent(context.Background(), conversation, root, 1, policy, coverage, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("stale owner/policy generations were accepted for publication")
	}
}

func TestProtectedOwnerTransitionRetiresPendingAdmissionBeforeReturning(t *testing.T) {
	service, err := newTestHostService()
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(context.Background())
	if err := service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, filepath.Join(t.TempDir(), "auth.json"), nil); err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerSettings, auth.SensitiveClassSettings, filepath.Join(t.TempDir(), "settings.json"), nil); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.txt")
	if err := os.WriteFile(target, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := service.InvalidateFilesystemRoot(context.Background(), runtime.FilesystemRootTransition{ConversationID: "c", Root: root, Generation: 1}); err != nil {
		t.Fatal(err)
	}
	invocation := tools.Invocation{ConversationID: "c", TurnID: "t", ProviderCallID: "call", ToolID: tools.ID("read_file"), Arguments: []byte(`{"path":"` + target + `"}`)}
	admission, err := service.AdmitFilesystemRead(context.Background(), invocation, "openai/gpt-test/profile")
	if err != nil {
		t.Fatal(err)
	}
	newRoute := filepath.Join(t.TempDir(), "auth-rotated.json")
	if err := service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, newRoute, nil); err != nil {
		t.Fatalf("protected owner transition: %v", err)
	}
	if _, err := service.PrepareAdmittedFilesystemRead(context.Background(), admission, 1); err == nil {
		t.Fatal("owner transition left a stale admission preparable")
	}
	impl := service.impl.(*linuxImplementation)
	if conversations := impl.session.AdmissionConversations(); len(conversations) != 0 {
		t.Fatal("owner transition returned before host admission retirement")
	}
}

func TestFailedOwnerIdentityValidationStaysPendingUntilSuccessfulRetry(t *testing.T) {
	for _, composition := range []string{"coordinator-backed", "backend-unavailable"} {
		for _, failure := range []string{"path-identity-mismatch", "cancellation-after-registration"} {
			t.Run(composition+"/"+failure, func(t *testing.T) {
				var service *Service
				if composition == "coordinator-backed" {
					var err error
					service, err = newTestHostService()
					if err != nil {
						t.Fatal(err)
					}
				} else {
					registry, err := readbrokerstudy.NewRegistry()
					if err != nil {
						t.Fatal(err)
					}
					impl, err := composeLinuxImplementation(registry, nil, readbrokerstudy.ErrUnavailable)
					if err != nil {
						t.Fatal(err)
					}
					service = &Service{impl: impl}
				}
				t.Cleanup(func() {
					if err := service.Close(context.Background()); err != nil {
						t.Errorf("close service: %v", err)
					}
				})

				route := filepath.Join(t.TempDir(), "auth.json")
				if err := service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, route, nil); err != nil {
					t.Fatalf("register initially absent route: %v", err)
				}
				opened, err := os.CreateTemp(filepath.Dir(route), ".auth-replacement-*")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = opened.Close() })
				var protectCtx context.Context = context.Background()
				if failure == "path-identity-mismatch" {
					movedPath := opened.Name() + ".moved"
					if err := os.Rename(opened.Name(), movedPath); err != nil {
						t.Fatal(err)
					}
					replacement, err := os.OpenFile(opened.Name(), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
					if err != nil {
						t.Fatal(err)
					}
					if err := replacement.Close(); err != nil {
						t.Fatal(err)
					}
				} else {
					protectCtx = &cancelAfterErrCalls{Context: context.Background(), cancelAt: 3}
				}
				if err := service.Protect(protectCtx, auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, route, opened); err == nil {
					t.Fatal("owner update unexpectedly completed")
				}
				assertOwnerState(t, service.CoverageSnapshot(), OwnerPending)

				// A fresh replacement at its current pathname is required to clear
				// the failure latch; the previous pathname pin cannot do so.
				fresh, err := os.CreateTemp(filepath.Dir(route), ".auth-retry-*")
				if err != nil {
					t.Fatal(err)
				}
				defer fresh.Close()
				if err := service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, route, fresh); err != nil {
					t.Fatalf("fresh owner registration did not recover coverage: %v", err)
				}
				assertOwnerState(t, service.CoverageSnapshot(), OwnerRegistered)
			})
		}
	}
}

type cancelAfterErrCalls struct {
	context.Context
	cancelAt int
	calls    int
}

func (c *cancelAfterErrCalls) Err() error {
	c.calls++
	if c.calls >= c.cancelAt {
		return context.Canceled
	}
	return nil
}

func assertOwnerState(t *testing.T, snapshot CoverageSnapshot, want OwnerState) {
	t.Helper()
	for _, owner := range snapshot.Owners {
		if owner.Owner == auth.SensitiveOwnerAuth {
			if owner.State != want {
				t.Fatalf("auth coverage state=%q want=%q snapshot=%+v", owner.State, want, snapshot)
			}
			return
		}
	}
	t.Fatalf("auth owner missing from coverage snapshot: %+v", snapshot)
}

func newTestHostService() (*Service, error) {
	return newTestHostServiceWithLauncher(inertWorkerLauncher{})
}

func newTestHostServiceWithLauncher(launcher readbrokerstudy.WorkerLauncher) (*Service, error) {
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		return nil, err
	}
	impl, err := composeLinuxImplementation(registry, launcher, nil)
	if err != nil {
		return nil, err
	}
	return &Service{impl: impl}, nil
}

type profileTestWorkerLauncher struct {
	inertWorkerLauncher
	mode readbrokerstudy.BindMode
}

func (launcher profileTestWorkerLauncher) AdvancedDirectoryListingProfileAvailable() bool {
	return goruntime.GOARCH == "amd64" && launcher.mode == readbrokerstudy.BindModeDescriptor
}

type searchProfileTestWorkerLauncher struct {
	profileTestWorkerLauncher
}

func (launcher searchProfileTestWorkerLauncher) SearchProfilesAvailable() bool {
	return goruntime.GOARCH == "amd64" && launcher.mode == readbrokerstudy.BindModeDescriptor
}

func TestInvalidateReadExecutionIsNoopWithoutBackendSession(t *testing.T) {
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	impl, err := composeLinuxImplementation(registry, nil, readbrokerstudy.ErrUnavailable)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{impl: impl}
	t.Cleanup(func() {
		if err := service.Close(context.Background()); err != nil {
			t.Errorf("close service: %v", err)
		}
	})
	if err := service.InvalidateFilesystemReadExecutions("conversation"); err != nil {
		t.Fatalf("service with no backend session should have no work to invalidate: %v", err)
	}
	chat := runtime.NewChatWithToolRuntime(nil, auth.Store{}, nil, runtime.ToolRuntimeConfig{Registry: tools.NewRegistry(), ReadAdmission: service})
	if err := chat.RevokeToolGrant("missing"); !errors.Is(err, permissions.ErrGrantNotFound) {
		t.Fatalf("revoke without a grant should report permission state, not backend unavailability: %v", err)
	}
	if count, err := chat.ClearToolGrants(); count != 0 || err != nil {
		t.Fatalf("clear with unavailable read backend: count=%d err=%v", count, err)
	}
}

func TestStage4RuntimeExecutesRetainedObjectIntoPrivateCapture(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "notes.txt")
	want := []byte("retained original bytes\n")
	if err := os.WriteFile(target, want, 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := &stage4WorkerLauncher{}
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	impl, err := composeLinuxImplementation(registry, launcher, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{impl: impl}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Errorf("close service: %v", err)
		}
	})
	chat := runtime.NewChatWithToolRuntime(nil, auth.Store{}, nil, runtime.ToolRuntimeConfig{
		Registry: tools.NewRegistry(), ReadAdmission: service, RootHost: service,
		Approval: runtime.ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			if err := os.Rename(target, target+".moved"); err != nil {
				return "", "", err
			}
			if err := os.WriteFile(target, []byte("replacement object must not be read"), 0o600); err != nil {
				return "", "", err
			}
			return permissions.ApprovalOnce, "", nil
		}),
	})
	if err := chat.SelectFilesystemRoot(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, filepath.Join(t.TempDir(), "auth.json"), nil); err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerSettings, auth.SensitiveClassSettings, filepath.Join(t.TempDir(), "settings.json"), nil); err != nil {
		t.Fatal(err)
	}
	arguments, err := json.Marshal(struct {
		Path string `json:"path"`
	}{target})
	if err != nil {
		t.Fatal(err)
	}
	capture, err := chat.ExecuteFilesystemRead(context.Background(), tools.Invocation{TurnID: "turn-1", ProviderCallID: "call-1", ToolID: "read_file", Arguments: arguments}, "openai/gpt-test/profile-test")
	if err != nil || capture == nil {
		t.Fatalf("retained execution capture=%v err=%v", capture, err)
	}
	if launcher.starts.Load() != 1 || string(launcher.readBytes) != string(want) {
		t.Fatalf("worker starts=%d read=%q want exact retained bytes %q", launcher.starts.Load(), launcher.readBytes, want)
	}
	if len(impl.session.PublishedResults()) != 0 || len(chat.Snapshot().Turns) != 0 {
		t.Fatal("Stage 4 capture escaped into study publication or chat transcript")
	}
	if err := chat.DiscardPrivateReadCapture(capture); err != nil {
		t.Fatalf("discard private capture: %v", err)
	}
	if err := chat.CloseContext(context.Background()); err != nil {
		t.Fatalf("close chat: %v", err)
	}
}

func TestStage5ServiceOwnerRotationRetiresCaptureAfterAdmissionCloses(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.txt")
	if err := os.WriteFile(target, []byte("synthetic owner rotation capture"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := &stage4WorkerLauncher{}
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	impl, err := composeLinuxImplementation(registry, launcher, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{impl: impl}
	chat := runtime.NewChatWithToolRuntime(nil, auth.Store{}, nil, runtime.ToolRuntimeConfig{
		Registry: tools.NewRegistry(), ReadAdmission: service, RootHost: service,
		Approval: runtime.ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			return permissions.ApprovalOnce, "", nil
		}),
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := chat.CloseContext(ctx); err != nil {
			t.Errorf("close chat: %v", err)
		}
		if err := service.Close(ctx); err != nil {
			t.Errorf("close service: %v", err)
		}
	})
	if err := chat.SelectFilesystemRoot(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []struct{ name, class string }{{auth.SensitiveOwnerAuth, auth.SensitiveClassAuth}, {auth.SensitiveOwnerSettings, auth.SensitiveClassSettings}} {
		if err := service.Protect(context.Background(), owner.name, owner.class, filepath.Join(t.TempDir(), owner.name+".json"), nil); err != nil {
			t.Fatal(err)
		}
	}
	arguments, err := json.Marshal(struct {
		Path string `json:"path"`
	}{target})
	if err != nil {
		t.Fatal(err)
	}
	capture, err := chat.ExecuteFilesystemRead(context.Background(), tools.Invocation{TurnID: "stage5-turn", ProviderCallID: "stage5-capture", ToolID: "read_file", Arguments: arguments}, "openai/model/profile")
	if err != nil || capture == nil {
		t.Fatalf("capture=%v err=%v", capture, err)
	}
	if len(impl.session.AdmissionConversations()) != 0 {
		t.Fatal("fixture admission remained open after capture")
	}
	if len(launcher.readBytes) == 0 {
		t.Fatal("retained worker did not produce synthetic bytes")
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, filepath.Join(t.TempDir(), "auth-rotated.json"), nil); err != nil {
		t.Fatalf("rotate protected owner after capture: %v", err)
	}
	if err := chat.DiscardPrivateReadCapture(capture); err == nil {
		t.Fatal("owner rotation left the post-admission private capture eligible")
	}
}

func TestStage5OwnerRotationAfterCaptureRetiresClosedAdmissionContinuation(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.txt")
	if err := os.WriteFile(target, []byte("synthetic bytes retained only in private capture"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := &stage4WorkerLauncher{}
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	impl, err := composeLinuxImplementation(registry, launcher, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{impl: impl}
	chat := runtime.NewChatWithToolRuntime(nil, auth.Store{}, nil, runtime.ToolRuntimeConfig{
		Registry: tools.NewRegistry(), ReadAdmission: service, RootHost: service,
		Approval: runtime.ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			return permissions.ApprovalOnce, "", nil
		}),
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := chat.CloseContext(ctx); err != nil {
			t.Errorf("close chat: %v", err)
		}
		if err := service.Close(ctx); err != nil {
			t.Errorf("close service: %v", err)
		}
	})
	if err := chat.SelectFilesystemRoot(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []struct{ name, class string }{{auth.SensitiveOwnerAuth, auth.SensitiveClassAuth}, {auth.SensitiveOwnerSettings, auth.SensitiveClassSettings}} {
		if err := service.Protect(context.Background(), owner.name, owner.class, filepath.Join(t.TempDir(), owner.name+".json"), nil); err != nil {
			t.Fatal(err)
		}
	}
	arguments, err := json.Marshal(struct {
		Path string `json:"path"`
	}{target})
	if err != nil {
		t.Fatal(err)
	}
	capture, err := chat.ExecuteFilesystemRead(context.Background(), tools.Invocation{TurnID: "stage5-turn", ProviderCallID: "stage5-capture", ToolID: "read_file", Arguments: arguments}, "openai/model/profile")
	if err != nil || capture == nil {
		t.Fatalf("capture=%v err=%v", capture, err)
	}
	if len(impl.session.AdmissionConversations()) != 0 {
		t.Fatal("fixture admission remained open after capture")
	}
	if len(launcher.readBytes) == 0 {
		t.Fatal("retained worker did not produce synthetic bytes")
	}
	policy, coverage := impl.registry.View().Generation, impl.inventory.Snapshot().Revision
	service.rootMu.Lock()
	conversation, rootGeneration := service.rootConversationID, service.rootGeneration
	service.rootMu.Unlock()
	handoffAccepted := false
	if err := service.WithFilesystemReadPublicationCurrent(context.Background(), conversation, root, rootGeneration, policy, coverage, func() error {
		handoffAccepted = true // stands for the runtime's accepted dispatcher handoff
		return nil
	}); err != nil || !handoffAccepted {
		t.Fatalf("post-capture handoff gate accepted=%v err=%v", handoffAccepted, err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, filepath.Join(t.TempDir(), "auth-rotated.json"), nil); err != nil {
		t.Fatalf("rotate protected owner after completed admission: %v", err)
	}
	if err := chat.DiscardPrivateReadCapture(capture); err == nil {
		t.Fatal("owner rotation left a post-admission private capture eligible")
	}
	replayReachedGate := false
	if err := service.WithFilesystemReadPublicationCurrent(context.Background(), conversation, root, rootGeneration, policy, coverage, func() error { replayReachedGate = true; return nil }); err == nil || replayReachedGate {
		t.Fatal("owner rotation allowed a stale-generation continuation handoff")
	}
}

func TestStage4TruncatedWorkerResponseIsNotStagedAsComplete(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "truncated.txt")
	if err := os.WriteFile(target, []byte("fixture prefix only"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := &stage4WorkerLauncher{truncated: true}
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	impl, err := composeLinuxImplementation(registry, launcher, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{impl: impl}
	chat := runtime.NewChatWithToolRuntime(nil, auth.Store{}, nil, runtime.ToolRuntimeConfig{Registry: tools.NewRegistry(), ReadAdmission: service, RootHost: service,
		Approval: runtime.ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			return permissions.ApprovalOnce, "", nil
		}),
	})
	if err := chat.SelectFilesystemRoot(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, filepath.Join(t.TempDir(), "auth.json"), nil); err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerSettings, auth.SensitiveClassSettings, filepath.Join(t.TempDir(), "settings.json"), nil); err != nil {
		t.Fatal(err)
	}
	arguments, err := json.Marshal(struct {
		Path string `json:"path"`
	}{target})
	if err != nil {
		t.Fatal(err)
	}
	capture, err := chat.ExecuteFilesystemRead(context.Background(), tools.Invocation{TurnID: "turn", ProviderCallID: "truncated-call", ToolID: "read_file", Arguments: arguments}, "openai/model/profile")
	if err == nil || capture != nil {
		t.Fatalf("truncated worker response became a complete private capture: capture=%v err=%v", capture, err)
	}
	if string(launcher.readBytes) != "fixture prefix only" || impl.session.ActiveOperationCount() != 0 || len(impl.session.PublishedResults()) != 0 {
		t.Fatalf("truncated response handling lost read evidence or leaked content: bytes=%q active=%d published=%d", launcher.readBytes, impl.session.ActiveOperationCount(), len(impl.session.PublishedResults()))
	}
	evidence := impl.session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadPartial {
		t.Fatalf("truncated response outcome evidence=%+v, want partial", evidence)
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := chat.CloseContext(cleanupCtx); err != nil {
		t.Fatalf("chat shutdown: %v", err)
	}
	if err := service.Close(cleanupCtx); err != nil {
		t.Fatalf("service shutdown: %v", err)
	}
}

func TestStage4RevocationWhileCapabilityHeldPreventsReadRelease(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "held.txt")
	if err := os.WriteFile(target, []byte("must remain unread"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := &stage4WorkerLauncher{}
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	impl, err := composeLinuxImplementation(registry, launcher, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{impl: impl}
	chat := runtime.NewChatWithToolRuntime(nil, auth.Store{}, nil, runtime.ToolRuntimeConfig{Registry: tools.NewRegistry(), ReadAdmission: service, RootHost: service,
		Approval: runtime.ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			return permissions.ApprovalOnce, "", nil
		}),
	})
	if err := chat.SelectFilesystemRoot(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, filepath.Join(t.TempDir(), "auth.json"), nil); err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerSettings, auth.SensitiveClassSettings, filepath.Join(t.TempDir(), "settings.json"), nil); err != nil {
		t.Fatal(err)
	}
	held := make(chan struct{})
	resume := make(chan struct{})
	impl.session.SetCheckpointHook(func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint == readbrokerstudy.CheckpointAfterCapabilityHeld {
			close(held)
			<-resume
		}
		return nil
	})
	arguments, err := json.Marshal(struct {
		Path string `json:"path"`
	}{target})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan struct {
		capture *runtime.PrivateReadCapture
		err     error
	}, 1)
	go func() {
		capture, execErr := chat.ExecuteFilesystemRead(context.Background(), tools.Invocation{TurnID: "turn", ProviderCallID: "held-call", ToolID: "read_file", Arguments: arguments}, "openai/model/profile")
		result <- struct {
			capture *runtime.PrivateReadCapture
			err     error
		}{capture, execErr}
	}()
	select {
	case <-held:
	case <-time.After(3 * time.Second):
		t.Fatal("worker never reached held capability checkpoint")
	}
	if count, err := chat.ClearToolGrants(); count != 0 || err == nil {
		t.Fatalf("empty-store revocation did not report pending worker cleanup: count=%d err=%v", count, err)
	}
	close(resume)
	select {
	case completed := <-result:
		if completed.err == nil || completed.capture != nil {
			t.Fatalf("revoked held read staged a capture: capture=%v err=%v", completed.capture, completed.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("revoked held worker did not settle within the bounded wait")
	}
	settleDeadline := time.Now().Add(3 * time.Second)
	for impl.session.ActiveOperationCount() != 0 && time.Now().Before(settleDeadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if launcher.starts.Load() != 1 || len(launcher.readBytes) != 0 || impl.session.ActiveOperationCount() != 0 {
		t.Fatalf("revoked held operation started/read/leaked: starts=%d bytes=%q active=%d", launcher.starts.Load(), launcher.readBytes, impl.session.ActiveOperationCount())
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := chat.CloseContext(cleanupCtx); err != nil {
		t.Fatalf("chat shutdown after joined cancellation: %v", err)
	}
	if err := service.Close(cleanupCtx); err != nil {
		t.Fatalf("service shutdown after joined cancellation: %v", err)
	}
}

func TestStage4RevocationAfterWorkerCapturePreventsPrivateStaging(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "captured.txt")
	if err := os.WriteFile(target, []byte("captured but must not be staged"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := &stage4WorkerLauncher{}
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	impl, err := composeLinuxImplementation(registry, launcher, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{impl: impl}
	chat := runtime.NewChatWithToolRuntime(nil, auth.Store{}, nil, runtime.ToolRuntimeConfig{Registry: tools.NewRegistry(), ReadAdmission: service, RootHost: service,
		Approval: runtime.ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			return permissions.ApprovalOnce, "", nil
		}),
	})
	if err := chat.SelectFilesystemRoot(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, filepath.Join(t.TempDir(), "auth.json"), nil); err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerSettings, auth.SensitiveClassSettings, filepath.Join(t.TempDir(), "settings.json"), nil); err != nil {
		t.Fatal(err)
	}
	captured := make(chan struct{})
	resume := make(chan struct{})
	impl.session.SetCheckpointHook(func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint == readbrokerstudy.CheckpointBeforePublish {
			close(captured)
			<-resume
		}
		return nil
	})
	arguments, err := json.Marshal(struct {
		Path string `json:"path"`
	}{target})
	if err != nil {
		t.Fatal(err)
	}
	type execution struct {
		capture *runtime.PrivateReadCapture
		err     error
	}
	result := make(chan execution, 1)
	go func() {
		capture, execErr := chat.ExecuteFilesystemRead(context.Background(), tools.Invocation{TurnID: "turn", ProviderCallID: "captured-call", ToolID: "read_file", Arguments: arguments}, "openai/model/profile")
		result <- execution{capture: capture, err: execErr}
	}()
	select {
	case <-captured:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not reach post-read/pre-staging checkpoint")
	}
	if count, err := chat.ClearToolGrants(); count != 0 || err != nil {
		t.Fatalf("revocation after capture did not retire already joined worker: count=%d err=%v", count, err)
	}
	close(resume)
	select {
	case completed := <-result:
		if completed.err == nil || completed.capture != nil {
			t.Fatalf("revoked post-read operation staged a private capture: capture=%v err=%v", completed.capture, completed.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("post-capture revocation did not settle within the bounded wait")
	}
	if len(launcher.readBytes) == 0 || impl.session.ActiveOperationCount() != 0 {
		t.Fatalf("expected read to have completed before revocation and no active operation: bytes=%q active=%d", launcher.readBytes, impl.session.ActiveOperationCount())
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := chat.CloseContext(cleanupCtx); err != nil {
		t.Fatalf("chat shutdown after post-capture revocation: %v", err)
	}
	if err := service.Close(cleanupCtx); err != nil {
		t.Fatalf("service shutdown after post-capture revocation: %v", err)
	}
}

func TestStage4CancelledStalledJoinReturnsCleanupPendingAndRetainsPreparation(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "stalled.txt")
	if err := os.WriteFile(target, []byte("read result awaits process join"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := &stage4WorkerLauncher{waitEntered: make(chan struct{}), waitRelease: make(chan struct{})}
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	impl, err := composeLinuxImplementation(registry, launcher, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{impl: impl}
	chat := runtime.NewChatWithToolRuntime(nil, auth.Store{}, nil, runtime.ToolRuntimeConfig{Registry: tools.NewRegistry(), ReadAdmission: service, RootHost: service,
		Approval: runtime.ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			return permissions.ApprovalOnce, "", nil
		}),
	})
	if err := chat.SelectFilesystemRoot(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, filepath.Join(t.TempDir(), "auth.json"), nil); err != nil {
		t.Fatal(err)
	}
	if err := service.Protect(context.Background(), auth.SensitiveOwnerSettings, auth.SensitiveClassSettings, filepath.Join(t.TempDir(), "settings.json"), nil); err != nil {
		t.Fatal(err)
	}
	arguments, err := json.Marshal(struct {
		Path string `json:"path"`
	}{target})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	type execution struct {
		capture *runtime.PrivateReadCapture
		err     error
	}
	result := make(chan execution, 1)
	go func() {
		capture, execErr := chat.ExecuteFilesystemRead(ctx, tools.Invocation{TurnID: "turn", ProviderCallID: "stalled-call", ToolID: "read_file", Arguments: arguments}, "openai/model/profile")
		result <- execution{capture: capture, err: execErr}
	}()
	select {
	case <-launcher.waitEntered:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("worker did not enter the deliberately stalled Wait")
	}
	cancel()
	var completed execution
	select {
	case completed = <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled execution remained blocked in worker.Wait")
	}
	if completed.capture != nil || !errors.Is(completed.err, readbrokerstudy.ErrCleanupPending) {
		t.Fatalf("stalled join should return cleanup-pending without capture: capture=%v err=%v", completed.capture, completed.err)
	}
	if impl.session.ActiveOperationCount() != 1 {
		t.Fatalf("unjoined worker obligation was dropped: active=%d", impl.session.ActiveOperationCount())
	}
	if err := service.InvalidateFilesystemReadExecutions("conversation"); !errors.Is(err, readbrokerstudy.ErrCleanupPending) {
		t.Fatalf("revocation should retain/report unjoined worker: %v", err)
	}
	close(launcher.waitRelease)
	deadline := time.Now().Add(3 * time.Second)
	for impl.session.ActiveOperationCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if active := impl.session.ActiveOperationCount(); active != 0 {
		t.Fatalf("late worker join did not settle retained operation: active=%d", active)
	}
	if err := service.InvalidateFilesystemReadExecutions("conversation"); err != nil {
		t.Fatalf("bounded cleanup retry after confirmed join: %v", err)
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cleanupCancel()
	if err := chat.CloseContext(cleanupCtx); err != nil {
		t.Fatalf("chat shutdown after bounded cleanup retry: %v", err)
	}
	if err := service.Close(cleanupCtx); err != nil {
		t.Fatalf("service shutdown after bounded cleanup retry: %v", err)
	}
}

func TestStage4CancellationBeforeHeldReadReleasePreservesUnknownUntilDelayedJoin(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "held-cancel.txt")
	if err := os.WriteFile(target, []byte("must remain unread before held-read release"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := &stage4WorkerLauncher{waitEntered: make(chan struct{}), waitRelease: make(chan struct{})}
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	impl, err := composeLinuxImplementation(registry, launcher, nil)
	if err != nil {
		t.Fatal(err)
	}
	session := impl.session
	service := &Service{impl: impl}
	chat := runtime.NewChatWithToolRuntime(nil, auth.Store{}, nil, runtime.ToolRuntimeConfig{Registry: tools.NewRegistry(), ReadAdmission: service, RootHost: service,
		Approval: runtime.ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			return permissions.ApprovalOnce, "", nil
		}),
	})
	var releaseOnce sync.Once
	releaseWait := func() { releaseOnce.Do(func() { close(launcher.waitRelease) }) }
	t.Cleanup(func() {
		releaseWait()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := chat.CloseContext(ctx); err != nil {
			t.Errorf("close delayed-join chat: %v", err)
		}
		if err := service.Close(ctx); err != nil {
			t.Errorf("close delayed-join service: %v", err)
		}
	})
	if err := chat.SelectFilesystemRoot(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []struct{ name, class string }{{auth.SensitiveOwnerAuth, auth.SensitiveClassAuth}, {auth.SensitiveOwnerSettings, auth.SensitiveClassSettings}} {
		if err := service.Protect(context.Background(), owner.name, owner.class, filepath.Join(t.TempDir(), owner.name+".json"), nil); err != nil {
			t.Fatalf("register %s owner: %v", owner.name, err)
		}
	}
	arguments, err := json.Marshal(struct {
		Path string `json:"path"`
	}{target})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	impl.session.SetCheckpointHook(func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint == readbrokerstudy.CheckpointAfterCapabilityHeld {
			cancel()
			return context.Canceled
		}
		return nil
	})
	type execution struct {
		capture *runtime.PrivateReadCapture
		err     error
	}
	result := make(chan execution, 1)
	go func() {
		capture, executeErr := chat.ExecuteFilesystemRead(ctx, tools.Invocation{TurnID: "turn", ProviderCallID: "held-cancel", ToolID: "read_file", Arguments: arguments}, "openai/model/profile")
		result <- execution{capture: capture, err: executeErr}
	}()
	select {
	case <-launcher.waitEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled held read did not reach the deliberately delayed worker join")
	}
	var initial execution
	select {
	case initial = <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled execution blocked instead of reporting incomplete cleanup")
	}
	if initial.capture != nil || !errors.Is(initial.err, readbrokerstudy.ErrCleanupPending) {
		t.Fatalf("unjoined held read capture=%v err=%v, want cleanup pending without captured data", initial.capture, initial.err)
	}
	if impl.session.ActiveOperationCount() != 1 || launcher.starts.Load() != 1 || len(launcher.readBytes) != 0 || len(impl.session.PublishedResults()) != 0 {
		t.Fatalf("unjoined held read was not retained safely: active=%d starts=%d read=%q published=%d", impl.session.ActiveOperationCount(), launcher.starts.Load(), launcher.readBytes, len(impl.session.PublishedResults()))
	}
	evidence := impl.session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadUnknown || evidence[0].WorkerJoined || !evidence[0].CapabilityHeld || !evidence[0].CapabilitySent || evidence[0].ReadStarted || evidence[0].Published {
		t.Fatalf("initial delayed-join evidence=%+v, want unknown with held capability and no read/publication", evidence)
	}

	// The supported shutdown API uses an independent bounded context after the
	// caller's cancelled operation has returned. Joining later retires the
	// obligation but must not retroactively turn the unknown result into a
	// cancelled result.
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cleanupCancel()
	releaseWait()
	for {
		err := chat.CloseContext(cleanupCtx)
		if err == nil {
			break
		}
		if !errors.Is(err, runtime.ErrFilesystemRootPending) {
			t.Fatalf("bounded shutdown failed while joining the held worker: %v", err)
		}
		select {
		case <-cleanupCtx.Done():
			t.Fatalf("bounded shutdown did not join the held worker: %v", errors.Join(err, cleanupCtx.Err()))
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := service.Close(cleanupCtx); err != nil {
		t.Fatalf("bounded service shutdown did not drain the worker: %v", err)
	}
	if active := session.ActiveOperationCount(); active != 0 {
		t.Fatalf("independent bounded cleanup did not observe the worker join: active=%d", active)
	}
	evidence = session.Evidence()
	if len(evidence) != 1 || evidence[0].Outcome != readbrokerstudy.ReadUnknown || evidence[0].WorkerJoined || !evidence[0].CapabilityHeld || evidence[0].ReadStarted || evidence[0].Published {
		t.Fatalf("post-join evidence=%+v, want preserved initial unknown observation without read/publication", evidence)
	}
	launcher.mu.Lock()
	worker := launcher.lastWorker
	launcher.mu.Unlock()
	if worker == nil || !worker.Joined() {
		t.Fatal("bounded service shutdown returned without confirming the delayed worker join")
	}
}

type inertWorkerLauncher struct{}

func (inertWorkerLauncher) Start(context.Context) (readbrokerstudy.SandboxWorker, error) {
	return nil, readbrokerstudy.ErrUnavailable
}

func (inertWorkerLauncher) Close() error { return nil }

type stage4WorkerLauncher struct {
	starts      atomic.Int32
	readBytes   []byte
	waitEntered chan struct{}
	waitRelease chan struct{}
	truncated   bool
	mu          sync.Mutex
	lastWorker  *stage4Worker
}

func (l *stage4WorkerLauncher) Start(context.Context) (readbrokerstudy.SandboxWorker, error) {
	l.starts.Add(1)
	worker := &stage4Worker{launcher: l}
	l.mu.Lock()
	l.lastWorker = worker
	l.mu.Unlock()
	return worker, nil
}
func (*stage4WorkerLauncher) Close() error { return nil }

type stage4Worker struct {
	launcher   *stage4WorkerLauncher
	capability *os.File
	data       []byte
	joined     bool
	mu         sync.Mutex
	waitOnce   sync.Once
	released   bool
	terminated bool
}

func (*stage4Worker) Ready(context.Context) error { return nil }
func (*stage4Worker) Probe() readbrokerstudy.WorkerProbe {
	return readbrokerstudy.WorkerProbe{NoNewPrivs: true, SeccompMode: 2, SocketCreationDenied: true, PathOpenDenied: true, ReadCapabilityInitiallyAbsent: true, OnlyProtocolDescriptor: true, EnvironmentCleared: true}
}
func (w *stage4Worker) TransferCapability(ctx context.Context, file *os.File, identity readbrokerstudy.FileIdentity) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return err
	}
	if uint64(stat.Dev) != identity.Device || stat.Ino != identity.Inode || uint32(stat.Mode&unix.S_IFMT) != identity.Type {
		return readbrokerstudy.ErrStaleReceipt
	}
	fd, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return err
	}
	w.capability = os.NewFile(uintptr(fd), "stage4-test-worker-capability")
	if w.capability == nil {
		_ = unix.Close(fd)
		return readbrokerstudy.ErrProtocol
	}
	return nil
}
func (w *stage4Worker) AwaitReadStarted(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	released := w.released
	capability := w.capability
	w.mu.Unlock()
	if capability == nil || !released {
		return readbrokerstudy.ErrProtocol
	}
	w.data, _ = io.ReadAll(io.NewSectionReader(capability, 0, readbrokerstudy.MaxReadBytes+1))
	if len(w.data) > readbrokerstudy.MaxReadBytes {
		return readbrokerstudy.ErrResultTooLarge
	}
	w.launcher.readBytes = append([]byte(nil), w.data...)
	return nil
}
func (w *stage4Worker) ReadResult(context.Context) (readbrokerstudy.WorkerResult, error) {
	return readbrokerstudy.WorkerResult{Outcome: readbrokerstudy.ReadSucceeded, Bytes: append([]byte(nil), w.data...), Truncated: w.launcher.truncated}, nil
}
func (w *stage4Worker) AwaitCapabilityHeld(ctx context.Context) error { return ctx.Err() }
func (w *stage4Worker) ReleaseHeldRead(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.terminated || w.released {
		return readbrokerstudy.ErrProtocol
	}
	w.released = true
	return nil
}
func (w *stage4Worker) Terminate() error {
	w.mu.Lock()
	w.terminated = true
	w.mu.Unlock()
	return nil
}
func (w *stage4Worker) Wait() error {
	if w.launcher.waitEntered != nil {
		w.waitOnce.Do(func() { close(w.launcher.waitEntered) })
		<-w.launcher.waitRelease
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.capability != nil {
		_ = w.capability.Close()
		w.capability = nil
	}
	w.joined = true
	return nil
}
func (w *stage4Worker) Joined() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.joined
}
