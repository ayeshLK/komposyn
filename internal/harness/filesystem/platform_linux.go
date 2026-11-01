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
	"io"
	"os"
	"path"
	"strings"
	"syscall"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	resolveBeneath = unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS
	resolveScoped  = resolveBeneath | unix.RESOLVE_NO_XDEV
	createMode     = 0o600
)

type linuxRoot struct {
	fd       int
	rootPath string
	hook     func(string)
}

type fileIdentity struct {
	device uint64
	inode  uint64
	type_  uint32
}

type linuxReadTarget struct {
	owner          *linuxRoot
	rootFD         int
	rootPath       string
	parentFD       int
	fileFD         int
	parentRelative string
	leaf           string
	parentID       fileIdentity
	fileID         fileIdentity
}

type linuxWriteTarget struct {
	owner          *linuxRoot
	rootFD         int
	rootPath       string
	parentFD       int
	parentRelative string
	leaf           string
	parentID       fileIdentity
	exists         bool
	targetID       fileIdentity
	targetFD       int
}

type linuxDeleteTarget struct {
	owner          *linuxRoot
	rootFD         int
	rootPath       string
	parentFD       int
	parentRelative string
	leaf           string
	parentID       fileIdentity
	targetID       fileIdentity
	targetFD       int
	recursive      bool
}

func openRootNative(root string) (rootBackend, error) {
	filesystemFD, err := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, mapOpenError(err)
	}
	relative := strings.TrimPrefix(root, "/")
	if relative == "" {
		relative = "."
	}
	fd, err := openat2(filesystemFD, relative, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0, resolveBeneath)
	_ = unix.Close(filesystemFD)
	if err != nil {
		return nil, mapOpenError(err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		_ = unix.Close(fd)
		if err != nil {
			return nil, err
		}
		return nil, ErrInvalidPath
	}
	return &linuxRoot{fd: fd, rootPath: root}, nil
}

func (r *linuxRoot) close() error { return unix.Close(r.fd) }

func (r *linuxRoot) runTestHook(stage string) {
	if r.hook != nil {
		r.hook(stage)
	}
}

func (r *linuxRoot) prepareRead(relative string) (readBackend, error) {
	rootFD, err := dupCloexec(r.fd)
	if err != nil {
		return nil, err
	}
	parentRelative, leaf := splitTarget(relative)
	parentFD, err := openDirectory(rootFD, parentRelative)
	if err != nil {
		_ = unix.Close(rootFD)
		return nil, err
	}
	fileFD, err := openat2(parentFD, leaf, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0, resolveScoped)
	if err != nil {
		closeFDs(parentFD, rootFD)
		return nil, mapOpenError(err)
	}
	var fileStat, parentStat unix.Stat_t
	if err := unix.Fstat(fileFD, &fileStat); err != nil || fileStat.Mode&unix.S_IFMT != unix.S_IFREG {
		closeFDs(fileFD, parentFD, rootFD)
		if err != nil {
			return nil, err
		}
		return nil, ErrNotRegular
	}
	if fileStat.Nlink > 1 {
		closeFDs(fileFD, parentFD, rootFD)
		return nil, ErrAliasedTarget
	}
	if err := unix.Fstat(parentFD, &parentStat); err != nil {
		closeFDs(fileFD, parentFD, rootFD)
		return nil, err
	}
	return &linuxReadTarget{owner: r, rootFD: rootFD, rootPath: r.rootPath, parentFD: parentFD, fileFD: fileFD, parentRelative: parentRelative, leaf: leaf, parentID: identityOf(&parentStat), fileID: identityOf(&fileStat)}, nil
}

func (r *linuxRoot) prepareWrite(relative string) (writeBackend, error) {
	rootFD, err := dupCloexec(r.fd)
	if err != nil {
		return nil, err
	}
	parentRelative, leaf := splitTarget(relative)
	parentFD, err := openDirectory(rootFD, parentRelative)
	if err != nil {
		_ = unix.Close(rootFD)
		return nil, err
	}
	var parentStat, targetStat unix.Stat_t
	if err := unix.Fstat(parentFD, &parentStat); err != nil {
		closeFDs(parentFD, rootFD)
		return nil, err
	}
	err = unix.Fstatat(parentFD, leaf, &targetStat, unix.AT_SYMLINK_NOFOLLOW)
	exists := err == nil
	if err != nil && !errors.Is(err, unix.ENOENT) {
		closeFDs(parentFD, rootFD)
		return nil, mapOpenError(err)
	}
	if exists {
		if targetStat.Mode&unix.S_IFMT == unix.S_IFLNK {
			closeFDs(parentFD, rootFD)
			return nil, ErrSymlink
		}
		if targetStat.Mode&unix.S_IFMT != unix.S_IFREG {
			closeFDs(parentFD, rootFD)
			return nil, ErrNotRegular
		}
		if targetStat.Nlink > 1 {
			closeFDs(parentFD, rootFD)
			return nil, ErrAliasedTarget
		}
		targetFD, err := openat2(parentFD, leaf, unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0, resolveScoped)
		if err != nil {
			closeFDs(parentFD, rootFD)
			return nil, mapOpenError(err)
		}
		var pinned unix.Stat_t
		if err := unix.Fstat(targetFD, &pinned); err != nil || !sameIdentity(identityOf(&pinned), identityOf(&targetStat)) {
			closeFDs(targetFD, parentFD, rootFD)
			if err != nil {
				return nil, err
			}
			return nil, ErrTargetChanged
		}
		return &linuxWriteTarget{owner: r, rootFD: rootFD, rootPath: r.rootPath, parentFD: parentFD, parentRelative: parentRelative, leaf: leaf, parentID: identityOf(&parentStat), exists: true, targetID: identityOf(&targetStat), targetFD: targetFD}, nil
	}
	return &linuxWriteTarget{owner: r, rootFD: rootFD, rootPath: r.rootPath, parentFD: parentFD, parentRelative: parentRelative, leaf: leaf, parentID: identityOf(&parentStat), targetFD: -1}, nil
}

func (r *linuxRoot) prepareDelete(relative string, recursive bool) (deleteBackend, error) {
	rootFD, err := dupCloexec(r.fd)
	if err != nil {
		return nil, err
	}
	parentRelative, leaf := splitTarget(relative)
	parentFD, err := openDirectory(rootFD, parentRelative)
	if err != nil {
		_ = unix.Close(rootFD)
		return nil, err
	}
	var parentStat, targetStat unix.Stat_t
	if err := unix.Fstat(parentFD, &parentStat); err != nil {
		closeFDs(parentFD, rootFD)
		return nil, err
	}
	if err := unix.Fstatat(parentFD, leaf, &targetStat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		closeFDs(parentFD, rootFD)
		return nil, mapOpenError(err)
	}
	if targetStat.Mode&unix.S_IFMT == unix.S_IFDIR && !recursive {
		closeFDs(parentFD, rootFD)
		return nil, ErrRecursiveIntentNeeded
	}
	targetFD, err := openat2(parentFD, leaf, unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0, resolveScoped)
	if err != nil {
		closeFDs(parentFD, rootFD)
		return nil, mapOpenError(err)
	}
	var pinned unix.Stat_t
	if err := unix.Fstat(targetFD, &pinned); err != nil || !sameIdentity(identityOf(&pinned), identityOf(&targetStat)) {
		closeFDs(targetFD, parentFD, rootFD)
		if err != nil {
			return nil, err
		}
		return nil, ErrTargetChanged
	}
	return &linuxDeleteTarget{owner: r, rootFD: rootFD, rootPath: r.rootPath, parentFD: parentFD, parentRelative: parentRelative, leaf: leaf, parentID: identityOf(&parentStat), targetID: identityOf(&targetStat), targetFD: targetFD, recursive: recursive}, nil
}

func identityOf(stat *unix.Stat_t) fileIdentity {
	return fileIdentity{device: uint64(stat.Dev), inode: stat.Ino, type_: stat.Mode & unix.S_IFMT}
}

func sameIdentity(a, b fileIdentity) bool { return a == b }

func splitTarget(relative string) (string, string) {
	parent, leaf := path.Split(relative)
	parent = strings.TrimSuffix(parent, "/")
	if parent == "" {
		parent = "."
	}
	return parent, leaf
}

func openDirectory(rootFD int, relative string) (int, error) {
	return openat2(rootFD, relative, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0, resolveScoped)
}

func openat2(dirFD int, name string, flags int, mode uint32, resolve uint64) (int, error) {
	how := &unix.OpenHow{Flags: uint64(flags), Mode: uint64(mode), Resolve: resolve}
	return unix.Openat2(dirFD, name, how)
}

func dupCloexec(fd int) (int, error) {
	return unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
}

func mapOpenError(err error) error {
	switch {
	case errors.Is(err, unix.ELOOP):
		return ErrSymlink
	case errors.Is(err, unix.EXDEV):
		return ErrOutOfScope
	case errors.Is(err, unix.EAGAIN):
		return ErrTargetChanged
	case errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EINVAL), errors.Is(err, unix.E2BIG):
		return ErrUnsupported
	default:
		return err
	}
}

func closeFDs(fds ...int) {
	for _, fd := range fds {
		_ = unix.Close(fd)
	}
}

func closeMany(fds ...int) error {
	var first error
	for _, fd := range fds {
		if fd < 0 {
			continue
		}
		if err := unix.Close(fd); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (t *linuxReadTarget) close() error { return closeMany(t.fileFD, t.parentFD, t.rootFD) }

func (t *linuxReadTarget) read(ctx context.Context, maxBytes int) (ReadResult, error) {
	if err := revalidateParent(t.rootFD, t.rootPath, t.parentRelative, t.parentFD, t.parentID); err != nil {
		return ReadResult{Outcome: OutcomeNotExecuted}, err
	}
	if err := revalidateEntry(t.parentFD, t.leaf, t.fileID); err != nil {
		return ReadResult{Outcome: OutcomeNotExecuted}, err
	}
	t.owner.runTestHook("read-after-revalidate")
	var stat unix.Stat_t
	if err := unix.Fstat(t.fileFD, &stat); err != nil {
		return ReadResult{Outcome: OutcomeFailed}, err
	}
	if !sameIdentity(identityOf(&stat), t.fileID) || stat.Nlink == 0 {
		return ReadResult{Outcome: OutcomeNotExecuted}, ErrTargetChanged
	}
	if stat.Nlink > 1 {
		return ReadResult{Outcome: OutcomeNotExecuted}, ErrAliasedTarget
	}
	buffer := make([]byte, min(maxBytes+1, 32<<10))
	content := make([]byte, 0, min(maxBytes+1, 32<<10))
	var offset int64
	for len(content) <= maxBytes {
		if err := ctx.Err(); err != nil {
			return ReadResult{Outcome: OutcomeCancelled}, err
		}
		remaining := maxBytes + 1 - len(content)
		if remaining < len(buffer) {
			buffer = buffer[:remaining]
		}
		count, err := unix.Pread(t.fileFD, buffer, offset)
		if count > 0 {
			content = append(content, buffer[:count]...)
			offset += int64(count)
		}
		if errors.Is(err, syscall.EINTR) {
			return ReadResult{Outcome: OutcomeUnknown}, err
		}
		if err != nil {
			return ReadResult{Outcome: OutcomeFailed}, err
		}
		if count == 0 {
			break
		}
	}
	truncated := len(content) > maxBytes
	if truncated {
		content = content[:maxBytes]
	}
	if truncated {
		prefix, err := validUTF8Prefix(content)
		if err != nil {
			return ReadResult{Outcome: OutcomeFailed}, err
		}
		content = prefix
	} else if !utf8.Valid(content) {
		return ReadResult{Outcome: OutcomeFailed}, ErrInvalidText
	}
	return ReadResult{Outcome: OutcomeSucceeded, Text: string(content), Truncated: truncated}, nil
}

func validUTF8Prefix(content []byte) ([]byte, error) {
	for offset := 0; offset < len(content); {
		_, size := utf8.DecodeRune(content[offset:])
		if size == 1 && content[offset] >= utf8.RuneSelf {
			if !utf8.FullRune(content[offset:]) {
				return content[:offset], nil
			}
			return nil, ErrInvalidText
		}
		offset += size
	}
	return content, nil
}

func (t *linuxWriteTarget) close() error { return closeMany(t.targetFD, t.parentFD, t.rootFD) }

func (t *linuxWriteTarget) write(ctx context.Context, text []byte) (MutationResult, error) {
	if err := revalidateParent(t.rootFD, t.rootPath, t.parentRelative, t.parentFD, t.parentID); err != nil {
		return MutationResult{Outcome: OutcomeNotExecuted}, err
	}
	if t.exists {
		if err := revalidateEntry(t.parentFD, t.leaf, t.targetID); err != nil {
			return MutationResult{Outcome: OutcomeNotExecuted}, err
		}
	} else if entryExists(t.parentFD, t.leaf) {
		return MutationResult{Outcome: OutcomeNotExecuted}, ErrTargetChanged
	}
	t.owner.runTestHook("write-after-revalidate")
	if err := ctx.Err(); err != nil {
		return MutationResult{Outcome: OutcomeCancelled}, err
	}
	fd, created, err := t.openForWrite()
	if err != nil {
		return MutationResult{Outcome: OutcomeNotExecuted}, err
	}
	defer unix.Close(fd)
	if err := ctx.Err(); err != nil {
		if created {
			return MutationResult{Outcome: OutcomePartial}, err
		}
		return MutationResult{Outcome: OutcomeCancelled}, err
	}
	mutated := created
	if !created {
		if err := unix.Ftruncate(fd, 0); err != nil {
			if errors.Is(err, syscall.EINTR) {
				return MutationResult{Outcome: OutcomeUnknown}, err
			}
			return MutationResult{Outcome: OutcomeFailed}, err
		}
		mutated = true
		t.owner.runTestHook("write-after-truncate")
	} else {
		t.owner.runTestHook("write-after-create")
	}
	var written int64
	for offset := 0; offset < len(text); {
		if err := ctx.Err(); err != nil {
			if mutated {
				return MutationResult{Outcome: OutcomePartial, BytesWritten: written}, err
			}
			return MutationResult{Outcome: OutcomeCancelled}, err
		}
		end := min(offset+(32<<10), len(text))
		count, err := unix.Write(fd, text[offset:end])
		written += int64(count)
		offset += count
		if errors.Is(err, syscall.EINTR) {
			return MutationResult{Outcome: OutcomeUnknown, BytesWritten: written}, err
		}
		if err != nil {
			if mutated {
				return MutationResult{Outcome: OutcomePartial, BytesWritten: written}, err
			}
			return MutationResult{Outcome: OutcomeFailed}, err
		}
		if count == 0 {
			return MutationResult{Outcome: OutcomeUnknown, BytesWritten: written}, io.ErrShortWrite
		}
	}
	return MutationResult{Outcome: OutcomeSucceeded, BytesWritten: written}, nil
}

func (t *linuxWriteTarget) openForWrite() (int, bool, error) {
	if t.exists {
		fd, err := openat2(t.parentFD, t.leaf, unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0, resolveScoped)
		if err != nil {
			if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ELOOP) {
				return -1, false, ErrTargetChanged
			}
			return -1, false, mapOpenError(err)
		}
		var stat unix.Stat_t
		if err := unix.Fstat(fd, &stat); err != nil {
			_ = unix.Close(fd)
			return -1, false, err
		}
		if stat.Mode&unix.S_IFMT != unix.S_IFREG || !sameIdentity(identityOf(&stat), t.targetID) {
			_ = unix.Close(fd)
			return -1, false, ErrTargetChanged
		}
		if stat.Nlink > 1 {
			_ = unix.Close(fd)
			return -1, false, ErrAliasedTarget
		}
		return fd, false, nil
	}
	fd, err := openat2(t.parentFD, t.leaf, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, createMode, resolveScoped)
	if errors.Is(err, unix.EEXIST) {
		return -1, false, ErrTargetChanged
	}
	if err != nil {
		return -1, false, mapOpenError(err)
	}
	return fd, true, nil
}

func (t *linuxDeleteTarget) close() error { return closeMany(t.targetFD, t.parentFD, t.rootFD) }

func (t *linuxDeleteTarget) remove(ctx context.Context) (MutationResult, error) {
	if err := revalidateParent(t.rootFD, t.rootPath, t.parentRelative, t.parentFD, t.parentID); err != nil {
		return MutationResult{Outcome: OutcomeNotExecuted}, err
	}
	if err := revalidateEntry(t.parentFD, t.leaf, t.targetID); err != nil {
		return MutationResult{Outcome: OutcomeNotExecuted}, err
	}
	t.owner.runTestHook("delete-after-revalidate")
	if err := revalidateEntry(t.parentFD, t.leaf, t.targetID); err != nil {
		return MutationResult{Outcome: OutcomeNotExecuted}, err
	}
	if err := ctx.Err(); err != nil {
		return MutationResult{Outcome: OutcomeCancelled}, err
	}
	if t.targetID.type_ == unix.S_IFDIR {
		if !t.recursive {
			return MutationResult{Outcome: OutcomeNotExecuted}, ErrRecursiveIntentNeeded
		}
		dirFD, err := openat2(t.parentFD, t.leaf, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0, resolveScoped)
		if err != nil {
			return MutationResult{Outcome: OutcomeNotExecuted}, mapOpenError(err)
		}
		var stat unix.Stat_t
		if err := unix.Fstat(dirFD, &stat); err != nil || !sameIdentity(identityOf(&stat), t.targetID) {
			_ = unix.Close(dirFD)
			if err != nil {
				return MutationResult{Outcome: OutcomeNotExecuted}, err
			}
			return MutationResult{Outcome: OutcomeNotExecuted}, ErrTargetChanged
		}
		count := int64(0)
		entries := 0
		if err := removeContents(ctx, dirFD, 0, &entries, &count, t.owner); err != nil {
			_ = unix.Close(dirFD)
			return deleteFailure(count, err)
		}
		_ = unix.Close(dirFD)
		if err := ctx.Err(); err != nil {
			return deleteFailure(count, err)
		}
		if err := revalidateEntry(t.parentFD, t.leaf, t.targetID); err != nil {
			return deleteFailure(count, err)
		}
		if err := unix.Unlinkat(t.parentFD, t.leaf, unix.AT_REMOVEDIR); err != nil {
			if errors.Is(err, syscall.EINTR) {
				return MutationResult{Outcome: OutcomeUnknown, EntriesDeleted: count}, err
			}
			return deleteFailure(count, err)
		}
		return MutationResult{Outcome: OutcomeSucceeded, EntriesDeleted: count + 1}, nil
	}
	if err := unix.Unlinkat(t.parentFD, t.leaf, 0); err != nil {
		if errors.Is(err, syscall.EINTR) {
			return MutationResult{Outcome: OutcomeUnknown}, err
		}
		return MutationResult{Outcome: OutcomeFailed}, err
	}
	return MutationResult{Outcome: OutcomeSucceeded, EntriesDeleted: 1}, nil
}

func removeContents(ctx context.Context, dirFD int, depth int, entries *int, deleted *int64, owner *linuxRoot) error {
	if depth > MaxDeleteDepth {
		return ErrTooLarge
	}
	dup, err := dupCloexec(dirFD)
	if err != nil {
		return err
	}
	directory := os.NewFile(uintptr(dup), "filesystem-directory")
	defer directory.Close()
	for {
		items, readErr := directory.ReadDir(256)
		for _, item := range items {
			if err := ctx.Err(); err != nil {
				return err
			}
			*entries = *entries + 1
			if *entries > MaxDeleteEntries {
				return ErrTooLarge
			}
			name := item.Name()
			var stat unix.Stat_t
			if err := unix.Fstatat(dirFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return mapOpenError(err)
			}
			identity := identityOf(&stat)
			if identity.type_ == unix.S_IFDIR {
				childFD, err := openat2(dirFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0, resolveScoped)
				if err != nil {
					return mapOpenError(err)
				}
				var childStat unix.Stat_t
				if err := unix.Fstat(childFD, &childStat); err != nil || !sameIdentity(identityOf(&childStat), identity) {
					_ = unix.Close(childFD)
					if err != nil {
						return err
					}
					return ErrTargetChanged
				}
				if err := removeContents(ctx, childFD, depth+1, entries, deleted, owner); err != nil {
					_ = unix.Close(childFD)
					return err
				}
				_ = unix.Close(childFD)
				if err := revalidateEntry(dirFD, name, identity); err != nil {
					return err
				}
				if err := unix.Unlinkat(dirFD, name, unix.AT_REMOVEDIR); err != nil {
					return err
				}
				*deleted = *deleted + 1
				owner.runTestHook("delete-entry-removed")
				continue
			}
			if err := revalidateEntry(dirFD, name, identity); err != nil {
				return err
			}
			// unlinkat removes a symlink entry itself; it never follows its target.
			if err := unix.Unlinkat(dirFD, name, 0); err != nil {
				return err
			}
			*deleted = *deleted + 1
			owner.runTestHook("delete-entry-removed")
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if errors.Is(readErr, io.EOF) || len(items) == 0 {
			return nil
		}
	}
}

func deleteFailure(deleted int64, err error) (MutationResult, error) {
	outcome := OutcomeFailed
	if deleted > 0 {
		outcome = OutcomePartial
	}
	if errors.Is(err, syscall.EINTR) {
		outcome = OutcomeUnknown
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		if deleted > 0 {
			outcome = OutcomePartial
		} else {
			outcome = OutcomeCancelled
		}
	}
	return MutationResult{Outcome: outcome, EntriesDeleted: deleted}, err
}

func revalidateParent(rootFD int, rootPath, relative string, preparedFD int, expected fileIdentity) error {
	currentRootFD, err := openAbsoluteDirectory(rootPath)
	if err != nil {
		return ErrTargetChanged
	}
	defer unix.Close(currentRootFD)
	var rootStat, preparedRootStat unix.Stat_t
	if err := unix.Fstat(currentRootFD, &rootStat); err != nil {
		return err
	}
	if err := unix.Fstat(rootFD, &preparedRootStat); err != nil {
		return err
	}
	if !sameIdentity(identityOf(&rootStat), identityOf(&preparedRootStat)) {
		return ErrTargetChanged
	}
	currentFD, err := openDirectory(rootFD, relative)
	if err != nil {
		return ErrTargetChanged
	}
	defer unix.Close(currentFD)
	var stat unix.Stat_t
	if err := unix.Fstat(currentFD, &stat); err != nil {
		return err
	}
	if !sameIdentity(identityOf(&stat), expected) {
		return ErrTargetChanged
	}
	var prepared unix.Stat_t
	if err := unix.Fstat(preparedFD, &prepared); err != nil {
		return err
	}
	if !sameIdentity(identityOf(&prepared), expected) {
		return ErrTargetChanged
	}
	return nil
}

func openAbsoluteDirectory(root string) (int, error) {
	filesystemFD, err := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	relative := strings.TrimPrefix(root, "/")
	if relative == "" {
		relative = "."
	}
	fd, err := openat2(filesystemFD, relative, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0, resolveBeneath)
	_ = unix.Close(filesystemFD)
	return fd, err
}

func revalidateEntry(parentFD int, name string, expected fileIdentity) error {
	var stat unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return ErrTargetChanged
	}
	if !sameIdentity(identityOf(&stat), expected) {
		return ErrTargetChanged
	}
	return nil
}

func entryExists(parentFD int, name string) bool {
	var stat unix.Stat_t
	return unix.Fstatat(parentFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW) == nil
}
