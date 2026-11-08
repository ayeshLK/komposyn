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
	EvidenceKind              string `json:"evidence_kind,omitempty"`
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
	LateResponseReturned      bool   `json:"late_response_returned"`
	LateDeltaAttempted        bool   `json:"late_delta_attempted"`
	LateDeltaSuppressed       bool   `json:"late_delta_suppressed"`
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
		"ordinary_chat_without_root", "read_refused_without_root_or_backend", "positive_exact_read_and_replay", "empty_file_read",
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
			"Provider-source positive authorization/rotation, mutation, shell, aggregate cgroup quotas, and non-Linux enforcement are not established here.",
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
		paths := []struct{ callID, itemID, path string }{{"stage6-read", "stage6-item-read", p.target}}
		if p.multipleReads {
			paths = append(paths, struct{ callID, itemID, path string }{"stage6-read-b", "stage6-item-read-b", p.secondTarget})
		}
		calls := make([]provider.ToolCall, 0, len(paths))
		nativeCalls := make([]json.RawMessage, 0, len(paths))
		for _, item := range paths {
			arguments, _ := json.Marshal(map[string]string{"path": item.path})
			nativeArguments, _ := json.Marshal(string(arguments))
			nativeCalls = append(nativeCalls, json.RawMessage(`{"type":"function_call","id":"`+item.itemID+`","call_id":"`+item.callID+`","name":"read_file","arguments":`+string(nativeArguments)+`,"status":"completed"}`))
			calls = append(calls, provider.ToolCall{CallID: item.callID, ToolID: tools.ID("read_file"), Arguments: arguments})
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
	if err := authStore.RegisterProtection(ctx); err != nil {
		t.Fatalf("synthetic auth owner registration failed: %v", err)
	}
	chat := runtime.NewChatWithToolRuntime(providers, authStore, nil, runtime.ToolRuntimeConfig{
		Registry: tools.NewRegistry(), Preparer: service, ReadAdmission: &stage6ReadHost{service: service}, RootHost: service,
		Approval: runtime.ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
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
	if len(requests) != 2 || impl.executions != 1 {
		t.Fatalf("synthetic read requests=%d executions=%d, want 2 and 1", len(requests), impl.executions)
	}
	first, _ := json.Marshal(requests[0].Messages)
	if strings.Contains(string(first), "stage6-synthetic-forwarded-content") {
		t.Fatal("synthetic read bytes appeared before execution and provider continuation")
	}
	content, found := readResultContent(requests[1], "stage6-read")
	if !found || content != `{"outcome":"succeeded","truncated":false,"content":"stage6-synthetic-forwarded-content"}` {
		t.Fatalf("synthetic forwarded result=%q found=%t", content, found)
	}
}

type stage6Fixture struct {
	service             *Service
	chat                *runtime.Chat
	provider            *stage6Provider
	authStore           auth.Store
	root                string
	authPath            string
	content             string
	approvals           atomic.Int32
	approvalWaitAt      atomic.Int32
	approvalEntered     chan struct{}
	approvalRelease     chan struct{}
	approvalReleaseOnce sync.Once
	approvalWaitOnce    sync.Once
	secondContent       string
	reportCases         []string
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
	secondContent := "stage6-second-synthetic-private-content"
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
	secondTarget := filepath.Join(root, "second-notes.txt")
	if targetKind == "multiple" {
		if err := os.WriteFile(secondTarget, []byte(secondContent), 0600); err != nil {
			_ = service.Close(context.Background())
			t.Fatalf("second synthetic target setup failed: %v", err)
		}
	}
	base := &stage6Provider{target: target, secondTarget: secondTarget, multipleReads: targetKind == "multiple", blockSecond: blockSecond, firstToolCall: true, secondStarted: make(chan struct{}), releaseSecond: make(chan struct{})}
	providers, err := model.NewRegistry(base)
	if err != nil {
		_ = service.Close(context.Background())
		t.Fatalf("fixture provider registry failed: %v", err)
	}
	fixture := &stage6Fixture{service: service, provider: base, authStore: authStore, root: root, authPath: authPath, content: content, secondContent: secondContent, approvalEntered: make(chan struct{}), approvalRelease: make(chan struct{})}
	config := runtime.ToolRuntimeConfig{
		Registry: tools.NewRegistry(), Preparer: service, ReadAdmission: &stage6ReadHost{service: service, beforeHandoff: beforeHandoff}, RootHost: service,
		Approval: runtime.ToolApprovalFunc(func(ctx context.Context, _ permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			if err := ctx.Err(); err != nil {
				return "", "", err
			}
			count := fixture.approvals.Add(1)
			if fixture.approvalWaitAt.Load() == count {
				fixture.approvalWaitOnce.Do(func() { close(fixture.approvalEntered) })
				select {
				case <-fixture.approvalRelease:
				case <-ctx.Done():
					return "", "", ctx.Err()
				}
			}
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
	if err != nil || turn.Outcome != provider.OutcomeInterrupted {
		t.Fatalf("read without root was not refused before continuation: outcome=%s err=%v", turn.Outcome, err)
	}
	requests := base.captured()
	if len(requests) != 1 {
		t.Fatalf("provider requests=%d, want only the initial tool-call request", len(requests))
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
	if messages == 0 || results != 1 || markerFound || !payloadFree {
		t.Fatalf("no-root refusal history was not payload-free: messages=%d results=%d marker=%t payload_free=%t", messages, results, markerFound, payloadFree)
	}
	stage6Update("read_refused_without_root_or_backend", func(item *stage6Case) {
		item.EvidenceKind, item.ProviderRequestCount, item.DisclosureWithheld, item.StaleDataAbsent = "synthetic-runtime-composition", len(requests), true, true
	})
	stage6PassSynthetic("read_refused_without_root_or_backend", "actual runtime and unavailable host Service refused a provider-requested read before approval, backend worker creation, or continuation disclosure")
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
	if err != nil || turn.Outcome != provider.OutcomeInterrupted {
		t.Fatalf("protected auth refusal did not interrupt before continuation: outcome=%s err=%v", turn.Outcome, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 1 {
		t.Fatalf("provider requests=%d, want only the initial request before protected-read refusal", len(requests))
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
	stage6Pass("protected_auth_refusal", "registered synthetic auth route refused before approval, worker launch or provider disclosure")
}

func TestStage6ProtectedSettingsObjectIsWithheld(t *testing.T) {
	stage6Begin("protected_settings_refusal")
	fixture := newStage6Fixture(t, "protected_settings_refusal", "settings", false, nil)
	fixture.reportCases = []string{"protected_settings_refusal"}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	turn, err := fixture.chat.Send(ctx, "request the protected fixture", nil)
	if err != nil || turn.Outcome != provider.OutcomeInterrupted {
		t.Fatalf("protected settings refusal did not interrupt before continuation: outcome=%s err=%v", turn.Outcome, err)
	}
	requests := fixture.provider.captured()
	if len(requests) != 1 {
		t.Fatalf("provider requests=%d, want only the initial request before protected-read refusal", len(requests))
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
	stage6Pass("protected_settings_refusal", "registered synthetic settings route refused before approval, worker launch or provider disclosure")
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
