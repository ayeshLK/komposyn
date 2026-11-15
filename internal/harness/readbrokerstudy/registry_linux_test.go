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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/sys/unix"
)

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
	filtered, err := prepared.(*preparedDirectoryList).filterListing(raw)
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
}

func TestDirectoryListHonorsAncestorIgnoreRules(t *testing.T) {
	base := t.TempDir()
	directoryPath := filepath.Join(base, "src")
	if err := os.Mkdir(directoryPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, ".gitignore"), []byte("*.log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	for _, name := range []string{"trace.log", "keep.go"} {
		if err := os.WriteFile(filepath.Join(directoryPath, name), []byte("synthetic"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := NewBroker(registry).(DirectoryListingBroker).PrepareDirectoryList(base, directoryPath, GenerationSet{Root: 1, Policy: registry.View().Generation})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Close() })
	entries := make([]directoryListingEntry, 0, 2)
	for _, name := range []string{"trace.log", "keep.go"} {
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
	raw, err := json.Marshal(directoryListingResult{Entries: entries, Examined: 2, MtimeNS: directoryStat.Mtim.Sec*1e9 + directoryStat.Mtim.Nsec, CtimeNS: directoryStat.Ctim.Sec*1e9 + directoryStat.Ctim.Nsec})
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := prepared.(*preparedDirectoryList).filterListing(raw)
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
