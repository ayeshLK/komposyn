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

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/permissions"
	"github.com/ayeshLK/komposyn/plugins/tools"
)

type retainedAdmissionFixture struct {
	metadata     FilesystemReadAdmission
	current      FilesystemReadAdmission
	prepareError error
	prepared     int
	committed    int
	closed       int
}

func (f *retainedAdmissionFixture) AdmitFilesystemRead(_ context.Context, _ tools.Invocation, destination string) (FilesystemReadAdmission, error) {
	metadata := f.metadata
	metadata.Destination = destination
	f.current = metadata
	return metadata, nil
}

func (f *retainedAdmissionFixture) PrepareAdmittedFilesystemRead(_ context.Context, _ FilesystemReadAdmission, revision uint64) (FilesystemReadPreparation, error) {
	f.prepared++
	if f.prepareError != nil {
		return nil, f.prepareError
	}
	if revision == 0 {
		return nil, errors.New("missing approved revision")
	}
	return &retainedPreparationFixture{host: f, metadata: f.current}, nil
}

func (f *retainedAdmissionFixture) CloseFilesystemReadAdmission(context.Context, FilesystemReadAdmission) error {
	f.closed++
	return nil
}

type retainedPreparationFixture struct {
	host     *retainedAdmissionFixture
	metadata FilesystemReadAdmission
}

func (f *retainedPreparationFixture) Metadata() FilesystemReadAdmission { return f.metadata }
func (f *retainedPreparationFixture) WithAuthorityCommit(ctx context.Context, commit func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.host.committed++
	return commit()
}
func (f *retainedPreparationFixture) Close(context.Context) error {
	f.host.closed++
	return nil
}

func TestRetainedReadApprovalUsesCheckedRuntimeTransactionsAndNeverDispatches(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "notes.txt")
	arguments, err := json.Marshal(struct {
		Path string `json:"path"`
	}{target})
	if err != nil {
		t.Fatal(err)
	}
	host := &retainedAdmissionFixture{metadata: FilesystemReadAdmission{
		Reference: new(struct{}), ServiceID: "host-service", ConversationID: "conversation", Root: root, Deadline: time.Now().Add(time.Minute),
		AdmissionBinding: "canonical-admission-digest", Target: target, Class: "ordinary",
		Identity: "1:2:32768", RootGeneration: 4, PolicyRevision: 7, CoverageRevision: 9,
	}}
	runtime := newToolRuntime(ToolRuntimeConfig{
		Registry: tools.NewRegistry(), ReadAdmission: host,
		Approval: ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			return permissions.ApprovalOnce, "", nil
		}),
	})
	invocation := tools.Invocation{ConversationID: "conversation", TurnID: "turn", ProviderCallID: "call", ToolID: tools.ID("read_file"), Arguments: arguments}
	if err := runtime.validateFilesystemReadApproval(context.Background(), invocation, "openai/gpt-test/profile-revision"); err != nil {
		t.Fatalf("checked read approval: %v", err)
	}
	revision, grants := runtime.grants.Snapshot(invocation.ConversationID)
	if revision != 2 || len(grants) != 0 || runtime.readAuthorityCount() != 0 || runtime.lastReadIssuanceRevision != revision {
		t.Fatalf("Stage 3 authority not immediately retired: revision=%d grants=%d authority=%d last-issued=%d", revision, len(grants), runtime.readAuthorityCount(), runtime.lastReadIssuanceRevision)
	}
	if host.prepared != 1 || host.committed != 1 || host.closed != 1 {
		t.Fatalf("retained lifecycle counts: prepared=%d commits=%d closes=%d", host.prepared, host.committed, host.closed)
	}
}

func TestRetainedReadPreparationRefusalDoesNotConsumeOnceGrant(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "file")
	arguments, err := json.Marshal(struct {
		Path string `json:"path"`
	}{target})
	if err != nil {
		t.Fatal(err)
	}
	host := &retainedAdmissionFixture{
		prepareError: errors.New("synthetic preparation refusal"),
		metadata:     FilesystemReadAdmission{Reference: new(struct{}), ServiceID: "host", ConversationID: "c", Root: root, Deadline: time.Now().Add(time.Minute), AdmissionBinding: "digest", Target: target, Class: "ordinary", RootGeneration: 1, PolicyRevision: 2, CoverageRevision: 3, Destination: "provider/model/profile"},
	}
	runtime := newToolRuntime(ToolRuntimeConfig{
		Registry: tools.NewRegistry(), ReadAdmission: host,
		Approval: ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			return permissions.ApprovalOnce, "", nil
		}),
	})
	invocation := tools.Invocation{ConversationID: "c", TurnID: "t", ProviderCallID: "call", ToolID: tools.ID("read_file"), Arguments: arguments}
	if err := runtime.validateFilesystemReadApproval(context.Background(), invocation, "provider/model/profile"); err == nil {
		t.Fatal("preparation refusal unexpectedly succeeded")
	}
	revision, grants := runtime.grants.Snapshot("c")
	if revision != 1 || len(grants) != 2 || runtime.readAuthorityCount() != 0 || host.closed != 1 {
		t.Fatalf("preparation refusal consumed or leaked approval: revision=%d grants=%d authorities=%d closes=%d", revision, len(grants), runtime.readAuthorityCount(), host.closed)
	}
}

func TestRetainedReadNonApprovalChoicesDoNotPrepareOrConsume(t *testing.T) {
	for _, test := range []struct {
		name     string
		choice   permissions.ApprovalChoice
		redirect string
	}{
		{name: "deny", choice: permissions.ApprovalDeny},
		{name: "cancel", choice: permissions.ApprovalCancel},
		{name: "redirect", choice: permissions.ApprovalRedirect, redirect: "ask for a summary instead"},
		{name: "no response"},
	} {
		t.Run(test.name, func(t *testing.T) {
			host := &retainedAdmissionFixture{metadata: FilesystemReadAdmission{
				Reference: new(struct{}), ServiceID: "host", ConversationID: "c",
				Deadline: time.Now().Add(time.Minute), AdmissionBinding: "digest", Class: "ordinary",
				Identity: "1:2:32768", RootGeneration: 1, PolicyRevision: 2,
				CoverageRevision: 3, Destination: "provider/model/profile",
			}}
			runtime := newToolRuntime(ToolRuntimeConfig{
				Registry: tools.NewRegistry(), ReadAdmission: host,
				Approval: ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
					return test.choice, test.redirect, nil
				}),
			})
			root := t.TempDir()
			target := filepath.Join(root, "file")
			host.metadata.Root = root
			host.metadata.Target = target
			arguments, err := json.Marshal(struct {
				Path string `json:"path"`
			}{target})
			if err != nil {
				t.Fatal(err)
			}
			invocation := tools.Invocation{ConversationID: "c", TurnID: "t", ProviderCallID: "call", ToolID: tools.ID("read_file"), Arguments: arguments}
			if err := runtime.validateFilesystemReadApproval(context.Background(), invocation, "provider/model/profile"); err == nil {
				t.Fatal("non-approval choice unexpectedly authorized retained read")
			}
			revision, grants := runtime.grants.Snapshot("c")
			if revision != 0 || len(grants) != 0 || host.prepared != 0 || runtime.readAuthorityCount() != 0 {
				t.Fatalf("non-approval changed authority: revision=%d grants=%d prepared=%d authority=%d", revision, len(grants), host.prepared, runtime.readAuthorityCount())
			}
		})
	}
}

func TestRetainedReadDeadlineBoundsApprovalWait(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "file")
	arguments, err := json.Marshal(struct {
		Path string `json:"path"`
	}{target})
	if err != nil {
		t.Fatal(err)
	}
	host := &retainedAdmissionFixture{metadata: FilesystemReadAdmission{
		Reference: new(struct{}), ServiceID: "host", ConversationID: "c", Root: root,
		Deadline: time.Now().Add(25 * time.Millisecond), AdmissionBinding: "digest", Target: target,
		Class: "ordinary", Identity: "1:2:32768", RootGeneration: 1, PolicyRevision: 2,
		CoverageRevision: 3, Destination: "provider/model/profile",
	}}
	approvalReturned := make(chan struct{})
	runtime := newToolRuntime(ToolRuntimeConfig{
		Registry: tools.NewRegistry(), ReadAdmission: host,
		Approval: ToolApprovalFunc(func(ctx context.Context, _ permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			defer close(approvalReturned)
			<-ctx.Done()
			return permissions.ApprovalOnce, "", ctx.Err()
		}),
	})
	invocation := tools.Invocation{ConversationID: "c", TurnID: "t", ProviderCallID: "call", ToolID: tools.ID("read_file"), Arguments: arguments}
	if err := runtime.validateFilesystemReadApproval(context.Background(), invocation, "provider/model/profile"); err == nil {
		t.Fatal("approval wait exceeded the host admission deadline")
	}
	select {
	case <-approvalReturned:
	default:
		t.Fatal("approval callback did not observe the admission deadline")
	}
	_, grants := runtime.grants.Snapshot("c")
	if host.prepared != 0 || host.closed != 1 || len(grants) != 0 {
		t.Fatalf("expired approval progressed or leaked: prepared=%d closed=%d grants=%d", host.prepared, host.closed, len(grants))
	}
}

func TestRelativeFilesystemReadResolvesAgainstWorkingDirectory(t *testing.T) {
	workingDirectory := filepath.Join(t.TempDir(), "project")
	invocation := tools.Invocation{
		ToolID:    tools.ID("read_file"),
		Arguments: json.RawMessage(`{"path":"docs/README.md"}`),
	}

	resolved, err := resolveFilesystemReadInvocation(invocation, workingDirectory)
	if err != nil {
		t.Fatal(err)
	}
	var arguments struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(resolved.Arguments, &arguments); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(workingDirectory, "docs", "README.md")
	if arguments.Path != want {
		t.Fatalf("resolved path = %q, want %q", arguments.Path, want)
	}
}

func TestRelativeFilesystemReadKeepsAbsoluteTargetAndRejectsMissingWorkingDirectory(t *testing.T) {
	absolute := filepath.Join(t.TempDir(), "README.md")
	arguments, err := json.Marshal(struct {
		Path string `json:"path"`
	}{absolute})
	if err != nil {
		t.Fatal(err)
	}
	invocation := tools.Invocation{ToolID: tools.ID("read_file"), Arguments: arguments}
	resolved, err := resolveFilesystemReadInvocation(invocation, "")
	if err != nil {
		t.Fatalf("absolute path needs no working directory: %v", err)
	}
	if string(resolved.Arguments) != string(arguments) {
		t.Fatalf("absolute path arguments changed: got %s, want %s", resolved.Arguments, arguments)
	}

	invocation.Arguments = json.RawMessage(`{"path":"README.md"}`)
	if _, err := resolveFilesystemReadInvocation(invocation, ""); err == nil {
		t.Fatal("relative path without a working directory unexpectedly resolved")
	}
}

func TestFilesystemReadApprovalClearlyIdentifiesOutsideWorkingDirectoryTarget(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	arguments, err := json.Marshal(struct {
		Path string `json:"path"`
	}{outside})
	if err != nil {
		t.Fatal(err)
	}
	admission := FilesystemReadAdmission{
		Reference: new(struct{}), ServiceID: "host-service", ConversationID: "conversation", Root: root,
		Deadline: time.Now().Add(time.Minute), AdmissionBinding: "canonical-admission-digest", Target: outside,
		Class: "ordinary", Identity: "1:2:32768", RootGeneration: 4, PolicyRevision: 7,
		CoverageRevision: 9, Destination: "openai/gpt-test/profile-revision",
	}
	invocation := tools.Invocation{ConversationID: "conversation", TurnID: "turn", ProviderCallID: "call", ToolID: tools.ID("read_file"), Arguments: arguments}
	request, err := buildFilesystemReadRequest(invocation, admission)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Requirements) != 2 || request.Requirements[1].Resource == nil || request.Requirements[1].Resource.Target != outside {
		t.Fatalf("approval did not bind the exact outside target: %+v", request.Requirements)
	}
	if !strings.Contains(request.Requirements[1].Description, "outside the working directory") {
		t.Fatalf("approval did not identify outside-working-directory target: %q", request.Requirements[1].Description)
	}
	if request.Requirements[1].SessionAllowed || len(request.RequireOnceKeys) != 1 || request.RequireOnceKeys[0] != request.Requirements[1].Key {
		t.Fatalf("outside target approval is not individual once approval: %+v", request)
	}
}

func TestOutsideProviderSourceApprovalKeepsWorkingDirectoryNotice(t *testing.T) {
	workingDirectory := t.TempDir()
	target := filepath.Join(t.TempDir(), "provider-source.jsonl")
	arguments, err := json.Marshal(struct {
		Path string `json:"path"`
	}{target})
	if err != nil {
		t.Fatal(err)
	}
	admission := FilesystemReadAdmission{
		Reference: new(struct{}), ServiceID: "host-service", ConversationID: "conversation", Root: workingDirectory,
		Deadline: time.Now().Add(time.Minute), AdmissionBinding: "canonical-admission-digest", Target: target,
		Class: "provider_source", Identity: "1:2:32768", RootGeneration: 4, PolicyRevision: 7,
		CoverageRevision: 9, Destination: "openai/gpt-test/profile-revision",
	}
	invocation := tools.Invocation{ConversationID: "conversation", TurnID: "turn", ProviderCallID: "call", ToolID: tools.ID("read_file"), Arguments: arguments}
	request, err := buildFilesystemReadRequest(invocation, admission)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Requirements) != 2 || request.Requirements[1].Resource == nil || request.Requirements[1].Resource.Target != target {
		t.Fatalf("approval did not bind exact provider-source target: %+v", request.Requirements)
	}
	description := strings.ToLower(request.Requirements[1].Description)
	if !strings.Contains(description, "outside the working directory") || !strings.Contains(description, "classified provider source") {
		t.Fatalf("provider-source approval omitted its location or sensitivity notice: %q", description)
	}
}
