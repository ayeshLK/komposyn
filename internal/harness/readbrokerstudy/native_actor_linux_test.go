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
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const nativeActorTestHelper = "KOMPOSYN_READ_BROKER_ACTOR_TEST_HELPER"
const nativeActorTestArgs = "KOMPOSYN_READ_BROKER_ACTOR_TEST_ARGS"
const nativeActorEnvironmentSentinel = "KOMPOSYN_NATIVE_ACTOR_PARENT_SECRET_SENTINEL"

func TestNativeActorHelperProcess(t *testing.T) {
	if os.Getenv(nativeActorTestHelper) != "1" {
		return
	}
	if os.Getenv(nativeActorEnvironmentSentinel) != "" {
		os.Exit(4)
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv(nativeActorTestArgs)), &args); err != nil {
		os.Exit(5)
	}
	handled, err := RunNativeActorMode(args)
	if !handled || err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func privateNativeActorTestRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func startTestNativeActor(t *testing.T, root string, action NativeActorAction, source, target string) *NativeActor {
	t.Helper()
	args, err := nativeActorArgs(os.Args[0], root, action, source, target)
	if err != nil {
		t.Fatal(err)
	}
	encodedArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	commandArgs := []string{"-test.run=^TestNativeActorHelperProcess$"}
	t.Setenv(nativeActorEnvironmentSentinel, "must-not-be-inherited")
	actor, err := startNativeActor(context.Background(), os.Args[0], commandArgs, []string{nativeActorTestHelper + "=1", nativeActorTestArgs + "=" + string(encodedArgs)})
	if err != nil {
		t.Fatalf("start native actor: %v", err)
	}
	t.Cleanup(func() {
		if err := actor.Close(); err != nil {
			t.Errorf("close native actor: %v", err)
		}
	})
	return actor
}

func TestNativeActorUsesReadyRunDoneAndConfirmsExit(t *testing.T) {
	root := privateNativeActorTestRoot(t)
	source := filepath.Join(root, "approved", "original.fixture")
	target := filepath.Join(root, "outside", "original.fixture")
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	want := []byte("synthetic moved object\n")
	if err := os.WriteFile(source, want, 0o600); err != nil {
		t.Fatal(err)
	}
	actor := startTestNativeActor(t, root, NativeActorRename, "approved/original.fixture", "outside/original.fixture")
	if !actor.Ready() || actor.TransitionCompleted() || actor.Joined() {
		t.Fatalf("initial actor state ready=%t changed=%t joined=%t", actor.Ready(), actor.TransitionCompleted(), actor.Joined())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := actor.Run(ctx); err != nil {
		t.Fatalf("run actor: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != string(want) {
		t.Fatalf("actor target=%q err=%v, want original fixture", got, err)
	}
	if !actor.TransitionCompleted() || !actor.Joined() {
		t.Fatalf("completed actor state changed=%t joined=%t", actor.TransitionCompleted(), actor.Joined())
	}
	if err := actor.Run(ctx); !errors.Is(err, ErrNativeActorProtocol) {
		t.Fatalf("second actor run=%v, want protocol refusal", err)
	}
}

func TestNativeActorReplaceKeepsTheApprovedObjectAvailable(t *testing.T) {
	root := privateNativeActorTestRoot(t)
	source := filepath.Join(root, "target.fixture")
	archive := filepath.Join(root, "opened-object.fixture")
	original := []byte("original approved object\n")
	if err := os.WriteFile(source, original, 0o600); err != nil {
		t.Fatal(err)
	}
	actor := startTestNativeActor(t, root, NativeActorReplace, "target.fixture", "opened-object.fixture")
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := actor.Run(ctx); err != nil {
		t.Fatalf("replace actor: %v", err)
	}
	gotOriginal, err := os.ReadFile(archive)
	if err != nil || string(gotOriginal) != string(original) {
		t.Fatalf("archived original=%q err=%v", gotOriginal, err)
	}
	gotReplacement, err := os.ReadFile(source)
	if err != nil || string(gotReplacement) != "actor-substitute\n" {
		t.Fatalf("replacement=%q err=%v", gotReplacement, err)
	}
}

func TestNativeActorPathAndActionValidation(t *testing.T) {
	root := privateNativeActorTestRoot(t)
	source := filepath.Join(root, "source.fixture")
	if err := os.WriteFile(source, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		action NativeActorAction
		source string
	}{
		{name: "unsupported action", action: "shell", source: "source.fixture"},
		{name: "parent traversal", action: NativeActorRename, source: "../outside"},
		{name: "absolute source", action: NativeActorRename, source: source},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := nativeActorArgs(os.Args[0], root, test.action, test.source, "target.fixture"); err == nil {
				t.Fatal("invalid actor request was accepted")
			}
		})
	}
}

func TestNativeActorRejectsSymlinkedFixtureParentWithoutEscapingRoot(t *testing.T) {
	root := privateNativeActorTestRoot(t)
	outside := privateNativeActorTestRoot(t)
	source := filepath.Join(outside, "source.fixture")
	if err := os.WriteFile(source, []byte("outside fixture sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "redirect")); err != nil {
		t.Fatal(err)
	}
	actor := startTestNativeActor(t, root, NativeActorRename, "redirect/source.fixture", "redirect/moved.fixture")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := actor.Run(ctx); !errors.Is(err, ErrNativeActorProtocol) {
		t.Fatalf("actor through symlink parent=%v, want protocol refusal", err)
	}
	if !actor.Joined() {
		t.Fatal("rejected actor was not reaped")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("outside fixture sentinel was changed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "moved.fixture")); !os.IsNotExist(err) {
		t.Fatalf("actor escaped fixture root: moved path stat=%v", err)
	}
}

func TestNativeActorRequiresPrivateSubdirectoryOfTempRoot(t *testing.T) {
	root := privateNativeActorTestRoot(t)
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := nativeActorArgs(os.Args[0], root, NativeActorRename, "source.fixture", "target.fixture"); err == nil {
		t.Fatal("non-private fixture root was accepted")
	}
}

func TestNativeActorTimeoutKillsAndJoinsActor(t *testing.T) {
	root := privateNativeActorTestRoot(t)
	source := filepath.Join(root, "source.fixture")
	if err := os.WriteFile(source, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	actor := startTestNativeActor(t, root, NativeActorRename, "source.fixture", "target.fixture")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := actor.Run(ctx)
	if !errors.Is(err, ErrNativeActorProtocol) {
		t.Fatalf("cancelled actor run=%v, want bounded protocol failure", err)
	}
	if !actor.Joined() {
		t.Fatal("timed out actor process was not joined")
	}
	if actor.TransitionCompleted() {
		t.Fatal("actor marked a cancelled transition complete")
	}
}

func TestRunNativeActorModeIgnoresUnrelatedArguments(t *testing.T) {
	handled, err := RunNativeActorMode([]string{"--unrelated"})
	if err != nil || handled {
		t.Fatalf("unrelated mode=(%t,%v), want unhandled", handled, err)
	}
}
