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

//go:build !windows

package auth

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRegisterSensitiveJSONStoreRejectsFIFOWithoutPeer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.fifo")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	protector := protectionFunc(func(context.Context, string, string, string, *os.File) error {
		t.Fatal("special file was registered")
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- RegisterSensitiveJSONStore(ctx, protector, SensitiveOwnerAuth, SensitiveClassAuth, path)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO registration unexpectedly succeeded")
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("FIFO registration blocked without a peer")
	}
}
