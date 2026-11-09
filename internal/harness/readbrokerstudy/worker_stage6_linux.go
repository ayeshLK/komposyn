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

package readbrokerstudy

import (
	"os"
)

// InspectBubblewrapForStage6 resolves an explicitly requested, already
// feature-detected worker bind mode for the disposable Stage 6 study. An empty
// mode preserves automatic selection. Backend trust validation and bounded
// capability probes always run before the requested mode is considered.
func InspectBubblewrapForStage6(path string, preferred BindMode) BackendInspection {
	result := InspectBubblewrap(path)
	if preferred == "" || !result.Trusted || result.Error != "" {
		return result
	}
	workerPath, err := os.Executable()
	if err != nil {
		result.Error = "running worker source is unavailable"
		return result
	}
	source, err := openCurrentExecutable(workerPath)
	if err != nil {
		result.Error = "running worker source could not be opened"
		return result
	}
	mode, modeErr := selectWorkerBindModePreferred(
		BubblewrapCapabilities{Options: result.Capabilities, BindMode: result.BindMode}, source, preferred,
	)
	closeErr := source.Close()
	if modeErr != nil || closeErr != nil {
		result.BindMode = ""
		result.Error = "trusted Bubblewrap does not support the requested Stage 6 bind mode"
		return result
	}
	result.BindMode = mode
	return result
}

// NewWorkerLauncherForStage6 selects one supported mode for the disposable
// Stage 6 study. It is absent from non-study builds and does not alter normal
// launcher selection.
func NewWorkerLauncherForStage6(bwrapPath, workerPath string, mode BindMode, holdBeforeRead bool) (WorkerLauncher, error) {
	return newWorkerLauncherWithBindMode(bwrapPath, workerPath, holdBeforeRead, mode)
}
