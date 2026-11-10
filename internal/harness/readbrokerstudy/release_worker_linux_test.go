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
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestReleaseExecutableFitsWorkerImageAndSnapshotBounds(t *testing.T) {
	root := moduleRoot(t)
	output := filepath.Join(t.TempDir(), "komposyn")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-buildvcs=false",
		"-ldflags", "-s -w -buildid= -X main.cliVersion=0.0.0-test -X main.cliCommit=0000000000000000000000000000000000000000",
		"-o", output, "./cmd/harness")
	command.Dir = root
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOTOOLCHAIN=local")
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build release-shaped cmd/harness: %v\n%s", err, result)
	}

	image, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(int(image.Fd()), &stat); err != nil {
		t.Fatal(err)
	}
	if stat.Size <= 0 || stat.Size > maxWorkerSnapshotBytes {
		t.Fatalf("release worker image size=%d, allowed maximum=%d", stat.Size, maxWorkerSnapshotBytes)
	}
	if workerRootSizeBytes < stat.Size+2<<20 {
		t.Fatalf("worker root=%d leaves less than 2 MiB beyond release image size=%d", workerRootSizeBytes, stat.Size)
	}
	if err := validateStaticWorkerFile(image); err != nil {
		t.Fatalf("release image is not an accepted static worker: %v", err)
	}
	snapshot, err := createSealedWorkerSnapshot(image)
	if err != nil {
		t.Fatalf("release image could not be constructed as a sealed worker snapshot: %v", err)
	}
	if err := validateStaticWorkerFile(snapshot); err != nil {
		_ = snapshot.Close()
		t.Fatalf("sealed release snapshot is invalid: %v", err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatalf("close release snapshot: %v", err)
	}

	if descriptorBindSourcePathCompatible(image) {
		bound, err := openWorkerMountDescriptor(image, BindModeDescriptor)
		if err != nil {
			t.Fatalf("release image could not be opened for descriptor binding: %v", err)
		}
		if err := bound.Close(); err != nil {
			t.Fatalf("close release descriptor-bind source: %v", err)
		}
	}
}

func TestStage6OverlayRunsSyntheticRuntimeControls(t *testing.T) {
	root := moduleRoot(t)
	work := t.TempDir()
	overlay := filepath.Join(work, "overlay.json")
	content := `{"Replace":{"` + filepath.Join(root, "internal/harness/runtime/native_read_study_linux.go") + `":"` + filepath.Join(root, "testdata/native-stage6/runtime_study_linux.go") + `","` + filepath.Join(root, "internal/harness/sandbox/native_stage6_linux_test.go") + `":"` + filepath.Join(root, "testdata/native-stage6/sandbox_stage6_linux_test.go") + `"}}`
	if err := os.WriteFile(overlay, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(work, "stage6.test")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-c", "-buildvcs=false", "-tags=komposyn_stage6_study", "-overlay="+overlay, "-ldflags=-s -w", "-o", output, "./internal/harness/sandbox")
	command.Dir = root
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOTOOLCHAIN=local")
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("compile Stage 6 study overlay: %v\n%s", err, result)
	}

	reportPath := filepath.Join(work, "stage6-report.json")
	runCtx, runCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer runCancel()
	run := exec.CommandContext(runCtx, output,
		"-test.run=^(TestStage6SyntheticRuntimeReadForwarding|TestStage6ReadRefusedWithoutRootOrBackend|TestStage6IncompleteOwnerInventoryBlocksRuntimeAdmission)$",
		"-test.timeout=75s", "-test.count=1")
	run.Env = append(os.Environ(),
		"KOMPOSYN_STAGE6_REPORT="+reportPath,
		"KOMPOSYN_STAGE6_SOURCE_COMMIT=synthetic-control-regression",
		"KOMPOSYN_STAGE6_BWRAP="+filepath.Join(work, "intentionally-absent-bubblewrap"),
	)
	run.Dir = root
	if result, err := run.CombinedOutput(); err != nil {
		t.Fatalf("run non-native Stage 6 runtime controls: %v\n%s", err, result)
	}
	reportData, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read Stage 6 control report: %v", err)
	}
	var report struct {
		RunnerVersion string `json:"runner_version"`
		Kernel        string `json:"kernel"`
		Cases         []struct {
			ID           string `json:"case_id"`
			Status       string `json:"status"`
			EvidenceKind string `json:"evidence_kind"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatalf("decode Stage 6 control report: %v", err)
	}
	if report.RunnerVersion != "8" || report.Kernel == "" {
		t.Fatalf("Stage 6 report metadata runner_version=%q kernel=%q; want runner 8 and a kernel release", report.RunnerVersion, report.Kernel)
	}
	want := map[string]bool{
		"synthetic_once_authority_flow":        false,
		"read_refused_without_root_or_backend": false,
		"incomplete_owner_inventory":           false,
	}
	for _, item := range report.Cases {
		if _, required := want[item.ID]; !required {
			continue
		}
		if item.Status != "measured_pass" || item.EvidenceKind != "synthetic-runtime-composition" {
			t.Errorf("Stage 6 control %s: status=%q evidence=%q, want measured synthetic pass", item.ID, item.Status, item.EvidenceKind)
			continue
		}
		want[item.ID] = true
	}
	for id, passed := range want {
		if !passed {
			t.Errorf("Stage 6 synthetic runtime control %s is missing or did not pass", id)
		}
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test source path is unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../../.."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root not found: %v", err)
	}
	return root
}
