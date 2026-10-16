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

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDetachedCollectorHelper(t *testing.T) {
	if os.Getenv("KOMPOSYN_DETACHED_HELPER") != "1" {
		return
	}
	marker := os.Getenv("KOMPOSYN_DETACHED_MARKER")
	if marker == "" {
		os.Exit(2)
	}
	if err := os.WriteFile(marker, []byte("ready"), 0o600); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestStartDetachedCollectorLaunchesWithoutInheritedStreams(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "ready")
	t.Setenv("KOMPOSYN_DETACHED_HELPER", "1")
	t.Setenv("KOMPOSYN_DETACHED_MARKER", marker)
	if err := startDetachedCollector(executable, []string{"-test.run=^TestDetachedCollectorHelper$"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		contents, readErr := os.ReadFile(marker)
		if readErr == nil {
			if string(contents) != "ready" {
				t.Fatalf("detached marker = %q", contents)
			}
			return
		}
		if !os.IsNotExist(readErr) {
			t.Fatal(readErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("detached helper did not signal readiness")
}
