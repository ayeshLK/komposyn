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
	"errors"
	"testing"

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
	host := &retainedAdmissionFixture{metadata: FilesystemReadAdmission{
		Reference: new(struct{}), ServiceID: "host-service", ConversationID: "conversation", Root: "/synthetic-root",
		AdmissionBinding: "canonical-admission-digest", Target: "/synthetic-root/notes.txt", Class: "ordinary",
		Identity: "1:2:32768", RootGeneration: 4, PolicyRevision: 7, CoverageRevision: 9,
	}}
	runtime := newToolRuntime(ToolRuntimeConfig{
		Registry: tools.NewRegistry(), ReadAdmission: host,
		Approval: ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			return permissions.ApprovalOnce, "", nil
		}),
	})
	invocation := tools.Invocation{ConversationID: "conversation", TurnID: "turn", ProviderCallID: "call", ToolID: tools.ID("read_file"), Arguments: []byte(`{"path":"/synthetic-root/notes.txt"}`)}
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
	host := &retainedAdmissionFixture{
		prepareError: errors.New("synthetic preparation refusal"),
		metadata:     FilesystemReadAdmission{Reference: new(struct{}), ServiceID: "host", ConversationID: "c", Root: "/root", AdmissionBinding: "digest", Target: "/root/file", Class: "ordinary", RootGeneration: 1, PolicyRevision: 2, CoverageRevision: 3, Destination: "provider/model/profile"},
	}
	runtime := newToolRuntime(ToolRuntimeConfig{
		Registry: tools.NewRegistry(), ReadAdmission: host,
		Approval: ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
			return permissions.ApprovalOnce, "", nil
		}),
	})
	invocation := tools.Invocation{ConversationID: "c", TurnID: "t", ProviderCallID: "call", ToolID: tools.ID("read_file"), Arguments: []byte(`{"path":"/root/file"}`)}
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
				Reference: new(struct{}), ServiceID: "host", ConversationID: "c", Root: "/root",
				AdmissionBinding: "digest", Target: "/root/file", Class: "ordinary",
				Identity: "1:2:32768", RootGeneration: 1, PolicyRevision: 2,
				CoverageRevision: 3, Destination: "provider/model/profile",
			}}
			runtime := newToolRuntime(ToolRuntimeConfig{
				Registry: tools.NewRegistry(), ReadAdmission: host,
				Approval: ToolApprovalFunc(func(context.Context, permissions.PendingApproval) (permissions.ApprovalChoice, string, error) {
					return test.choice, test.redirect, nil
				}),
			})
			invocation := tools.Invocation{ConversationID: "c", TurnID: "t", ProviderCallID: "call", ToolID: tools.ID("read_file"), Arguments: []byte(`{"path":"/root/file"}`)}
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
