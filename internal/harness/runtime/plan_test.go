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
	"testing"

	"github.com/ayeshLK/komposyn/plugins/tools"
)

func TestConversationPlansReplaceAtomicallyAndRemainConversationLocal(t *testing.T) {
	plans := NewConversationPlans()
	initial := []tools.PlanStep{{Step: "inspect", Status: tools.PlanInProgress}}
	if err := plans.Replace("conversation-a", initial); err != nil {
		t.Fatal(err)
	}
	initial[0].Step = "caller mutation"
	if got := plans.Snapshot("conversation-a"); len(got) != 1 || got[0].Step != "inspect" {
		t.Fatalf("snapshot did not own plan state: %+v", got)
	}

	if err := plans.Replace("conversation-a", []tools.PlanStep{{Step: "first", Status: tools.PlanInProgress}, {Step: "second", Status: tools.PlanInProgress}}); err == nil {
		t.Fatal("invalid replacement accepted")
	}
	if got := plans.Snapshot("conversation-a"); len(got) != 1 || got[0].Step != "inspect" {
		t.Fatalf("invalid replacement partially changed state: %+v", got)
	}

	if err := plans.Replace("conversation-b", []tools.PlanStep{{Step: "separate", Status: tools.PlanPending}}); err != nil {
		t.Fatal(err)
	}
	copy := plans.Snapshot("conversation-a")
	copy[0].Step = "snapshot mutation"
	if got := plans.Snapshot("conversation-a"); got[0].Step != "inspect" {
		t.Fatalf("snapshot mutation changed stored plan: %+v", got)
	}
	if got := plans.Snapshot("conversation-b"); len(got) != 1 || got[0].Step != "separate" {
		t.Fatalf("conversation plans were shared: %+v", got)
	}

	plans.Clear("conversation-a")
	if got := plans.Snapshot("conversation-a"); len(got) != 0 {
		t.Fatalf("cleared plan remains: %+v", got)
	}
	if got := plans.Snapshot("conversation-b"); len(got) != 1 {
		t.Fatalf("clearing another conversation changed plan: %+v", got)
	}
	plans.Close("conversation-b")
	if err := plans.Replace("conversation-b", []tools.PlanStep{{Step: "late callback", Status: tools.PlanPending}}); err == nil {
		t.Fatal("closed conversation accepted a late plan update")
	}
	if got := plans.Snapshot("conversation-b"); len(got) != 0 {
		t.Fatalf("closed conversation retained a plan: %+v", got)
	}
}
