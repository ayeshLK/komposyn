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

package openai

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
)

const oauthProcessHelperEnv = "KOMPOSYN_OAUTH_PROCESS_HELPER"

func TestOAuthProcessHelper(t *testing.T) {
	mode := os.Getenv(oauthProcessHelperEnv)
	if mode == "" {
		return
	}
	path := os.Getenv("KOMPOSYN_OAUTH_PROCESS_PATH")
	endpoint := os.Getenv("KOMPOSYN_OAUTH_PROCESS_ENDPOINT")
	_, _ = os.Stdout.WriteString("ready\n")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	manager := New(auth.Store{Path: path})
	manager.token, manager.client = endpoint, &http.Client{Timeout: 5 * time.Second}
	_, access, err := manager.Resolve(context.Background(), os.Getenv("KOMPOSYN_OAUTH_PROCESS_PROFILE"))
	if mode == "unknown" {
		if !errors.Is(err, auth.ErrOAuthRefreshOutcomeUnknown) {
			t.Fatalf("resolve error=%v, want unknown refresh outcome", err)
		}
	} else if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if mode == "resolve" && access != "rotated-process-access" {
		t.Fatalf("resolved stale token %q", access)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if mode == "unknown" {
		_, _ = fmt.Fprintln(os.Stdout, "unknown")
	} else {
		_, _ = fmt.Fprintln(os.Stdout, "resolved")
	}
	os.Exit(0)
}

func TestUnknownRefreshOutcomeSurvivesProcessAndBlocksReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store := auth.Store{Path: path}
	profile := addOAuthTestProfile(t, store, time.Now().Add(-time.Minute))
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("unexpected request method %s", r.Method)
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("close consumed response: %v", err)
			return
		}
		_ = conn.Close()
	}))
	defer server.Close()
	for i := 0; i < 2; i++ {
		child := startOAuthProcess(t, "unknown", path, profile.ID, server.URL)
		child.ready(t)
		child.release()
		line, err := child.stdout.ReadString('\n')
		if err != nil || strings.TrimSpace(line) != "unknown" {
			t.Fatalf("child result=%q err=%v", line, err)
		}
		if err := child.cmd.Wait(); err != nil {
			t.Fatalf("uncertain refresh child failed: %v", err)
		}
	}
	stored, err := store.Get(profile.ID)
	if err != nil || !stored.Auth.OAuth.RefreshOutcomeUnknown || !stored.Auth.OAuth.ReconnectRequired || stored.Auth.OAuth.RefreshToken != profile.Auth.OAuth.RefreshToken {
		t.Fatalf("unknown state not persisted safely: %+v err=%v", stored.Auth.OAuth, err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("consumed refresh token replayed %d times", got)
	}
}

func TestConcurrentRefreshAcrossProcessesUsesOneReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store := auth.Store{Path: path}
	profile := addOAuthTestProfile(t, store, time.Now().Add(-time.Minute))
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	releaseHandler := closeOnce(release)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse refresh form: %v", err)
		}
		if r.Form.Get("refresh_token") != profile.Auth.OAuth.RefreshToken {
			t.Errorf("request submitted wrong refresh token")
		}
		close(started)
		<-release
		_, _ = fmt.Fprint(w, `{"access_token":"rotated-process-access","refresh_token":"rotated-process-refresh","token_type":"Bearer","expires_in":3600}`)
	}))
	defer func() {
		releaseHandler()
		server.Close()
	}()
	first := startOAuthProcess(t, "resolve", path, profile.ID, server.URL)
	first.ready(t)
	first.release()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("first process did not begin refresh")
	}
	second := startOAuthProcess(t, "resolve", path, profile.ID, server.URL)
	second.ready(t)
	second.release()
	releaseHandler()
	first.waitResolved(t)
	second.waitResolved(t)
	if got := calls.Load(); got != 1 {
		t.Fatalf("token endpoint called %d times; want one exchange", got)
	}
	stored, err := store.Get(profile.ID)
	if err != nil || stored.Auth.OAuth.AccessToken != "rotated-process-access" || stored.Auth.OAuth.RefreshToken != "rotated-process-refresh" {
		t.Fatalf("rotated token set not persisted: profile=%+v err=%v", stored.Auth.OAuth, err)
	}
}

func TestRefreshHandlerCleanupReleasesBlockedServer(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	releaseHandler := closeOnce(release)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = fmt.Fprint(w, "released")
	}))
	requestDone := make(chan error, 1)
	go func() {
		response, err := http.Get(server.URL)
		if err == nil {
			_ = response.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		releaseHandler()
		server.Close()
		t.Fatal("blocked handler did not start")
	}
	releaseHandler()
	releaseHandler()
	closed := make(chan struct{})
	go func() { server.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("server close waited on a blocked refresh handler")
	}
	select {
	case err := <-requestDone:
		if err != nil {
			t.Fatalf("released refresh request failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("refresh request did not finish after cleanup release")
	}
}

func closeOnce(channel chan struct{}) func() {
	var once sync.Once
	return func() { once.Do(func() { close(channel) }) }
}

type oauthProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
}

func startOAuthProcess(t *testing.T, mode, path, profileID, endpoint string) oauthProcess {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOAuthProcessHelper$")
	cmd.Env = append(os.Environ(), oauthProcessHelperEnv+"="+mode, "KOMPOSYN_OAUTH_PROCESS_PATH="+path, "KOMPOSYN_OAUTH_PROCESS_PROFILE="+profileID, "KOMPOSYN_OAUTH_PROCESS_ENDPOINT="+endpoint)
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
	proc := oauthProcess{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout)}
	t.Cleanup(func() {
		cancel()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})
	return proc
}

func (p oauthProcess) ready(t *testing.T) {
	t.Helper()
	line, err := p.stdout.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("helper readiness=%q err=%v", line, err)
	}
}

func (p oauthProcess) release() {
	_, _ = p.stdin.Write([]byte("go\n"))
}

func (p oauthProcess) waitResolved(t *testing.T) {
	t.Helper()
	line, err := p.stdout.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "resolved" {
		t.Fatalf("helper result=%q err=%v", line, err)
	}
	if err := p.cmd.Wait(); err != nil {
		t.Fatalf("helper process failed: %v", err)
	}
}
