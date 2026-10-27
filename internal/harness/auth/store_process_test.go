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

package auth

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const authProcessHelperEnv = "KOMPOSYN_AUTH_PROCESS_HELPER"

func TestAuthProcessHelper(t *testing.T) {
	mode := os.Getenv(authProcessHelperEnv)
	if mode == "" {
		return
	}
	path := os.Getenv("KOMPOSYN_AUTH_PROCESS_PATH")
	switch mode {
	case "hold-lock":
		unlock, err := lockPath(path)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		_, _ = os.Stdout.WriteString("ready\n")
		if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
			t.Fatal(err)
		}
	case "cancel-read":
		_, _ = os.Stdout.WriteString("waiting\n")
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		_, err := (Store{Path: path}).GetContext(ctx, "process-profile")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("read error=%v, want deadline exceeded", err)
		}
	case "stale-reconnect":
		store := Store{Path: path}
		profile, err := store.Get(os.Getenv("KOMPOSYN_AUTH_PROCESS_PROFILE"))
		if err != nil {
			t.Fatal(err)
		}
		candidate := cloneProfile(profile)
		candidate.Auth.OAuth.AccessToken = "candidate-access"
		candidate.Auth.OAuth.RefreshToken = "candidate-refresh"
		candidate.Auth.OAuth.ExpiresAt = time.Now().Add(time.Hour)
		_, _ = os.Stdout.WriteString("read\n")
		if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		if err := store.ReplaceOAuth(profile.ID, profile.Revision, candidate); !errors.Is(err, ErrOAuthProfileChanged) {
			t.Fatalf("stale reconnect result=%v", err)
		}
		_, _ = os.Stdout.WriteString("rejected\n")
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
	os.Exit(0)
}

func TestOAuthLifecycleMutationWinsAcrossProcesses(t *testing.T) {
	for _, mutation := range []string{"disconnect", "remove"} {
		t.Run(mutation, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			store := Store{Path: path}
			profile := addStoreOAuthProfile(t, store)
			child := startAuthHelper(t, "stale-reconnect", path, profile.ID)
			line, err := bufio.NewReader(child.stdout).ReadString('\n')
			if err != nil || strings.TrimSpace(line) != "read" {
				t.Fatalf("stale operation readiness=%q err=%v", line, err)
			}
			if mutation == "disconnect" {
				err = store.ClearOAuthTokens(profile.ID)
			} else {
				err = store.Remove(profile.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, _ = child.stdin.Write([]byte("continue\n"))
			line, err = bufio.NewReader(child.stdout).ReadString('\n')
			if err != nil || strings.TrimSpace(line) != "rejected" {
				t.Fatalf("stale operation result=%q err=%v", line, err)
			}
			if err := child.cmd.Wait(); err != nil {
				t.Fatalf("stale child failed: %v", err)
			}
			if mutation == "disconnect" {
				got, err := store.Get(profile.ID)
				if err != nil || !got.Auth.OAuth.ReconnectRequired || got.Auth.OAuth.AccessToken != "" {
					t.Fatalf("disconnect was overwritten: %+v err=%v", got.Auth.OAuth, err)
				}
			} else if _, err := store.Get(profile.ID); err == nil {
				t.Fatal("stale reconnect restored a removed profile")
			}
		})
	}
}

func TestNativeAuthLockExcludesAndCancelsAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store := Store{Path: path}
	if err := store.AddAPIKey("process-profile", "openai", "Test", "fake-key", nil); err != nil {
		t.Fatal(err)
	}
	owner := startAuthHelper(t, "hold-lock", path)
	ownerLine, err := bufio.NewReader(owner.stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(ownerLine) != "ready" {
		t.Fatalf("lock owner readiness=%q err=%v", ownerLine, err)
	}
	waiter := startAuthHelper(t, "cancel-read", path)
	line, err := bufio.NewReader(waiter.stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "waiting" {
		t.Fatalf("waiter readiness=%q err=%v", line, err)
	}
	if err := waiter.cmd.Wait(); err != nil {
		t.Fatalf("canceled child failed: %v", err)
	}
	// The owner still holds the native lock after the competing process exits.
	if unlock, err := lockPathContext(shortContext(t), path); err == nil {
		unlock()
		t.Fatal("parent acquired lock while child owner still held it")
	}
	_, _ = owner.stdin.Write([]byte("release\n"))
	if err := owner.cmd.Wait(); err != nil {
		t.Fatalf("lock owner failed: %v", err)
	}
	unlock, err := lockPath(path)
	if err != nil {
		t.Fatalf("lock not released after child exit: %v", err)
	}
	unlock()
	if _, key, err := store.Resolve("process-profile"); err != nil || key != "fake-key" {
		t.Fatalf("canceled waiter changed credentials: key=%q err=%v", key, err)
	}
}

func shortContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}

type authHelperProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func startAuthHelper(t *testing.T, mode, path string, profileIDs ...string) authHelperProcess {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAuthProcessHelper$")
	cmd.Env = append(os.Environ(), authProcessHelperEnv+"="+mode, "KOMPOSYN_AUTH_PROCESS_PATH="+path)
	if len(profileIDs) > 0 {
		cmd.Env = append(cmd.Env, "KOMPOSYN_AUTH_PROCESS_PROFILE="+profileIDs[0])
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})
	return authHelperProcess{cmd: cmd, stdin: stdin, stdout: stdout}
}
