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
	"context"
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
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

var (
	errInvalidReadPath       = errors.New("read broker requires clean absolute paths")
	errInvalidListingOptions = errors.New("directory listing options are invalid or unsupported")
	errReadNotRegular        = errors.New("read target is not a regular file")
	errSymlinkLookupRefused  = errors.New("read broker refused a symlink lookup")
	errMountBoundaryRefused  = errors.New("read broker refused a mount-boundary lookup")
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
	options        ListingOptions
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
	return b.PrepareDirectoryListWithOptions(root, target, generation, ListingOptions{})
}

func (b *broker) PrepareDirectoryListWithOptions(root, target string, generation GenerationSet, options ListingOptions) (PreparedRead, error) {
	if b == nil || b.registry == nil {
		return nil, ErrUnavailable
	}
	if err := validateListingOptions(options); err != nil {
		return nil, err
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
		digest: requestDigest(root, target, id, class, generation, AdmissionListDirectory, options), options: options.Clone(),
	}, nil
}

func validateListingOptions(options ListingOptions) error {
	if len(options.NameGlobs) > 32 {
		return errInvalidListingOptions
	}
	for _, pattern := range options.NameGlobs {
		if pattern == "" || len(pattern) > 128 || strings.ContainsAny(pattern, "/\\\x00") || strings.Contains(pattern, "**") || strings.Contains(pattern, `\\`) {
			return errInvalidListingOptions
		}
		if _, err := path.Match(strings.ReplaceAll(pattern, "[!", "[^"), ""); err != nil {
			return errInvalidListingOptions
		}
	}
	return nil
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
const maxDirectoryFilterWork = 5_000_000
const maxDirectoryFilterDuration = 5 * time.Second

var errDirectoryFilterBudget = errors.New("directory listing filter work budget exceeded")

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
		// POSIX character classes, collating symbols, and equivalence classes
		// are outside this bounded matcher, so refuse those patterns rather
		// than risk exposing names under different matching semantics.
		if strings.Contains(line, "[[") || strings.Contains(line, "[:") || strings.Contains(line, "[.") || strings.Contains(line, "[=") {
			return nil, ErrProtected
		}
		if _, err := path.Match(strings.ReplaceAll(line, "[!", "[^"), "validation"); err != nil {
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

func (rule ignoreRule) matches(namePath string, directory bool, budget *directoryFilterBudget) (bool, error) {
	if rule.directoryOnly && !directory {
		return false, nil
	}
	relative, err := filepath.Rel(rule.base, namePath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false, nil
	}
	name := filepath.ToSlash(relative)
	if name == "." {
		// An ignore file does not apply its patterns to the directory that
		// contains it, only to entries below that directory.
		return false, nil
	}
	if rule.anchored {
		return gitByteMatch(budget, rule.pattern, name)
	}
	// ignoredByRules evaluates each path prefix separately. Match this
	// prefix's own name, never an ancestor component of a descendant.
	component := name[strings.LastIndex(name, "/")+1:]
	return gitByteMatch(budget, rule.pattern, component)
}

type directoryFilterBudget struct {
	ctx       context.Context
	remaining int
}

func (budget *directoryFilterBudget) consume(work int) error {
	if err := budget.ctx.Err(); err != nil {
		return err
	}
	if work < 0 || work > budget.remaining {
		return errDirectoryFilterBudget
	}
	budget.remaining -= work
	return nil
}

// gitByteMatch uses a two-row dynamic program with O(pattern bytes × name
// bytes) work. Git's ?, classes, and wildcard semantics operate on filename
// bytes, so UTF-8 characters may consume multiple wildcard positions. '**'
// and backslash escapes are rejected by the parser.
func gitByteMatch(budget *directoryFilterBudget, pattern, name string) (bool, error) {
	work := (len(pattern) + 1) * (len(name) + 1)
	if err := budget.consume(work); err != nil {
		return false, err
	}
	previous := make([]bool, len(name)+1)
	current := make([]bool, len(name)+1)
	previous[0] = true
	for patternOffset := 0; patternOffset < len(pattern); {
		if patternOffset&31 == 0 {
			if err := budget.ctx.Err(); err != nil {
				return false, err
			}
		}
		clear(current)
		if pattern[patternOffset] == '*' {
			current[0] = previous[0]
			for nameOffset := 1; nameOffset <= len(name); nameOffset++ {
				if nameOffset&255 == 0 {
					if err := budget.ctx.Err(); err != nil {
						return false, err
					}
				}
				// The star may consume zero bytes even when the next name byte
				// is '/'; only consuming that slash is forbidden.
				current[nameOffset] = previous[nameOffset] || (name[nameOffset-1] != '/' && current[nameOffset-1])
			}
			patternOffset++
		} else if pattern[patternOffset] == '?' {
			for nameOffset := 1; nameOffset <= len(name); nameOffset++ {
				if nameOffset&255 == 0 {
					if err := budget.ctx.Err(); err != nil {
						return false, err
					}
				}
				current[nameOffset] = name[nameOffset-1] != '/' && previous[nameOffset-1]
			}
			patternOffset++
		} else if pattern[patternOffset] == '[' {
			classEnd := patternOffset + 1 + strings.IndexByte(pattern[patternOffset+1:], ']')
			class := pattern[patternOffset+1 : classEnd]
			for nameOffset := 1; nameOffset <= len(name); nameOffset++ {
				if nameOffset&255 == 0 {
					if err := budget.ctx.Err(); err != nil {
						return false, err
					}
				}
				current[nameOffset] = name[nameOffset-1] != '/' && previous[nameOffset-1] && gitByteClassMatch(class, name[nameOffset-1])
			}
			patternOffset = classEnd + 1
		} else {
			for nameOffset := 1; nameOffset <= len(name); nameOffset++ {
				if nameOffset&255 == 0 {
					if err := budget.ctx.Err(); err != nil {
						return false, err
					}
				}
				current[nameOffset] = pattern[patternOffset] == name[nameOffset-1] && previous[nameOffset-1]
			}
			patternOffset++
		}
		previous, current = current, previous
	}
	return previous[len(name)], nil
}

func gitByteClassMatch(class string, value byte) bool {
	negated := strings.HasPrefix(class, "!") || strings.HasPrefix(class, "^")
	if negated {
		class = class[1:]
	}
	contains := false
	for i := 0; i < len(class); i++ {
		if i+2 < len(class) && class[i+1] == '-' {
			if value >= class[i] && value <= class[i+2] {
				contains = true
			}
			i += 2
			continue
		}
		if class[i] == value {
			contains = true
		}
	}
	if negated {
		return !contains
	}
	return contains
}

func ignoredByRules(namePath string, directory bool, rules []ignoreRule, budget *directoryFilterBudget) (bool, error) {
	if len(rules) == 0 {
		return false, nil
	}
	base := rules[0].base
	relative, err := filepath.Rel(base, namePath)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false, nil
	}
	components := strings.Split(filepath.ToSlash(relative), "/")
	prefix := base
	ignored := false
	for i, component := range components {
		prefix = filepath.Join(prefix, filepath.FromSlash(component))
		isDirectory := i < len(components)-1 || directory
		parentIgnored := ignored
		for _, rule := range rules {
			if err := budget.consume(1); err != nil {
				return false, err
			}
			matches, err := rule.matches(prefix, isDirectory, budget)
			if err != nil {
				return false, err
			}
			if !matches {
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
	return ignored, nil
}

func (p *preparedDirectoryList) filterListing(ctx context.Context, raw []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, maxDirectoryFilterDuration)
	defer cancel()
	budget := &directoryFilterBudget{ctx: ctx, remaining: maxDirectoryFilterWork}
	var listing directoryListingResult
	if err := json.Unmarshal(raw, &listing); err != nil || listing.Examined > 10_000 || listing.Returned > 10_000 || len(listing.Entries) > 10_000 || len(listing.Directories) > 10_000 || len(listing.Names) != 0 {
		return nil, ErrProtocol
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.directory == nil || p.registry == nil {
		return nil, os.ErrClosed
	}
	if err := budget.consume(len(raw)); err != nil {
		return nil, err
	}
	var parentStat unix.Stat_t
	if err := unix.Fstat(int(p.directory.Fd()), &parentStat); err != nil || identityFromStat(&parentStat) != p.identity {
		return nil, ErrStaleReceipt
	}
	mtimeNS := parentStat.Mtim.Sec*1e9 + parentStat.Mtim.Nsec
	ctimeNS := parentStat.Ctim.Sec*1e9 + parentStat.Ctim.Nsec
	if mtimeNS != listing.MtimeNS || ctimeNS != listing.CtimeNS {
		return nil, ErrStaleReceipt
	}
	rootFD := int(p.directory.Fd())
	parentMountID, err := mountIDForFD(rootFD)
	if err != nil {
		return nil, ErrUnavailable
	}
	if listing.Directories == nil {
		if p.options.Recursive {
			return nil, ErrProtocol
		}
		state := directoryListingDirectory{Device: uint64(parentStat.Dev), Inode: parentStat.Ino, MountID: parentMountID, MtimeNS: mtimeNS, CtimeNS: ctimeNS}
		if err := snapshotListingIgnoreEntry(rootFD, &state); err != nil {
			return nil, err
		}
		listing.Directories = []directoryListingDirectory{state}
	}
	blockedDirectories, err := p.revalidateListingDirectories(listing.Directories, budget)
	if err != nil {
		return nil, err
	}
	allRules := append([]ignoreRule(nil), p.ignoreRules...)
	if !p.options.IncludeIgnored {
		currentRules, readErr := readDirectoryIgnoreRules(p.directory, p.path, p.identity, p.registry)
		if readErr != nil || !slices.Equal(currentRules, p.ignoreRules) {
			return nil, ErrPolicyChanged
		}
		allRules, err = p.loadNestedListingRules(listing.Directories, blockedDirectories, budget)
		if err != nil {
			return nil, err
		}
	}
	filtered := make([]string, 0, min(len(listing.Entries), 1_000))
	blockedPrefixes := make(map[string]bool)
	for _, entry := range listing.Entries {
		if err := budget.consume(1); err != nil {
			return nil, err
		}
		name := entry.Name
		relative := entry.Path
		if relative == "" {
			relative = name
		}
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") || !validListingRelativePath(relative) {
			return nil, ErrProtocol
		}
		if path.Base(relative) != name {
			return nil, ErrProtocol
		}
		if hasBlockedListingAncestor(relative, blockedDirectories) || hasBlockedListingAncestor(relative, blockedPrefixes) {
			continue
		}
		namePath := filepath.Join(p.path, filepath.FromSlash(relative))
		mode, ok := modeForDirentType(entry.Type)
		if entry.Device == 0 && relative == name {
			entry.Device = p.identity.Device
		}
		if !ok || entry.Inode == 0 || entry.Device == 0 {
			return nil, ErrUnavailable
		}
		parentFD, childName, closeParent, err := openListingParent(rootFD, relative)
		if err != nil {
			return nil, err
		}
		identity, mounted, err := childIdentityAt(parentFD, childName, parentMountID)
		closeErr := closeParent()
		if closeErr != nil {
			return nil, closeErr
		}
		if err != nil {
			return nil, err
		}
		if !mounted && (identity.Device != entry.Device || identity.Inode != entry.Inode || identity.Type != mode) {
			return nil, ErrStaleReceipt
		}
		if name == ".git" || strings.Contains(relative, "/.git/") || strings.HasSuffix(relative, "/.git") {
			continue
		}
		if !p.options.IncludeHidden && strings.HasPrefix(name, ".") {
			continue
		}
		if _, protected := p.registry.ClassifyRoute(namePath); protected {
			if identity.Type == unix.S_IFDIR {
				blockedPrefixes[relative] = true
			}
			continue
		}
		if _, protected := p.registry.ClassifyWithAncestors(namePath, identity, nil); protected {
			if identity.Type == unix.S_IFDIR {
				blockedPrefixes[relative] = true
			}
			continue
		}
		if !p.options.IncludeIgnored {
			ignored, err := ignoredByRules(namePath, identity.Type == unix.S_IFDIR, allRules, budget)
			if err != nil {
				return nil, err
			}
			if ignored {
				if identity.Type == unix.S_IFDIR {
					blockedPrefixes[relative] = true
				}
				continue
			}
		}
		if len(p.options.NameGlobs) > 0 {
			matched := false
			for _, pattern := range p.options.NameGlobs {
				ok, matchErr := gitByteMatch(budget, pattern, name)
				if matchErr != nil {
					return nil, matchErr
				}
				matched = matched || ok
			}
			if !matched {
				continue
			}
		}
		if len(filtered) >= 1_000 {
			listing.Incomplete = true
			continue
		}
		filtered = append(filtered, relative)
	}
	listing.Names = filtered
	listing.Entries = nil
	// Directory snapshots contain private traversal observations (including
	// paths, identities, mount IDs, timestamps, and ignore-file metadata).
	// They are needed only until host filtering and revalidation finish.
	listing.Directories = nil
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

func validListingRelativePath(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.ContainsRune(value, '\\') || strings.ContainsRune(value, '\x00') {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}

func snapshotListingIgnoreEntry(directoryFD int, state *directoryListingDirectory) error {
	var stat unix.Stat_t
	err := unix.Fstatat(directoryFD, ".gitignore", &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return ErrProtected
	}
	state.IgnoreExists = true
	state.IgnoreDevice, state.IgnoreInode, state.IgnoreSize = uint64(stat.Dev), stat.Ino, stat.Size
	state.IgnoreMtimeNS = stat.Mtim.Sec*1e9 + stat.Mtim.Nsec
	state.IgnoreCtimeNS = stat.Ctim.Sec*1e9 + stat.Ctim.Nsec
	return nil
}

func openListingDirectory(rootFD int, relative string) (int, error) {
	if relative == "" {
		return unix.Openat(rootFD, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	}
	fd, err := unix.Openat2(rootFD, relative, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NONBLOCK, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV})
	if err == unix.EXDEV || err == unix.ELOOP {
		return -1, errMountBoundaryRefused
	}
	if err != nil {
		return -1, mapPrepareError(err)
	}
	return fd, nil
}

func openListingParent(rootFD int, relative string) (int, string, func() error, error) {
	parent := path.Dir(relative)
	name := path.Base(relative)
	if parent == "." {
		return rootFD, name, func() error { return nil }, nil
	}
	fd, err := openListingDirectory(rootFD, parent)
	if err != nil {
		return -1, "", func() error { return nil }, err
	}
	return fd, name, func() error { return unix.Close(fd) }, nil
}

func (p *preparedDirectoryList) revalidateListingDirectories(states []directoryListingDirectory, budget *directoryFilterBudget) (map[string]bool, error) {
	seen := make(map[string]struct{}, len(states))
	blocked := make(map[string]bool)
	slices.SortFunc(states, func(a, b directoryListingDirectory) int {
		return strings.Count(a.Path, "/") - strings.Count(b.Path, "/")
	})
	for _, state := range states {
		if err := budget.consume(1); err != nil {
			return nil, err
		}
		if state.Path != "" && !validListingRelativePath(state.Path) {
			return nil, ErrProtocol
		}
		if _, exists := seen[state.Path]; exists {
			return nil, ErrProtocol
		}
		seen[state.Path] = struct{}{}
		if hasBlockedListingAncestor(state.Path, blocked) {
			blocked[state.Path] = true
			continue
		}
		fd, err := openListingDirectory(int(p.directory.Fd()), state.Path)
		if err != nil {
			return nil, err
		}
		var stat unix.Stat_t
		mountID, mountErr := mountIDForFD(fd)
		statErr := unix.Fstat(fd, &stat)
		closeErr := unix.Close(fd)
		if mountErr != nil || statErr != nil || closeErr != nil {
			return nil, errors.Join(ErrUnavailable, mountErr, statErr, closeErr)
		}
		if stat.Mode&unix.S_IFMT != unix.S_IFDIR || uint64(stat.Dev) != state.Device || stat.Ino != state.Inode || mountID != state.MountID || stat.Mtim.Sec*1e9+stat.Mtim.Nsec != state.MtimeNS || stat.Ctim.Sec*1e9+stat.Ctim.Nsec != state.CtimeNS {
			return nil, ErrStaleReceipt
		}
		verifyFD, verifyErr := openListingDirectory(int(p.directory.Fd()), state.Path)
		if verifyErr != nil {
			return nil, verifyErr
		}
		ignoreErr := verifyListingIgnoreEntry(verifyFD, state)
		verifyCloseErr := unix.Close(verifyFD)
		if ignoreErr != nil || verifyCloseErr != nil {
			return nil, errors.Join(ErrStaleReceipt, ignoreErr, verifyCloseErr)
		}
		identity := FileIdentity{Device: state.Device, Inode: state.Inode, Type: unix.S_IFDIR}
		fullPath := p.path
		if state.Path != "" {
			fullPath = filepath.Join(p.path, filepath.FromSlash(state.Path))
		}
		if p.registry.DirectoryCoverageIncomplete(identity) {
			return nil, ErrDirectoryCoverage
		}
		_, routeProtected := p.registry.ClassifyRoute(fullPath)
		_, identityProtected := p.registry.ClassifyWithAncestors(fullPath, identity, nil)
		if routeProtected || identityProtected {
			blocked[state.Path] = true
		}
	}
	if _, exists := seen[""]; !exists {
		return nil, ErrProtocol
	}
	return blocked, nil
}

func (p *preparedDirectoryList) loadNestedListingRules(states []directoryListingDirectory, blocked map[string]bool, budget *directoryFilterBudget) ([]ignoreRule, error) {
	rules := append([]ignoreRule(nil), p.ignoreRules...)
	inputBytes := 0
	for _, rule := range rules {
		inputBytes += len(rule.pattern) + len(rule.base)
	}
	for _, state := range states {
		if state.Path == "" || blocked[state.Path] || hasBlockedListingAncestor(state.Path, blocked) {
			continue
		}
		if err := budget.consume(1); err != nil {
			return nil, err
		}
		fd, err := openListingDirectory(int(p.directory.Fd()), state.Path)
		if err != nil {
			return nil, err
		}
		ignoreFD, openErr := unix.Openat(fd, ".gitignore", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
		if verifyErr := verifyListingIgnoreEntry(fd, state); verifyErr != nil {
			if openErr == nil {
				_ = unix.Close(ignoreFD)
			}
			_ = unix.Close(fd)
			return nil, verifyErr
		}
		closeDirErr := unix.Close(fd)
		if closeDirErr != nil {
			if openErr == nil {
				_ = unix.Close(ignoreFD)
			}
			return nil, closeDirErr
		}
		if openErr == unix.ENOENT {
			continue
		}
		if openErr != nil {
			return nil, ErrProtected
		}
		var stat unix.Stat_t
		if unix.Fstat(ignoreFD, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || uint64(stat.Dev) != state.IgnoreDevice || stat.Ino != state.IgnoreInode || stat.Size != state.IgnoreSize || stat.Mtim.Sec*1e9+stat.Mtim.Nsec != state.IgnoreMtimeNS || stat.Ctim.Sec*1e9+stat.Ctim.Nsec != state.IgnoreCtimeNS || stat.Size < 0 || stat.Size > int64(maxIgnoreBytes-inputBytes) {
			_ = unix.Close(ignoreFD)
			return nil, ErrProtected
		}
		ignorePath := filepath.Join(p.path, filepath.FromSlash(state.Path), ".gitignore")
		identity := identityFromStat(&stat)
		if _, protected := p.registry.ClassifyRoute(ignorePath); protected {
			_ = unix.Close(ignoreFD)
			return nil, ErrProtected
		}
		if _, protected := p.registry.ClassifyWithAncestors(ignorePath, identity, nil); protected {
			_ = unix.Close(ignoreFD)
			return nil, ErrProtected
		}
		file := os.NewFile(uintptr(ignoreFD), "nested-listing-ignore-rules")
		data, readErr := io.ReadAll(io.LimitReader(file, int64(maxIgnoreBytes-inputBytes+1)))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(data) > maxIgnoreBytes-inputBytes || !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
			return nil, ErrProtected
		}
		parsed, parseErr := parseIgnoreRules(data, filepath.Join(p.path, filepath.FromSlash(state.Path)))
		if parseErr != nil || len(rules)+len(parsed) > maxIgnoreRules {
			return nil, ErrProtected
		}
		inputBytes += len(data)
		rules = append(rules, parsed...)
	}
	return rules, nil
}

func verifyListingIgnoreEntry(directoryFD int, state directoryListingDirectory) error {
	var stat unix.Stat_t
	err := unix.Fstatat(directoryFD, ".gitignore", &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		if state.IgnoreExists {
			return ErrStaleReceipt
		}
		return nil
	}
	if err != nil || !state.IgnoreExists {
		return ErrStaleReceipt
	}
	if uint64(stat.Dev) != state.IgnoreDevice || stat.Ino != state.IgnoreInode || stat.Size != state.IgnoreSize || stat.Mtim.Sec*1e9+stat.Mtim.Nsec != state.IgnoreMtimeNS || stat.Ctim.Sec*1e9+stat.Ctim.Nsec != state.IgnoreCtimeNS {
		return ErrStaleReceipt
	}
	return nil
}

func hasBlockedListingAncestor(relative string, blocked map[string]bool) bool {
	for parent := path.Dir(relative); parent != "."; parent = path.Dir(parent) {
		if blocked[parent] {
			return true
		}
	}
	return false
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

func (p *preparedDirectoryList) Identity() FileIdentity         { return p.identity }
func (p *preparedDirectoryList) ListingOptions() ListingOptions { return p.options.Clone() }
func (p *preparedDirectoryList) Class() ResourceClass           { return p.class }
func (p *preparedDirectoryList) Path() string                   { return p.path }
func (p *preparedDirectoryList) Generations() GenerationSet     { return p.generation }
func (p *preparedDirectoryList) RequestDigest() [32]byte        { return p.digest }

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

func requestDigest(root, target string, id FileIdentity, class ResourceClass, generation GenerationSet, operation AdmissionOperation, listing ...ListingOptions) [32]byte {
	hash := sha256.New()
	_, _ = hash.Write([]byte(root))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(target))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(class))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(operation))
	if len(listing) > 0 {
		if encodedOptions, err := json.Marshal(listing[0]); err == nil {
			_, _ = hash.Write([]byte{0})
			_, _ = hash.Write(encodedOptions)
		}
	}
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
