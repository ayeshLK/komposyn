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
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestBubblewrapCapabilitySelection(t *testing.T) {
	help := bubblewrapHelp("--ro-bind-fd")
	version, modern, err := bubblewrapCapabilities("bubblewrap 0.10.0\n", help)
	if err != nil || version != "bubblewrap 0.10.0" || modern.BindMode != BindModeDescriptor {
		t.Fatalf("modern capability selection: version=%q mode=%q err=%v", version, modern.BindMode, err)
	}
	_, legacy, err := bubblewrapCapabilities("bubblewrap 0.9.0\n", bubblewrapHelp("--ro-bind-data", "--perms"))
	if err != nil || legacy.BindMode != BindModeFileSnapshot {
		t.Fatalf("secure 0.9 file-snapshot selection: mode=%q err=%v", legacy.BindMode, err)
	}
	if !slices.Contains(legacy.Options, "--as-pid-1") || !slices.Contains(legacy.Options, "--sync-fd") {
		t.Fatalf("legacy capabilities missing from report: %#v", legacy.Options)
	}
	_, incomplete, err := bubblewrapCapabilities("bubblewrap 0.9.0\n", bubblewrapHelp("--ro-bind-data"))
	if !errors.Is(err, ErrUnavailable) || incomplete.BindMode != "" {
		t.Fatalf("read-only data snapshot without explicit permissions must be refused: mode=%q err=%v", incomplete.BindMode, err)
	}
}

func TestListDirectoryBoundedReturnsRawSingleLevelNamesForHostFiltering(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "visible.txt"), []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".secret"), []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "visible.txt"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	directory, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	result := listDirectoryBounded(int(directory.Fd()))
	if result.Outcome != ReadSucceeded || result.Truncated {
		t.Fatalf("listing outcome=%s truncated=%t detail=%s", result.Outcome, result.Truncated, result.Detail)
	}
	var listing directoryListingResult
	if err := json.Unmarshal(result.Bytes, &listing); err != nil {
		t.Fatal(err)
	}
	if listing.Examined < 3 || listing.Incomplete {
		t.Fatalf("listing metadata=%+v", listing)
	}
	if !slices.Equal(listingEntryNames(listing.Entries), []string{".secret", "child", "link", "visible.txt"}) {
		t.Fatalf("entries=%+v, want raw one-level entries for host option and protection filtering", listing.Entries)
	}
}

func TestDirectoryHandoffReturnsBoundedListingFrames(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "first.txt"), []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden"), []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	identity, err := identityFromFile(directory)
	if err != nil {
		t.Fatal(err)
	}
	frames, exitCode := exchangeWorkerFrame(t, identityFrame(frameDirectoryHandoff, identity), []*os.File{directory})
	if exitCode != 0 || len(frames) < 3 || frames[0][0] != frameStarted || !sameIdentity(frames[0][1:], identity) {
		t.Fatalf("directory worker frames=%q exit=%d", frameKinds(frames), exitCode)
	}
	var result []byte
	for _, frame := range frames[1:] {
		if frame[0] == frameData {
			result = append(result, frame[1:]...)
		}
	}
	var listing directoryListingResult
	if err := json.Unmarshal(result, &listing); err != nil {
		t.Fatalf("listing payload %q: %v", result, err)
	}
	if !slices.Equal(listingEntryNames(listing.Entries), []string{".hidden", "first.txt"}) || listing.Incomplete || listing.Examined < 2 {
		t.Fatalf("directory handoff listing=%+v", listing)
	}
}

func TestSearchSnapshotHandoffReturnsPathlessLiteralMatches(t *testing.T) {
	encoded, err := json.Marshal(searchSnapshotInput{Query: "needle", WorkBudget: maxSearchWorkUnits, Files: []searchSnapshotFile{{Content: []byte("needle once\nnone\nneedle twice\n")}, {Content: []byte("other\n")}}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := createSealedSearchSnapshot(encoded)
	clear(encoded)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	identity, err := identityFromFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	frames, exitCode := exchangeWorkerFrame(t, identityFrame(frameSearchSnapshot, identity), []*os.File{snapshot})
	if exitCode != 0 || len(frames) < 3 || frames[0][0] != frameStarted || !sameIdentity(frames[0][1:], identity) {
		t.Fatalf("search worker frames=%q exit=%d", frameKinds(frames), exitCode)
	}
	var resultBytes []byte
	for _, frame := range frames[1:] {
		if frame[0] == frameData {
			resultBytes = append(resultBytes, frame[1:]...)
		}
	}
	var result searchWorkerResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("search worker payload %q: %v", resultBytes, err)
	}
	if result.Incomplete || len(result.Matches) != 2 || result.Matches[0].Index != 0 || result.Matches[0].Line != 1 || result.Matches[1].Line != 3 {
		t.Fatalf("search worker result=%+v", result)
	}
	if bytes.Contains(resultBytes, []byte(`"path"`)) {
		t.Fatalf("worker response contains a provider-visible path: %q", resultBytes)
	}
}

func TestListDirectoryRecursiveReturnsRelativePathsAndNeverWalksGitOrSymlinks(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"src", ".hidden", ".git"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"src/main.go", ".hidden/secret.txt", ".git/config"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(file)), []byte("synthetic"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "src"), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	directory, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	result := listDirectoryBounded(int(directory.Fd()), ListingOptions{Recursive: true})
	if result.Outcome != ReadSucceeded || result.Truncated {
		t.Fatalf("listing result=%+v", result)
	}
	var listing directoryListingResult
	if err := json.Unmarshal(result.Bytes, &listing); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(listing.Entries))
	for _, entry := range listing.Entries {
		paths = append(paths, entry.Path)
	}
	slices.Sort(paths)
	if !slices.Equal(paths, []string{".git", ".hidden", "linked", "src", "src/main.go"}) {
		t.Fatalf("recursive paths=%v", paths)
	}
	if listing.Examined > 10_000 || len(listing.Directories) != 2 {
		t.Fatalf("recursive bounds/snapshots=%+v", listing)
	}
	visible, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer visible.Close()
	result = listDirectoryBounded(int(visible.Fd()), ListingOptions{Recursive: true, IncludeHidden: true})
	if result.Outcome != ReadSucceeded {
		t.Fatalf("hidden-inclusive listing result=%+v", result)
	}
	if err := json.Unmarshal(result.Bytes, &listing); err != nil {
		t.Fatal(err)
	}
	paths = paths[:0]
	for _, entry := range listing.Entries {
		paths = append(paths, entry.Path)
	}
	slices.Sort(paths)
	if !slices.Equal(paths, []string{".git", ".hidden", ".hidden/secret.txt", "linked", "src", "src/main.go"}) {
		t.Fatalf("hidden-inclusive paths=%v", paths)
	}
}

func TestRecursiveTraversalStopsAtSharedEntryBudget(t *testing.T) {
	root := t.TempDir()
	for index := range 20 {
		child := filepath.Join(root, fmt.Sprintf("dir-%02d", index))
		if err := os.Mkdir(child, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(child, "entry.txt"), []byte("synthetic"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	directory, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()

	listing := directoryListingResult{}
	var entries []directoryListingEntry
	examined, incomplete := 0, false
	if err := walkListingDirectory(context.Background(), int(directory.Fd()), "", ListingOptions{Recursive: true}, &listing, &entries, &examined, &incomplete, 10, 100); err != nil {
		t.Fatal(err)
	}
	if examined != 10 || examined > 10 || !incomplete {
		t.Fatalf("shared traversal budget examined=%d incomplete=%t", examined, incomplete)
	}
	if len(listing.Directories) != 1 {
		t.Fatalf("traversed %d directory snapshots after the entry budget was exhausted", len(listing.Directories))
	}
	for _, entry := range entries {
		if strings.Contains(entry.Path, "/") {
			t.Fatalf("descended below the entry budget: %q", entry.Path)
		}
	}
}

func TestDirectoryListingOutputBoundTrimsEntriesWithoutQuadraticRetries(t *testing.T) {
	entries := make([]directoryListingEntry, 10_000)
	for index := range entries {
		name := fmt.Sprintf("entry-%05d.txt", index)
		entries[index] = directoryListingEntry{Name: name, Path: name, Device: 1, Inode: uint64(index + 1), Type: unix.DT_REG}
	}
	result := encodeDirectoryListingBounded(context.Background(), directoryListingResult{
		Entries: entries, Examined: len(entries), Returned: len(entries),
	})
	if result.Outcome != ReadSucceeded || len(result.Bytes) > MaxReadBytes {
		t.Fatalf("bounded listing result=%s size=%d", result.Outcome, len(result.Bytes))
	}
	var listing directoryListingResult
	if err := json.Unmarshal(result.Bytes, &listing); err != nil {
		t.Fatal(err)
	}
	if !listing.Incomplete || len(listing.Entries) == 0 || len(listing.Entries) >= len(entries) || listing.Returned != len(listing.Entries) {
		t.Fatalf("output-bound listing=%+v, raw entries=%d", listing, len(entries))
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if result := encodeDirectoryListingBounded(cancelled, directoryListingResult{Entries: entries}); result.Outcome != ReadFailed {
		t.Fatalf("expired listing encoder outcome=%s, want failure", result.Outcome)
	}
}

func listingEntryNames(entries []directoryListingEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

func TestReceiveAndReadUsesExactReceivedFrameAndReadOnlyDescriptor(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "handoff.fixture")
	want := bytes.Repeat([]byte("bounded synthetic read\n"), MaxReadBytes/23+2)
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	identity, err := identityFromFile(file)
	if err != nil {
		t.Fatal(err)
	}

	frames, exitCode := exchangeWorkerFrame(t, identityFrame(frameHandoff, identity), []*os.File{file})
	if exitCode != 0 || len(frames) < 3 || frames[0][0] != frameStarted || !sameIdentity(frames[0][1:], identity) {
		t.Fatalf("worker startup/read frames=%q exit=%d, want read-start and bounded result", frameKinds(frames), exitCode)
	}
	var got []byte
	var finish []byte
	for _, frame := range frames[1:] {
		switch frame[0] {
		case frameData:
			got = append(got, frame[1:]...)
		case frameFinish:
			finish = frame
		default:
			t.Fatalf("unexpected worker result frame kind %q", frame[0])
		}
	}
	if len(finish) != 7 || finish[1] != 'S' || finish[2] != 1 || binary.BigEndian.Uint32(finish[3:7]) != MaxReadBytes {
		t.Fatalf("finish frame=%v, want succeeded/truncated with %d bytes", finish, MaxReadBytes)
	}
	if len(got) != MaxReadBytes || !bytes.Equal(got, want[:MaxReadBytes]) {
		t.Fatalf("read result bytes=%d match=%t, want first bounded %d bytes", len(got), bytes.Equal(got, want[:MaxReadBytes]), MaxReadBytes)
	}
	if gotFDs := countOpenIdentityFDs(t, identity); gotFDs != 1 {
		t.Fatalf("open descriptors for target after worker return=%d, want only original descriptor", gotFDs)
	}
}

func TestReceiveAndReadRefusesMalformedFramesAndClosesReceivedDescriptors(t *testing.T) {
	for _, test := range []struct {
		name     string
		mutate   func([]byte) []byte
		fdCopies int
		wantCode int
	}{
		{name: "short handoff", mutate: func(frame []byte) []byte { return frame[:len(frame)-1] }, fdCopies: 1, wantCode: 73},
		{name: "truncated handoff", mutate: func(frame []byte) []byte { return append(frame, bytes.Repeat([]byte{'x'}, 64)...) }, fdCopies: 1, wantCode: 73},
		{name: "wrong frame kind", mutate: func(frame []byte) []byte { frame[0] = 'Z'; return frame }, fdCopies: 1, wantCode: 73},
		{name: "mismatched identity", mutate: func(frame []byte) []byte { frame[1] ^= 1; return frame }, fdCopies: 1, wantCode: 74},
		{name: "multiple descriptors", mutate: func(frame []byte) []byte { return frame }, fdCopies: 2, wantCode: 73},
	} {
		t.Run(test.name, func(t *testing.T) {
			file, identity := openSyntheticReadTarget(t)
			defer file.Close()
			frame := test.mutate(identityFrame(frameHandoff, identity))
			descriptors := make([]*os.File, test.fdCopies)
			for i := range descriptors {
				descriptors[i] = file
			}
			frames, exitCode := exchangeWorkerFrame(t, frame, descriptors)
			if exitCode != test.wantCode || len(frames) != 1 || len(frames[0]) != 1 || frames[0][0] != frameRefused {
				t.Fatalf("refusal frames=%q exit=%d, want X/%d", frameKinds(frames), exitCode, test.wantCode)
			}
			if gotFDs := countOpenIdentityFDs(t, identity); gotFDs != 1 {
				t.Fatalf("malformed-frame handling leaked target descriptors: open=%d", gotFDs)
			}
		})
	}
}

func TestReceiveAndReadRefusesWritableAndNonRegularDescriptors(t *testing.T) {
	t.Run("writable descriptor", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "writable.fixture")
		if err := os.WriteFile(path, []byte("synthetic"), 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		identity, err := identityFromFile(file)
		if err != nil {
			t.Fatal(err)
		}
		frames, exitCode := exchangeWorkerFrame(t, identityFrame(frameHandoff, identity), []*os.File{file})
		if exitCode != 74 || len(frames) != 1 || frames[0][0] != frameRefused {
			t.Fatalf("writable descriptor response=%q exit=%d, want refusal/74", frameKinds(frames), exitCode)
		}
		if gotFDs := countOpenIdentityFDs(t, identity); gotFDs != 1 {
			t.Fatalf("writable descriptor refusal leaked FD: open=%d", gotFDs)
		}
	})

	t.Run("directory descriptor", func(t *testing.T) {
		directory, err := os.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer directory.Close()
		identity, err := identityFromFile(directory)
		if err != nil {
			t.Fatal(err)
		}
		frames, exitCode := exchangeWorkerFrame(t, identityFrame(frameHandoff, identity), []*os.File{directory})
		if exitCode != 74 || len(frames) != 1 || frames[0][0] != frameRefused {
			t.Fatalf("directory descriptor response=%q exit=%d, want refusal/74", frameKinds(frames), exitCode)
		}
		if gotFDs := countOpenIdentityFDs(t, identity); gotFDs != 1 {
			t.Fatalf("directory descriptor refusal leaked FD: open=%d", gotFDs)
		}
	})
}

func openSyntheticReadTarget(t *testing.T) (*os.File, FileIdentity) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "target.fixture")
	if err := os.WriteFile(path, []byte("synthetic SCM_RIGHTS fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := identityFromFile(file)
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	return file, identity
}

func exchangeWorkerFrame(t *testing.T, frame []byte, files []*os.File) ([][]byte, int) {
	t.Helper()
	sockets, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(sockets[0])
	workerControl := os.NewFile(uintptr(sockets[1]), "worker-control")
	defer workerControl.Close()
	done := make(chan int, 1)
	operation := AdmissionReadFile
	if len(frame) > 0 && frame[0] == frameDirectoryHandoff {
		operation = AdmissionListDirectory
	} else if len(frame) > 0 && frame[0] == frameSearchSnapshot {
		operation = AdmissionSearchContent
	}
	go func() { done <- receiveAndRead(int(workerControl.Fd()), false, operation) }()
	var rights []int
	for _, file := range files {
		rights = append(rights, int(file.Fd()))
	}
	var oob []byte
	if len(rights) > 0 {
		oob = unix.UnixRights(rights...)
	}
	n, err := unix.SendmsgN(sockets[0], frame, oob, nil, unix.MSG_NOSIGNAL)
	if err != nil || n != len(frame) {
		t.Fatalf("send handoff frame bytes=%d err=%v", n, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	frames := make([][]byte, 0, 4)
	first, err := recvPacket(ctx, sockets[0], 64)
	if err != nil {
		t.Fatalf("receive worker response: %v", err)
	}
	frames = append(frames, first)
	if len(first) == 0 || first[0] == frameRefused {
		return frames, waitWorkerExitCode(t, done)
	}
	if first[0] != frameStarted {
		t.Fatalf("first worker response=%q, want start/refusal", first)
	}
	for {
		packet, recvErr := recvPacket(ctx, sockets[0], dataChunkBytes+16)
		if recvErr != nil {
			t.Fatalf("receive worker data frame: %v", recvErr)
		}
		frames = append(frames, packet)
		if len(packet) > 0 && packet[0] == frameFinish {
			break
		}
	}
	return frames, waitWorkerExitCode(t, done)
}

func TestHeldCapabilityProtocolDoesNotReadBeforeRelease(t *testing.T) {
	file, identity := openSyntheticReadTarget(t)
	defer file.Close()
	sockets, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(sockets[0])
	defer unix.Close(sockets[1])
	workerFD, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", file.Fd()), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(workerFD)
	done := make(chan int, 1)
	go func() {
		done <- readTransferredFD(sockets[1], workerFD, identityFrame(frameHandoff, identity)[1:], true)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	held, err := recvPacket(ctx, sockets[0], 64)
	if err != nil || len(held) != 21 || held[0] != frameCapabilityHeld || !sameIdentity(held[1:], identity) {
		t.Fatalf("held acknowledgement=%x err=%v", held, err)
	}
	var poll []unix.PollFd = []unix.PollFd{{Fd: int32(sockets[0]), Events: unix.POLLIN}}
	if ready, err := unix.Poll(poll, 0); err != nil || ready != 0 {
		t.Fatalf("worker produced read data before release: ready=%d revents=%#x err=%v", ready, poll[0].Revents, err)
	}
	if err := sendPacket(sockets[0], identityFrame(frameReleaseRead, identity)); err != nil {
		t.Fatal(err)
	}
	started, err := recvPacket(ctx, sockets[0], 64)
	if err != nil || len(started) != 21 || started[0] != frameStarted || !sameIdentity(started[1:], identity) {
		t.Fatalf("read-started acknowledgement=%x err=%v", started, err)
	}
	data, err := recvPacket(ctx, sockets[0], dataChunkBytes+16)
	if err != nil || len(data) < 2 || data[0] != frameData {
		t.Fatalf("read data frame=%x err=%v", data, err)
	}
	finish, err := recvPacket(ctx, sockets[0], 64)
	if err != nil || len(finish) != 7 || finish[0] != frameFinish || finish[1] != encodeOutcome(ReadSucceeded) {
		t.Fatalf("read finish frame=%x err=%v", finish, err)
	}
	select {
	case exit := <-done:
		if exit != 0 {
			t.Fatalf("held worker exit=%d, want 0", exit)
		}
	case <-ctx.Done():
		t.Fatal("held worker did not exit after bounded release")
	}
}

func waitWorkerExitCode(t *testing.T, done <-chan int) int {
	t.Helper()
	select {
	case code := <-done:
		return code
	case <-time.After(3 * time.Second):
		t.Fatal("receiveAndRead did not return")
		return -1
	}
}

func frameKinds(frames [][]byte) string {
	var kinds strings.Builder
	for _, frame := range frames {
		if len(frame) == 0 {
			kinds.WriteByte('?')
		} else {
			kinds.WriteByte(frame[0])
		}
	}
	return kinds.String()
}

func countOpenIdentityFDs(t *testing.T, identity FileIdentity) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		fd, parseErr := strconv.Atoi(entry.Name())
		if parseErr != nil {
			continue
		}
		var stat unix.Stat_t
		if unix.Fstat(fd, &stat) == nil && identityFromStat(&stat) == identity {
			count++
		}
	}
	return count
}

func TestModernDescriptorModeRequiresResolvableSourceAndFallsBackToDataCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker-source")
	if err := os.WriteFile(path, []byte("path-backed running image"), 0o500); err != nil {
		t.Fatal(err)
	}
	pathSource, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer pathSource.Close()
	modern := BubblewrapCapabilities{
		Options: []string{"--ro-bind-fd", "--ro-bind-data", "--perms"}, BindMode: BindModeDescriptor,
	}
	if !descriptorBindSourcePathCompatible(pathSource) {
		t.Fatal("ordinary path-backed descriptor should resolve for Bubblewrap's modern mount path")
	}
	if mode, err := selectWorkerBindMode(modern, pathSource); err != nil || mode != BindModeDescriptor {
		t.Fatalf("modern descriptor path selection: mode=%q err=%v", mode, err)
	}

	snapshot, err := createSealedWorkerSnapshot(pathSource)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if descriptorBindSourcePathCompatible(snapshot) {
		t.Fatal("anonymous memfd must not be selected for Bubblewrap's realpath-based descriptor bind")
	}
	if descriptorSource, err := openWorkerMountDescriptor(snapshot, BindModeDescriptor); err == nil {
		_ = descriptorSource.Close()
		t.Fatal("anonymous memfd was accepted directly by the realpath-based descriptor mode")
	}
	if mode, err := selectWorkerBindMode(modern, snapshot); err != nil || mode != BindModeFileSnapshot {
		t.Fatalf("memfd source did not select descriptor-fed byte copy: mode=%q err=%v", mode, err)
	}
	modernWithoutCopy := BubblewrapCapabilities{
		Options: []string{"--ro-bind-fd"}, BindMode: BindModeDescriptor,
	}
	if mode, err := selectWorkerBindMode(modernWithoutCopy, snapshot); err == nil || mode != "" {
		t.Fatalf("unresolvable memfd source did not fail closed: mode=%q err=%v", mode, err)
	}
}

func TestStage6PreferredBindModeIsFeatureCheckedWithoutChangingAutomaticSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker-source")
	if err := os.WriteFile(path, []byte("synthetic worker"), 0o500); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	modern := BubblewrapCapabilities{
		Options: []string{"--ro-bind-fd", "--ro-bind-data", "--perms"}, BindMode: BindModeDescriptor,
	}
	if mode, err := selectWorkerBindModePreferred(modern, source, ""); err != nil || mode != BindModeDescriptor {
		t.Fatalf("automatic mode changed: mode=%q err=%v", mode, err)
	}
	if mode, err := selectWorkerBindModePreferred(modern, source, BindModeFileSnapshot); err != nil || mode != BindModeFileSnapshot {
		t.Fatalf("explicit snapshot mode not selected: mode=%q err=%v", mode, err)
	}
	if mode, err := selectWorkerBindModePreferred(modern, source, BindModeDescriptor); err != nil || mode != BindModeDescriptor {
		t.Fatalf("explicit descriptor mode not selected: mode=%q err=%v", mode, err)
	}
	snapshot, err := createSealedWorkerSnapshot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if mode, err := selectWorkerBindModePreferred(modern, snapshot, BindModeDescriptor); err == nil || mode != "" {
		t.Fatalf("descriptor mode accepted a source without a resolvable host path: mode=%q err=%v", mode, err)
	}
	if mode, err := selectWorkerBindModePreferred(modern, snapshot, BindModeFileSnapshot); err != nil || mode != BindModeFileSnapshot {
		t.Fatalf("explicit snapshot mode rejected a sealed image: mode=%q err=%v", mode, err)
	}
	withoutPermissions := BubblewrapCapabilities{Options: []string{"--ro-bind-data"}}
	if mode, err := selectWorkerBindModePreferred(withoutPermissions, source, BindModeFileSnapshot); err == nil || mode != "" {
		t.Fatalf("snapshot mode without explicit permission support was accepted: mode=%q err=%v", mode, err)
	}
	withoutDescriptor := BubblewrapCapabilities{Options: []string{"--ro-bind-data", "--perms"}}
	if mode, err := selectWorkerBindModePreferred(withoutDescriptor, source, BindModeDescriptor); err == nil || mode != "" {
		t.Fatalf("descriptor mode without descriptor support was accepted: mode=%q err=%v", mode, err)
	}
	if mode, err := selectWorkerBindModePreferred(modern, source, BindMode("unknown")); err == nil || mode != "" {
		t.Fatalf("unknown explicit mode was accepted: mode=%q err=%v", mode, err)
	}
}

func TestAdvancedDirectoryListingProfileRequiresMeasuredDescriptorBind(t *testing.T) {
	if (*workerLauncher)(nil).AdvancedDirectoryListingProfileAvailable() {
		t.Fatal("nil launcher exposed the advanced listing profile")
	}
	for _, mode := range []BindMode{BindModeFileSnapshot, BindMode("unknown")} {
		launcher := &workerLauncher{capability: BubblewrapCapabilities{BindMode: mode}}
		if launcher.AdvancedDirectoryListingProfileAvailable() {
			t.Fatalf("bind mode %q exposed an unmeasured advanced listing profile", mode)
		}
	}
	launcher := &workerLauncher{capability: BubblewrapCapabilities{BindMode: BindModeDescriptor}}
	if got, want := launcher.AdvancedDirectoryListingProfileAvailable(), runtime.GOARCH == "amd64"; got != want {
		t.Fatalf("descriptor bind profile eligibility=%t, want amd64-only result %t on %s", got, want, runtime.GOARCH)
	}
}

func TestSafeSystemErrorOmitsPathText(t *testing.T) {
	err := &os.PathError{Op: "open", Path: "/tmp/synthetic-private-path", Err: syscall.EACCES}
	got := safeSystemError(err)
	if got != syscall.EACCES.Error() || strings.Contains(got, "/tmp/") {
		t.Fatalf("safe system error=%q, want errno only", got)
	}
	if got := safeSystemError(errors.New("host-specific path diagnostic")); got != "system call failed" {
		t.Fatalf("unknown system error=%q, want generic classification", got)
	}
}

func TestModernDescriptorBindUsesFreshPathBackedDescriptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker")
	want := []byte("pinned path-backed worker")
	if err := os.WriteFile(path, want, 0o500); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	first, err := openWorkerMountDescriptor(source, BindModeDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	second, err := openWorkerMountDescriptor(source, BindModeDescriptor)
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	var prefix [4]byte
	if _, err := first.Read(prefix[:]); err != nil {
		t.Fatal(err)
	}
	if offset, err := second.Seek(0, io.SeekCurrent); err != nil || offset != 0 {
		t.Fatalf("descriptor source shared offsets: offset=%d err=%v", offset, err)
	}
	var got []byte
	got, err = io.ReadAll(second)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("fresh descriptor did not read pinned object: got=%q err=%v", got, err)
	}
	_ = first.Close()
	_ = second.Close()
}

func TestRunningExecutableCanBeReopenedForDescriptorBind(t *testing.T) {
	source, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatalf("running test executable could not be opened: %v", err)
	}
	defer source.Close()
	opened, err := openWorkerMountDescriptor(source, BindModeDescriptor)
	if err != nil {
		t.Fatalf("pinned running executable could not be reopened for descriptor binding: %v", err)
	}
	defer opened.Close()
	var sourceStat, openedStat unix.Stat_t
	if err := unix.Fstat(int(source.Fd()), &sourceStat); err != nil {
		t.Fatal("pinned executable identity could not be read")
	}
	if err := unix.Fstat(int(opened.Fd()), &openedStat); err != nil {
		t.Fatal("reopened executable identity could not be read")
	}
	if sourceStat.Dev != openedStat.Dev || sourceStat.Ino != openedStat.Ino {
		t.Fatal("descriptor bind reopen did not preserve the running executable identity")
	}
}

func TestBubblewrapArgumentsSelectModernMountAndSeparateVerifierFD(t *testing.T) {
	modern := strings.Join(EffectiveBubblewrapArguments(BindModeDescriptor), " ")
	if !strings.Contains(modern, "--ro-bind-fd 5 /worker") || strings.Contains(modern, "/proc/self/fd") {
		t.Fatalf("unexpected modern mount arguments: %s", modern)
	}
	for _, value := range []string{"--as-pid-1", "--sync-fd 6", "--readbrokerstudy-bootstrap", "--worker-fd 6", "--seccomp-fd 3", "--size 16777216"} {
		if !strings.Contains(modern, value) {
			t.Fatalf("modern arguments missing %q: %s", value, modern)
		}
	}
	if workerMountFD == workerVerificationFD {
		t.Fatal("mount source and retained verifier descriptors must be distinct")
	}
	snapshot := strings.Join(EffectiveBubblewrapArguments(BindModeFileSnapshot), " ")
	for _, value := range []string{"--perms 0500 --ro-bind-data 5 /worker", "--sync-fd 6", "--worker-mode snapshot", "--size 16777216"} {
		if !strings.Contains(snapshot, value) {
			t.Fatalf("Bubblewrap 0.9 snapshot arguments missing %q: %s", value, snapshot)
		}
	}
	if strings.Contains(snapshot, "/proc/self/fd") {
		t.Fatalf("file snapshot mode must not resolve a source pathname: %s", snapshot)
	}
	if len(EffectiveBubblewrapArguments("unknown")) != 0 {
		t.Fatal("unknown bind mode must produce no launch arguments")
	}
}

func TestCapabilityHoldBootstrapArgumentIsOptIn(t *testing.T) {
	normal := strings.Join(effectiveBubblewrapArguments(BindModeDescriptor, false), " ")
	held := strings.Join(effectiveBubblewrapArguments(BindModeDescriptor, true), " ")
	if strings.Contains(normal, workerHoldBootstrapArg) {
		t.Fatalf("normal worker unexpectedly enables hold mode: %s", normal)
	}
	if !strings.HasSuffix(held, workerHoldBootstrapArg) {
		t.Fatalf("held worker lacks the exact fixed bootstrap option: %s", held)
	}
}

func TestCapabilityHoldInterfaceIsOptIn(t *testing.T) {
	ordinary := any(&sandboxWorker{})
	if _, ok := ordinary.(HeldCapabilityWorker); ok {
		t.Fatal("ordinary worker unexpectedly exposes the owner-lifecycle hold protocol")
	}
	held := any(&heldSandboxWorker{sandboxWorker: &sandboxWorker{holdMode: true}})
	if _, ok := held.(HeldCapabilityWorker); !ok {
		t.Fatal("explicit held worker does not expose its fixed hold protocol")
	}
}

func TestModernBindConsumptionLeavesIndependentVerifierDescriptor(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "worker")
	if err := os.WriteFile(path, []byte("fixture"), 0o500); err != nil {
		t.Fatal(err)
	}
	mountFD, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	verificationFD, err := unix.Dup(mountFD)
	if err != nil {
		_ = unix.Close(mountFD)
		t.Fatal(err)
	}
	if mountFD == verificationFD {
		t.Fatal("descriptor duplication reused the mount FD")
	}
	// Bubblewrap's --ro-bind-fd consumes the mount source after setting up the
	// mount. The bootstrap must rely on the separately duplicated FD instead.
	if err := unix.Close(mountFD); err != nil {
		t.Fatal(err)
	}
	if err := verifyDescriptorMounts([]DescriptorMount{{FD: verificationFD, Destination: path}}); err != nil {
		t.Fatalf("modern bootstrap could not verify after mount FD consumption: %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(verificationFD), unix.F_GETFD, 0); err == nil {
		t.Fatal("retained verification descriptor survived bootstrap verification")
	}
}

func TestSealedSnapshotSurvivesSourcePathAndAncestorReplacement(t *testing.T) {
	base := t.TempDir()
	parent := filepath.Join(base, "selected", "bin")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(parent, "worker")
	want := []byte("reviewed executable snapshot")
	if err := os.WriteFile(sourcePath, want, 0o500); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	snapshot, err := createSealedWorkerSnapshot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	seals, err := unix.FcntlInt(snapshot.Fd(), unix.F_GET_SEALS, 0)
	if err != nil || seals&(unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL) !=
		unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL {
		t.Fatalf("snapshot is not immutable: seals=%#x err=%v", seals, err)
	}
	writeFD, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", snapshot.Fd()), unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open sealed memfd for rejected-write check: %v", err)
	}
	if _, err := unix.Pwrite(writeFD, []byte("x"), 0); !errors.Is(err, unix.EPERM) {
		_ = unix.Close(writeFD)
		t.Fatalf("sealed snapshot accepted a write: %v", err)
	}
	if err := unix.Close(writeFD); err != nil {
		t.Fatal(err)
	}

	movedParent := filepath.Join(base, "moved")
	if err := os.Rename(filepath.Join(base, "selected"), movedParent); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("replacement at original pathname"), 0o500); err != nil {
		t.Fatal(err)
	}
	movedPath := filepath.Join(movedParent, "bin", "worker")
	if err := os.Chmod(movedPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(movedPath, []byte("same inode changed after snapshot"), 0o500); err != nil {
		t.Fatal(err)
	}

	// Simulate Bubblewrap's descriptor-fed copy after the source pathname and
	// its ancestor have both been replaced. Only the sealed descriptor is used.
	mountInput, err := openSealedWorkerSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	privateRoot := t.TempDir()
	workerPath := filepath.Join(privateRoot, "worker")
	destination, err := os.Create(workerPath)
	if err != nil {
		_ = mountInput.Close()
		t.Fatal(err)
	}
	copied, copyErr := io.CopyN(destination, mountInput, int64(len(want)))
	closeInputErr := mountInput.Close()
	closeDestinationErr := destination.Close()
	if copyErr != nil || closeInputErr != nil || closeDestinationErr != nil || copied != int64(len(want)) {
		t.Fatalf("snapshot copy failed: copied=%d copy=%v input_close=%v destination_close=%v", copied, copyErr, closeInputErr, closeDestinationErr)
	}
	if err := os.Chmod(workerPath, 0o500); err != nil {
		t.Fatal(err)
	}
	verifier, err := openSealedWorkerSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	verifierFD := int(verifier.Fd())
	if err := verifySnapshotExecutable(verifier, workerPath); err != nil {
		t.Fatalf("copied private worker did not match sealed source: %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(verifierFD), unix.F_GETFD, 0); err == nil {
		t.Fatal("snapshot verifier descriptor was not closed")
	}
	if got, err := os.ReadFile(workerPath); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("path/ancestor replacement changed copied worker: %q err=%v", got, err)
	}
}

func TestSnapshotCurrentExecutableRejectsReplacementPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replacement")
	if err := os.WriteFile(path, []byte("replacement executable"), 0o500); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := snapshotCurrentExecutable(path); err == nil {
		_ = snapshot.Close()
		t.Fatal("replacement pathname was accepted as the running executable")
	}
}

func TestExecutableIdentityHintFIFOIsNonblocking(t *testing.T) {
	const childPath = "KOMPOSYN_READBROKER_FIFO_IDENTITY_HINT"
	if fifoPath := os.Getenv(childPath); fifoPath != "" {
		if _, err := openCurrentExecutable(fifoPath); err == nil {
			t.Fatal("FIFO was accepted as the running executable identity hint")
		}
		fmt.Print("FIFO_HINT_REFUSED_PROMPTLY")
		return
	}
	fifoPath := filepath.Join(t.TempDir(), "worker")
	if err := unix.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExecutableIdentityHintFIFOIsNonblocking$")
	command.Env = append(os.Environ(), childPath+"="+fifoPath)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("identity-hint inspection blocked on FIFO until timeout: %s", output)
	}
	if err != nil || !bytes.Contains(output, []byte("FIFO_HINT_REFUSED_PROMPTLY")) {
		t.Fatalf("FIFO identity-hint subprocess did not refuse promptly: err=%v output=%s", err, output)
	}
}

func TestSealedSnapshotDescriptorsHaveIndependentOffsetsAndMismatchCloses(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(sourcePath, []byte("bounded executable fixture"), 0o500); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	snapshot, err := createSealedWorkerSnapshot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	first, err := openSealedWorkerSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := openSealedWorkerSnapshot(snapshot)
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	var prefix [4]byte
	if _, err := first.Read(prefix[:]); err != nil {
		t.Fatal(err)
	}
	if offset, err := second.Seek(0, io.SeekCurrent); err != nil || offset != 0 {
		t.Fatalf("fresh descriptor shares source offset: offset=%d err=%v", offset, err)
	}
	_ = first.Close()
	_ = second.Close()

	privateWorker := filepath.Join(t.TempDir(), "worker")
	if err := os.WriteFile(privateWorker, []byte("altered fixture"), 0o500); err != nil {
		t.Fatal(err)
	}
	verifier, err := openSealedWorkerSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	verifierFD := int(verifier.Fd())
	if err := verifySnapshotExecutable(verifier, privateWorker); !errors.Is(err, ErrStaleReceipt) {
		t.Fatalf("integrity mismatch was accepted: %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(verifierFD), unix.F_GETFD, 0); err == nil {
		t.Fatal("mismatched snapshot verifier descriptor was not closed")
	}
}

func TestSealedSnapshotRejectsOversizedSource(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "oversized")
	if err := os.WriteFile(sourcePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := os.OpenFile(sourcePath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err := source.Truncate(maxWorkerSnapshotBytes + 1); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := createSealedWorkerSnapshot(source); err == nil {
		_ = snapshot.Close()
		t.Fatal("oversized worker snapshot was accepted")
	}
}

func TestBubblewrapCapabilityProbeRejectsMissingAndMalformedFeatures(t *testing.T) {
	_, partial, err := bubblewrapCapabilities("bubblewrap 0.9.0", "--ro-bind --ro-bind-fd --tmpfs\n")
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "missing required Bubblewrap features") {
		t.Fatalf("missing required options were not actionable: %v", err)
	}
	if partial.BindMode != "" || !slices.Contains(partial.Options, "--ro-bind") {
		t.Fatalf("missing-feature report lost detected options or selected an unusable path: %#v", partial)
	}
	for _, version := range []string{"", "bubblewrap 0.9.0 extra", "bwrap 0.9.0"} {
		if _, _, err := bubblewrapCapabilities(version, bubblewrapHelp("")); !errors.Is(err, ErrUnavailable) {
			t.Errorf("malformed version %q was accepted: %v", version, err)
		}
	}
}

func TestBubblewrapProbeOutputIsBounded(t *testing.T) {
	var output boundedProbeOutput
	if n, err := output.Write(make([]byte, backendProbeOutputLimit+1)); err != nil || n != backendProbeOutputLimit+1 {
		t.Fatalf("bounded output write: n=%d err=%v", n, err)
	}
	if !output.overflow || len(output.bytes) != backendProbeOutputLimit {
		t.Fatalf("probe output was not bounded: len=%d overflow=%t", len(output.bytes), output.overflow)
	}
}

func TestBubblewrapProbeFailuresRefuseCompatibility(t *testing.T) {
	for _, failOn := range []string{"--version", "--help"} {
		calls := 0
		_, _, err := probeBubblewrapWith(func(argument string) (string, error) {
			calls++
			if argument == failOn {
				return "", ErrUnavailable
			}
			if argument == "--version" {
				return "bubblewrap 0.9.0", nil
			}
			return bubblewrapHelp(""), nil
		})
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("failed %s probe was accepted: %v", failOn, err)
		}
		if failOn == "--version" && calls != 1 {
			t.Errorf("help probe ran after version failure: calls=%d", calls)
		}
	}
}

func TestUntrustedBubblewrapIsRejectedWithoutProbe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bwrap")
	if err := os.WriteFile(path, []byte("synthetic untrusted executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	inspection := InspectBubblewrap(path)
	if inspection.Trusted || inspection.Version != "" || inspection.BindMode != "" || inspection.Error == "" {
		t.Fatalf("untrusted backend yielded probe data: %#v", inspection)
	}
}

func TestVerifyDescriptorMountConsumesAndChecksDescriptor(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "worker")
	if err := os.WriteFile(path, []byte("fixture"), 0o500); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyDescriptorMounts([]DescriptorMount{{FD: fd, Destination: path}}); err != nil {
		t.Fatalf("matching mount rejected: %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
		t.Fatal("successful verification descriptor remained open")
	}
}

func TestVerifyDescriptorMountRejectsMismatchAndClosesDescriptor(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "first")
	second := filepath.Join(directory, "second")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte(path), 0o500); err != nil {
			t.Fatal(err)
		}
	}
	fd, err := unix.Open(first, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyDescriptorMounts([]DescriptorMount{{FD: fd, Destination: second}}); !errors.Is(err, ErrStaleReceipt) {
		t.Fatalf("mismatched mount was accepted: %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
		t.Fatal("mismatched verification descriptor remained open")
	}
}

func TestVerifyDescriptorMountRejectsSymlinkDestination(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	alias := filepath.Join(directory, "alias")
	if err := os.WriteFile(target, []byte("fixture"), 0o500); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(target, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyDescriptorMounts([]DescriptorMount{{FD: fd, Destination: alias}}); !errors.Is(err, ErrStaleReceipt) {
		t.Fatalf("symlink destination was followed: %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
		t.Fatal("symlink verification descriptor remained open")
	}
}

func TestVerifyDescriptorMountRejectsInvalidDescriptors(t *testing.T) {
	if err := verifyDescriptorMounts([]DescriptorMount{{FD: 0, Destination: "/worker"}}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("standard descriptor accepted: %v", err)
	}
	if err := verifyDescriptorMounts([]DescriptorMount{{FD: 987654, Destination: "/worker"}}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("missing descriptor accepted: %v", err)
	}
	fd, err := unix.Open(os.DevNull, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyDescriptorMounts([]DescriptorMount{{FD: fd, Destination: "/worker"}, {FD: fd, Destination: "/worker"}}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("duplicate descriptor accepted: %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
		t.Fatal("duplicate verification descriptor was not closed")
	}
}

func TestWorkerSeccompProgramIncludesBootstrapAndProcessRestrictions(t *testing.T) {
	denied := workerDeniedSyscalls()
	for _, required := range []uint32{
		unix.SYS_OPENAT, unix.SYS_OPENAT2, unix.SYS_NEWFSTATAT, unix.SYS_STATX,
		unix.SYS_SOCKET, unix.SYS_CLONE3, unix.SYS_EXECVE,
	} {
		if !slices.Contains(denied, required) {
			t.Errorf("seccomp policy does not deny syscall %d", required)
		}
	}
	if slices.Contains(denied, unix.SYS_CLONE) {
		t.Fatal("clone must use the narrow thread-only argument policy, not an unconditional syscall denial")
	}
	if _, err := workerSeccompFilterBytes(); err != nil {
		t.Fatalf("worker seccomp program could not be built: %v", err)
	}
	listingDenied := workerDeniedSyscallsForOperation(AdmissionListDirectory)
	for _, required := range []uint32{unix.SYS_OPENAT, unix.SYS_SOCKET, unix.SYS_CLONE3, unix.SYS_EXECVE} {
		if !slices.Contains(listingDenied, required) {
			t.Errorf("listing seccomp policy does not deny syscall %d", required)
		}
	}
	for _, needed := range []uint32{unix.SYS_OPENAT2, unix.SYS_NEWFSTATAT, unix.SYS_STATX} {
		if slices.Contains(listingDenied, needed) {
			t.Errorf("listing seccomp policy denies required directory syscall %d", needed)
		}
	}
	creationDenied := workerDeniedSyscallsForOperation(AdmissionCreateFile)
	for _, required := range []uint32{unix.SYS_OPENAT, unix.SYS_GETDENTS64, unix.SYS_MLOCK, unix.SYS_SOCKET, unix.SYS_CLONE3, unix.SYS_EXECVE} {
		if !slices.Contains(creationDenied, required) {
			t.Errorf("creation seccomp policy does not deny syscall %d", required)
		}
	}
	for _, needed := range []uint32{unix.SYS_OPENAT2, unix.SYS_NEWFSTATAT, unix.SYS_STATX} {
		if slices.Contains(creationDenied, needed) {
			t.Errorf("creation seccomp policy denies descriptor-relative creation syscall %d", needed)
		}
	}
	for _, needed := range []uint32{unix.SYS_UNLINKAT, unix.SYS_RENAMEAT2, unix.SYS_FSYNC} {
		if slices.Contains(creationDenied, needed) {
			t.Errorf("creation seccomp policy denies cleanup/publication syscall %d", needed)
		}
	}
	searchTraversal := workerDeniedSyscallsForOperation(AdmissionSearchFiles)
	if slices.Contains(searchTraversal, unix.SYS_OPENAT2) || !slices.Contains(searchTraversal, unix.SYS_OPENAT) || !slices.Contains(searchTraversal, unix.SYS_MLOCK) {
		t.Fatalf("search traversal syscall policy=%v", searchTraversal)
	}
	searchContent := workerDeniedSyscallsForOperation(AdmissionSearchContent)
	if !slices.Contains(searchContent, unix.SYS_OPENAT2) || !slices.Contains(searchContent, unix.SYS_OPENAT) || !slices.Contains(searchContent, unix.SYS_MLOCK) {
		t.Fatalf("search content syscall policy=%v", searchContent)
	}
	readProfile, _ := workerSeccompFilterBytesForOperation(AdmissionReadFile)
	listingProfile, _ := workerSeccompFilterBytesForOperation(AdmissionListDirectory)
	searchTraversalProfile, _ := workerSeccompFilterBytesForOperation(AdmissionSearchFiles)
	searchContentProfile, _ := workerSeccompFilterBytesForOperation(AdmissionSearchContent)
	creationProfile, err := workerSeccompFilterBytesForOperation(AdmissionCreateFile)
	if err != nil {
		t.Fatalf("creation worker profile unavailable: %v", err)
	}
	if bytes.Equal(searchTraversalProfile, listingProfile) || bytes.Equal(searchContentProfile, readProfile) || bytes.Equal(searchContentProfile, searchTraversalProfile) || bytes.Equal(creationProfile, listingProfile) || bytes.Equal(creationProfile, readProfile) {
		t.Fatal("search operations did not receive distinct seccomp profiles")
	}
	if _, err := workerSeccompFilterBytesForOperation(AdmissionOperation("unexpected")); err == nil {
		t.Fatal("unknown worker operation received a seccomp profile")
	}
}

func TestCreateWorkerStagePublishAndAbortProtocol(t *testing.T) {
	t.Run("maximum bounded input fits one packet", func(t *testing.T) {
		stageName, err := NewCreationStageName()
		if err != nil {
			t.Fatal(err)
		}
		leaf := strings.Repeat("x", maxCreationLeafBytes)
		frame := makeCreationInputFrame(FileIdentity{Device: 1, Inode: 2, Type: unix.S_IFDIR}, stageName, leaf, bytes.Repeat([]byte("x"), maxCreationContentBytes))
		if len(frame) > maxCreationPacketBytes || len(frame) != 1+20+1+1+4+len(stageName)+len(leaf)+maxCreationContentBytes {
			t.Fatalf("max frame length=%d packet capacity=%d", len(frame), maxCreationPacketBytes)
		}
	})
	t.Run("publishes exact bytes with no replace", func(t *testing.T) {
		root := t.TempDir()
		directory, err := os.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		defer directory.Close()
		content := []byte("prototype creation\n")
		stageName, err := NewCreationStageName()
		if err != nil {
			t.Fatal(err)
		}
		result, stageIdentity, exitCode := runCreationProtocol(t, directory, stageName, "created.txt", content, frameCreatePublish)
		if exitCode != 0 || result.Outcome != CreationCreated || result.Bytes != uint32(len(content)) || result.Identity != stageIdentity {
			t.Fatalf("create result=%+v stage=%+v exit=%d", result, stageIdentity, exitCode)
		}
		got, err := os.ReadFile(filepath.Join(root, "created.txt"))
		if err != nil || !bytes.Equal(got, content) {
			t.Fatalf("published content=%q err=%v", got, err)
		}
		info, err := os.Stat(filepath.Join(root, "created.txt"))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("published mode=%v err=%v; want 0600", info.Mode(), err)
		}
		if _, err := os.Lstat(filepath.Join(root, stageName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stage remained after publish: %v", err)
		}
	})

	t.Run("collision leaves incumbent intact", func(t *testing.T) {
		root := t.TempDir()
		directory, err := os.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		defer directory.Close()
		incumbent := []byte("do not replace")
		if err := os.WriteFile(filepath.Join(root, "exists.txt"), incumbent, 0o600); err != nil {
			t.Fatal(err)
		}
		stageName, err := NewCreationStageName()
		if err != nil {
			t.Fatal(err)
		}
		result, _, exitCode := runCreationProtocol(t, directory, stageName, "exists.txt", []byte("approved replacement"), frameCreatePublish)
		if exitCode != 0 || result.Outcome != CreationConflict {
			t.Fatalf("collision result=%+v exit=%d", result, exitCode)
		}
		got, err := os.ReadFile(filepath.Join(root, "exists.txt"))
		if err != nil || !bytes.Equal(got, incumbent) {
			t.Fatalf("incumbent changed to %q err=%v", got, err)
		}
		if _, err := os.Lstat(filepath.Join(root, stageName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("collision stage was not cleaned: %v", err)
		}
	})

	t.Run("abort removes private stage", func(t *testing.T) {
		root := t.TempDir()
		directory, err := os.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		defer directory.Close()
		stageName, err := NewCreationStageName()
		if err != nil {
			t.Fatal(err)
		}
		result, _, exitCode := runCreationProtocol(t, directory, stageName, "aborted.txt", []byte("approved but cancelled"), frameCreateAbort)
		if exitCode != 0 || result.Outcome != CreationNotCreated {
			t.Fatalf("abort result=%+v exit=%d", result, exitCode)
		}
		if _, err := os.Lstat(filepath.Join(root, stageName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("abort stage remained: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(root, "aborted.txt")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("abort published target: %v", err)
		}
	})
}

func TestBoundedCreationWorkerReceiptIsOneUse(t *testing.T) {
	root := t.TempDir()
	directory, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	parentIdentity, err := identityFromFile(directory)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	parent := os.NewFile(uintptr(pair[0]), "bounded-create-parent")
	child := os.NewFile(uintptr(pair[1]), "bounded-create-worker")
	worker := &sandboxWorker{control: parent, operation: AdmissionCreateFile, ready: true}
	done := make(chan int, 1)
	go func() { done <- receiveAndRead(int(child.Fd()), false, AdmissionCreateFile) }()
	stageName, err := NewCreationStageName()
	if err != nil {
		t.Fatal(err)
	}
	stage, err := worker.StageCreation(context.Background(), directory, parentIdentity, stageName, "once.txt", []byte("one use"))
	if err != nil {
		t.Fatalf("stage creation: %v", err)
	}
	if stage.StageName() != stageName || stage.StageIdentity().Inode == 0 {
		t.Fatalf("stage metadata is incomplete: name=%q identity=%+v", stage.StageName(), stage.StageIdentity())
	}
	result, err := worker.PublishCreation(context.Background(), stage)
	if err != nil || result.Outcome != CreationCreated {
		t.Fatalf("publish result=%+v err=%v", result, err)
	}
	if _, err := worker.PublishCreation(context.Background(), stage); !errors.Is(err, ErrProtocol) {
		t.Fatalf("creation stage receipt was reusable: %v", err)
	}
	if code := <-done; code != 0 {
		t.Fatalf("worker exit code=%d", code)
	}
	_ = child.Close()
	if err := worker.Wait(); err != nil {
		t.Fatalf("join protocol worker: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "once.txt"))
	if err != nil || string(got) != "one use" {
		t.Fatalf("created bytes=%q err=%v", got, err)
	}
}

func TestBoundedCreationWorkerCreatesEmptyFile(t *testing.T) {
	root := t.TempDir()
	directory, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	stageName, err := NewCreationStageName()
	if err != nil {
		t.Fatal(err)
	}
	result, _, exitCode := runCreationProtocol(t, directory, stageName, "empty.txt", nil, frameCreatePublish)
	if exitCode != 0 || result.Outcome != CreationCreated || result.Bytes != 0 {
		t.Fatalf("empty creation result=%+v exit=%d", result, exitCode)
	}
	info, err := os.Stat(filepath.Join(root, "empty.txt"))
	if err != nil || !info.Mode().IsRegular() || info.Size() != 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("empty file stat=%v err=%v", info, err)
	}
}

func runCreationProtocol(t *testing.T, directory *os.File, stageName, leaf string, content []byte, decision byte) (CreationResult, FileIdentity, int) {
	t.Helper()
	parentIdentity, err := identityFromFile(directory)
	if err != nil {
		t.Fatal(err)
	}
	sockets, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(sockets[0])
	workerControl := os.NewFile(uintptr(sockets[1]), "creation-worker-control")
	defer workerControl.Close()
	done := make(chan int, 1)
	go func() { done <- receiveAndRead(int(workerControl.Fd()), false, AdmissionCreateFile) }()
	request := makeCreationInputFrame(parentIdentity, stageName, leaf, content)
	n, err := unix.SendmsgN(sockets[0], request, unix.UnixRights(int(directory.Fd())), nil, unix.MSG_NOSIGNAL)
	clear(request)
	if err != nil || n == 0 {
		t.Fatalf("send create request bytes=%d err=%v", n, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	first, err := recvPacket(ctx, sockets[0], 64)
	if err != nil {
		t.Fatalf("receive stage acknowledgement: %v", err)
	}
	if len(first) == 1+1+20+4 && first[0] == frameCreateFinish {
		result, parseErr := parseCreationResult(first)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		return result, FileIdentity{}, <-done
	}
	if len(first) != 1+20 || first[0] != frameCreateStaged {
		t.Fatalf("stage response=%x", first)
	}
	stageIdentity := FileIdentity{Device: binary.BigEndian.Uint64(first[1:9]), Inode: binary.BigEndian.Uint64(first[9:17]), Type: binary.BigEndian.Uint32(first[17:21])}
	info, err := os.Stat(filepath.Join(directory.Name(), stageName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("stage stat=%v err=%v", info, err)
	}
	packet := identityFrame(decision, stageIdentity)
	if err := sendPacket(sockets[0], packet); err != nil {
		t.Fatalf("send stage decision: %v", err)
	}
	last, err := recvPacket(ctx, sockets[0], 64)
	if err != nil {
		t.Fatalf("receive create outcome: %v", err)
	}
	result, err := parseCreationResult(last)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case exit := <-done:
		return result, stageIdentity, exit
	case <-ctx.Done():
		t.Fatalf("worker did not finish: %v", ctx.Err())
		return CreationResult{Outcome: CreationUnknown}, stageIdentity, -1
	}
}

func TestInstallWorkerSeccompRejectsInvalidProgramAndClosesFD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad-filter")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := installWorkerSeccomp(fd); !errors.Is(err, ErrProtocol) {
		t.Fatalf("empty seccomp program was accepted: %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
		t.Fatal("rejected seccomp descriptor remained open")
	}
}

func bubblewrapHelp(extra ...string) string {
	options := slices.Clone(requiredBubblewrapOptions)
	options = append(options, extra...)
	var help strings.Builder
	for _, option := range options {
		help.WriteString("  ")
		help.WriteString(option)
		help.WriteString(" ARG\n")
	}
	return help.String()
}
