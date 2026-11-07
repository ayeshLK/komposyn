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
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	"github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
	"github.com/ayeshLK/komposyn/internal/harness/runtime"
	"github.com/ayeshLK/komposyn/internal/harness/settings"
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
	registry, err := readbrokerstudy.NewRegistry()
	if err != nil {
		return nil, err
	}
	impl, err := composeLinuxImplementation(registry, inertWorkerLauncher{}, nil)
	if err != nil {
		return nil, err
	}
	return &Service{impl: impl}, nil
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

type inertWorkerLauncher struct{}

func (inertWorkerLauncher) Start(context.Context) (readbrokerstudy.SandboxWorker, error) {
	return nil, readbrokerstudy.ErrUnavailable
}

func (inertWorkerLauncher) Close() error { return nil }

type stage4WorkerLauncher struct {
	starts    atomic.Int32
	readBytes []byte
}

func (l *stage4WorkerLauncher) Start(context.Context) (readbrokerstudy.SandboxWorker, error) {
	l.starts.Add(1)
	return &stage4Worker{launcher: l}, nil
}
func (*stage4WorkerLauncher) Close() error { return nil }

type stage4Worker struct {
	launcher   *stage4WorkerLauncher
	capability *os.File
	data       []byte
	joined     bool
	mu         sync.Mutex
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
	return readbrokerstudy.WorkerResult{Outcome: readbrokerstudy.ReadSucceeded, Bytes: append([]byte(nil), w.data...)}, nil
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
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.capability != nil {
		_ = w.capability.Close()
		w.capability = nil
	}
	w.joined = true
	return nil
}
func (w *stage4Worker) Joined() bool { return w.joined }
