// Copyright 2026 Ayesh Almeida
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build linux

package sqlitevfsstudy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRunExercisesNamedVFSAndBackupRoutes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	report, err := Run(ctx, strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	if report.SourceCommit != strings.Repeat("a", 40) {
		t.Fatalf("source commit = %q", report.SourceCommit)
	}
	if report.SQLiteVersion == "" || report.NamedVFS == "" || report.DefaultVFS == "" {
		t.Fatalf("incomplete runtime provenance: %+v", report)
	}
	want := map[string]string{
		"named_vfs_read_control":                           "measured_pass",
		"named_vfs_write_refusal":                          "measured_refusal",
		"backup_plain_destination_control":                 "measured_unprotected_control",
		"backup_named_destination_refusal_and_fresh_retry": "measured_refusal",
	}
	for _, testCase := range report.Cases {
		if classification, ok := want[testCase.Name]; ok {
			if testCase.Classification != classification {
				t.Errorf("%s classification = %q, want %q", testCase.Name, testCase.Classification, classification)
			}
			delete(want, testCase.Name)
		}
	}
	if len(want) != 0 {
		t.Errorf("missing expected cases: %v", want)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "synthetic-marker-215") {
		t.Fatal("report contains fixture content instead of only effect classification")
	}
}

func TestRunRequiresSourceCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := Run(ctx, ""); err == nil {
		t.Fatal("Run accepted an absent source commit")
	}
}
