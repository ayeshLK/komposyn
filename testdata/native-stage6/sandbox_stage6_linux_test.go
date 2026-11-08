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
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
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
)

func init() {
	if handled, code := WorkerBootstrap(os.Args[1:]); handled {
		os.Exit(code)
	}
}

type stage6Case struct {
	ID                        string `json:"case_id"`
	Status                    string `json:"status"`
	Reason                    string `json:"reason,omitempty"`
	WorkerStarted             bool   `json:"worker_started"`
	WorkerReady               bool   `json:"worker_ready"`
	WorkerJoined              bool   `json:"worker_joined"`
	CapabilityTransferred     bool   `json:"capability_transferred"`
	ReadStarted               bool   `json:"read_started"`
	ProbePassed               bool   `json:"required_probe_passed"`
	OnceApprovalCount         int32  `json:"once_approval_count"`
	OnceGrantConsumptionCount int    `json:"once_grant_consumption_count"`
	GrantRevisionBefore       uint64 `json:"grant_revision_before"`
	GrantRevisionAfter        uint64 `json:"grant_revision_after"`
	RemainingGrantCount       int    `json:"remaining_grant_count"`
	ProviderRequestCount      int    `json:"provider_request_count"`
	FixtureBytesMatched       bool   `json:"fixture_bytes_matched"`
	DisclosureWithheld        bool   `json:"disclosure_withheld"`
	StaleDataAbsent           bool   `json:"stale_data_absent"`
	CleanupConfirmed          bool   `json:"cleanup_confirmed"`
}

type stage6Report struct {
	SchemaVersion int            `json:"schema_version"`
	SourceCommit  string         `json:"source_commit"`
	RunnerVersion string         `json:"runner_version"`
	OS            string         `json:"os"`
	Architecture  string         `json:"architecture"`
	Kernel        string         `json:"kernel"`
	UID           int            `json:"uid"`
	Backend       map[string]any `json:"backend"`
	LaunchPolicy  map[string]any `json:"launch_policy"`
	Overall       string         `json:"overall_status"`
	Cases         []stage6Case   `json:"cases"`
	Limitations   []string       `json:"limitations"`
}

var stage6 = newStage6Report()

var stage6Mu sync.Mutex

func newStage6Report() stage6Report {
	ids := []string{
		"ordinary_chat_without_root", "positive_exact_read_and_replay", "empty_file_read",
		"protected_auth_refusal", "protected_settings_refusal", "provider_source_owner",
		"incomplete_owner_inventory", "independent_namespace_actor", "expected_once_transitions",
		"captured_batch_revocation", "pre_handoff_owner_invalidation", "post_handoff_owner_invalidation",
		"late_response_and_delta_retirement", "cancellation_and_cleanup", "launch_profile_and_limits",
		"mutation_profile", "shell_profile",
	}
	cases := make([]stage6Case, 0, len(ids))
	for _, id := range ids {
		cases = append(cases, stage6Case{ID: id, Status: "unavailable", Reason: "not exercised by native runner revision 1"})
	}
	return stage6Report{
		SchemaVersion: 1, SourceCommit: os.Getenv("KOMPOSYN_STAGE6_SOURCE_COMMIT"), RunnerVersion: "1",
		OS: "linux", Architecture: stdruntime.GOARCH, UID: os.Getuid(), Overall: "stage6_profile_incomplete",
		Backend: map[string]any{}, LaunchPolicy: map[string]any{}, Cases: cases,
		Limitations: []string{
			"This runner does not enable or register read_file in the shipped executable.",
			"Native acceptance remains incomplete until every required case is measured on the final integrated profile.",
			"Provider-source, mutation, shell, aggregate cgroup quotas, and non-Linux enforcement are not established here.",
		},
	}
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
	data, err := json.MarshalIndent(stage6, "", "  ")
	stage6Mu.Unlock()
	if path := os.Getenv("KOMPOSYN_STAGE6_REPORT"); path != "" && err == nil {
		_ = os.WriteFile(path, append(data, '\n'), 0600)
	}
	os.Exit(code)
}

func allNativeCasesUnavailable(cases []stage6Case) bool {
	for _, item := range cases {
		if item.Status != "unavailable" {
			return false
		}
	}
	return true
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

func stage6Begin(id string) {
	stage6Update(id, func(item *stage6Case) {
		item.Status = "inconclusive"
		item.Reason = "case started; assertions not yet complete"
	})
}

func stage6Pass(id string, reason string) {
	stage6Update(id, func(item *stage6Case) { item.Status, item.Reason = "measured_pass", reason })
}

func stage6Skip(id, reason string) {
	stage6Update(id, func(item *stage6Case) { item.Status, item.Reason = "unavailable", reason })
}

type stage6Provider struct {
	mu              sync.Mutex
	requests        []provider.ChatRequest
	target          string
	blockSecond     bool
	firstToolCall   bool
	secondStarted   chan struct{}
	releaseSecond   chan struct{}
	secondStartOnce sync.Once
}

func (p *stage6Provider) ID() provider.ID { return provider.OpenAI }
func (p *stage6Provider) AuthMethods() []provider.AuthMethod {
	return []provider.AuthMethod{provider.AuthAPIKey}
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
func (p *stage6Provider) StreamChat(ctx context.Context, request provider.ChatRequest, _ func(string)) (provider.Result, error) {
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
		arguments, _ := json.Marshal(map[string]string{"path": p.target})
		nativeArguments, _ := json.Marshal(string(arguments))
		native := json.RawMessage(`{"type":"function_call","id":"stage6-item-read","call_id":"stage6-read","name":"read_file","arguments":` + string(nativeArguments) + `,"status":"completed"}`)
		return provider.Result{
			RequestID: request.RequestID, ModelID: request.ModelID, Outcome: provider.OutcomeCompleted,
			Completion: provider.CompletionToolCalls, Text: "Reading the exact fixture.", Native: []json.RawMessage{native},
			ToolCalls: []provider.ToolCall{{CallID: "stage6-read", ToolID: tools.ID("read_file"), Arguments: arguments}},
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
}

func (h *stage6ReadHost) AdmitFilesystemRead(ctx context.Context, invocation tools.Invocation, destination string) (runtime.FilesystemReadAdmission, error) {
	return h.service.AdmitFilesystemRead(ctx, invocation, destination)
}
func (h *stage6ReadHost) PrepareAdmittedFilesystemRead(ctx context.Context, admission runtime.FilesystemReadAdmission, revision uint64) (runtime.FilesystemReadPreparation, error) {
	return h.service.PrepareAdmittedFilesystemRead(ctx, admission, revision)
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

type stage6Fixture struct {
	service     *Service
	chat        *runtime.Chat
	provider    *stage6Provider
	authStore   auth.Store
	root        string
	authPath    string
	content     string
	approvals   atomic.Int32
	reportCases []string
}

func newStage6Fixture(t *testing.T, caseID, targetKind string, blockSecond bool, beforeHandoff func() error) *stage6Fixture {
	t.Helper()
	backendPath := os.Getenv("KOMPOSYN_STAGE6_BWRAP")
	if backendPath == "" {
		backendPath = "/usr/bin/bwrap"
	}
	inspection := readbrokerstudy.InspectBubblewrap(backendPath)
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
		"root_mount":                     "private 8 MiB tmpfs",
		"worker_mount":                   "one fixed worker image, read-only",
		"runtime_mounts":                 []string{},
		"extra_descriptors":              []string{"fd 3: seccomp filter", "fd 4: local protocol socket", "fd 5: worker mount source (consumed by Bubblewrap)", "fd 6: worker verification image (retained through startup)"},
		"worker_environment":             []string{"KOMPOSYN_READBROKER_SENTINEL=synthetic-only"},
		"aggregate_cgroup_limits":        "not provisioned or claimed",
		"process_creation":               "denied by worker seccomp after Go thread bootstrap",
	}
	stage6Mu.Unlock()
	if !inspection.Trusted || inspection.Error != "" {
		for _, id := range []string{"positive_exact_read_and_replay", "empty_file_read", "protected_auth_refusal", "protected_settings_refusal", "pre_handoff_owner_invalidation", "post_handoff_owner_invalidation", "cancellation_and_cleanup", "late_response_and_delta_retirement", "expected_once_transitions"} {
			stage6Skip(id, "trusted backend validation refused before backend probes or worker launch")
		}
		t.Skip("trusted native Bubblewrap backend unavailable; no probe or worker launch was attempted")
	}
	service, err := NewHostService(backendPath)
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
	if targetKind == "empty" {
		content = ""
	}
	target := filepath.Join(root, "notes.txt")
	if targetKind == "auth" {
		target, _ = authStore.EffectivePath()
		content = "synthetic-never-real-key"
	} else if targetKind == "settings" {
		target, _ = settingsStore.EffectivePath()
		content = "synthetic-settings-marker"
	} else if err := os.WriteFile(target, []byte(content), 0600); err != nil {
		_ = service.Close(context.Background())
		t.Fatalf("synthetic target setup failed: %v", err)
	}
	base := &stage6Provider{target: target, blockSecond: blockSecond, firstToolCall: true, secondStarted: make(chan struct{}), releaseSecond: make(chan struct{})}
	providers, err := model.NewRegistry(base)
	if err != nil {
		_ = service.Close(context.Background())
		t.Fatalf("fixture provider registry failed: %v", err)
	}
	fixture := &stage6Fixture{service: service, provider: base, authStore: authStore, root: root, authPath: authPath, content: content}
	config := runtime.ToolRuntimeConfig{
		Registry: tools.NewRegistry(), Preparer: service, ReadAdmission: &stage6ReadHost{service: service, beforeHandoff: beforeHandoff}, RootHost: service,
		Approval: runtime.ToolApprovalFunc(func(ctx context.Context, _ permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			if err := ctx.Err(); err != nil {
				return "", "", err
			}
			fixture.approvals.Add(1)
			return permissions.ApprovalOnce, "", nil
		}), Grants: permissions.NewMemoryStore(),
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
	if err := runtime.EnableNativeReadStudy(fixture.chat); err != nil {
		_ = fixture.chat.CloseContext(context.Background())
		_ = service.Close(context.Background())
		t.Fatalf("test-only read path setup failed: %v", err)
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
	stage6Pass("ordinary_chat_without_root", "fake-provider text chat completed with a safely unavailable sandbox service and no selected root or worker")
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
		t.Fatalf("protected refusal continuation failed: outcome=%s err=%v", turn.Outcome, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 2 {
		t.Fatalf("provider request count=%d, want initial request plus refusal continuation", len(requests))
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
	stage6Update("protected_auth_refusal", func(item *stage6Case) {
		item.ProviderRequestCount, item.DisclosureWithheld = len(requests), true
		item.OnceApprovalCount, item.WorkerStarted, item.WorkerJoined = fixture.approvals.Load(), false, false
		item.StaleDataAbsent = true
	})
	stage6Pass("protected_auth_refusal", "registered synthetic auth route refused before approval, worker launch or provider disclosure")
}

func TestStage6ProtectedSettingsObjectIsWithheld(t *testing.T) {
	stage6Begin("protected_settings_refusal")
	fixture := newStage6Fixture(t, "protected_settings_refusal", "settings", false, nil)
	fixture.reportCases = []string{"protected_settings_refusal"}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	turn, err := fixture.chat.Send(ctx, "request the protected fixture", nil)
	if err != nil || turn.Outcome != provider.OutcomeCompleted {
		t.Fatalf("protected settings refusal continuation failed: outcome=%s err=%v", turn.Outcome, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 2 {
		t.Fatalf("provider request count=%d, want initial request plus refusal continuation", len(requests))
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
	stage6Update("protected_settings_refusal", func(item *stage6Case) {
		item.ProviderRequestCount, item.DisclosureWithheld = len(requests), true
		item.OnceApprovalCount, item.WorkerStarted, item.WorkerJoined = fixture.approvals.Load(), false, false
		item.StaleDataAbsent = true
	})
	stage6Pass("protected_settings_refusal", "registered synthetic settings route refused before approval, worker launch or provider disclosure")
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
	if fixture.chat.Snapshot().Active {
		t.Fatal("chat remained active after the refused handoff")
	}
	impl := fixture.service.impl.(*linuxImplementation)
	evidence := impl.session.Evidence()
	if len(evidence) != 1 || !evidence[0].WorkerJoined || !evidence[0].ReadStarted {
		t.Fatal("the completed read and confirmed join were not observed before invalidation")
	}
	stage6Update("pre_handoff_owner_invalidation", func(item *stage6Case) {
		item.WorkerStarted, item.WorkerReady, item.WorkerJoined = evidence[0].WorkerStarted, evidence[0].WorkerReady, evidence[0].WorkerJoined
		item.CapabilityTransferred, item.ReadStarted = evidence[0].CapabilitySent, evidence[0].ReadStarted
		item.ProviderRequestCount, item.DisclosureWithheld = len(requests), true
		item.OnceApprovalCount = fixture.approvals.Load()
	})
	stage6Pass("pre_handoff_owner_invalidation", "actual Service owner rotation completed at a deterministic pre-gate checkpoint; no stale capture reached the provider")
}

func TestStage6OwnerRotationAfterHandoffRetiresContinuation(t *testing.T) {
	stage6Begin("post_handoff_owner_invalidation")
	fixture := newStage6Fixture(t, "post_handoff_owner_invalidation", "ordinary", true, nil)
	fixture.reportCases = []string{"post_handoff_owner_invalidation"}
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(fixture.provider.releaseSecond) })
	result := make(chan error, 1)
	go func() {
		_, err := fixture.chat.Send(context.Background(), "read before post-handoff rotation", nil)
		result <- err
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
	case err := <-result:
		if err == nil {
			t.Fatal("late provider response after invalidation was not rejected")
		}
	case <-time.After(12 * time.Second):
		t.Fatal("provider turn did not finish after release")
	}
	sensitive, messages := runtime.NativeReadStudyState(fixture.chat)
	if sensitive || messages != 0 {
		t.Fatal("owner invalidation did not retire the committed in-memory continuation")
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
}

func rotateSyntheticAuth(t *testing.T, fixture *stage6Fixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := fixture.authStore.ReplaceAPIKeyContext(ctx, "stage6-profile", "synthetic rotated profile", "synthetic-never-real-rotated-key", nil); err != nil {
		t.Fatalf("synthetic auth owner rotation failed: %v", err)
	}
}
