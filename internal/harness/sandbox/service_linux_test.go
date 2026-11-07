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
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	"github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
	"github.com/ayeshLK/komposyn/internal/harness/runtime"
	"github.com/ayeshLK/komposyn/internal/harness/settings"
	"github.com/ayeshLK/komposyn/plugins/tools"
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

type inertWorkerLauncher struct{}

func (inertWorkerLauncher) Start(context.Context) (readbrokerstudy.SandboxWorker, error) {
	return nil, readbrokerstudy.ErrUnavailable
}

func (inertWorkerLauncher) Close() error { return nil }
