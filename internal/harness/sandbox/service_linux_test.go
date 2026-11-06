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
