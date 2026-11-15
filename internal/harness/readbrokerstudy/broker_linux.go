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
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

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

type preparedDirectoryList struct {
	mu             sync.Mutex
	directory      *os.File
	registry       PolicyRegistry
	path           string
	identity       FileIdentity
	class          ResourceClass
	generation     GenerationSet
	digest         [32]byte
	closed         bool
	closeAttempted bool
	closeErr       error
	ignoreRules    []ignoreRule
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
		digest: requestDigest(root, target, id, class, generation, AdmissionReadFile),
	}, nil
}

func (b *broker) PrepareDirectoryList(root, target string, generation GenerationSet) (PreparedRead, error) {
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
	routeClass, routeProtected := b.registry.ClassifyRoute(target)
	if latest := b.registry.View(); latest.Generation != view.Generation || latest.Generation != generation.Policy {
		return nil, ErrPolicyChanged
	}
	if routeProtected && routeClass != ClassProvider {
		return nil, ErrProtected
	}
	fd, err := openAbsolute(target, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NONBLOCK)
	if err != nil {
		return nil, mapPrepareError(err)
	}
	directory := os.NewFile(uintptr(fd), "prepared-directory-list-capability")
	id, err := identityFromFile(directory)
	if err != nil || id.Type != unix.S_IFDIR {
		_ = directory.Close()
		if err != nil {
			return nil, err
		}
		return nil, errReadNotRegular
	}
	class, protected := b.registry.ClassifyWithAncestors(target, id, nil)
	if latest := b.registry.View(); latest.Generation != view.Generation || latest.Generation != generation.Policy {
		_ = directory.Close()
		return nil, ErrPolicyChanged
	}
	if protected && class != ClassProvider {
		_ = directory.Close()
		return nil, ErrProtected
	}
	if !protected {
		class = ClassOrdinary
	}
	if b.registry.DirectoryCoverageIncomplete(id) {
		_ = directory.Close()
		return nil, ErrDirectoryCoverage
	}
	ignoreRules, err := readDirectoryIgnoreRules(directory, target, id, b.registry)
	if err != nil {
		_ = directory.Close()
		return nil, err
	}
	return &preparedDirectoryList{
		directory: directory, registry: b.registry, ignoreRules: ignoreRules, path: target, identity: id, class: class, generation: generation,
		digest: requestDigest(root, target, id, class, generation, AdmissionListDirectory),
	}, nil
}

type ignoreRule struct {
	pattern       string
	base          string
	negated       bool
	directoryOnly bool
	anchored      bool
}

const maxIgnoreBytes = 64 << 10
const maxIgnoreAncestors = 64
const maxIgnoreRules = 512

type ancestorFD struct {
	fd   int
	path string
}

func readDirectoryIgnoreRules(directory *os.File, target string, directoryIdentity FileIdentity, registry PolicyRegistry) ([]ignoreRule, error) {
	// Walk from the selected directory toward / using its pinned descriptor.
	// The bounded walk captures every applicable ancestor .gitignore without
	// resolving an attacker-controlled replacement pathname.
	ancestors := make([]ancestorFD, 0, maxIgnoreAncestors)
	currentFD, err := unix.Dup(int(directory.Fd()))
	if err != nil {
		return nil, ErrProtected
	}
	currentPath := target
	for depth := 0; depth < maxIgnoreAncestors; depth++ {
		ancestors = append(ancestors, ancestorFD{fd: currentFD, path: currentPath})
		parentFD, openErr := unix.Openat(currentFD, "..", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if openErr != nil {
			return nil, closeAncestors(ancestors, ErrProtected)
		}
		var currentStat, parentStat unix.Stat_t
		if unix.Fstat(currentFD, &currentStat) != nil || unix.Fstat(parentFD, &parentStat) != nil {
			_ = unix.Close(parentFD)
			return nil, closeAncestors(ancestors, ErrProtected)
		}
		if currentStat.Ino == parentStat.Ino && currentStat.Dev == parentStat.Dev {
			_ = unix.Close(parentFD)
			break
		}
		if depth == maxIgnoreAncestors-1 {
			_ = unix.Close(parentFD)
			return nil, closeAncestors(ancestors, ErrProtected)
		}
		currentFD = parentFD
		currentPath = filepath.Dir(currentPath)
	}
	defer func() {
		for _, item := range ancestors {
			_ = unix.Close(item.fd)
		}
	}()
	var rules []ignoreRule
	totalBytes := 0
	for i := len(ancestors) - 1; i >= 0; i-- {
		item := ancestors[i]
		fd, openErr := unix.Openat(item.fd, ".gitignore", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
		if openErr == unix.ENOENT {
			continue
		}
		if openErr != nil {
			return nil, ErrProtected
		}
		var stat unix.Stat_t
		if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Size < 0 || totalBytes+int(stat.Size) > maxIgnoreBytes {
			_ = unix.Close(fd)
			return nil, ErrProtected
		}
		identity := identityFromStat(&stat)
		ignorePath := filepath.Join(item.path, ".gitignore")
		if _, protected := registry.ClassifyWithAncestors(ignorePath, identity, nil); protected {
			_ = unix.Close(fd)
			return nil, ErrProtected
		}
		if _, protected := registry.ClassifyRoute(ignorePath); protected {
			_ = unix.Close(fd)
			return nil, ErrProtected
		}
		file := os.NewFile(uintptr(fd), "bounded-directory-ignore-rules")
		data, readErr := io.ReadAll(io.LimitReader(file, int64(maxIgnoreBytes-totalBytes+1)))
		_ = file.Close()
		if readErr != nil || len(data) > maxIgnoreBytes-totalBytes || !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
			return nil, ErrProtected
		}
		totalBytes += len(data)
		parsed, parseErr := parseIgnoreRules(data, item.path)
		if parseErr != nil || len(rules)+len(parsed) > maxIgnoreRules {
			return nil, ErrProtected
		}
		rules = append(rules, parsed...)
	}
	if len(ancestors) > 0 {
		var selectedStat unix.Stat_t
		if unix.Fstat(ancestors[0].fd, &selectedStat) != nil || identityFromStat(&selectedStat) != directoryIdentity {
			return nil, ErrStaleReceipt
		}
	}
	return rules, nil
}

func parseIgnoreRules(data []byte, base string) ([]ignoreRule, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 1024), 2048)
	rules := make([]ignoreRule, 0, 32)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.ContainsRune(line, '\\') || strings.Contains(line, "**") {
			return nil, ErrProtected
		}
		// Git ignores unescaped trailing spaces. Preserve leading spaces, which
		// are part of the pattern and can name real filesystem entries.
		line = strings.TrimRight(line, " ")
		if line == "" {
			continue
		}
		rule := ignoreRule{}
		if strings.HasPrefix(line, "!") {
			rule.negated = true
			line = line[1:]
		}
		if line == "" {
			return nil, ErrProtected
		}
		if strings.HasSuffix(line, "/") {
			rule.directoryOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		if strings.HasPrefix(line, "/") {
			rule.anchored = true
			line = strings.TrimPrefix(line, "/")
		}
		if line == "" {
			return nil, ErrProtected
		}
		if _, err := path.Match(line, "validation"); err != nil {
			return nil, ErrProtected
		}
		rule.pattern, rule.base = line, base
		rule.anchored = rule.anchored || strings.Contains(line, "/")
		rules = append(rules, rule)
	}
	if scanner.Err() != nil {
		return nil, ErrProtected
	}
	return rules, nil
}

func closeAncestors(ancestors []ancestorFD, err error) error {
	for _, item := range ancestors {
		_ = unix.Close(item.fd)
	}
	return err
}

func (rule ignoreRule) matches(namePath string, directory bool) bool {
	if rule.directoryOnly && !directory {
		return false
	}
	relative, err := filepath.Rel(rule.base, namePath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	name := filepath.ToSlash(relative)
	if rule.anchored {
		matched, _ := path.Match(rule.pattern, name)
		return matched
	}
	for _, component := range strings.Split(name, "/") {
		matched, _ := path.Match(rule.pattern, component)
		if matched {
			return true
		}
	}
	return false
}

func ignoredByRules(namePath string, directory bool, rules []ignoreRule) bool {
	if len(rules) == 0 {
		return false
	}
	base := rules[0].base
	relative, err := filepath.Rel(base, namePath)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	components := strings.Split(filepath.ToSlash(relative), "/")
	prefix := base
	ignored := false
	for i, component := range components {
		prefix = filepath.Join(prefix, filepath.FromSlash(component))
		isDirectory := i < len(components)-1 || directory
		parentIgnored := ignored
		for _, rule := range rules {
			if !rule.matches(prefix, isDirectory) {
				continue
			}
			if rule.negated {
				// Git cannot re-include a child while one of its parent
				// directories remains excluded.
				if !parentIgnored {
					ignored = false
				}
			} else {
				ignored = true
			}
		}
	}
	return ignored
}

func (p *preparedDirectoryList) filterListing(raw []byte) ([]byte, error) {
	var listing directoryListingResult
	if err := json.Unmarshal(raw, &listing); err != nil || listing.Examined > 10_000 || len(listing.Entries) > 1_000 || len(listing.Names) != 0 {
		return nil, ErrProtocol
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.directory == nil || p.registry == nil {
		return nil, os.ErrClosed
	}
	var parentStat unix.Stat_t
	if err := unix.Fstat(int(p.directory.Fd()), &parentStat); err != nil || identityFromStat(&parentStat) != p.identity {
		return nil, ErrStaleReceipt
	}
	currentRules, err := readDirectoryIgnoreRules(p.directory, p.path, p.identity, p.registry)
	if err != nil || !slices.Equal(currentRules, p.ignoreRules) {
		return nil, ErrPolicyChanged
	}
	mtimeNS := parentStat.Mtim.Sec*1e9 + parentStat.Mtim.Nsec
	ctimeNS := parentStat.Ctim.Sec*1e9 + parentStat.Ctim.Nsec
	if mtimeNS != listing.MtimeNS || ctimeNS != listing.CtimeNS {
		return nil, ErrStaleReceipt
	}
	filtered := make([]string, 0, len(listing.Entries))
	parentMountID, err := mountIDForFD(int(p.directory.Fd()))
	if err != nil {
		return nil, ErrUnavailable
	}
	for _, entry := range listing.Entries {
		name := entry.Name
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
			return nil, ErrProtocol
		}
		namePath := filepath.Join(p.path, name)
		mode, ok := modeForDirentType(entry.Type)
		if !ok || entry.Inode == 0 {
			return nil, ErrUnavailable
		}
		identity, mounted, err := childIdentityAt(int(p.directory.Fd()), name, parentMountID)
		if err != nil {
			return nil, err
		}
		if !mounted && (identity.Device != p.identity.Device || identity.Inode != entry.Inode || identity.Type != mode) {
			return nil, ErrStaleReceipt
		}
		if _, protected := p.registry.ClassifyRoute(namePath); protected {
			continue
		}
		if _, protected := p.registry.ClassifyWithAncestors(namePath, identity, nil); protected {
			continue
		}
		if ignoredByRules(namePath, identity.Type == unix.S_IFDIR, p.ignoreRules) {
			continue
		}
		filtered = append(filtered, name)
	}
	listing.Names = filtered
	listing.Entries = nil
	listing.Returned = len(filtered)
	// Raw traversal count includes hidden and protected entries. It is needed
	// only for enforcing the worker bound and must not be disclosed.
	listing.Examined = len(filtered)
	listing.MtimeNS = 0
	listing.CtimeNS = 0
	encoded, err := json.Marshal(listing)
	if err != nil || len(encoded) > MaxReadBytes {
		return nil, ErrResultTooLarge
	}
	return encoded, nil
}

func childIdentityAt(parentFD int, name string, parentMountID uint64) (FileIdentity, bool, error) {
	fd, err := unix.Openat(parentFD, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return FileIdentity{}, false, ErrStaleReceipt
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return FileIdentity{}, false, ErrStaleReceipt
	}
	mountID, err := mountIDForFD(fd)
	if err != nil {
		return FileIdentity{}, false, err
	}
	return identityFromStat(&stat), mountID != parentMountID, nil
}

func modeForDirentType(fileType uint8) (uint32, bool) {
	switch fileType {
	case unix.DT_REG:
		return unix.S_IFREG, true
	case unix.DT_DIR:
		return unix.S_IFDIR, true
	case unix.DT_LNK:
		return unix.S_IFLNK, true
	case unix.DT_FIFO:
		return unix.S_IFIFO, true
	case unix.DT_SOCK:
		return unix.S_IFSOCK, true
	case unix.DT_CHR:
		return unix.S_IFCHR, true
	case unix.DT_BLK:
		return unix.S_IFBLK, true
	default:
		return 0, false
	}
}

func direntTypeForMode(mode uint32) uint8 {
	switch mode & unix.S_IFMT {
	case unix.S_IFREG:
		return unix.DT_REG
	case unix.S_IFDIR:
		return unix.DT_DIR
	case unix.S_IFLNK:
		return unix.DT_LNK
	case unix.S_IFIFO:
		return unix.DT_FIFO
	case unix.S_IFSOCK:
		return unix.DT_SOCK
	case unix.S_IFCHR:
		return unix.DT_CHR
	case unix.S_IFBLK:
		return unix.DT_BLK
	default:
		return unix.DT_UNKNOWN
	}
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
	switch prepared := capability.(type) {
	case *preparedRead:
		return b.revalidateRead(prepared)
	case *preparedDirectoryList:
		return b.revalidateDirectoryList(prepared)
	default:
		return ErrStaleReceipt
	}
}

func (b *broker) revalidateRead(prepared *preparedRead) error {
	if prepared == nil {
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

func (b *broker) revalidateDirectoryList(prepared *preparedDirectoryList) error {
	if prepared == nil {
		return ErrStaleReceipt
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	if prepared.closed || prepared.directory == nil {
		return ErrStaleReceipt
	}
	view := b.registry.View()
	if view.Generation != prepared.generation.Policy {
		return ErrPolicyChanged
	}
	identity, err := identityFromFile(prepared.directory)
	if err != nil || identity != prepared.identity || identity.Type != unix.S_IFDIR {
		return ErrStaleReceipt
	}
	class, protected := b.registry.ClassifyWithAncestors(prepared.path, identity, nil)
	if protected && class != prepared.class {
		return ErrProtected
	}
	if b.registry.DirectoryCoverageIncomplete(identity) {
		return ErrDirectoryCoverage
	}
	if latest := b.registry.View(); latest.Generation != prepared.generation.Policy {
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

func (p *preparedDirectoryList) Identity() FileIdentity     { return p.identity }
func (p *preparedDirectoryList) Class() ResourceClass       { return p.class }
func (p *preparedDirectoryList) Path() string               { return p.path }
func (p *preparedDirectoryList) Generations() GenerationSet { return p.generation }
func (p *preparedDirectoryList) RequestDigest() [32]byte    { return p.digest }

func (p *preparedDirectoryList) DuplicateForWorker() (*os.File, error) {
	if p == nil {
		return nil, os.ErrInvalid
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.directory == nil {
		return nil, os.ErrClosed
	}
	fd, err := unix.FcntlInt(p.directory.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "directory-list-capability-for-worker"), nil
}

func (p *preparedDirectoryList) Close() error {
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
	if p.directory != nil {
		p.closeErr = p.directory.Close()
	}
	return p.closeErr
}

func requestDigest(root, target string, id FileIdentity, class ResourceClass, generation GenerationSet, operation AdmissionOperation) [32]byte {
	hash := sha256.New()
	_, _ = hash.Write([]byte(root))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(target))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(class))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(operation))
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
