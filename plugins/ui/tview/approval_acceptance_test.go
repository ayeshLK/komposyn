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

package tview

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	hruntime "github.com/ayeshLK/komposyn/internal/harness/runtime"
	harnessui "github.com/ayeshLK/komposyn/internal/harness/ui"
	provider "github.com/ayeshLK/komposyn/plugins/model-provider"
	"github.com/ayeshLK/komposyn/plugins/tools"
	frontend "github.com/ayeshLK/komposyn/plugins/ui"
	"github.com/gdamore/tcell/v2"
)

const approvalFixtureSecret = "approval-ui-private-fixture-content"

type approvalJourneyProvider struct {
	mu       sync.Mutex
	path     string
	calls    int
	requests []provider.ChatRequest
	mode     string
}

func (*approvalJourneyProvider) ID() provider.ID { return provider.OpenAI }
func (*approvalJourneyProvider) AuthMethods() []provider.AuthMethod {
	return []provider.AuthMethod{provider.AuthAPIKey}
}
func (*approvalJourneyProvider) Authenticate(context.Context, provider.Credential) (provider.AuthResult, error) {
	return provider.AuthResult{Provider: provider.OpenAI}, nil
}
func (*approvalJourneyProvider) ListModels(context.Context, provider.Credential) ([]provider.Model, error) {
	return []provider.Model{{ID: "fixture-model", DisplayName: "Fixture model", Compatibility: provider.CompatibilitySupported}}, nil
}
func (*approvalJourneyProvider) RouteCapabilities(method provider.AuthMethod) provider.RouteCapabilities {
	if method == provider.AuthAPIKey {
		return provider.RouteCapabilities{ClientTools: provider.SupportSupported}
	}
	return provider.RouteCapabilities{ClientTools: provider.SupportUnknown}
}
func (p *approvalJourneyProvider) StreamChat(_ context.Context, request provider.ChatRequest, delta func(string)) (provider.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, request)
	p.calls++
	if p.calls == 1 || p.mode == "fresh" && p.calls == 3 {
		args, _ := json.Marshal(map[string]string{"path": p.path})
		nativeArgs, _ := json.Marshal(string(args))
		callID := "read-1"
		itemID := "item-read-1"
		if p.calls == 3 {
			callID, itemID = "read-2", "item-read-2"
		}
		raw := json.RawMessage(`{"type":"function_call","id":"` + itemID + `","call_id":"` + callID + `","name":"read_file","arguments":` + string(nativeArgs) + `,"status":"completed"}`)
		return provider.Result{Outcome: provider.OutcomeCompleted, Completion: provider.CompletionToolCalls, Text: "Reading the requested report.", Native: []json.RawMessage{raw}, ToolCalls: []provider.ToolCall{{CallID: callID, ToolID: tools.ID("read_file"), Arguments: args}}}, nil
	}
	text := "File read complete."
	if p.mode == "redirect" || p.mode == "deny" {
		text = "Continuing without file contents."
	} else if p.calls > 2 {
		text = "Follow-up answer."
	}
	if delta != nil {
		delta(text)
	}
	return provider.Result{Outcome: provider.OutcomeCompleted, Completion: provider.CompletionFinal, Text: text, Native: []json.RawMessage{json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + text + `"}]}`)}}, nil
}

type approvalJourneyHost struct {
	mu          sync.Mutex
	root        string
	rootGen     uint64
	reads       int
	invalidator func(string)
}
type approvalJourneyPreparation struct {
	metadata hruntime.FilesystemReadAdmission
}

func (p *approvalJourneyPreparation) Metadata() hruntime.FilesystemReadAdmission { return p.metadata }
func (*approvalJourneyPreparation) WithAuthorityCommit(ctx context.Context, commit func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return commit()
}
func (*approvalJourneyPreparation) Close(context.Context) error { return nil }

func (h *approvalJourneyHost) AdmitFilesystemRead(_ context.Context, invocation tools.Invocation, destination string) (hruntime.FilesystemReadAdmission, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(invocation.Arguments, &args); err != nil || args.Path == "" {
		return hruntime.FilesystemReadAdmission{}, errors.New("invalid fixture target")
	}
	h.mu.Lock()
	root, rootGen := h.root, h.rootGen
	h.mu.Unlock()
	return hruntime.FilesystemReadAdmission{Reference: new(struct{}), ServiceID: "approval-fixture", ConversationID: invocation.ConversationID, TurnID: invocation.TurnID, CallID: invocation.ProviderCallID, Root: root, Deadline: time.Now().Add(time.Minute), AdmissionBinding: "approval-fixture-binding", Target: args.Path, Class: "ordinary", Identity: "approval-fixture-object", RootGeneration: rootGen, PolicyRevision: 1, CoverageRevision: 1, Destination: destination}, nil
}
func (*approvalJourneyHost) PrepareAdmittedFilesystemRead(_ context.Context, admission hruntime.FilesystemReadAdmission, _ uint64) (hruntime.FilesystemReadPreparation, error) {
	return &approvalJourneyPreparation{metadata: admission}, nil
}
func (*approvalJourneyHost) CloseFilesystemReadAdmission(context.Context, hruntime.FilesystemReadAdmission) error {
	return nil
}
func (*approvalJourneyHost) ValidateFilesystemRoot(ctx context.Context, root string) (string, error) {
	return hruntime.CanonicalFilesystemRoot(ctx, root)
}
func (h *approvalJourneyHost) InvalidateFilesystemRoot(_ context.Context, change hruntime.FilesystemRootTransition) error {
	h.mu.Lock()
	h.root, h.rootGen = change.Root, change.Generation
	h.mu.Unlock()
	return nil
}
func (*approvalJourneyHost) BackendStatus() (bool, string)                   { return true, "fixture backend" }
func (*approvalJourneyHost) InvalidateFilesystemReadExecutions(string) error { return nil }
func (h *approvalJourneyHost) SetFilesystemReadInvalidator(invalidate func(string)) {
	h.invalidator = invalidate
}
func (h *approvalJourneyHost) WithFilesystemReadPublicationCurrent(ctx context.Context, conversationID, root string, generation, policy, coverage uint64, operation func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	current := conversationID != "" && root == h.root && generation == h.rootGen && policy == 1 && coverage == 1
	h.mu.Unlock()
	if !current {
		return hruntime.ErrFilesystemRootStale
	}
	return operation()
}
func (h *approvalJourneyHost) ExecuteAdmittedFilesystemRead(ctx context.Context, admission hruntime.FilesystemReadAdmission, preparation hruntime.FilesystemReadPreparation, _ *hruntime.ReadExecutionReference, authority hruntime.FilesystemReadExecutionAuthority) (*hruntime.PrivateReadCapture, error) {
	if err := authority.Claim(); err != nil {
		return nil, err
	}
	var capture *hruntime.PrivateReadCapture
	err := authority.WithCurrent(func() error {
		h.mu.Lock()
		h.reads++
		h.mu.Unlock()
		var err error
		capture, err = authority.Stage(admission, hruntime.FilesystemReadExecutionResult{Outcome: tools.OutcomeSucceeded, Bytes: []byte(approvalFixtureSecret), ServiceID: admission.ServiceID, AdmissionBinding: admission.AdmissionBinding, AuthorizationBinding: authority.AuthorizationBinding(), Identity: admission.Identity, RootGeneration: admission.RootGeneration, PolicyRevision: admission.PolicyRevision, CoverageRevision: admission.CoverageRevision, Destination: admission.Destination, ReadStarted: true, WorkerJoined: true, CapabilitiesRetired: true})
		return err
	})
	return capture, errors.Join(err, preparation.Close(ctx))
}

func TestReadApprovalConversationJourney(t *testing.T) {
	for _, mode := range []string{"approve", "deny", "cancel", "redirect", "fresh"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "private report.txt")
			if err := os.WriteFile(path, []byte(approvalFixtureSecret), 0o600); err != nil {
				t.Fatal(err)
			}
			p := &approvalJourneyProvider{path: path, mode: mode}
			providers, err := model.NewRegistry(p)
			if err != nil {
				t.Fatal(err)
			}
			store := auth.Store{Path: filepath.Join(t.TempDir(), "auth.json")}
			bridge := harnessui.NewApprovalBridge()
			host := &approvalJourneyHost{}
			clarification := hruntime.ToolClarificationFunc(func(context.Context, hruntime.ClarificationRequest) (hruntime.ClarificationResponse, error) {
				return hruntime.ClarificationResponse{}, errors.New("clarification is not part of this fixture")
			})
			chat := hruntime.NewChatWithToolRuntime(providers, store, nil, hruntime.BundledConversationToolRuntimeConfigWithFilesystemRead(bridge, clarification, host))
			if err := chat.AddProfile(context.Background(), "personal", string(provider.OpenAI), "Personal", "synthetic-key", nil); err != nil {
				t.Fatal(err)
			}
			discovery := chat.Discover(context.Background())
			if len(discovery.Models) != 1 {
				t.Fatalf("models=%+v", discovery.Models)
			}
			if err := chat.Select(context.Background(), discovery.Models[0].Key(), false); err != nil {
				t.Fatal(err)
			}
			if err := chat.SelectFilesystemRoot(context.Background(), filepath.Dir(path)); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(chat.Close)
			client := harnessui.Client{Chat: chat, ProfileStore: store, Version: "test", Approval: bridge}
			screen := tcell.NewSimulationScreen("UTF-8")
			screen.SetSize(84, 26)
			safe := &synchronizedSimulation{SimulationScreen: screen}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- (Plugin{screen: safe}).Run(ctx, client) }()
			waitForScreen(t, safe, "KompoSyn is ready", "")
			press := func(key tcell.Key, r rune) {
				screen.InjectKey(key, r, tcell.ModNone)
				time.Sleep(20 * time.Millisecond)
			}
			for _, r := range "Read private report.txt and summarize it" {
				press(tcell.KeyRune, r)
			}
			press(tcell.KeyEnter, 0)
			waitForScreen(t, safe, "Tool approval", "")
			waitForScreen(t, safe, "Provider destination: openai", "")
			press(tcell.KeyEnd, 0) // first key is guarded against stale composer input.
			press(tcell.KeyEnd, 0)
			waitForExactTarget(t, safe, path)
			if mode == "cancel" {
				press(tcell.KeyEscape, 0)
			} else {
				press(tcell.KeyTab, 0)
				switch mode {
				case "approve", "fresh":
					press(tcell.KeyUp, 0)
					press(tcell.KeyUp, 0)
				case "deny":
					press(tcell.KeyUp, 0)
				case "cancel":
				case "redirect":
					press(tcell.KeyDown, 0)
				}
				press(tcell.KeyEnter, 0)
				if mode == "redirect" {
					waitForScreen(t, safe, "Original tool call will be rejected", "")
					for _, r := range "Do not read; summarize the filename" {
						press(tcell.KeyRune, r)
					}
					press(tcell.KeyEscape, 0)
					waitForScreen(t, safe, "Tool approval", "")
					press(tcell.KeyTab, 0)
					press(tcell.KeyTab, 0)
					press(tcell.KeyDown, 0)
					press(tcell.KeyEnter, 0)
					waitForScreen(t, safe, "Do not read; summarize the filename", "")
					press(tcell.KeyTab, 0)
					press(tcell.KeyEnter, 0)
				}
			}
			if mode == "approve" {
				waitForScreen(t, safe, "File read complete.", "")
				for _, r := range "Continue with one more answer" {
					press(tcell.KeyRune, r)
				}
				press(tcell.KeyEnter, 0)
				waitForScreen(t, safe, "Follow-up answer.", "")
			} else if mode == "fresh" {
				waitForScreen(t, safe, "File read complete.", "")
				for _, r := range "Read the report again" {
					press(tcell.KeyRune, r)
				}
				press(tcell.KeyEnter, 0)
				waitForScreen(t, safe, "Tool approval", "")
				host.mu.Lock()
				readsBeforeSecondApproval := host.reads
				host.mu.Unlock()
				p.mu.Lock()
				requestsBeforeSecondApproval := len(p.requests)
				p.mu.Unlock()
				if readsBeforeSecondApproval != 1 || requestsBeforeSecondApproval != 3 {
					t.Fatalf("second read proceeded before fresh approval: reads=%d requests=%d", readsBeforeSecondApproval, requestsBeforeSecondApproval)
				}
				press(tcell.KeyEnd, 0)
				press(tcell.KeyEnd, 0)
				press(tcell.KeyTab, 0)
				press(tcell.KeyUp, 0)
				press(tcell.KeyUp, 0)
				press(tcell.KeyEnter, 0)
				waitForScreen(t, safe, "Follow-up answer.", "")
			} else if mode != "cancel" {
				waitForScreen(t, safe, "Continuing without file contents.", "")
			}
			p.mu.Lock()
			requests := append([]provider.ChatRequest(nil), p.requests...)
			p.mu.Unlock()
			if len(requests) == 0 {
				t.Fatal("fake provider received no request")
			}
			if strings.Contains(approvalProviderRequestText(requests[0]), approvalFixtureSecret) {
				t.Fatal("private bytes reached the provider before approval")
			}
			if mode != "approve" && mode != "fresh" {
				for _, request := range requests {
					if strings.Contains(approvalProviderRequestText(request), approvalFixtureSecret) {
						t.Fatalf("%s disclosed unapproved bytes", mode)
					}
				}
			}
			host.mu.Lock()
			reads := host.reads
			host.mu.Unlock()
			if mode == "approve" {
				if reads != 1 || len(requests) != 3 || !strings.Contains(approvalProviderRequestText(requests[1]), approvalFixtureSecret) || !strings.Contains(approvalProviderRequestText(requests[2]), approvalFixtureSecret) {
					t.Fatalf("approved journey: reads=%d requests=%d", reads, len(requests))
				}
			} else if mode == "fresh" {
				if reads != 2 || len(requests) != 4 || !strings.Contains(approvalProviderRequestText(requests[1]), approvalFixtureSecret) || !strings.Contains(approvalProviderRequestText(requests[3]), approvalFixtureSecret) {
					t.Fatalf("fresh-approval journey: reads=%d requests=%d", reads, len(requests))
				}
			} else if reads != 0 {
				t.Fatalf("%s read executed without approval: %d", mode, reads)
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("terminal did not stop")
			}
		})
	}
}

func waitForExactTarget(t *testing.T, screen *synchronizedSimulation, target string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	targetWithoutSpaces := strings.ReplaceAll(filepath.Clean(target), " ", "")
	for time.Now().Before(deadline) {
		visible := strings.NewReplacer("\\\\", "\\").Replace(approvalDetailsOnScreen(screen))
		visibleWithoutWhitespace := strings.Join(strings.Fields(visible), "")
		if strings.Contains(visibleWithoutWhitespace, targetWithoutSpaces) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	visible := strings.NewReplacer("\\\\", "\\").Replace(approvalDetailsOnScreen(screen))
	visibleWithoutWhitespace := strings.Join(strings.Fields(visible), "")
	if !strings.Contains(visibleWithoutWhitespace, targetWithoutSpaces) {
		t.Fatalf("screen did not show the exact target %q across line wrapping: %q", target, visible)
	}
}

func approvalDetailsOnScreen(screen *synchronizedSimulation) string {
	cells, width, height := screen.GetContents()
	rows := make([]string, height)
	for y := range height {
		var row strings.Builder
		for x := 0; x < width; x++ {
			cell := cells[y*width+x]
			if len(cell.Runes) > 0 {
				row.WriteString(string(cell.Runes))
			} else {
				row.WriteRune(' ')
			}
		}
		rows[y] = row.String()
	}
	for titleRow, row := range rows {
		title := strings.Index(row, "Review read approval")
		if title < 0 {
			continue
		}
		leftByte := strings.LastIndex(row[:title], "╔")
		rightOffset := strings.Index(row[title:], "╗")
		if leftByte < 0 || rightOffset < 0 {
			continue
		}
		rightByte := title + rightOffset
		left := utf8.RuneCountInString(row[:leftByte])
		right := utf8.RuneCountInString(row[:rightByte])
		var details strings.Builder
		for y := titleRow + 1; y < height; y++ {
			line := []rune(rows[y])
			if strings.Contains(string(line[left:right+1]), "╚") {
				break
			}
			if right > left+1 {
				details.WriteString(string(line[left+1 : right]))
				details.WriteRune(' ')
			}
		}
		return strings.Join(strings.Fields(details.String()), " ")
	}
	return ""
}

func approvalProviderRequestText(request provider.ChatRequest) string {
	b, _ := json.Marshal(request.Messages)
	return string(b)
}

var _ frontend.Client = harnessui.Client{}
