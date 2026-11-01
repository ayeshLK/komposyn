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

package filesystem

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func openFixtureRoot(t *testing.T) (*Root, string) {
	t.Helper()
	base := t.TempDir()
	rootPath := filepath.Join(base, "authorized")
	if err := os.Mkdir(rootPath, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := OpenRoot(rootPath)
	if errors.Is(err, ErrUnsupported) {
		t.Skip("Linux openat2 enforcement is unavailable on this kernel")
	}
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root, rootPath
}

func TestLinuxDescriptorBoundReadWriteDelete(t *testing.T) {
	root, rootPath := openFixtureRoot(t)
	if err := os.Mkdir(filepath.Join(rootPath, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	readPath := filepath.Join(rootPath, "nested", "read.txt")
	if err := os.WriteFile(readPath, []byte("authorized text"), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := root.PrepareRead(readPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := read.ReadText(context.Background(), MaxReadBytes)
	_ = read.Close()
	if err != nil || result.Outcome != OutcomeSucceeded || result.Text != "authorized text" {
		t.Fatalf("read result=%+v err=%v", result, err)
	}

	createdPath := filepath.Join(rootPath, "nested", "created.txt")
	write, err := root.PrepareWrite(createdPath)
	if err != nil {
		t.Fatal(err)
	}
	mutation, err := write.WriteText(context.Background(), "created")
	_ = write.Close()
	if err != nil || mutation.Outcome != OutcomeSucceeded || mutation.BytesWritten != int64(len("created")) {
		t.Fatalf("create result=%+v err=%v", mutation, err)
	}
	write, err = root.PrepareWrite(createdPath)
	if err != nil {
		t.Fatal(err)
	}
	mutation, err = write.WriteText(context.Background(), "updated")
	_ = write.Close()
	if err != nil || mutation.Outcome != OutcomeSucceeded {
		t.Fatalf("update result=%+v err=%v", mutation, err)
	}
	if got, err := os.ReadFile(createdPath); err != nil || string(got) != "updated" {
		t.Fatalf("updated content=%q err=%v", got, err)
	}

	if _, err := root.PrepareDelete(filepath.Join(rootPath, "nested"), false); !errors.Is(err, ErrRecursiveIntentNeeded) {
		t.Fatalf("directory deletion without recursive intent err=%v", err)
	}
	deleteTarget, err := root.PrepareDelete(filepath.Join(rootPath, "nested"), true)
	if err != nil {
		t.Fatal(err)
	}
	mutation, err = deleteTarget.Delete(context.Background())
	_ = deleteTarget.Close()
	if err != nil || mutation.Outcome != OutcomeSucceeded || mutation.EntriesDeleted != 3 {
		t.Fatalf("recursive delete result=%+v err=%v", mutation, err)
	}
	if _, err := os.Stat(filepath.Join(rootPath, "nested")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recursive target remains: %v", err)
	}
}

func TestLinuxRejectsPathAliasesAndSymlinksForReadWrite(t *testing.T) {
	root, rootPath := openFixtureRoot(t)
	base := filepath.Dir(rootPath)
	sibling := filepath.Join(base, "authorized-sibling")
	if err := os.Mkdir(sibling, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(sibling, "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := root.PrepareRead(outside); !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("sibling-prefix read err=%v", err)
	}
	traversal := rootPath + string(filepath.Separator) + "nested" + string(filepath.Separator) + ".." + string(filepath.Separator) + "outside.txt"
	if _, err := root.PrepareRead(traversal); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("noncanonical traversal err=%v", err)
	}
	if _, err := root.PrepareRead(rootPath); !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("scope root used as file err=%v", err)
	}
	link := filepath.Join(rootPath, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := root.PrepareRead(link); !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlink read err=%v", err)
	}
	if _, err := root.PrepareWrite(link); !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlink write err=%v", err)
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "outside" {
		t.Fatalf("outside fixture changed: %q err=%v", got, err)
	}
	rootLink := filepath.Join(base, "root-link")
	if err := os.Symlink(rootPath, rootLink); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenRoot(rootLink); !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlink root accepted: %v", err)
	}
}

func TestLinuxHardlinkAliasesAreNotReadableOrWritable(t *testing.T) {
	root, rootPath := openFixtureRoot(t)
	base := filepath.Dir(rootPath)
	external := filepath.Join(base, "external.txt")
	inside := filepath.Join(rootPath, "alias.txt")
	if err := os.WriteFile(external, []byte("protected fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(external, inside); err != nil {
		t.Fatal(err)
	}
	if _, err := root.PrepareRead(inside); !errors.Is(err, ErrAliasedTarget) {
		t.Fatalf("hard-linked read err=%v", err)
	}
	if _, err := root.PrepareWrite(inside); !errors.Is(err, ErrAliasedTarget) {
		t.Fatalf("hard-linked write err=%v", err)
	}
	if got, err := os.ReadFile(external); err != nil || string(got) != "protected fixture" {
		t.Fatalf("external hard-link fixture changed: %q err=%v", got, err)
	}
}

func TestLinuxDeleteRemovesLinkEntryWithoutFollowingTarget(t *testing.T) {
	root, rootPath := openFixtureRoot(t)
	base := filepath.Dir(rootPath)
	external := filepath.Join(base, "protected.txt")
	if err := os.WriteFile(external, []byte("protected"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(rootPath, "link")
	if err := os.Symlink(external, link); err != nil {
		t.Fatal(err)
	}
	target, err := root.PrepareDelete(link, false)
	if err != nil {
		t.Fatal(err)
	}
	result, err := target.Delete(context.Background())
	_ = target.Close()
	if err != nil || result.Outcome != OutcomeSucceeded || result.EntriesDeleted != 1 {
		t.Fatalf("link deletion result=%+v err=%v", result, err)
	}
	if _, err := os.Lstat(link); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("link entry remains: %v", err)
	}
	if got, err := os.ReadFile(external); err != nil || string(got) != "protected" {
		t.Fatalf("link target changed: %q err=%v", got, err)
	}
}

func TestLinuxRecursiveDeleteDoesNotTraverseSymlinks(t *testing.T) {
	root, rootPath := openFixtureRoot(t)
	base := filepath.Dir(rootPath)
	external := filepath.Join(base, "external-dir")
	if err := os.Mkdir(external, 0o700); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(external, "keep.txt")
	if err := os.WriteFile(protected, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(rootPath, "tree")
	if err := os.Mkdir(tree, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "inside.txt"), []byte("remove"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(tree, "external-link")); err != nil {
		t.Fatal(err)
	}
	target, err := root.PrepareDelete(tree, true)
	if err != nil {
		t.Fatal(err)
	}
	result, err := target.Delete(context.Background())
	_ = target.Close()
	if err != nil || result.Outcome != OutcomeSucceeded || result.EntriesDeleted != 3 {
		t.Fatalf("recursive link-aware deletion result=%+v err=%v", result, err)
	}
	if got, err := os.ReadFile(protected); err != nil || string(got) != "keep" {
		t.Fatalf("recursive deletion traversed a link: %q err=%v", got, err)
	}
}

func TestLinuxChangedParentsAndTargetsFailClosed(t *testing.T) {
	root, rootPath := openFixtureRoot(t)
	parent := filepath.Join(rootPath, "parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	target, err := root.PrepareWrite(filepath.Join(parent, "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(rootPath), "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(rootPath, "moved-parent")
	if err := os.Rename(parent, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, parent); err != nil {
		t.Fatal(err)
	}
	result, err := target.WriteText(context.Background(), "must not escape")
	_ = target.Close()
	if !errors.Is(err, ErrTargetChanged) || result.Outcome != OutcomeNotExecuted {
		t.Fatalf("changed parent write=%+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "new.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("changed parent escaped to outside: %v", err)
	}

	file := filepath.Join(rootPath, "file.txt")
	if err := os.WriteFile(file, []byte("approved"), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := root.PrepareRead(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), file); err != nil {
		t.Fatal(err)
	}
	readResult, err := read.ReadText(context.Background(), MaxReadBytes)
	_ = read.Close()
	if !errors.Is(err, ErrTargetChanged) || readResult.Outcome != OutcomeNotExecuted {
		t.Fatalf("changed target read=%+v err=%v", readResult, err)
	}
}

func TestLinuxRootAndDeleteTargetReplacementFailClosed(t *testing.T) {
	root, rootPath := openFixtureRoot(t)
	base := filepath.Dir(rootPath)
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(outside, "protected.txt")
	if err := os.WriteFile(protected, []byte("protected"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(rootPath, "delete.txt")
	if err := os.WriteFile(file, []byte("approved"), 0o600); err != nil {
		t.Fatal(err)
	}
	deleteTarget, err := root.PrepareDelete(file, false)
	if err != nil {
		t.Fatal(err)
	}
	backend := root.backend.(*linuxRoot)
	backend.hook = func(stage string) {
		if stage == "delete-after-revalidate" {
			backend.hook = nil
			if err := os.Remove(file); err != nil {
				t.Errorf("remove prepared entry: %v", err)
			}
			if err := os.Symlink(protected, file); err != nil {
				t.Errorf("replace prepared entry with outside link: %v", err)
			}
		}
	}
	result, err := deleteTarget.Delete(context.Background())
	_ = deleteTarget.Close()
	if !errors.Is(err, ErrTargetChanged) || result.Outcome != OutcomeNotExecuted {
		t.Fatalf("substituted delete result=%+v err=%v", result, err)
	}
	if got, err := os.ReadFile(protected); err != nil || string(got) != "protected" {
		t.Fatalf("delete substitution reached link target: %q err=%v", got, err)
	}

	readFile := filepath.Join(rootPath, "root-bound.txt")
	if err := os.WriteFile(readFile, []byte("root-bound"), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := root.PrepareRead(readFile)
	if err != nil {
		t.Fatal(err)
	}
	movedRoot := filepath.Join(base, "moved-root")
	if err := os.Rename(rootPath, movedRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, rootPath); err != nil {
		t.Fatal(err)
	}
	readResult, err := read.ReadText(context.Background(), MaxReadBytes)
	_ = read.Close()
	if !errors.Is(err, ErrTargetChanged) || readResult.Outcome != OutcomeNotExecuted {
		t.Fatalf("replaced root read=%+v err=%v", readResult, err)
	}
}

func TestLinuxDescriptorBindingSurvivesPostCheckSubstitution(t *testing.T) {
	root, rootPath := openFixtureRoot(t)
	parent := filepath.Join(rootPath, "parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(rootPath), "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	backend := root.backend.(*linuxRoot)
	backend.hook = func(stage string) {
		if stage == "write-after-revalidate" {
			backend.hook = nil
			if err := os.Rename(parent, filepath.Join(rootPath, "moved-parent")); err != nil {
				t.Errorf("rename prepared parent: %v", err)
			}
			if err := os.Symlink(outside, parent); err != nil {
				t.Errorf("substitute outside symlink: %v", err)
			}
		}
	}
	write, err := root.PrepareWrite(filepath.Join(parent, "bound.txt"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := write.WriteText(context.Background(), "bound to prepared directory")
	_ = write.Close()
	if err != nil || result.Outcome != OutcomeSucceeded {
		t.Fatalf("descriptor-bound write=%+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "bound.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("post-check parent substitution escaped: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(rootPath, "moved-parent", "bound.txt")); err != nil || string(got) != "bound to prepared directory" {
		t.Fatalf("prepared directory capability lost: %q err=%v", got, err)
	}
}

func TestLinuxCancellationAndPartialOutcomeAreTruthful(t *testing.T) {
	root, rootPath := openFixtureRoot(t)
	file := filepath.Join(rootPath, "file.txt")
	if err := os.WriteFile(file, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	write, err := root.PrepareWrite(file)
	if err != nil {
		t.Fatal(err)
	}
	preCancelled, cancelBeforeWrite := context.WithCancel(context.Background())
	cancelBeforeWrite()
	preResult, preErr := write.WriteText(preCancelled, "must not start")
	if !errors.Is(preErr, context.Canceled) || preResult.Outcome != OutcomeCancelled {
		t.Fatalf("pre-cancelled write result=%+v err=%v", preResult, preErr)
	}
	if got, err := os.ReadFile(file); err != nil || string(got) != "original" {
		t.Fatalf("pre-cancelled write had an effect: %q err=%v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	backend := root.backend.(*linuxRoot)
	backend.hook = func(stage string) {
		if stage == "write-after-truncate" {
			backend.hook = nil
			cancel()
		}
	}
	result, err := write.WriteText(ctx, "replacement")
	_ = write.Close()
	if !errors.Is(err, context.Canceled) || result.Outcome != OutcomePartial || result.BytesWritten != 0 {
		t.Fatalf("cancelled update result=%+v err=%v", result, err)
	}
	if got, err := os.ReadFile(file); err != nil || len(got) != 0 {
		t.Fatalf("partial truncation not represented in fixture: %q err=%v", got, err)
	}

	tree := filepath.Join(rootPath, "tree")
	if err := os.Mkdir(tree, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "one"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "two"), []byte("2"), 0o600); err != nil {
		t.Fatal(err)
	}
	deleteTarget, err := root.PrepareDelete(tree, true)
	if err != nil {
		t.Fatal(err)
	}
	preCancelledDelete, cancelBeforeDelete := context.WithCancel(context.Background())
	cancelBeforeDelete()
	preDeleteResult, preDeleteErr := deleteTarget.Delete(preCancelledDelete)
	if !errors.Is(preDeleteErr, context.Canceled) || preDeleteResult.Outcome != OutcomeCancelled {
		t.Fatalf("pre-cancelled delete result=%+v err=%v", preDeleteResult, preDeleteErr)
	}
	if _, err := os.Stat(filepath.Join(tree, "one")); err != nil {
		t.Fatalf("pre-cancelled delete had an effect: %v", err)
	}
	deleteCtx, stopDelete := context.WithCancel(context.Background())
	backend.hook = func(stage string) {
		if stage == "delete-entry-removed" {
			backend.hook = nil
			stopDelete()
		}
	}
	result, err = deleteTarget.Delete(deleteCtx)
	_ = deleteTarget.Close()
	if !errors.Is(err, context.Canceled) || result.Outcome != OutcomePartial || result.EntriesDeleted != 1 {
		t.Fatalf("cancelled recursive delete result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(tree); err != nil {
		t.Fatalf("partially deleted tree should remain: %v", err)
	}
	stopDelete()
}

func TestLinuxResourceRequestsAndClosedCapabilities(t *testing.T) {
	root, rootPath := openFixtureRoot(t)
	file := filepath.Join(rootPath, "file.txt")
	if err := os.WriteFile(file, []byte("text"), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := root.PrepareRead(file)
	if err != nil {
		t.Fatal(err)
	}
	request := read.ResourceRequest()
	if request.Kind != "filesystem" || request.Target != file || request.Operation != "read" {
		t.Fatalf("read authorization requirement=%+v", request)
	}
	_ = read.Close()
	_ = read.Close()
	if _, err := read.ReadText(context.Background(), 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed read capability err=%v", err)
	}
	if strings.Contains(request.Target, "..") {
		t.Fatalf("canonical authorized target contains parent traversal: %q", request.Target)
	}
}

func TestLinuxUnknownOutcomeClassification(t *testing.T) {
	result, err := deleteFailure(0, unix.EINTR)
	if !errors.Is(err, unix.EINTR) || result.Outcome != OutcomeUnknown {
		t.Fatalf("interrupted delete classification result=%+v err=%v", result, err)
	}
}

func TestLinuxPreparedWriteAndDeletePinTargetAcrossReplacement(t *testing.T) {
	root, rootPath := openFixtureRoot(t)
	writePath := filepath.Join(rootPath, "write.txt")
	if err := os.WriteFile(writePath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	write, err := root.PrepareWrite(writePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(writePath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(writePath, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := write.WriteText(context.Background(), "must-not-overwrite")
	if !errors.Is(err, ErrTargetChanged) || result.Outcome != OutcomeNotExecuted {
		t.Fatalf("replacement write = %+v, %v", result, err)
	}
	if got, err := os.ReadFile(writePath); err != nil || string(got) != "replacement" {
		t.Fatalf("replacement contents = %q, %v", got, err)
	}
	_ = write.Close()

	deletePath := filepath.Join(rootPath, "delete.txt")
	if err := os.WriteFile(deletePath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	deletion, err := root.PrepareDelete(deletePath, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(deletePath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deletePath, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = deletion.Delete(context.Background())
	if !errors.Is(err, ErrTargetChanged) || result.Outcome != OutcomeNotExecuted {
		t.Fatalf("replacement delete = %+v, %v", result, err)
	}
	if got, err := os.ReadFile(deletePath); err != nil || string(got) != "replacement" {
		t.Fatalf("replacement survived = %q, %v", got, err)
	}
	_ = deletion.Close()
}

func TestLinuxPreparedDescriptorsAreCloseOnExec(t *testing.T) {
	root, rootPath := openFixtureRoot(t)
	file := filepath.Join(rootPath, "file.txt")
	if err := os.WriteFile(file, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := root.PrepareRead(file)
	if err != nil {
		t.Fatal(err)
	}
	write, err := root.PrepareWrite(file)
	if err != nil {
		t.Fatal(err)
	}
	deletion, err := root.PrepareDelete(file, false)
	if err != nil {
		t.Fatal(err)
	}
	rootBackend := root.backend.(*linuxRoot)
	readBackend := read.backend.(*linuxReadTarget)
	writeBackend := write.backend.(*linuxWriteTarget)
	deleteBackend := deletion.backend.(*linuxDeleteTarget)
	for name, fd := range map[string]int{
		"root": rootBackend.fd, "read root": readBackend.rootFD, "read parent": readBackend.parentFD,
		"read file": readBackend.fileFD, "write root": writeBackend.rootFD, "write parent": writeBackend.parentFD,
		"write target": writeBackend.targetFD, "delete root": deleteBackend.rootFD,
		"delete parent": deleteBackend.parentFD, "delete target": deleteBackend.targetFD,
	} {
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err != nil {
			t.Fatalf("get descriptor flags for %s: %v", name, err)
		}
		if flags&unix.FD_CLOEXEC == 0 {
			t.Errorf("%s descriptor %d is inheritable", name, fd)
		}
	}
	_ = read.Close()
	_ = write.Close()
	_ = deletion.Close()
}

func TestLinuxReadTruncatesOnlyAtUTF8Boundary(t *testing.T) {
	root, rootPath := openFixtureRoot(t)
	file := filepath.Join(rootPath, "unicode.txt")
	if err := os.WriteFile(file, []byte("雪雪"), 0o600); err != nil {
		t.Fatal(err)
	}
	target, err := root.PrepareRead(file)
	if err != nil {
		t.Fatal(err)
	}
	result, err := target.ReadText(context.Background(), 4)
	if err != nil || result.Outcome != OutcomeSucceeded || result.Text != "雪" || !result.Truncated {
		t.Fatalf("UTF-8 boundary read = %+v, %v", result, err)
	}
	_ = target.Close()

	if err := os.WriteFile(file, []byte{'a', 0xff, 'b'}, 0o600); err != nil {
		t.Fatal(err)
	}
	target, err = root.PrepareRead(file)
	if err != nil {
		t.Fatal(err)
	}
	result, err = target.ReadText(context.Background(), 2)
	if !errors.Is(err, ErrInvalidText) || result.Outcome != OutcomeFailed {
		t.Fatalf("malformed UTF-8 read = %+v, %v", result, err)
	}
	_ = target.Close()
}
