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
	"bytes"
	"errors"
	"strings"
	"unicode/utf8"
)

const searchSnippetMaxBytes = 2 << 10

var errSearchBudget = errors.New("search budget exceeded")
var errInvalidSearchRequest = errors.New("search request is invalid")

type searchBudget struct {
	bytes int
	work  int
}

type searchLineMatch struct {
	Line             int    `json:"line"`
	Offset           int    `json:"offset"`
	Snippet          string `json:"snippet"`
	SnippetTruncated bool   `json:"snippet_truncated,omitempty"`
}

type searchFileMatches struct {
	Matches     []searchLineMatch
	Unsupported bool
}

// searchUTF8LiteralLines applies the worker's deterministic single-line,
// case-sensitive literal semantics. Caller owns aggregate budgets across
// files and must not expose unsupported-file identities or errors.
func searchUTF8LiteralLines(content []byte, query string, budget *searchBudget) (searchFileMatches, error) {
	if budget == nil || query == "" || len(query) > 512 || !utf8.ValidString(query) || strings.ContainsAny(query, "\r\n\x00") {
		return searchFileMatches{}, errInvalidSearchRequest
	}
	if len(content) > MaxReadBytes {
		return searchFileMatches{Unsupported: true}, nil
	}
	if budget.bytes < len(content) {
		return searchFileMatches{}, errSearchBudget
	}
	budget.bytes -= len(content)
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return searchFileMatches{Unsupported: true}, nil
	}
	prefix, err := searchPrefixTable(query, budget)
	if err != nil {
		return searchFileMatches{}, err
	}
	var result searchFileMatches
	lineNumber := 1
	for start := 0; start <= len(content); {
		end := bytes.IndexByte(content[start:], '\n')
		lineEnd := len(content)
		next := len(content) + 1
		if end >= 0 {
			lineEnd = start + end
			next = lineEnd + 1
		}
		line := content[start:lineEnd]
		if len(line) > 0 && line[len(line)-1] == '\r' && end >= 0 {
			line = line[:len(line)-1]
		}
		match, err := firstLiteralMatch(line, query, prefix, budget)
		if err != nil {
			return searchFileMatches{}, err
		}
		if match >= 0 {
			snippet, truncated := searchSnippet(line, match, match+len(query))
			result.Matches = append(result.Matches, searchLineMatch{Line: lineNumber, Offset: match, Snippet: snippet, SnippetTruncated: truncated})
		}
		if next > len(content) {
			break
		}
		start = next
		lineNumber++
	}
	return result, nil
}

func searchPrefixTable(query string, budget *searchBudget) ([]int, error) {
	prefix := make([]int, len(query))
	for index, matched := 1, 0; index < len(query); index++ {
		for matched > 0 && query[index] != query[matched] {
			if err := spendSearchWork(budget, 1); err != nil {
				return nil, err
			}
			matched = prefix[matched-1]
		}
		if err := spendSearchWork(budget, 1); err != nil {
			return nil, err
		}
		if query[index] == query[matched] {
			matched++
		}
		prefix[index] = matched
	}
	return prefix, nil
}

func firstLiteralMatch(line []byte, query string, prefix []int, budget *searchBudget) (int, error) {
	matched := 0
	for index, value := range line {
		if err := spendSearchWork(budget, 1); err != nil {
			return -1, err
		}
		for matched > 0 && value != query[matched] {
			if err := spendSearchWork(budget, 1); err != nil {
				return -1, err
			}
			matched = prefix[matched-1]
		}
		if value == query[matched] {
			matched++
			if matched == len(query) {
				return index - len(query) + 1, nil
			}
		}
	}
	return -1, nil
}

func spendSearchWork(budget *searchBudget, amount int) error {
	if amount < 0 || budget.work < amount {
		return errSearchBudget
	}
	budget.work -= amount
	return nil
}

func searchSnippet(line []byte, matchStart, matchEnd int) (string, bool) {
	if len(line) <= searchSnippetMaxBytes {
		return string(line), false
	}
	available := searchSnippetMaxBytes - (matchEnd - matchStart)
	left := matchStart - available/2
	if left < 0 {
		left = 0
	}
	for left > 0 && !utf8.RuneStart(line[left]) {
		left--
	}
	right := min(len(line), left+searchSnippetMaxBytes)
	for right > left && right < len(line) && !utf8.RuneStart(line[right]) {
		right--
	}
	if right < matchEnd {
		right = matchEnd
		for right < len(line) && !utf8.RuneStart(line[right]) {
			right++
		}
		left = max(0, right-searchSnippetMaxBytes)
		for left > 0 && !utf8.RuneStart(line[left]) {
			left--
		}
	}
	return string(line[left:right]), left > 0 || right < len(line)
}

func validUTF8String(value string) bool { return utf8.ValidString(value) }
