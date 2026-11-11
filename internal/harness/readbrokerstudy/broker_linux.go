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
	errInvalidReadPath      = errors.New("read broker requires clean absolute paths")
	errReadNotRegular       = errors.New("read target is not a regular file")
	errSymlinkLookupRefused = errors.New("read broker refused a symlink lookup")
	errMountBoundaryRefused = errors.New("read broker refused a mount-boundary lookup")
)

type broker struct {
	registry PolicyRegistry
}

type preparedRead struct {
	mu             sync.Mutex
	file           *os.File
	parent         *os.File
	path           string
	identity       FileIdentity
	class          ResourceClass
	generation     GenerationSet
	digest         [32]byte
	closed         bool
	closeAttempted bool
	closeErr       error
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
	view := b.registry.View()
	if generation.Policy != view.Generation {
		return nil, ErrPolicyChanged
	}
	// Check the requested pathname before opening it. Location policy must
	// reject reserved protected leaves even when the entry is absent or the
	// current replacement cannot be opened. Provider-source locations retain
	// their separate explicit-approval path.
	routeClass, routeProtected := b.registry.ClassifyRoute(target)
	if latest := b.registry.View(); latest.Generation != view.Generation || latest.Generation != generation.Policy {
		return nil, ErrPolicyChanged
	}
	if routeProtected && routeClass != ClassProvider {
		return nil, ErrProtected
	}
	rootFD, err := openAbsolute(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC)
	if err != nil {
		return nil, mapPrepareError(err)
	}
	defer unix.Close(rootFD)
	targetParentPath := filepath.Dir(target)
	targetMountRoot, targetMountRootPath, err := openTargetMountRoot(targetParentPath)
	if err != nil {
		return nil, mapPrepareError(err)
	}
	defer targetMountRoot.Close()
	parentRelative, err := filepath.Rel(targetMountRootPath, targetParentPath)
	if err != nil || parentRelative == ".." || strings.HasPrefix(parentRelative, ".."+string(filepath.Separator)) || !filepath.IsLocal(parentRelative) {
		return nil, ErrOutOfScope
	}
	if parentRelative == "" {
		parentRelative = "."
	}
	parentFD, err := unix.Openat2(int(targetMountRoot.Fd()), parentRelative, &unix.OpenHow{
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
	fileFD, err := unix.Openat2(parentFD, filepath.Base(target), &unix.OpenHow{
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
	parent := os.NewFile(uintptr(parentFD), "prepared-read-parent")
	parentOwned = true
	return &preparedRead{
		file: file, parent: parent, path: target, identity: id, class: class, generation: generation,
		digest: requestDigest(root, target, id, class, generation),
	}, nil
}

func mountIDForFD(fd int) (uint64, error) {
	var stat unix.Statx_t
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_STATX_SYNC_AS_STAT, unix.STATX_MNT_ID, &stat); err != nil {
		return 0, err
	}
	if stat.Mask&unix.STATX_MNT_ID == 0 || stat.Mnt_id == 0 {
		return 0, ErrUnavailable
	}
	return stat.Mnt_id, nil
}

func openTargetMountRoot(targetParent string) (*os.File, string, error) {
	currentFD, err := openAbsolute(targetParent, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC)
	if err != nil {
		return nil, "", err
	}
	currentMountID, err := mountIDForFD(currentFD)
	if err != nil {
		_ = unix.Close(currentFD)
		return nil, "", fmt.Errorf("inspect target mount: %w", ErrUnavailable)
	}
	currentPath := targetParent
	for depth := 0; depth < 256; depth++ {
		parentFD, err := unix.Openat(currentFD, "..", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			_ = unix.Close(currentFD)
			return nil, "", err
		}
		parentMountID, err := mountIDForFD(parentFD)
		if err != nil {
			_ = unix.Close(parentFD)
			_ = unix.Close(currentFD)
			return nil, "", fmt.Errorf("inspect target mount ancestry: %w", ErrUnavailable)
		}
		var currentStat, parentStat unix.Stat_t
		if err := unix.Fstat(currentFD, &currentStat); err != nil {
			_ = unix.Close(parentFD)
			_ = unix.Close(currentFD)
			return nil, "", err
		}
		if err := unix.Fstat(parentFD, &parentStat); err != nil {
			_ = unix.Close(parentFD)
			_ = unix.Close(currentFD)
			return nil, "", err
		}
		if parentMountID != currentMountID || identityFromStat(&currentStat) == identityFromStat(&parentStat) {
			_ = unix.Close(parentFD)
			return os.NewFile(uintptr(currentFD), "target-mount-root"), currentPath, nil
		}
		_ = unix.Close(currentFD)
		currentFD = parentFD
		currentPath = filepath.Dir(currentPath)
	}
	_ = unix.Close(currentFD)
	return nil, "", errors.New("target mount ancestry exceeded its bound")
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
	identity, err := identityFromFile(prepared.file)
	if err != nil || identity != prepared.identity || identity.Type != unix.S_IFREG {
		return ErrStaleReceipt
	}
	if b.registry.DirectoryCoverageIncomplete(prepared.identity) {
		return ErrDirectoryCoverage
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
	if p.closeAttempted {
		return p.closeErr
	}
	p.closeAttempted = true
	p.closed = true
	var errs []error
	if p.file != nil {
		if err := p.file.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if p.parent != nil {
		if err := p.parent.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	p.closeErr = errors.Join(errs...)
	return p.closeErr
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
	case errors.Is(err, unix.ELOOP):
		return fmt.Errorf("%w: %w", errSymlinkLookupRefused, err)
	case errors.Is(err, unix.EXDEV):
		return fmt.Errorf("%w: %w", errMountBoundaryRefused, err)
	case errors.Is(err, unix.ENOENT), errors.Is(err, unix.ENOTDIR):
		return fmt.Errorf("read target refused: %w", err)
	default:
		return err
	}
}
