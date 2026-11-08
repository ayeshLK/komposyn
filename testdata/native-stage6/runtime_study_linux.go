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

package runtime

import (
	"encoding/json"
	"errors"
	"strings"
)

// EnableNativeReadStudy exposes only the reserved filesystem-read path to the
// separately built native acceptance test binary. This file is overlaid from
// testdata and is absent from normal builds and the shipped executable.
func EnableNativeReadStudy(chat *Chat) error {
	if chat == nil || chat.toolRuntime == nil || chat.toolRuntime.readAdmission == nil || chat.toolRuntime.readPublicationGate == nil {
		return errors.New("native read study composition is incomplete")
	}
	chat.toolRuntime.readPublicationTestMode = true
	return nil
}

// NativeReadStudyState returns only whether history is sensitive and its
// message count for lifecycle assertions; it never exposes message content.
func NativeReadStudyState(chat *Chat) (bool, int) {
	if chat == nil {
		return false, 0
	}
	chat.mu.Lock()
	defer chat.mu.Unlock()
	return chat.historySensitive, len(chat.history)
}

// NativeReadStudyHistoryEvidence returns only structural checks over private
// history. It never returns fixture contents or arbitrary transcript text.
func NativeReadStudyHistoryEvidence(chat *Chat, sensitiveMarkers ...string) (historySensitive bool, messageCount, toolResultCount int, markerFound, payloadFreeToolResults bool) {
	if chat == nil {
		return false, 0, 0, false, true
	}
	chat.mu.Lock()
	defer chat.mu.Unlock()
	messageCount = len(chat.history)
	historySensitive = chat.historySensitive
	payloadFreeToolResults = true
	containsMarker := func(value string) bool {
		for _, marker := range sensitiveMarkers {
			if marker != "" && strings.Contains(value, marker) {
				return true
			}
		}
		return false
	}
	for _, message := range chat.history {
		markerFound = markerFound || containsMarker(message.Text)
		for _, native := range message.Native {
			markerFound = markerFound || containsMarker(string(native))
		}
		if message.ToolResult == nil {
			continue
		}
		toolResultCount++
		markerFound = markerFound || containsMarker(message.ToolResult.Content)
		var envelope struct {
			Outcome string `json:"outcome"`
			Content string `json:"content"`
		}
		if json.Unmarshal([]byte(message.ToolResult.Content), &envelope) != nil || envelope.Outcome == "" || envelope.Content == "" {
			payloadFreeToolResults = false
		}
	}
	return historySensitive, messageCount, toolResultCount, markerFound, payloadFreeToolResults
}

// NativeReadStudyGrantState exposes only revision and remaining grant count.
func NativeReadStudyGrantState(chat *Chat) (uint64, int) {
	if chat == nil {
		return 0, 0
	}
	chat.mu.Lock()
	runtime := chat.toolRuntime
	conversationID := chat.conversationID
	chat.mu.Unlock()
	if runtime == nil || runtime.grants == nil {
		return 0, 0
	}
	revision, grants := runtime.grants.Snapshot(conversationID)
	return revision, len(grants)
}
