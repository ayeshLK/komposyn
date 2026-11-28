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
	"github.com/ayeshLK/komposyn/internal/harness/model"
	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	"github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
	"github.com/ayeshLK/komposyn/internal/harness/runtime"
	provider "github.com/ayeshLK/komposyn/plugins/model-provider"
	"github.com/ayeshLK/komposyn/plugins/tools"
	"golang.org/x/sys/unix"
)

func TestRewritePrototypeTargetCheckRefusesMissingAndSubstitutedName(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "target")
	if err := os.WriteFile(path, []byte("approved base"), 0o600); err != nil {
		t.Fatal(err)
	}
	parent, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		t.Fatal(err)
	}
	expected := identityFromUnixStat(&stat)
	pinned, err := openRewriteTarget(parent, "target", expected)
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	revision := readbrokerstudy.RewriteFileRevision(pinned)
	if revision == "" {
		t.Skip("fixture filesystem lacks the required statx revision fields")
	}
	if err := os.Rename(path, path+".moved"); err != nil {
		t.Fatal(err)
	}
	if _, err := openRewriteTarget(parent, "target", expected); !errors.Is(err, errRewriteTargetMissing) {
		t.Fatalf("missing target: %v", err)
	}
	if err := os.WriteFile(path, []byte("incumbent"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openRewriteTarget(parent, "target", expected); !errors.Is(err, errRewriteTargetChanged) {
		t.Fatalf("substituted target: %v", err)
	}
	file, err := openPinnedRewriteFile(pinned)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := compareRewriteBytes(file, []byte("approved base")); err != nil {
		t.Fatalf("retained file has wrong contents: %v", err)
	}
	if readbrokerstudy.RewriteFileRevision(file) == revision {
		t.Fatal("rename did not invalidate the observed read revision")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "incumbent" {
		t.Fatalf("substitute contents changed: %q, %v", got, err)
	}
}

type rewriteReadFixtureLauncher struct{ stage4WorkerLauncher }

func (*rewriteReadFixtureLauncher) CreationProfileAvailable() bool { return true }

func TestRewritePrototypeComposedReadAndApprovalRefuseRenamedTarget(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	const base = "original contents"
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := newTestHostServiceWithLauncher(&rewriteReadFixtureLauncher{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	captureTestStartupScope(t, service, root)
	for owner, class := range map[string]string{auth.SensitiveOwnerAuth: auth.SensitiveClassAuth, auth.SensitiveOwnerSettings: auth.SensitiveClassSettings} {
		if err := service.Protect(ctx, owner, class, filepath.Join(t.TempDir(), owner+".json"), nil); err != nil {
			t.Fatal(err)
		}
	}
	fixture := &creationBarrierCaptureProvider{path: target, callID: "approved-read", want: base, barrierRetired: true}
	providers, err := model.NewRegistry(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var destination string
	approvals := 0
	config := runtime.BundledConversationToolRuntimeConfigWithFilesystemRead(runtime.ToolApprovalFunc(func(_ context.Context, pending permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
		approvals++
		destination = pending.Request.Destination
		if approvals == 2 {
			if err := os.Rename(target, target+".moved"); err != nil {
				t.Fatal(err)
			}
		}
		return permissions.ApprovalOnce, "", nil
	}), runtime.ToolClarificationFunc(func(context.Context, runtime.ClarificationRequest) (runtime.ClarificationResponse, error) {
		return runtime.ClarificationResponse{}, errors.New("unexpected clarification")
	}), service)
	config.EnableRewritePrototype = true
	chat := runtime.NewChatWithToolRuntime(providers, auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}, nil, config)
	t.Cleanup(chat.Close)
	if err := chat.AddProfile(ctx, "profile", string(provider.OpenAI), "Fixture", "synthetic credential", nil); err != nil {
		t.Fatal(err)
	}
	discovery := chat.Discover(ctx)
	if len(discovery.Models) != 1 {
		t.Fatal("fixture model unavailable")
	}
	if err := chat.Select(ctx, discovery.Models[0].Key(), false); err != nil {
		t.Fatal(err)
	}
	if err := chat.SelectFilesystemRoot(ctx, root); err != nil {
		t.Fatal(err)
	}
	turn, err := chat.Send(ctx, "read synthetic fixture", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted || len(turn.ToolActivities) != 1 || turn.ToolActivities[0].Outcome != tools.OutcomeSucceeded {
		t.Fatalf("approved read failed: %+v, %v", turn, err)
	}
	result, err := chat.RunRewritePrototype(ctx, runtime.RewritePrototypeRequest{
		ReadCallID: fixture.callID, Destination: destination, Base: base, Content: "new",
	})
	if approvals != 2 || !errors.Is(err, errRewriteTargetMissing) || result.Outcome != tools.OutcomeNotExecuted || result.ErrorCode != "rewrite_target_missing" {
		t.Fatalf("rename after review: approvals=%d, result=%+v, err=%v", approvals, result, err)
	}
	if got, err := os.ReadFile(target + ".moved"); err != nil || string(got) != base {
		t.Fatalf("moved file changed: %q, %v", got, err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("original name should remain missing: %v", err)
	}
}
