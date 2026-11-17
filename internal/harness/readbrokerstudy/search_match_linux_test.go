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

package readbrokerstudy

import (
	"strings"
	"testing"
)

func TestSearchUTF8LiteralLinesReturnsFirstMatchOncePerLine(t *testing.T) {
	result, err := searchUTF8LiteralLines([]byte("first needle needle\r\nno match\nneedle at end"), "needle", &searchBudget{bytes: 8 << 20, work: 5_000_000})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 2 || result.Matches[0].Line != 1 || result.Matches[0].Offset != 6 || result.Matches[1].Line != 3 || result.Matches[1].Offset != 0 {
		t.Fatalf("matches=%+v", result.Matches)
	}
}

func TestSearchUTF8LiteralLinesBoundsLongSnippetsAtRuneBoundary(t *testing.T) {
	line := strings.Repeat("é", 1500) + "needle" + strings.Repeat("界", 1500)
	result, err := searchUTF8LiteralLines([]byte(line), "needle", &searchBudget{bytes: 8 << 20, work: 5_000_000})
	if err != nil || len(result.Matches) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	snippet := result.Matches[0].Snippet
	if len(snippet) > searchSnippetMaxBytes || !strings.Contains(snippet, "needle") || !validUTF8String(snippet) || !result.Matches[0].SnippetTruncated {
		t.Fatalf("snippet bytes=%d match=%+v", len(snippet), result.Matches[0])
	}
}

func TestSearchUTF8LiteralLinesRejectsEmptyAndNewlineQueries(t *testing.T) {
	for _, query := range []string{"", "line\nbreak", "\x00"} {
		if _, err := searchUTF8LiteralLines([]byte("content"), query, &searchBudget{bytes: 8 << 20, work: 5_000_000}); err == nil {
			t.Fatalf("query %q accepted", query)
		}
	}
}

func TestSearchUTF8LiteralLinesMarksInvalidUTF8AndBudgets(t *testing.T) {
	if got, err := searchUTF8LiteralLines([]byte{0xff}, "x", &searchBudget{bytes: 8 << 20, work: 5_000_000}); err != nil || !got.Unsupported {
		t.Fatalf("invalid UTF-8 content result=%+v err=%v; want safe unsupported skip", got, err)
	}
	if _, err := searchUTF8LiteralLines([]byte("content"), "x", &searchBudget{bytes: 3, work: 5_000_000}); err == nil {
		t.Fatal("aggregate byte budget exceeded without refusal")
	}
	if _, err := searchUTF8LiteralLines([]byte("content"), strings.Repeat("x", 4), &searchBudget{bytes: 8 << 20, work: 2}); err == nil {
		t.Fatal("matcher work budget exceeded without refusal")
	}
}
