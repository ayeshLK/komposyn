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
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

var (
	errInvalidReadPath = errors.New("read broker requires clean absolute paths")
	errReadOutOfScope  = errors.New("read target is outside the selected root")
	errReadNotRegular  = errors.New("read target is not a regular file")
)

type broker struct {
	registry PolicyRegistry
}

type preparedRead struct {
	mu         sync.Mutex
	file       *os.File
	parent     *os.File
	path       string
	identity   FileIdentity
	class      ResourceClass
	generation GenerationSet
	digest     [32]byte
	closed     bool
}

var _ PreparedRead = (*preparedRead)(nil)
var _ ReadBroker = (*broker)(nil)

func NewBroker(registry PolicyRegistry) ReadBroker {
	return &broker{registry: registry}
}

func (b *broker) PrepareRead(root, target string, generation GenerationSet) (PreparedRead, error) {
	if b == nil || b.registry == nil {
		return nil, ErrUnavailable
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || !filepath.IsAbs(target) || filepath.Clean(target) != target || strings.ContainsRune(target, '\x00') {
		return nil, errInvalidReadPath
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || !filepath.IsLocal(relative) {
		return nil, errReadOutOfScope
	}
	view := b.registry.View()
	if generation.Policy != view.Generation {
		return nil, ErrPolicyChanged
	}
	rootFD, err := openAbsolute(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC)
	if err != nil {
		return nil, mapPrepareError(err)
	}
	defer unix.Close(rootFD)
	parentRelative, leaf := filepath.Split(relative)
	parentRelative = strings.TrimSuffix(parentRelative, string(filepath.Separator))
	if parentRelative == "" {
		parentRelative = "."
	}
	parentFD, err := unix.Openat2(rootFD, parentRelative, &unix.OpenHow{
		Flags:   unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV,
	})
	if err != nil {
		return nil, mapPrepareError(err)
	}
	parentOwned := false
	defer func() {
		if !parentOwned {
			_ = unix.Close(parentFD)
		}
	}()
	fileFD, err := unix.Openat2(parentFD, leaf, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV,
	})
	if err != nil {
		return nil, mapPrepareError(err)
	}
	file := os.NewFile(uintptr(fileFD), "prepared-read-capability")
	id, err := identityFromFile(file)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if id.Type != unix.S_IFREG {
		_ = file.Close()
		return nil, errReadNotRegular
	}
	ancestors, err := directoryAncestors(parentFD)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	class, protected := b.registry.ClassifyWithAncestors(target, id, ancestors)
	if latest := b.registry.View(); latest.Generation != view.Generation || latest.Generation != generation.Policy {
		_ = file.Close()
		return nil, ErrPolicyChanged
	}
	if protected && class != ClassProvider {
		_ = file.Close()
		return nil, ErrProtected
	}
	if !protected {
		class = ClassOrdinary
	}
	if b.registry.DirectoryCoverageIncomplete(id) {
		_ = file.Close()
		return nil, ErrDirectoryCoverage
	}
	registeredClass, registered, err := b.registry.InspectRegisteredLocations(id)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("protected-location checkpoint failed closed: %w", err)
	}
	if registered && registeredClass != class {
		_ = file.Close()
		if registeredClass != ClassProvider {
			return nil, ErrProtected
		}
		return nil, ErrPolicyChanged
	}
	parent := os.NewFile(uintptr(parentFD), "prepared-read-parent")
	parentOwned = true
	return &preparedRead{
		file: file, parent: parent, path: target, identity: id, class: class, generation: generation,
		digest: requestDigest(root, target, id, class, generation),
	}, nil
}

func (b *broker) Revalidate(capability PreparedRead) error {
	if b == nil || b.registry == nil {
		return ErrUnavailable
	}
	prepared, ok := capability.(*preparedRead)
	if !ok || prepared == nil {
		return ErrStaleReceipt
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	if prepared.closed || prepared.file == nil || prepared.parent == nil {
		return ErrStaleReceipt
	}
	view := b.registry.View()
	if view.Generation != prepared.generation.Policy {
		return ErrPolicyChanged
	}
	ancestors, err := directoryAncestors(int(prepared.parent.Fd()))
	if err != nil {
		return fmt.Errorf("protected ancestor checkpoint failed closed: %w", err)
	}
	class, protected := b.registry.ClassifyWithAncestors(prepared.path, prepared.identity, ancestors)
	if !protected {
		class = ClassOrdinary
	}
	if b.registry.DirectoryCoverageIncomplete(prepared.identity) {
		return ErrDirectoryCoverage
	}
	liveClass, liveProtected, err := b.registry.InspectRegisteredLocations(prepared.identity)
	if err != nil {
		return fmt.Errorf("protected-location checkpoint failed closed: %w", err)
	}
	if liveProtected {
		class = stricterClass(class, liveClass)
		protected = true
	}
	if current := b.registry.View(); current.Generation != view.Generation {
		return ErrPolicyChanged
	}
	if protected && class != prepared.class {
		if class != ClassProvider {
			return ErrProtected
		}
		return ErrPolicyChanged
	}
	return nil
}

func directoryAncestors(startFD int) ([]FileIdentity, error) {
	current, err := unix.FcntlInt(uintptr(startFD), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(current) }()
	seen := make(map[FileIdentity]struct{})
	ancestors := make([]FileIdentity, 0, 16)
	for depth := 0; depth < 256; depth++ {
		var stat unix.Stat_t
		if err := unix.Fstat(current, &stat); err != nil {
			return nil, err
		}
		identity := identityFromStat(&stat)
		if _, ok := seen[identity]; ok {
			return nil, errors.New("directory ancestry contains a cycle")
		}
		seen[identity] = struct{}{}
		ancestors = append(ancestors, identity)
		parent, err := unix.Openat(current, "..", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return nil, err
		}
		var parentStat unix.Stat_t
		if err := unix.Fstat(parent, &parentStat); err != nil {
			_ = unix.Close(parent)
			return nil, err
		}
		if identityFromStat(&parentStat) == identity {
			_ = unix.Close(parent)
			return ancestors, nil
		}
		_ = unix.Close(current)
		current = parent
	}
	return nil, errors.New("directory ancestry exceeded its bound")
}

func (p *preparedRead) Identity() FileIdentity { return p.identity }
func (p *preparedRead) Class() ResourceClass   { return p.class }
func (p *preparedRead) Path() string           { return p.path }
func (p *preparedRead) Generations() GenerationSet {
	return p.generation
}
func (p *preparedRead) RequestDigest() [32]byte { return p.digest }

func (p *preparedRead) DuplicateForWorker() (*os.File, error) {
	if p == nil {
		return nil, os.ErrInvalid
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.file == nil {
		return nil, os.ErrClosed
	}
	fd, err := unix.FcntlInt(p.file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "read-capability-for-worker"), nil
}

func (p *preparedRead) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	var errs []error
	if p.file != nil {
		if err := p.file.Close(); err != nil {
			errs = append(errs, err)
		}
		p.file = nil
	}
	if p.parent != nil {
		if err := p.parent.Close(); err != nil {
			errs = append(errs, err)
		}
		p.parent = nil
	}
	return errors.Join(errs...)
}

func requestDigest(root, target string, id FileIdentity, class ResourceClass, generation GenerationSet) [32]byte {
	hash := sha256.New()
	_, _ = hash.Write([]byte(root))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(target))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(class))
	var encoded [8]byte
	for _, value := range []uint64{id.Device, id.Inode, uint64(id.Type), generation.Root, generation.Policy, generation.Grant} {
		binary.LittleEndian.PutUint64(encoded[:], value)
		_, _ = hash.Write(encoded[:])
	}
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}

func ReceiptMatches(prepared PreparedRead, receipt AuthorizationReceipt) bool {
	if prepared == nil || receipt.InvocationID == "" || !receipt.OnceConsumed {
		return false
	}
	return prepared.Identity() == receipt.Identity && prepared.Class() == receipt.Class &&
		prepared.Generations() == receipt.Generations && prepared.RequestDigest() == receipt.RequestDigest
}

func mapPrepareError(err error) error {
	switch {
	case errors.Is(err, unix.EXDEV), errors.Is(err, unix.ELOOP), errors.Is(err, unix.ENOENT), errors.Is(err, unix.ENOTDIR):
		return fmt.Errorf("read target refused: %w", err)
	default:
		return err
	}
}
