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
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestStage6ExpectedWorkerImageIsSealedAndDigestBound(t *testing.T) {
	imagePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	imageBytes, err := os.ReadFile(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(imageBytes)
	expected := hex.EncodeToString(digest[:])

	snapshot, err := sealedExpectedWorkerImage(imagePath, expected)
	if err != nil {
		t.Fatalf("matching worker image digest was rejected: %v", err)
	}
	if err := validateStaticWorkerFile(snapshot); err != nil {
		_ = snapshot.Close()
		t.Fatalf("sealed image is not an accepted static executable: %v", err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatalf("close sealed image: %v", err)
	}

	wrongDigest := append([]byte(nil), digest[:]...)
	wrongDigest[0] ^= 0xff
	if _, err := sealedExpectedWorkerImage(imagePath, hex.EncodeToString(wrongDigest)); err == nil {
		t.Fatal("mismatched image digest was accepted")
	}
	if _, err := sealedExpectedWorkerImage(imagePath, "../not-a-digest"); err == nil {
		t.Fatal("malformed image digest was accepted")
	}

	symlinkPath := filepath.Join(t.TempDir(), "worker-link")
	if err := os.Symlink(imagePath, symlinkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := sealedExpectedWorkerImage(symlinkPath, expected); err == nil {
		t.Fatal("symlink worker image was accepted")
	}
}
