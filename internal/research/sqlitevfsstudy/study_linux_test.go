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
	"errors"
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

func TestWALClassificationRequiresAbsenceBeforeAndAfter(t *testing.T) {
	clear := classifyWALRefusal(true, nil, "delete", pathAbsent, pathAbsent, pathAbsent, pathAbsent)
	if clear.Classification != "measured_refusal" || clear.Attempted != true {
		t.Fatalf("clear refusal classification = %+v", clear)
	}
	for name, states := range map[string][2]string{
		"WAL appeared":          {pathPresent, pathAbsent},
		"SHM appeared":          {pathAbsent, pathPresent},
		"WAL inspection failed": {pathInspectionError, pathAbsent},
		"SHM inspection failed": {pathAbsent, pathInspectionError},
		"preexisting SHM":       {pathAbsent, pathAbsent},
		"request not attempted": {pathAbsent, pathAbsent},
	} {
		walAfter, shmAfter := states[0], states[1]
		attempted := name != "request not attempted"
		beforeWAL, beforeSHM := pathAbsent, pathAbsent
		if name == "preexisting SHM" {
			beforeSHM = pathPresent
		}
		got := classifyWALRefusal(attempted, nil, "delete", beforeWAL, walAfter, beforeSHM, shmAfter)
		if got.Classification != "inconclusive" {
			t.Errorf("%s classified %q: %+v", name, got.Classification, got)
		}
		if len(got.Effects) != 2 || !strings.Contains(got.Effects[0], walAfter) || !strings.Contains(got.Effects[1], shmAfter) {
			t.Errorf("%s effects do not reflect observations: %+v", name, got.Effects)
		}
	}
}

func TestBackupRefusalClassificationRequiresAbsentPaths(t *testing.T) {
	clear := classifyBackupRefusal(true, errors.New("refused"), pathAbsent, pathAbsent, true, errors.New("refused"), pathAbsent, pathAbsent)
	if clear.Classification != "measured_refusal" {
		t.Fatalf("clear refusal classification = %+v", clear)
	}
	cases := map[string][4]string{
		"artifact before":       {pathPresent, pathPresent, pathAbsent, pathAbsent},
		"artifact after":        {pathAbsent, pathPresent, pathAbsent, pathAbsent},
		"inspection error":      {pathAbsent, pathInspectionError, pathAbsent, pathAbsent},
		"retry artifact":        {pathAbsent, pathAbsent, pathAbsent, pathPresent},
		"retry inspect failure": {pathAbsent, pathAbsent, pathAbsent, pathInspectionError},
	}
	for name, states := range cases {
		got := classifyBackupRefusal(true, errors.New("refused"), states[0], states[1], true, errors.New("refused"), states[2], states[3])
		if got.Classification != "inconclusive" {
			t.Errorf("%s classified %q: %+v", name, got.Classification, got)
		}
		for _, state := range states {
			if !strings.Contains(got.Observed, state) {
				t.Errorf("%s omitted observed state %q: %+v", name, state, got)
			}
		}
	}
	unexpectedSuccess := classifyBackupRefusal(true, nil, pathAbsent, pathPresent, true, errors.New("refused"), pathAbsent, pathAbsent)
	if unexpectedSuccess.Classification != "inconclusive" || !strings.Contains(unexpectedSuccess.Observed, "returned_error(first/retry)=false/true") {
		t.Fatalf("unexpected success was overclassified: %+v", unexpectedSuccess)
	}
}
