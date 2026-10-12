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

package tokenlogger

// RecordOutcome is the client-neutral reason a source record did not produce
// a newly imported usage event.
type RecordOutcome string

const (
	OutcomeIgnoredNonUsage        RecordOutcome = "ignored_non_usage"
	OutcomeUnchangedSnapshot      RecordOutcome = "unchanged_snapshot"
	OutcomeDeferredIncompleteTail RecordOutcome = "deferred_incomplete_tail"
	OutcomeUnsupportedUsage       RecordOutcome = "unsupported_usage"
	OutcomeMalformedRecord        RecordOutcome = "malformed_record"
	OutcomeFailedSource           RecordOutcome = "failed_source"
)

// OutcomeCounts is deliberately a struct rather than a map so JSON output has
// a stable, complete set of fields across versions and providers.
type OutcomeCounts struct {
	IgnoredNonUsage        int `json:"ignored_non_usage"`
	UnchangedSnapshot      int `json:"unchanged_snapshot"`
	DeferredIncompleteTail int `json:"deferred_incomplete_tail"`
	UnsupportedUsage       int `json:"unsupported_usage"`
	MalformedRecord        int `json:"malformed_record"`
	FailedSource           int `json:"failed_source"`
}

// Add records one outcome.
func (counts *OutcomeCounts) Add(outcome RecordOutcome) {
	switch outcome {
	case OutcomeIgnoredNonUsage:
		counts.IgnoredNonUsage++
	case OutcomeUnchangedSnapshot:
		counts.UnchangedSnapshot++
	case OutcomeDeferredIncompleteTail:
		counts.DeferredIncompleteTail++
	case OutcomeUnsupportedUsage:
		counts.UnsupportedUsage++
	case OutcomeMalformedRecord:
		counts.MalformedRecord++
	case OutcomeFailedSource:
		counts.FailedSource++
	}
}

// Merge adds another result's outcome counts.
func (counts *OutcomeCounts) Merge(other OutcomeCounts) {
	counts.IgnoredNonUsage += other.IgnoredNonUsage
	counts.UnchangedSnapshot += other.UnchangedSnapshot
	counts.DeferredIncompleteTail += other.DeferredIncompleteTail
	counts.UnsupportedUsage += other.UnsupportedUsage
	counts.MalformedRecord += other.MalformedRecord
	counts.FailedSource += other.FailedSource
}

// Pending reports whether a final partial record is waiting for more bytes.
func (counts OutcomeCounts) Pending() bool { return counts.DeferredIncompleteTail > 0 }

// Incomplete reports whether accounting may be incomplete and needs attention.
func (counts OutcomeCounts) Incomplete() bool {
	return counts.UnsupportedUsage > 0 || counts.MalformedRecord > 0 || counts.FailedSource > 0
}

// ImportResult summarizes an import without collapsing normal, pending,
// warning, and failure cases into an opaque skipped count.
type ImportResult struct {
	Receipt  Receipt       `json:"-"`
	Outcomes OutcomeCounts `json:"outcomes"`
}

// Merge combines source results while keeping imported events and storage
// duplicates separate from record outcomes.
func (result *ImportResult) Merge(other ImportResult) {
	result.Receipt.Imported += other.Receipt.Imported
	result.Receipt.Duplicates += other.Receipt.Duplicates
	result.Outcomes.Merge(other.Outcomes)
}
