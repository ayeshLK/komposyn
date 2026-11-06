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

//go:build linux && amd64

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const callbackStressHelperEnv = "KOMPOSYN_WAL_SHM_CALLBACK_STRESS_HELPER"

func TestInstalledCallbacksSurviveStackGrowthAndGC(t *testing.T) {
	if os.Getenv(callbackStressHelperEnv) == "1" {
		if _, err := runWithCallbackStress("subprocess-test", true); err != nil {
			t.Fatalf("callback stress study failed: %v", err)
		}
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestInstalledCallbacksSurviveStackGrowthAndGC$")
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, callbackStressHelperEnv+"=") {
			env = append(env, entry)
		}
	}
	cmd.Env = append(env, callbackStressHelperEnv+"=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("callback stress subprocess failed: %v\n%s", err, output)
	}
}
