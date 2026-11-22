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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestClassifyFromRetainedDirectoryProtectsMovedOwnerAncestor(t *testing.T) {
	root := t.TempDir()
	ownerDir := filepath.Join(root, "owner")
	if err := os.Mkdir(ownerDir, 0700); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	if err := registry.Register(OwnerRegistration{Owner: "settings", Class: ClassSettings, Path: ownerDir, Directory: true}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(ownerDir, filepath.Join(root, "moved-owner")); err != nil {
		t.Fatal(err)
	}
	parentPath := filepath.Join(root, "moved-owner", "nested")
	if err := os.Mkdir(parentPath, 0700); err != nil {
		t.Fatal(err)
	}
	parent, err := os.Open(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()

	class, protected, err := registry.ClassifyFromRetainedDirectory(filepath.Join(parentPath, "new.json"), FileIdentity{}, int(parent.Fd()))
	if err != nil || !protected || class != ClassSettings {
		t.Fatalf("moved protected ancestor classification = (%q, %t, %v); want settings protection", class, protected, err)
	}
	if _, _, err := registry.ClassifyFromRetainedDirectory(filepath.Join(parentPath, "new.json"), FileIdentity{}, -1); err == nil {
		t.Fatal("invalid retained parent descriptor was treated as unprotected")
	}
}

func TestExactFileRoutesAndPinnedIdentitiesUseCapturedClassification(t *testing.T) {
	root := t.TempDir()
	ordinary := filepath.Join(root, "ordinary.fixture")
	credential := filepath.Join(root, "credential.fixture")
	reservedCredential := filepath.Join(root, "reserved-credential.fixture")
	provider := filepath.Join(root, "provider.fixture")
	for path, contents := range map[string]string{ordinary: "ordinary", credential: "synthetic credential", provider: "synthetic provider"} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	if err := registry.Register(OwnerRegistration{Owner: "auth-fixture", Class: ClassCredential, Path: credential}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(OwnerRegistration{Owner: "auth-reserved-fixture", Class: ClassCredential, Path: reservedCredential}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(OwnerRegistration{Owner: "provider-fixture", Class: ClassProvider, Path: provider}); err != nil {
		t.Fatal(err)
	}

	ordinaryID := identityAt(t, ordinary)
	providerID := identityAt(t, provider)
	assertClassified(t, registry, ordinary, ordinaryID, ClassOrdinary)
	assertClassified(t, registry, credential, identityAt(t, credential), ClassCredential)
	assertClassified(t, registry, provider, providerID, ClassProvider)

	// The mutable parent directory does not globally disable exact-file reads.
	// A late alias's protected pathname is protected for a fresh request through
	// that pathname, while the original ordinary route retains its own class.
	if err := os.Link(ordinary, reservedCredential); err != nil {
		t.Fatal(err)
	}
	assertClassified(t, registry, ordinary, ordinaryID, ClassOrdinary)
	assertClassified(t, registry, reservedCredential, ordinaryID, ClassCredential)

	// A registered provider identity remains provider-class through aliases,
	// so an ordinary-route receipt cannot silently authorize that object.
	providerAlias := filepath.Join(root, "provider-alias.fixture")
	if err := os.Link(provider, providerAlias); err != nil {
		t.Fatal(err)
	}
	assertClassified(t, registry, providerAlias, providerID, ClassProvider)
}

func TestSearchFilesystemRequiresExactExt4MountType(t *testing.T) {
	for _, filesystemType := range []string{"ext2", "ext3", "ext4", "tmpfs"} {
		t.Run(filesystemType, func(t *testing.T) {
			got := isSupportedSearchFilesystem(extFilesystemMagic, filesystemType)
			want := filesystemType == "ext4"
			if got != want {
				t.Fatalf("search filesystem support for %q = %t, want %t", filesystemType, got, want)
			}
		})
	}
}

func TestMountFilesystemLookupUsesDescriptorMountID(t *testing.T) {
	fdInfo := []byte("pos:\t0\nflags:\t0100000\nmnt_id:\t42\n")
	mountID, err := parseDescriptorMountID(fdInfo)
	if err != nil || mountID != 42 {
		t.Fatalf("parse descriptor mount ID = %d, %v; want 42", mountID, err)
	}
	mountInfo := []byte("41 1 8:1 / /ext3 rw,relatime - ext3 /dev/example rw\n42 1 8:2 / /ext4 rw,relatime - ext4 /dev/example rw\n")
	filesystemType, err := parseMountFilesystemType(mountInfo, mountID)
	if err != nil || filesystemType != "ext4" {
		t.Fatalf("descriptor mount filesystem = %q, %v; want ext4", filesystemType, err)
	}
	if _, err := parseMountFilesystemType(mountInfo, 43); err == nil {
		t.Fatal("missing descriptor mount ID was accepted")
	}
}

func TestBrokerCanPrepareAnIndividuallyScopedTargetOutsideWorkingDirectory(t *testing.T) {
	base := t.TempDir()
	workingDirectory := filepath.Join(base, "working")
	outsideDirectory := filepath.Join(base, "outside")
	if err := os.Mkdir(workingDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outsideDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outsideDirectory, "notes.txt")
	want := []byte("synthetic outside-directory content\n")
	if err := os.WriteFile(target, want, 0o600); err != nil {
		t.Fatal(err)
	}

	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	broker := NewBroker(registry)
	prepared, err := broker.PrepareRead(workingDirectory, target, GenerationSet{Root: 1, Policy: registry.View().Generation})
	if err != nil {
		t.Fatalf("prepare exact target outside the working directory: %v", err)
	}
	t.Cleanup(func() { _ = prepared.Close() })
	if prepared.Path() != target || prepared.Identity() != identityAt(t, target) || prepared.Class() != ClassOrdinary {
		t.Fatalf("outside target was not bound to its exact ordinary object: path=%q identity=%+v class=%q", prepared.Path(), prepared.Identity(), prepared.Class())
	}
	opened, err := prepared.DuplicateForWorker()
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	got, err := os.ReadFile(fmt.Sprintf("/proc/self/fd/%d", opened.Fd()))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("opened exact outside target=%q err=%v, want %q", got, err, want)
	}
}

func TestDirectoryListPreparationPinsOnlyTheSelectedDirectoryObject(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	if err := os.Mkdir(selected, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(selected, "ordinary.fixture"), []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	broker := NewBroker(registry)
	listingBroker, ok := broker.(DirectoryListingBroker)
	if !ok {
		t.Fatal("production broker does not implement directory listing preparation")
	}
	generation := GenerationSet{Root: 1, Policy: registry.View().Generation}
	prepared, err := listingBroker.PrepareDirectoryList(root, selected, generation)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Close() })
	if prepared.Path() != selected || prepared.Class() != ClassOrdinary || prepared.Identity().Type != unix.S_IFDIR {
		t.Fatalf("prepared listing metadata path=%q class=%q identity=%+v", prepared.Path(), prepared.Class(), prepared.Identity())
	}
	if err := broker.Revalidate(prepared); err != nil {
		t.Fatalf("pinned directory did not revalidate: %v", err)
	}
	duplicated, err := prepared.DuplicateForWorker()
	if err != nil {
		t.Fatal(err)
	}
	defer duplicated.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(int(duplicated.Fd()), &stat); err != nil {
		t.Fatal(err)
	}
	flags, err := unix.FcntlInt(duplicated.Fd(), unix.F_GETFL, 0)
	if err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || flags&unix.O_ACCMODE != unix.O_RDONLY {
		t.Fatalf("transferred directory capability mode=%#o flags=%#x err=%v", stat.Mode, flags, err)
	}
	if err := os.Rename(selected, filepath.Join(root, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := broker.Revalidate(prepared); err != nil {
		t.Fatalf("external rename changed pinned-object authority: %v", err)
	}
}

func TestDirectoryListPreparationRefusesProtectedAndUncoveredDirectoryScopes(t *testing.T) {
	root := t.TempDir()
	protected := filepath.Join(root, "protected")
	ordinary := filepath.Join(root, "ordinary")
	for _, path := range []string{protected, ordinary} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	if err := registry.Register(OwnerRegistration{Owner: "settings-fixture", Class: ClassSettings, Path: protected, Directory: true}); err != nil {
		t.Fatal(err)
	}
	broker := NewBroker(registry)
	listingBroker := broker.(DirectoryListingBroker)
	generation := GenerationSet{Root: 1, Policy: registry.View().Generation}
	if prepared, err := listingBroker.PrepareDirectoryList(root, protected, generation); !errors.Is(err, ErrProtected) {
		if prepared != nil {
			_ = prepared.Close()
		}
		t.Fatalf("protected directory listing preparation=%v, err=%v", prepared, err)
	}
	if prepared, err := listingBroker.PrepareDirectoryList(root, ordinary, generation); !errors.Is(err, ErrDirectoryCoverage) {
		if prepared != nil {
			_ = prepared.Close()
		}
		t.Fatalf("same-device directory listing preparation=%v, err=%v; want missing descendant coverage", prepared, err)
	}
}

func TestDirectoryListFilteringHonorsIgnoreRulesAndProtectedAliases(t *testing.T) {
	base := t.TempDir()
	directoryPath := filepath.Join(base, "sample")
	if err := os.Mkdir(directoryPath, 0o700); err != nil {
		t.Fatal(err)
	}
	protectedPath := filepath.Join(base, "owned-secret")
	if err := os.WriteFile(protectedPath, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(protectedPath, filepath.Join(directoryPath, "ordinary-looking")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"keep.txt", "ignored.log", " secret.txt"} {
		if err := os.WriteFile(filepath.Join(directoryPath, name), []byte("synthetic"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(directoryPath, ".gitignore"), []byte("*.log\n secret.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	if err := registry.Register(OwnerRegistration{Owner: "credential-fixture", Class: ClassCredential, Path: protectedPath}); err != nil {
		t.Fatal(err)
	}
	broker := NewBroker(registry)
	prepared, err := broker.(DirectoryListingBroker).PrepareDirectoryList(base, directoryPath, GenerationSet{Root: 1, Policy: registry.View().Generation})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Close() })
	var entries []directoryListingEntry
	for _, name := range []string{"keep.txt", "ignored.log", "ordinary-looking", " secret.txt"} {
		fd, err := unix.Openat(int(prepared.(*preparedDirectoryList).directory.Fd()), name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		var stat unix.Stat_t
		if err := unix.Fstat(fd, &stat); err != nil {
			_ = unix.Close(fd)
			t.Fatal(err)
		}
		_ = unix.Close(fd)
		entries = append(entries, directoryListingEntry{Name: name, Inode: uint64(stat.Ino), Type: direntTypeForMode(stat.Mode)})
	}
	var directoryStat unix.Stat_t
	if err := unix.Fstat(int(prepared.(*preparedDirectoryList).directory.Fd()), &directoryStat); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(directoryListingResult{Entries: entries, Examined: 4, MtimeNS: directoryStat.Mtim.Sec*1e9 + directoryStat.Mtim.Nsec, CtimeNS: directoryStat.Ctim.Sec*1e9 + directoryStat.Ctim.Nsec})
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := prepared.(*preparedDirectoryList).filterListing(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	var listing directoryListingResult
	if err := json.Unmarshal(filtered, &listing); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(listing.Names, []string{"keep.txt"}) {
		t.Fatalf("filtered names=%q, want only keep.txt", listing.Names)
	}
	if listing.Examined != 1 || listing.Returned != 1 {
		t.Fatalf("filtered counts examined=%d returned=%d, want only disclosed count 1", listing.Examined, listing.Returned)
	}
	filterCtx, cancelFilter := context.WithCancel(context.Background())
	cancelFilter()
	if _, err := prepared.(*preparedDirectoryList).filterListing(filterCtx, raw); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled filter err=%v, want context.Canceled", err)
	}
	if err := prepared.Close(); err != nil {
		t.Fatalf("close after cancelled filtering: %v", err)
	}
}

func TestDirectoryListHonorsAncestorIgnoreRules(t *testing.T) {
	base := t.TempDir()
	directoryPath := filepath.Join(base, "src")
	if err := os.Mkdir(directoryPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, ".gitignore"), []byte("src/*.log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	for _, name := range []string{".log", "trace.log", "keep.go"} {
		if err := os.WriteFile(filepath.Join(directoryPath, name), []byte("synthetic"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := NewBroker(registry).(DirectoryListingBroker).PrepareDirectoryList(base, directoryPath, GenerationSet{Root: 1, Policy: registry.View().Generation})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Close() })
	entries := make([]directoryListingEntry, 0, 3)
	for _, name := range []string{".log", "trace.log", "keep.go"} {
		var stat unix.Stat_t
		if err := unix.Fstatat(int(prepared.(*preparedDirectoryList).directory.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, directoryListingEntry{Name: name, Inode: stat.Ino, Type: direntTypeForMode(stat.Mode)})
	}
	var directoryStat unix.Stat_t
	if err := unix.Fstat(int(prepared.(*preparedDirectoryList).directory.Fd()), &directoryStat); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(directoryListingResult{Entries: entries, Examined: 3, MtimeNS: directoryStat.Mtim.Sec*1e9 + directoryStat.Mtim.Nsec, CtimeNS: directoryStat.Ctim.Sec*1e9 + directoryStat.Ctim.Nsec})
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := prepared.(*preparedDirectoryList).filterListing(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	var listing directoryListingResult
	if err := json.Unmarshal(filtered, &listing); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(listing.Names, []string{"keep.go"}) {
		t.Fatalf("ancestor ignore result=%q, want only keep.go", listing.Names)
	}
}

func TestRecursiveDirectoryListingFiltersAfterTraversalAndAppliesNestedIgnores(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "sample")
	for _, directory := range []string{"src", ".private", ".git"} {
		if err := os.MkdirAll(filepath.Join(target, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{".gitignore": "*.tmp\n", "src/.gitignore": "!keep.tmp\n", "src/main.go": "synthetic", "src/keep.tmp": "synthetic", "src/drop.tmp": "synthetic", ".private/hidden.go": "synthetic", ".git/config": "synthetic"}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(target, filepath.FromSlash(name)), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	options := ListingOptions{Recursive: true, NameGlobs: []string{"*.go", "*.tmp"}}
	prepared, err := NewBroker(registry).(DirectoryListingBroker).PrepareDirectoryListWithOptions(base, target, GenerationSet{Root: 1, Policy: registry.View().Generation}, options)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	fd, err := prepared.DuplicateForWorker()
	if err != nil {
		t.Fatal(err)
	}
	raw := listDirectoryBounded(int(fd.Fd()), options)
	if err := fd.Close(); err != nil {
		t.Fatal(err)
	}
	if raw.Outcome != ReadSucceeded {
		t.Fatalf("worker result=%+v", raw)
	}
	filtered, err := prepared.(*preparedDirectoryList).filterListing(context.Background(), raw.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	var listing directoryListingResult
	if err := json.Unmarshal(filtered, &listing); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(listing.Names, []string{"src/keep.tmp", "src/main.go"}) {
		t.Fatalf("recursive filtered paths=%q incomplete=%t", listing.Names, listing.Incomplete)
	}
	if listing.Examined != 2 || listing.Returned != 2 {
		t.Fatalf("provider-visible counts include hidden, ignored or protected entries: %+v", listing)
	}

	options.IncludeHidden, options.IncludeIgnored = true, true
	preparedWithHidden, err := NewBroker(registry).(DirectoryListingBroker).PrepareDirectoryListWithOptions(base, target, GenerationSet{Root: 1, Policy: registry.View().Generation}, options)
	if err != nil {
		t.Fatal(err)
	}
	defer preparedWithHidden.Close()
	fd, err = preparedWithHidden.DuplicateForWorker()
	if err != nil {
		t.Fatal(err)
	}
	raw = listDirectoryBounded(int(fd.Fd()), options)
	_ = fd.Close()
	filtered, err = preparedWithHidden.(*preparedDirectoryList).filterListing(context.Background(), raw.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(filtered, &listing); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(listing.Names, []string{".private/hidden.go", "src/drop.tmp", "src/keep.tmp", "src/main.go"}) {
		t.Fatalf("explicit inclusion paths=%q", listing.Names)
	}
}

func TestDirectoryListingDoesNotPublishInternalTraversalSnapshots(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "sample")
	ignored := filepath.Join(target, "ignored")
	if err := os.MkdirAll(ignored, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, ".gitignore"), []byte("ignored/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ignored, "private.txt"), []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })

	for _, options := range []ListingOptions{{}, {Recursive: true}} {
		prepared, err := NewBroker(registry).(DirectoryListingBroker).PrepareDirectoryListWithOptions(base, target, GenerationSet{Root: 1, Policy: registry.View().Generation}, options)
		if err != nil {
			t.Fatal(err)
		}
		fd, err := prepared.DuplicateForWorker()
		if err != nil {
			_ = prepared.Close()
			t.Fatal(err)
		}
		raw := listDirectoryBounded(int(fd.Fd()), options)
		if err := fd.Close(); err != nil {
			_ = prepared.Close()
			t.Fatal(err)
		}
		if raw.Outcome != ReadSucceeded {
			_ = prepared.Close()
			t.Fatalf("worker result=%+v", raw)
		}
		filtered, err := prepared.(*preparedDirectoryList).filterListing(context.Background(), raw.Bytes)
		_ = prepared.Close()
		if err != nil {
			t.Fatal(err)
		}
		if got, want := string(filtered), `{"examined":0,"returned":0,"incomplete":false}`; got != want {
			t.Fatalf("serialized provider listing=%s, want only public empty result %s", got, want)
		}
	}
}

func TestNestedIgnoreVerificationRefusalClosesDescriptors(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "sample")
	nested := filepath.Join(target, "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	ignorePath := filepath.Join(nested, ".gitignore")
	if err := os.WriteFile(ignorePath, []byte("old-rule\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	preparedRead, err := NewBroker(registry).(DirectoryListingBroker).PrepareDirectoryListWithOptions(base, target, GenerationSet{Root: 1, Policy: registry.View().Generation}, ListingOptions{Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	prepared := preparedRead.(*preparedDirectoryList)
	defer prepared.Close()
	fd, err := prepared.DuplicateForWorker()
	if err != nil {
		t.Fatal(err)
	}
	raw := listDirectoryBounded(int(fd.Fd()), ListingOptions{Recursive: true})
	if err := fd.Close(); err != nil {
		t.Fatal(err)
	}
	if raw.Outcome != ReadSucceeded {
		t.Fatalf("worker result=%+v", raw)
	}
	var listing directoryListingResult
	if err := json.Unmarshal(raw.Bytes, &listing); err != nil {
		t.Fatal(err)
	}
	blocked, err := prepared.revalidateListingDirectories(listing.Directories, &directoryFilterBudget{ctx: context.Background(), remaining: maxDirectoryFilterWork})
	if err != nil {
		t.Fatalf("revalidate pre-mutation snapshots: %v", err)
	}
	if err := os.WriteFile(ignorePath, []byte("replacement-rule-is-longer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	baseline := openDescriptorCount(t)
	for attempt := range 4 {
		_, err := prepared.loadNestedListingRules(listing.Directories, blocked, &directoryFilterBudget{ctx: context.Background(), remaining: maxDirectoryFilterWork})
		if !errors.Is(err, ErrStaleReceipt) {
			t.Fatalf("attempt %d refusal=%v, want stale ignore snapshot", attempt, err)
		}
		if got := openDescriptorCount(t); got != baseline {
			t.Fatalf("attempt %d leaked host descriptors: before=%d after=%d", attempt, baseline, got)
		}
	}
}

func TestDirectoryListRefusedAtAncestorLimitClosesDescriptors(t *testing.T) {
	root := t.TempDir()
	target := root
	for range maxIgnoreAncestors + 12 {
		target = filepath.Join(target, "d")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	broker := NewBroker(registry).(DirectoryListingBroker)
	baseline := openDescriptorCount(t)
	for attempt := range 4 {
		prepared, err := broker.PrepareDirectoryList(root, target, GenerationSet{Root: 1, Policy: registry.View().Generation})
		if prepared != nil {
			_ = prepared.Close()
		}
		if !errors.Is(err, ErrProtected) {
			t.Fatalf("deep target attempt %d result=%v err=%v; want bounded refusal", attempt, prepared, err)
		}
		if got := openDescriptorCount(t); got != baseline {
			t.Fatalf("open descriptor count after refusal %d=%d, before=%d", attempt, got, baseline)
		}
	}
}

func TestDirectoryListAppliesIgnoredParentAndAnchoredRules(t *testing.T) {
	root := t.TempDir()
	private := filepath.Join(root, "private")
	src := filepath.Join(root, "src")
	rootSecret := filepath.Join(root, "secret")
	srcSecret := filepath.Join(src, "secret")
	for _, directory := range []string{private, src, rootSecret, srcSecret} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{filepath.Join(private, "secret.txt"), filepath.Join(root, "keep.log"), filepath.Join(rootSecret, "secret.txt"), filepath.Join(src, "keep.log"), filepath.Join(src, "visible.go"), filepath.Join(srcSecret, "visible.go")} {
		if err := os.WriteFile(target, []byte("synthetic"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("private/\n!private/secret.txt\n*.log\n!/keep.log\n/secret/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	prepare := func(directory string) *preparedDirectoryList {
		t.Helper()
		prepared, err := NewBroker(registry).(DirectoryListingBroker).PrepareDirectoryList(root, directory, GenerationSet{Root: 1, Policy: registry.View().Generation})
		if err != nil {
			t.Fatalf("prepare listing %s: %v", directory, err)
		}
		result, ok := prepared.(*preparedDirectoryList)
		if !ok {
			t.Fatalf("prepared listing has unexpected type %T", prepared)
		}
		return result
	}
	rootListing := prepare(root)
	t.Cleanup(func() { _ = rootListing.Close() })
	rootResult := filterPreparedListing(t, rootListing, []string{"keep.log", "src", "secret"})
	if !slices.Equal(rootResult.Names, []string{"keep.log", "src"}) {
		t.Fatalf("root anchored listing=%q, want keep.log and src", rootResult.Names)
	}
	srcListing := prepare(src)
	t.Cleanup(func() { _ = srcListing.Close() })
	srcResult := filterPreparedListing(t, srcListing, []string{"keep.log", "secret", "visible.go"})
	if !slices.Equal(srcResult.Names, []string{"secret", "visible.go"}) {
		t.Fatalf("nested anchored listing=%q, want secret and visible.go", srcResult.Names)
	}
	privateListing := prepare(private)
	t.Cleanup(func() { _ = privateListing.Close() })
	privateResult := filterPreparedListing(t, privateListing, []string{"secret.txt"})
	if len(privateResult.Names) != 0 || privateResult.Examined != 0 {
		t.Fatalf("ignored-parent listing=%+v, want empty disclosed result", privateResult)
	}
}

func TestIgnoreRulesFailClosedOnCharacterClassesAndMatchPrefixes(t *testing.T) {
	root := t.TempDir()
	for _, pattern := range []string{"[[:digit:]].txt"} {
		if _, err := parseIgnoreRules([]byte(pattern+"\n"), root); !errors.Is(err, ErrProtected) {
			t.Errorf("parseIgnoreRules(%q) err=%v, want ErrProtected", pattern, err)
		}
	}
	classRules, err := parseIgnoreRules([]byte("[!a].txt\n[abc].log\n[^a].cfg\n"), root)
	if err != nil {
		t.Fatalf("parse supported character classes: %v", err)
	}
	if !ignoreMatchesForTest(t, filepath.Join(root, "b.txt"), false, classRules) {
		t.Fatal("Git-negated character class did not exclude b.txt")
	}
	if ignoreMatchesForTest(t, filepath.Join(root, "a.txt"), false, classRules) {
		t.Fatal("Git-negated character class incorrectly excluded a.txt")
	}
	if !ignoreMatchesForTest(t, filepath.Join(root, "b.log"), false, classRules) {
		t.Fatal("ordinary character class did not exclude b.log")
	}
	if !ignoreMatchesForTest(t, filepath.Join(root, "b.cfg"), false, classRules) || ignoreMatchesForTest(t, filepath.Join(root, "a.cfg"), false, classRules) {
		t.Fatal("caret-negated character class did not exclude only non-a names")
	}
	byteRules, err := parseIgnoreRules([]byte("??.txt\n/??.log\n?.csv\n"), root)
	if err != nil {
		t.Fatal(err)
	}
	if !ignoreMatchesForTest(t, filepath.Join(root, "é.txt"), false, byteRules) {
		t.Fatal("unanchored question marks did not match UTF-8 filename bytes")
	}
	if !ignoreMatchesForTest(t, filepath.Join(root, "é.log"), false, byteRules) {
		t.Fatal("anchored question marks did not match UTF-8 filename bytes")
	}
	if ignoreMatchesForTest(t, filepath.Join(root, "é.csv"), false, byteRules) {
		t.Fatal("single question mark matched a two-byte UTF-8 character")
	}

	rules, err := parseIgnoreRules([]byte("*.secret\n!keep\n"), root)
	if err != nil {
		t.Fatal(err)
	}
	if !ignoreMatchesForTest(t, filepath.Join(root, "keep", "hidden.secret"), false, rules) {
		t.Fatal("negation matching an ancestor incorrectly re-included an ignored descendant")
	}

	nested := filepath.Join(root, "sub")
	ancestorRules, err := parseIgnoreRules([]byte("*.tmp\n"), root)
	if err != nil {
		t.Fatal(err)
	}
	nestedRules, err := parseIgnoreRules([]byte("*\n!visible.txt\n"), nested)
	if err != nil {
		t.Fatal(err)
	}
	allRules := append(ancestorRules, nestedRules...)
	if ignoreMatchesForTest(t, nested, true, allRules) {
		t.Fatal("nested ignore rules were applied to their containing directory")
	}
	if ignoreMatchesForTest(t, filepath.Join(nested, "visible.txt"), false, allRules) {
		t.Fatal("nested allowlist failed to re-include visible.txt")
	}
}

func TestDirectoryIgnoreMatcherBoundsAdversarialPatterns(t *testing.T) {
	root := t.TempDir()
	pattern := strings.Repeat("*a", 100) + "b"
	var input strings.Builder
	for range 40 {
		input.WriteString(pattern)
		input.WriteByte('\n')
	}
	rules, err := parseIgnoreRules([]byte(input.String()), root)
	if err != nil {
		t.Fatalf("parse legal adversarial patterns: %v", err)
	}
	namePath := filepath.Join(root, strings.Repeat("a", 255))
	budget := &directoryFilterBudget{ctx: context.Background(), remaining: maxDirectoryFilterWork}
	ignored, err := ignoredByRules(namePath, false, rules, budget)
	if err != nil || ignored {
		t.Fatalf("bounded adversarial match ignored=%t err=%v", ignored, err)
	}
	if budget.remaining <= 0 || budget.remaining >= maxDirectoryFilterWork {
		t.Fatalf("aggregate work was not accounted for, remaining=%d", budget.remaining)
	}
	limited := &directoryFilterBudget{ctx: context.Background(), remaining: 100_000}
	if _, err := ignoredByRules(namePath, false, rules, limited); !errors.Is(err, errDirectoryFilterBudget) {
		t.Fatalf("low aggregate budget err=%v, want filter budget exhaustion", err)
	}
}

func ignoreMatchesForTest(t *testing.T, namePath string, directory bool, rules []ignoreRule) bool {
	t.Helper()
	ignored, err := ignoredByRules(namePath, directory, rules, &directoryFilterBudget{ctx: context.Background(), remaining: maxDirectoryFilterWork})
	if err != nil {
		t.Fatalf("evaluate ignore rules: %v", err)
	}
	return ignored
}

func filterPreparedListing(t *testing.T, prepared *preparedDirectoryList, names []string) directoryListingResult {
	t.Helper()
	entries := make([]directoryListingEntry, 0, len(names))
	for _, name := range names {
		fd, err := unix.Openat(int(prepared.directory.Fd()), name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		var stat unix.Stat_t
		if err := unix.Fstat(fd, &stat); err != nil {
			_ = unix.Close(fd)
			t.Fatal(err)
		}
		_ = unix.Close(fd)
		entries = append(entries, directoryListingEntry{Name: name, Inode: stat.Ino, Type: direntTypeForMode(stat.Mode)})
	}
	var directoryStat unix.Stat_t
	if err := unix.Fstat(int(prepared.directory.Fd()), &directoryStat); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(directoryListingResult{Entries: entries, Examined: len(entries), MtimeNS: directoryStat.Mtim.Sec*1e9 + directoryStat.Mtim.Nsec, CtimeNS: directoryStat.Ctim.Sec*1e9 + directoryStat.Ctim.Nsec})
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := prepared.filterListing(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	var listing directoryListingResult
	if err := json.Unmarshal(filtered, &listing); err != nil {
		t.Fatal(err)
	}
	return listing
}

func openDescriptorCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func TestDirectoryListRefusesUnsupportedIgnoreRules(t *testing.T) {
	base := t.TempDir()
	directoryPath := filepath.Join(base, "sample")
	if err := os.Mkdir(directoryPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directoryPath, ".gitignore"), []byte("**/private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	prepared, err := NewBroker(registry).(DirectoryListingBroker).PrepareDirectoryList(base, directoryPath, GenerationSet{Root: 1, Policy: registry.View().Generation})
	if prepared != nil {
		_ = prepared.Close()
	}
	if !errors.Is(err, ErrProtected) {
		t.Fatalf("unsupported ignore syntax result=%v err=%v; want fail closed", prepared, err)
	}
}

func TestBrokerKeepsOutsideTargetRouteObjectAndTypeRefusals(t *testing.T) {
	base := t.TempDir()
	workingDirectory := filepath.Join(base, "working")
	outsideDirectory := filepath.Join(base, "outside")
	if err := os.Mkdir(workingDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outsideDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	ordinary := filepath.Join(outsideDirectory, "ordinary.txt")
	if err := os.WriteFile(ordinary, []byte("synthetic ordinary content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	credential := filepath.Join(base, "private", "auth.json")
	if err := os.Mkdir(filepath.Dir(credential), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credential, []byte("synthetic protected content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	credentialAlias := filepath.Join(outsideDirectory, "ordinary-looking.alias")
	if err := os.Link(credential, credentialAlias); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(outsideDirectory, "ordinary-looking.link")
	if err := os.Symlink(ordinary, symlink); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(outsideDirectory, "ordinary-looking.fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}

	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	if err := registry.Register(OwnerRegistration{Owner: "auth-fixture", Class: ClassCredential, Path: credential}); err != nil {
		t.Fatal(err)
	}
	broker := NewBroker(registry)
	generation := GenerationSet{Root: 1, Policy: registry.View().Generation}
	for _, test := range []struct {
		name string
		path string
		want error
	}{
		{name: "protected alias outside working directory", path: credentialAlias, want: ErrProtected},
		{name: "symbolic link outside working directory", path: symlink, want: unix.ELOOP},
		{name: "fifo outside working directory", path: fifo, want: errReadNotRegular},
	} {
		t.Run(test.name, func(t *testing.T) {
			prepared, err := broker.PrepareRead(workingDirectory, test.path, generation)
			if err == nil {
				_ = prepared.Close()
				t.Fatal("outside target bypassed an existing read guard")
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("outside target refusal=%v, want %v", err, test.want)
			}
		})
	}
}

func TestClassifyRouteProtectsReservedAbsentAndDirectoryRoutes(t *testing.T) {
	root := t.TempDir()
	reserved := filepath.Join(root, "reserved-auth.fixture")
	protectedDir := filepath.Join(root, "private")
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	if err := registry.Register(OwnerRegistration{Owner: "auth-fixture", Class: ClassCredential, Path: reserved}); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(protectedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(OwnerRegistration{Owner: "settings-fixture", Class: ClassSettings, Path: protectedDir, Directory: true}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]ResourceClass{
		reserved: ClassCredential,
		filepath.Join(protectedDir, "absent.fixture"): ClassSettings,
	} {
		class, protected := registry.ClassifyRoute(path)
		if !protected || class != want {
			t.Errorf("route classification for %q=(%q,%t), want (%q,true)", path, class, protected, want)
		}
	}
	if _, err := os.Lstat(reserved); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reserved fixture unexpectedly exists: %v", err)
	}
}

func TestRegistryProtectsRegisteredIdentityAndLocation(t *testing.T) {
	root := t.TempDir()
	protectedDir := filepath.Join(root, "private")
	allowedDir := filepath.Join(root, "allowed")
	if err := os.Mkdir(protectedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(allowedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(protectedDir, "auth.fixture")
	if err := os.WriteFile(authPath, []byte("credential-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	registration := OwnerRegistration{Owner: "auth-fixture", Class: ClassCredential, Path: authPath}
	if err := registry.Register(registration); err != nil {
		t.Fatal(err)
	}
	identity := identityAt(t, authPath)
	assertClassified(t, registry, authPath, identity, ClassCredential)

	// A pre-existing alias is classified by the pinned object identity.
	preexisting := filepath.Join(allowedDir, "preexisting.alias")
	if err := os.Link(authPath, preexisting); err != nil {
		t.Fatal(err)
	}
	assertClassified(t, registry, preexisting, identityAt(t, preexisting), ClassCredential)

	// A real independent process creates the late alias only after its READY
	// handshake, then the broker's policy lookup checks the resulting object.
	late := filepath.Join(allowedDir, "late.alias")
	actor := startNamespaceActor(t, root, "link", "private/auth.fixture", "allowed/late.alias")
	if _, err := os.Lstat(late); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("late alias appeared before actor release: %v", err)
	}
	actor.Run(t)
	assertClassified(t, registry, late, identityAt(t, late), ClassCredential)

	// The object remains protected after its registered name is moved.
	moved := filepath.Join(allowedDir, "renamed-auth.fixture")
	actor = startNamespaceActor(t, root, "rename", "private/auth.fixture", "allowed/renamed-auth.fixture")
	actor.Run(t)
	assertClassified(t, registry, moved, identityAt(t, moved), ClassCredential)

	// A replacement at a registered logical location remains protected even
	// though it has a different inode from the registered object.
	replacement := filepath.Join(allowedDir, "replacement.fixture")
	if err := os.WriteFile(replacement, []byte("ordinary-synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authPath, []byte("rotated-synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	actor = startNamespaceActor(t, root, "replace", "private/auth.fixture", "allowed/replacement.fixture")
	actor.Run(t)
	assertClassified(t, registry, authPath, identityAt(t, authPath), ClassCredential)

	// An ordinary inode moved into a protected location is protected by that
	// location rule, independent of the inode's prior class.
	ordinary := filepath.Join(allowedDir, "ordinary.fixture")
	if err := os.WriteFile(ordinary, []byte("ordinary-synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	actor = startNamespaceActor(t, root, "rename", "allowed/ordinary.fixture", "private/auth.fixture")
	actor.Run(t)
	assertClassified(t, registry, authPath, identityAt(t, authPath), ClassCredential)
}

func TestRegistryDirectoryPinDoesNotImplyDescendantIdentityCoverage(t *testing.T) {
	root := t.TempDir()
	protectedDir := filepath.Join(root, "registered-private")
	allowedDir := filepath.Join(root, "allowed")
	if err := os.Mkdir(protectedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(allowedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(protectedDir, "not-individually-registered.fixture")
	aliasChild := filepath.Join(protectedDir, "alias-child.fixture")
	for _, path := range []string{child, aliasChild} {
		if err := os.WriteFile(path, []byte("synthetic-child"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	if err := registry.Register(OwnerRegistration{Owner: "settings-fixture", Class: ClassSettings, Path: protectedDir, Directory: true}); err != nil {
		t.Fatal(err)
	}
	directoryID := identityAt(t, protectedDir)
	childID := identityAt(t, child)
	if directoryID == childID {
		t.Fatal("fixture directory and file unexpectedly share one identity")
	}
	view := registry.View()
	if len(view.Entries) != 1 || view.Entries[0].Identity != directoryID {
		t.Fatalf("registration should pin only the directory object, got %+v", view.Entries)
	}

	movedDir := filepath.Join(allowedDir, "moved-private")
	actor := startNamespaceActor(t, root, "rename", "registered-private", "allowed/moved-private")
	actor.Run(t)
	// A replacement directory at the protected logical location remains
	// protected by the location rule even though it has a different inode.
	if err := os.Mkdir(protectedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	replacementChild := filepath.Join(protectedDir, "replacement-child.fixture")
	if err := os.WriteFile(replacementChild, []byte("replacement-child-synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	broker := NewBroker(registry)
	prepared, err := broker.PrepareRead(root, replacementChild, GenerationSet{Root: 1, Policy: registry.View().Generation})
	if !errors.Is(err, ErrProtected) {
		if err == nil {
			_ = prepared.Close()
		}
		t.Fatalf("replacement protected-directory child result=%v, want ErrProtected", err)
	}

	movedChild := filepath.Join(movedDir, filepath.Base(child))
	if class, ok := registry.Classify(movedChild, identityAt(t, movedChild)); !ok || class != ClassSettings {
		t.Fatalf("registered directory's descendant after directory move was not protected: class=%q protected=%t", class, ok)
	}
	prepared, err = broker.PrepareRead(root, movedChild, GenerationSet{Root: 1, Policy: registry.View().Generation})
	if !errors.Is(err, ErrProtected) && !errors.Is(err, ErrDirectoryCoverage) {
		if err == nil {
			_ = prepared.Close()
		}
		t.Fatalf("broker result for read below moved registered directory=%v, want protected/coverage refusal", err)
	}

	// A directory pin plus ancestry check protects current descendants within
	// the moved directory, but it does not pin each child inode. Once the
	// independent actor moves a child out, the old location rule and pinned
	// directory identity alone cannot prove that child's protected identity.
	movedOut := filepath.Join(allowedDir, "moved-child.fixture")
	actor = startNamespaceActor(t, root, "rename", "allowed/moved-private/not-individually-registered.fixture", "allowed/moved-child.fixture")
	actor.Run(t)
	if _, ok := registry.Classify(movedOut, identityAt(t, movedOut)); ok {
		t.Fatal("directory pin unexpectedly claimed to pin a child object moved outside")
	}
	prepared, err = broker.PrepareRead(root, movedOut, GenerationSet{Root: 1, Policy: registry.View().Generation})
	if err == nil {
		_ = prepared.Close()
		t.Fatal("moved-out child under protected directory was preparable; expected fail-closed coverage refusal")
	} else if !errors.Is(err, ErrDirectoryCoverage) {
		t.Fatalf("moved-out child refusal=%v, want ErrDirectoryCoverage", err)
	} else {
		t.Logf("moved-out unregistered descendant refused before worker handoff: %v", err)
	}

	// A hard-link alias outside the registered tree is the same gap: the
	// directory registration did not pin the child's identity.
	outsideAlias := filepath.Join(allowedDir, "outside-child.alias")
	actor = startNamespaceActor(t, root, "link", "allowed/moved-private/alias-child.fixture", "allowed/outside-child.alias")
	actor.Run(t)
	if _, ok := registry.Classify(outsideAlias, identityAt(t, outsideAlias)); ok {
		t.Fatal("directory pin unexpectedly protected an unregistered descendant through an outside alias")
	}
	prepared, err = broker.PrepareRead(root, outsideAlias, GenerationSet{Root: 1, Policy: registry.View().Generation})
	if err == nil {
		_ = prepared.Close()
		t.Fatal("outside hard-link alias of unregistered protected descendant was preparable")
	} else if !errors.Is(err, ErrDirectoryCoverage) && !errors.Is(err, ErrProtected) {
		t.Fatalf("outside alias refusal=%v, want protected/coverage refusal", err)
	} else {
		t.Logf("outside hard-link alias refused before worker handoff: %v", err)
	}
	t.Log("known limitation: directory identity plus ancestry protects descendants while they remain inside the directory; unpinned child identities remain incomplete, so same-device scopes fail closed rather than claim per-child protection")
}

func TestRegistryDirectoryRuleCannotBeWeakenedByPinnedProviderChild(t *testing.T) {
	root := t.TempDir()
	protectedDir := filepath.Join(root, "settings")
	outsideDir := filepath.Join(root, "ordinary")
	if err := os.Mkdir(protectedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outsideDir, 0o700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(protectedDir, "provider-source.fixture")
	if err := os.WriteFile(child, []byte("synthetic provider source"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	if err := registry.Register(OwnerRegistration{Owner: "settings-fixture", Class: ClassSettings, Path: protectedDir, Directory: true}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(OwnerRegistration{Owner: "provider-fixture", Class: ClassProvider, Path: child}); err != nil {
		t.Fatal(err)
	}
	childID := identityAt(t, child)
	moved := filepath.Join(outsideDir, filepath.Base(child))
	if err := os.Rename(child, moved); err != nil {
		t.Fatal(err)
	}
	if !registry.DirectoryCoverageIncomplete(childID) {
		t.Fatal("weaker provider pin bypassed protected settings-directory coverage")
	}
	broker := NewBroker(registry)
	prepared, err := broker.PrepareRead(root, moved, GenerationSet{Root: 1, Policy: registry.View().Generation})
	if err == nil {
		_ = prepared.Close()
		t.Fatal("moved provider child under protected settings directory was preparable")
	}
	if !errors.Is(err, ErrDirectoryCoverage) {
		t.Fatalf("moved provider child refusal=%v, want ErrDirectoryCoverage", err)
	}
}

func TestRegistryReservesLocationBeforeCreationAndRetainsRotatedObjects(t *testing.T) {
	root := t.TempDir()
	credentialPath := filepath.Join(root, "auth.fixture")
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	if err := registry.Register(OwnerRegistration{Owner: "auth-fixture", Class: ClassCredential, Path: credentialPath}); err != nil {
		t.Fatal(err)
	}
	if len(registry.View().Entries) != 1 || registry.View().Entries[0].Identity != (FileIdentity{}) {
		t.Fatal("nonexistent registered location was not retained as a location rule")
	}
	if err := os.WriteFile(credentialPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// The new temporary file is empty when registered; no sensitive bytes are
	// written until its object identity is pinned.
	if err := registry.Register(OwnerRegistration{Owner: "auth-fixture", Class: ClassCredential, Path: credentialPath}); err != nil {
		t.Fatal(err)
	}
	newIdentity := identityAt(t, credentialPath)
	assertClassified(t, registry, credentialPath, newIdentity, ClassCredential)
	if err := os.WriteFile(credentialPath, []byte("new-credential-synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	newAlias := filepath.Join(root, "new.alias")
	if err := os.Link(credentialPath, newAlias); err != nil {
		t.Fatal(err)
	}
	assertClassified(t, registry, newAlias, identityAt(t, newAlias), ClassCredential)

	// Rotation retains both the old object pin and new object/path registration.
	oldPath := filepath.Join(root, "old.auth")
	if err := os.Rename(credentialPath, oldPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentialPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterRotation("auth-fixture", []OwnerRegistration{{Owner: "auth-fixture", Class: ClassCredential, Path: credentialPath}}); err != nil {
		t.Fatal(err)
	}
	latestIdentity := identityAt(t, credentialPath)
	assertClassified(t, registry, oldPath, identityAt(t, oldPath), ClassCredential)
	assertClassified(t, registry, newAlias, identityAt(t, newAlias), ClassCredential)
	assertClassified(t, registry, credentialPath, latestIdentity, ClassCredential)
	if latestIdentity == newIdentity {
		t.Fatal("rotation fixture did not create a distinct replacement object")
	}
}

func TestRegistryFailsClosedAtPinBudgetAndClosesPins(t *testing.T) {
	root := t.TempDir()
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxPinnedObjects; i++ {
		path := filepath.Join(root, fmt.Sprintf("pin-%03d.fixture", i))
		if err := os.WriteFile(path, []byte("synthetic"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := registry.Register(OwnerRegistration{Owner: "owner", Class: ClassTokenDatabase, Path: path}); err != nil {
			t.Fatalf("register pin %d: %v", i, err)
		}
	}
	tooMany := filepath.Join(root, "over-budget.fixture")
	if err := os.WriteFile(tooMany, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = registry.Register(OwnerRegistration{Owner: "owner", Class: ClassTokenDatabase, Path: tooMany})
	if !errors.Is(err, ErrPinBudget) {
		t.Fatalf("registration beyond fixed pin budget error=%v, want ErrPinBudget", err)
	}
	var fds []int
	for _, entry := range registry.entries {
		if entry.pin != nil {
			fds = append(fds, int(entry.pin.Fd()))
		}
	}
	if err := registry.Close(); err != nil {
		t.Fatal(err)
	}
	for _, fd := range fds {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
			t.Errorf("pinned descriptor %d remains open after registry close: %v", fd, err)
		}
	}
}

func identityAt(t *testing.T, path string) FileIdentity {
	t.Helper()
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		t.Fatal(err)
	}
	return identityFromStat(&stat)
}

func assertClassified(t *testing.T, registry PolicyRegistry, path string, identity FileIdentity, want ResourceClass) {
	t.Helper()
	class, protected := registry.Classify(path, identity)
	if want == ClassOrdinary {
		if protected || class != "" {
			t.Fatalf("Classify(%q,%+v)=(%q,%t), want unregistered ordinary result", path, identity, class, protected)
		}
		return
	}
	if !protected || class != want {
		t.Fatalf("Classify(%q,%+v)=(%q,%t), want protected class %q", path, identity, class, protected, want)
	}
}
