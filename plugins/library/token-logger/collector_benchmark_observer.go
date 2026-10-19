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

// collectorBenchmarkObserver is an internal, opt-in seam for measuring work in
// generated collector benchmarks. It is nil in normal application stores and
// is not part of the public API or runtime telemetry.
type collectorBenchmarkObserver struct {
	sourceBytesRead              int64
	sourceBytesHashed            int64
	recordsDecoded               int64
	parserRecordsDecoded         int64
	classificationRecordsDecoded int64
	eventInsertAttempts          int64
	historicalInsertAttempts     int64
	historicalEventIDs           map[string]struct{}
}

func digestSourceBytes(value []byte, observer *collectorBenchmarkObserver) string {
	if observer != nil {
		observer.sourceBytesHashed += int64(len(value))
	}
	return digest(value)
}

func digestRecordBytes(value []byte, observer *collectorBenchmarkObserver) string {
	if observer != nil {
		observer.sourceBytesHashed += int64(len(value))
	}
	return digest(value)
}
