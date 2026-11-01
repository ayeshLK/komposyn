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

package permissions

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestRulePrecedenceAndGrantEvaluation(t *testing.T) {
	store := NewMemoryStore()
	root := t.TempDir()
	resource := ResourceRequest{Kind: ResourceFilesystem, Target: filepath.Join(root, "file.txt"), Operation: OperationRead}
	request := Request{ConversationID: "conv", CallID: "call", Requirements: []Requirement{{Kind: RequirementAction, Key: "r1", ToolID: "exec_command", Operation: OperationExecute, ActionKey: "exact-command", SessionAllowed: true}, {Kind: RequirementResource, Key: "r2", ToolID: "read_file", Operation: OperationRead, Resource: &resource, SessionAllowed: true}}}
	grant := Grant{ID: "g1", ConversationID: "conv", ToolID: "read_file", Kind: RequirementResource, Operation: OperationRead, Lifetime: LifetimeConversation, Origin: "user", ResourceTarget: root, Resource: &ResourceScope{Kind: ResourceFilesystem, Root: root, Recursive: true, Operations: []Operation{OperationRead, OperationWrite}}}
	if err := store.Add(grant); err != nil {
		t.Fatal(err)
	}
	decision := (Evaluator{Grants: store}).Evaluate(request)
	if len(decision.Allowed) != 1 || len(decision.Missing) != 1 || len(decision.Denied) != 0 {
		t.Fatalf("unexpected grant decision: %+v", decision)
	}
	if decision.Missing[0].ToolID != "exec_command" {
		t.Fatalf("missing requirement=%+v", decision.Missing[0])
	}
	evaluator := Evaluator{Grants: store, Rules: []Rule{{Kind: RequirementResource, ToolID: "read_file", Operation: OperationRead, Effect: EffectAllow}, {Kind: RequirementResource, ToolID: "read_file", Operation: OperationRead, Effect: EffectAsk}, {Kind: RequirementResource, ToolID: "read_file", Operation: OperationRead, Effect: EffectDeny}}}
	decision = evaluator.Evaluate(request)
	if len(decision.Denied) != 1 || decision.Denied[0].ToolID != "read_file" {
		t.Fatalf("deny did not take precedence: %+v", decision)
	}
	evaluator.Rules = evaluator.Rules[:2]
	decision = evaluator.Evaluate(request)
	if len(decision.Denied) != 0 || len(decision.Missing) != 2 {
		t.Fatalf("explicit ask did not override grants/allow: %+v", decision)
	}
}

func TestWriteGrantIncludesReadButNotDeleteExecuteOrOtherPath(t *testing.T) {
	root := t.TempDir()
	store := NewMemoryStore()
	grant := Grant{ID: "g", ConversationID: "c", ToolID: "file", Kind: RequirementResource, Operation: OperationWrite, Lifetime: LifetimeConversation, Origin: "user", ResourceTarget: root, Resource: &ResourceScope{Kind: ResourceFilesystem, Root: root, Recursive: true, Operations: []Operation{OperationWrite}}}
	if err := store.Add(grant); err != nil {
		t.Fatal(err)
	}
	makeRequest := func(target string, op Operation) Request {
		resource := ResourceRequest{Kind: ResourceFilesystem, Target: target, Operation: op}
		return Request{ConversationID: "c", CallID: "call", Requirements: []Requirement{{Kind: RequirementResource, Key: string(op), ToolID: "file", Operation: op, Resource: &resource, SessionAllowed: true}}}
	}
	inside := filepath.Join(root, "nested", "file")
	if got := (Evaluator{Grants: store}).Evaluate(makeRequest(inside, OperationRead)); !got.Ready() {
		t.Fatalf("write grant did not imply read: %+v", got)
	}
	for _, op := range []Operation{OperationDelete, OperationExecute} {
		if got := (Evaluator{Grants: store}).Evaluate(makeRequest(inside, op)); len(got.Missing) != 1 {
			t.Fatalf("write grant covered %s: %+v", op, got)
		}
	}
	outside := filepath.Join(root+"-sibling", "secret")
	if got := (Evaluator{Grants: store}).Evaluate(makeRequest(outside, OperationWrite)); len(got.Missing) != 1 {
		t.Fatalf("recursive scope escaped its path boundary: %+v", got)
	}
}

func TestActionAndResourcePolicyCannotCrossAuthorize(t *testing.T) {
	root := t.TempDir()
	store := NewMemoryStore()
	if err := store.Add(Grant{ID: "action", ConversationID: "c", ToolID: "file", Kind: RequirementAction, Operation: OperationRead, ActionKey: "same", Lifetime: LifetimeConversation, Origin: "user"}); err != nil {
		t.Fatal(err)
	}
	resource := ResourceRequest{Kind: ResourceFilesystem, Target: filepath.Join(root, "f"), Operation: OperationRead}
	request := Request{ConversationID: "c", Requirements: []Requirement{{Kind: RequirementResource, Key: "r", ToolID: "file", Operation: OperationRead, Resource: &resource}}}
	if got := (Evaluator{Grants: store}).Evaluate(request); len(got.Missing) != 1 {
		t.Fatalf("action grant authorized a resource request: %+v", got)
	}
	if got := (Evaluator{Rules: []Rule{{Kind: RequirementAction, ToolID: "file", Operation: OperationRead, Effect: EffectAllow}}}).Evaluate(request); len(got.Missing) != 1 {
		t.Fatalf("action rule authorized a resource request: %+v", got)
	}
	request.Requirements[0].Kind = RequirementAction
	request.Requirements[0].Resource = nil
	request.Requirements[0].ActionKey = "same"
	request.Requirements[0].SessionAllowed = true
	if got := (Evaluator{Grants: store}).Evaluate(request); !got.Ready() {
		t.Fatalf("exact action grant did not authorize action: %+v", got)
	}
}

func TestMalformedRequirementFailsClosed(t *testing.T) {
	decision := (Evaluator{Rules: []Rule{{Kind: RequirementAction, Operation: OperationExecute, Effect: EffectAllow}}}).Evaluate(Request{Requirements: []Requirement{{ToolID: "exec_command", Operation: OperationExecute, ActionKey: "x"}}})
	if len(decision.Denied) != 1 || decision.Ready() {
		t.Fatalf("malformed untyped requirement did not fail closed: %+v", decision)
	}
}

func TestDuplicateRequirementKeysFailClosed(t *testing.T) {
	request := Request{Requirements: []Requirement{
		{Kind: RequirementAction, Key: "same", ToolID: "exec_command", Operation: OperationExecute, ActionKey: "one"},
		{Kind: RequirementAction, Key: "same", ToolID: "exec_command", Operation: OperationExecute, ActionKey: "two"},
	}}
	decision := (Evaluator{}).Evaluate(request)
	if len(decision.Denied) != 2 || decision.Ready() {
		t.Fatalf("duplicate requirement keys did not fail closed: %+v", decision)
	}
}

func TestOnceGrantIsCallBoundConsumedAndNotReusable(t *testing.T) {
	store := NewMemoryStore()
	grant := Grant{ID: "once", ConversationID: "c", CallID: "first", RequirementKey: "request-digest", ToolID: "exec_command", Kind: RequirementAction, Operation: OperationExecute, ActionKey: "command-digest", Lifetime: LifetimeOnce, Origin: "user"}
	if err := store.Add(grant); err != nil {
		t.Fatal(err)
	}
	evaluator := Evaluator{Grants: store}
	request := Request{ConversationID: "c", CallID: "first", Requirements: []Requirement{{Kind: RequirementAction, Key: "request-digest", ToolID: "exec_command", Operation: OperationExecute, ActionKey: "command-digest"}}}
	if got := evaluator.Evaluate(request); !got.Ready() {
		t.Fatalf("once grant did not cover exact call: %+v", got)
	}
	otherCall := request
	otherCall.CallID = "second"
	if got := evaluator.Evaluate(otherCall); len(got.Missing) != 1 {
		t.Fatalf("once grant covered another call: %+v", got)
	}
	if err := store.ConsumeOnce("c", "first", []string{"request-digest"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ConsumeOnce("c", "first", []string{"request-digest"}); !errors.Is(err, ErrOnceGrantMissing) {
		t.Fatalf("second consume error=%v", err)
	}
	if got := evaluator.Evaluate(request); len(got.Missing) != 1 {
		t.Fatalf("consumed once grant remained active: %+v", got)
	}
}

func TestAtomicGrantConsumptionRejectsRevocationAfterEvaluation(t *testing.T) {
	for _, lifetime := range []Lifetime{LifetimeOnce, LifetimeConversation} {
		t.Run(string(lifetime), func(t *testing.T) {
			store := NewMemoryStore()
			grant := Grant{
				ID: "grant", ConversationID: "c", ToolID: "lookup", Kind: RequirementAction,
				Operation: OperationRead, ActionKey: "digest", Lifetime: lifetime, Origin: "user",
			}
			request := Request{ConversationID: "c", CallID: "call", Requirements: []Requirement{{
				Kind: RequirementAction, Key: "action", ToolID: "lookup", Operation: OperationRead,
				ActionKey: "digest", SessionAllowed: true,
			}}}
			if lifetime == LifetimeOnce {
				grant.CallID = request.CallID
				grant.RequirementKey = request.Requirements[0].Key
			}
			if err := store.Add(grant); err != nil {
				t.Fatal(err)
			}
			decision := (Evaluator{Grants: store}).Evaluate(request)
			if !decision.Ready() {
				t.Fatalf("grant did not authorize request before revocation: %+v", decision)
			}
			if err := store.Revoke("c", grant.ID); err != nil {
				t.Fatal(err)
			}
			if err := store.ConsumeOnceMatchingAtRevision(request, decision.Revision); !errors.Is(err, ErrGrantRevisionChanged) {
				t.Fatalf("stale authorization was accepted: %v", err)
			}
			if after := (Evaluator{Grants: store}).Evaluate(request); after.Ready() {
				t.Fatalf("revoked grant still authorized request: %+v", after)
			}
		})
	}
}

func TestDeleteRequiresPerInvocationGrantAndCannotReceiveSessionGrant(t *testing.T) {
	store := NewMemoryStore()
	root := filepath.Join(t.TempDir(), "file")
	if err := store.Add(Grant{ID: "delete", ConversationID: "c", ToolID: "delete_path", Kind: RequirementResource, Operation: OperationDelete, Lifetime: LifetimeOnce, CallID: "call", RequirementKey: "delete-request", Origin: "user", ResourceTarget: root, Resource: &ResourceScope{Kind: ResourceFilesystem, Root: root, Operations: []Operation{OperationDelete}}}); err != nil {
		t.Fatal(err)
	}
	resource := ResourceRequest{Kind: ResourceFilesystem, Target: root, Operation: OperationDelete}
	request := Request{ConversationID: "c", CallID: "call", Requirements: []Requirement{{Kind: RequirementResource, Key: "delete-request", ToolID: "delete_path", Operation: OperationDelete, Resource: &resource}}}
	decision := (Evaluator{Grants: store}).Evaluate(request)
	if len(decision.Allowed) != 1 {
		t.Fatalf("per-invocation delete grant not honored: %+v", decision)
	}
	err := store.Add(Grant{ID: "bad-delete-session", ConversationID: "c", ToolID: "delete_path", Kind: RequirementResource, Operation: OperationDelete, Lifetime: LifetimeConversation, Origin: "user", ResourceTarget: root, Resource: &ResourceScope{Kind: ResourceFilesystem, Root: root, Operations: []Operation{OperationDelete}}})
	if !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("delete session grant error=%v", err)
	}
	err = store.Add(Grant{ID: "bad-delete-action-session", ConversationID: "c", ToolID: "delete_path", Kind: RequirementAction, Operation: OperationDelete, Lifetime: LifetimeConversation, Origin: "user", ActionKey: "exact-delete"})
	if !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("delete action session grant error=%v", err)
	}
}

func TestCommandActionKeyBindsArgumentsCWDAndAuthority(t *testing.T) {
	cwd := filepath.Clean(t.TempDir())
	first, err := CommandActionKey("git", []string{"status", "--short"}, cwd, []string{"read:/repo", "execute"})
	if err != nil {
		t.Fatal(err)
	}
	reorderedAuthority, err := CommandActionKey("git", []string{"status", "--short"}, cwd, []string{"execute", "read:/repo"})
	if err != nil || first != reorderedAuthority {
		t.Fatalf("authority order changed identity: %q %q err=%v", first, reorderedAuthority, err)
	}
	for _, change := range []struct {
		command string
		args    []string
		cwd     string
		auth    []string
	}{{"git", []string{"status", "--branch"}, cwd, []string{"execute", "read:/repo"}}, {"git", []string{"status", "--short"}, cwd + "-other", []string{"execute", "read:/repo"}}, {"git", []string{"status", "--short"}, cwd, []string{"execute"}}} {
		got, err := CommandActionKey(change.command, change.args, change.cwd, change.auth)
		if err != nil {
			t.Fatal(err)
		}
		if got == first {
			t.Fatalf("changed command authority reused identity: %+v", change)
		}
	}
}

func TestMemoryStoreRevisionRevokeClearAndConcurrentSnapshots(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Add(Grant{ID: "g1", ConversationID: "c", ToolID: "read", Kind: RequirementAction, Operation: OperationRead, Lifetime: LifetimeConversation, Origin: "user", ActionKey: "read-all"}); err != nil {
		t.Fatal(err)
	}
	revision, grants := store.Snapshot("c")
	if revision != 1 || len(grants) != 1 {
		t.Fatalf("initial snapshot revision=%d grants=%d", revision, len(grants))
	}
	grants[0].ActionKey = "mutated"
	if _, next := store.Snapshot("c"); next[0].ActionKey != "read-all" {
		t.Fatal("snapshot exposed mutable grant state")
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				store.Snapshot("c")
			}
		}()
	}
	if err := store.Revoke("c", "g1"); err != nil {
		t.Fatal(err)
	}
	store.Clear("c")
	wg.Wait()
	revision, grants = store.Snapshot("c")
	if revision != 2 || len(grants) != 0 {
		t.Fatalf("final snapshot revision=%d grants=%d", revision, len(grants))
	}
}

func TestNetworkSessionGrantMatchesExactOrigin(t *testing.T) {
	store := NewMemoryStore()
	scope := &ResourceScope{Kind: ResourceNetwork, Root: "https://example.test", Operations: []Operation{OperationNetwork}}
	if err := store.Add(Grant{ID: "web", ConversationID: "c", ToolID: "web_fetch", Kind: RequirementResource, Operation: OperationNetwork, Lifetime: LifetimeConversation, Origin: "user", ResourceTarget: "https://example.test", Resource: scope}); err != nil {
		t.Fatal(err)
	}
	request := func(target string) Request {
		resource := ResourceRequest{Kind: ResourceNetwork, Target: target, Operation: OperationNetwork}
		return Request{ConversationID: "c", CallID: "call", Requirements: []Requirement{{Kind: RequirementResource, Key: "fetch", ToolID: "web_fetch", Operation: OperationNetwork, Resource: &resource, SessionAllowed: true}}}
	}
	if got := (Evaluator{Grants: store}).Evaluate(request("https://EXAMPLE.test/path")); !got.Ready() {
		t.Fatalf("origin normalization failed: %+v", got)
	}
	if got := (Evaluator{Grants: store}).Evaluate(request("https://example.test:8443/path")); len(got.Missing) != 1 {
		t.Fatalf("network grant crossed ports: %+v", got)
	}
}
