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

package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
	"github.com/ayeshLK/komposyn/internal/harness/runtime"
	"github.com/ayeshLK/komposyn/plugins/tools"
	"golang.org/x/sys/unix"
)

var (
	errRewriteTargetMissing = errors.New("approved edit target is missing")
	errRewriteTargetChanged = errors.New("approved edit target changed")
	errRewriteMetadata      = errors.New("edit target metadata eligibility is unverified")
)

type rewritePrototypeResult struct {
	outcome   readbrokerstudy.RewriteOutcome
	pathErr   error
	unchanged bool
}

// ExecuteApprovedRewritePrototype has no production tool registration. Only an
// opaque once-consumed runtime approval is accepted.
func (s *Service) ExecuteApprovedRewritePrototype(ctx context.Context, approved *runtime.ApprovedRewritePrototype) (tools.Result, error) {
	result := tools.Result{Outcome: tools.OutcomeNotExecuted, ErrorCode: "rewrite_not_executed"}
	if s == nil || ctx == nil {
		return result, ErrServiceClosed
	}
	input, withAuthority, err := approved.Claim()
	if err != nil {
		return result, err
	}
	impl, ok := s.impl.(*linuxImplementation)
	if !ok {
		return result, ErrCoverageMissing
	}
	impl.mu.Lock()
	session, _ := impl.session.(interface{ RewritePrototypeServiceID() string })
	impl.mu.Unlock()
	if session == nil || input.ServiceID == "" || session.RewritePrototypeServiceID() != input.ServiceID {
		return result, ErrCoverageMissing
	}
	var expected readbrokerstudy.FileIdentity
	if n, err := fmt.Sscanf(input.Identity, "%d:%d:%d", &expected.Device, &expected.Inode, &expected.Type); err != nil || n != 3 ||
		fmt.Sprintf("%d:%d:%d", expected.Device, expected.Inode, expected.Type) != input.Identity ||
		expected.Inode == 0 || expected.Type != unix.S_IFREG {
		return result, readbrokerstudy.ErrStaleReceipt
	}
	scope, _, identity, generation, _, err := s.creationScopeForInvocation(input.Root)
	if err != nil {
		return result, err
	}
	if err := scope.Close(); err != nil {
		return result, err
	}
	effect, err := s.executeRewritePrototype(ctx, creationRootBinding{
		root: input.Root, conversation: input.Conversation, target: input.Target, generation: input.RootGeneration,
		scopeIdentity: identity, scopeGeneration: generation,
	}, input.PolicyRevision, input.CoverageRevision, expected, input.FileRevision, input.Base, input.Content, withAuthority)
	switch effect.outcome {
	case readbrokerstudy.RewriteComplete:
		result = tools.Result{Outcome: tools.OutcomeSucceeded}
	case readbrokerstudy.RewritePartial:
		result = tools.Result{Outcome: tools.OutcomePartial, ErrorCode: "rewrite_partial"}
	case readbrokerstudy.RewriteUnknown:
		result = tools.Result{Outcome: tools.OutcomeUnknown, ErrorCode: "rewrite_unknown"}
	case readbrokerstudy.RewriteNoEffect:
		switch {
		case effect.unchanged:
			result = tools.Result{Outcome: tools.OutcomeSucceeded, Content: []tools.Content{{Kind: tools.ContentText, Text: "The approved file already has the requested contents."}}}
		case errors.Is(err, errRewriteTargetMissing):
			result.ErrorCode = "rewrite_target_missing"
		case errors.Is(err, errRewriteTargetChanged):
			result.ErrorCode = "rewrite_target_changed"
		case errors.Is(err, errRewriteMetadata):
			result.ErrorCode = "rewrite_metadata_unverified"
		default:
			result.ErrorCode = "rewrite_no_effect"
		}
	}
	if effect.pathErr != nil {
		result.Content = []tools.Content{{Kind: tools.ContentText, Text: "The approved name could not be confirmed after the write attempt."}}
	}
	if effect.outcome == readbrokerstudy.RewriteComplete && effect.pathErr == nil {
		result.Content = []tools.Content{{Kind: tools.ContentText, Text: "The rewrite was verified on the approved file."}}
	}
	return result, err
}

// executeRewritePrototype accepts a frozen edit from a trusted caller after
// once approval. It is deliberately outside normal tool composition.
func (s *Service) executeRewritePrototype(ctx context.Context, binding creationRootBinding, policyRevision, coverageRevision uint64, expected readbrokerstudy.FileIdentity, revision string, base, content []byte, withAuthority func(func() error) error) (result rewritePrototypeResult, resultErr error) {
	result.outcome = readbrokerstudy.RewriteNoEffect
	if ctx == nil || s == nil || withAuthority == nil || revision == "" || len(base) > readbrokerstudy.MaxReadBytes || len(content) > readbrokerstudy.MaxReadBytes ||
		binding.conversation == "" || !filepath.IsAbs(binding.target) || filepath.Clean(binding.target) != binding.target {
		return result, readbrokerstudy.ErrProtocol
	}
	impl, ok := s.impl.(*linuxImplementation)
	if !ok {
		return result, ErrCoverageMissing
	}
	scope, scopePath, scopeIdentity, scopeGeneration, _, err := s.creationScopeForInvocation(binding.root)
	if err != nil {
		return result, err
	}
	defer scope.Close()
	if scopeIdentity != binding.scopeIdentity || scopeGeneration != binding.scopeGeneration {
		return result, ErrCoverageMissing
	}
	relative, err := filepath.Rel(scopePath, binding.target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, "../") {
		return result, ErrCoverageMissing
	}
	parent, parentIdentity, _, leaf, err := openCreationParent(scope, relative)
	if errors.Is(err, unix.ENOENT) {
		return result, errRewriteTargetMissing
	}
	if err != nil {
		return result, err
	}
	defer parent.Close()

	checkAuthority := func() error {
		if err := s.validateCreationOwnerProfile(scopeIdentity); err != nil {
			return err
		}
		if s.CoverageSnapshot().Revision != coverageRevision || impl.registry.View().Generation != policyRevision {
			return ErrCoverageMissing
		}
		_, protected, err := impl.registry.ClassifyFromRetainedDirectory(binding.target, expected, int(parent.Fd()))
		if err != nil || protected {
			return ErrCoverageMissing
		}
		return nil
	}
	if err := checkAuthority(); err != nil {
		return result, err
	}

	lease, err := impl.beginCreationBarrier(ctx, binding)
	if err != nil {
		if lease != nil {
			err = errors.Join(err, impl.finishCreationBarrier(lease, false))
		}
		return result, err
	}
	joined := true
	defer func() {
		resultErr = errors.Join(resultErr, impl.finishCreationBarrier(lease, joined))
	}()
	if err := checkAuthority(); err != nil {
		return result, err
	}
	pinned, err := openRewriteTarget(parent, leaf, expected)
	if err != nil {
		return result, err
	}
	defer pinned.Close()
	if readbrokerstudy.RewriteFileRevision(pinned) != revision {
		return result, errRewriteTargetChanged
	}
	file, err := openPinnedRewriteFile(pinned)
	if err != nil {
		return result, err
	}
	defer file.Close()
	if err := validateRewriteContents(file, base); err != nil {
		return result, err
	}

	impl.mu.Lock()
	launcher, ok := impl.launcher.(readbrokerstudy.OperationWorkerLauncher)
	impl.mu.Unlock()
	if !ok {
		return result, readbrokerstudy.ErrUnavailable
	}
	worker, err := launcher.StartForOperation(lease.ctx, readbrokerstudy.AdmissionRewritePrototype)
	if err != nil {
		return result, err
	}
	joined = false
	impl.mu.Lock()
	lease.worker = worker
	impl.mu.Unlock()
	stopped := false
	stop := func() (bool, error) {
		if stopped {
			return joined, nil
		}
		stopped = true
		var err error
		joined, err = stopRewriteWorker(worker)
		return joined, err
	}
	defer func() {
		_, err := stop()
		resultErr = errors.Join(resultErr, err)
	}()
	if err := worker.Ready(lease.ctx); err != nil {
		return result, err
	}
	editor, ok := worker.(readbrokerstudy.RewritePrototypeWorker)
	if !ok {
		return result, readbrokerstudy.ErrUnavailable
	}
	if err := impl.admitCreationStage(lease); err != nil {
		return result, err
	}
	if err := editor.HoldRewrite(lease.ctx, file, expected, content); err != nil {
		return result, err
	}

	checkName := func() error {
		currentScope, _, identity, generation, _, err := s.creationScopeForInvocation(binding.root)
		if err != nil {
			return err
		}
		defer currentScope.Close()
		if identity != scopeIdentity || generation != scopeGeneration {
			return ErrCoverageMissing
		}
		currentParent, identity, _, _, err := openCreationParent(currentScope, relative)
		if errors.Is(err, unix.ENOENT) {
			return errRewriteTargetMissing
		}
		if err != nil {
			return err
		}
		defer currentParent.Close()
		if identity != parentIdentity {
			return errRewriteTargetChanged
		}
		current, err := openRewriteTarget(currentParent, leaf, expected)
		if err != nil {
			return err
		}
		defer current.Close()
		return compareRewriteDescriptors(pinned, current)
	}
	if err := checkAuthority(); err != nil {
		return result, err
	}
	if err := validateRewriteContents(file, base); err != nil {
		return result, err
	}
	if readbrokerstudy.RewriteFileRevision(file) != revision {
		return result, errRewriteTargetChanged
	}
	if err := checkName(); err != nil {
		return result, err
	}
	if bytes.Equal(base, content) {
		err := withAuthority(func() error { return lease.ctx.Err() })
		result.unchanged = err == nil
		return result, err
	}
	sendAttempted := false
	err = withAuthority(func() error {
		if err := impl.releaseCreationPublication(lease); err != nil {
			return err
		}
		sendAttempted = true
		return editor.ReleaseRewrite(lease.ctx)
	})
	if err != nil {
		if sendAttempted {
			result.outcome = readbrokerstudy.RewriteUnknown
		}
		return result, err
	}
	result.outcome, err = waitAndStopRewriteWorker(lease.ctx, editor, stop)
	if !joined {
		return result, err
	}
	result.pathErr = checkName()
	if result.outcome == readbrokerstudy.RewriteComplete {
		if currentErr := lease.ctx.Err(); currentErr != nil {
			result.outcome = readbrokerstudy.RewriteUnknown
			err = errors.Join(err, currentErr)
		} else if currentErr := checkAuthority(); currentErr != nil {
			result.outcome = readbrokerstudy.RewriteUnknown
			err = errors.Join(err, currentErr)
		} else if verifyErr := withAuthority(func() error { return compareRewriteBytes(file, content) }); verifyErr != nil {
			result.outcome = readbrokerstudy.RewriteUnknown
			err = errors.Join(err, verifyErr)
		}
	}
	return result, err
}

func waitAndStopRewriteWorker(ctx context.Context, editor readbrokerstudy.RewritePrototypeWorker, stop func() (bool, error)) (readbrokerstudy.RewriteOutcome, error) {
	outcome, waitErr := editor.WaitRewrite(ctx)
	joined, stopErr := stop()
	if !joined {
		outcome = readbrokerstudy.RewriteUnknown
	}
	return outcome, errors.Join(waitErr, stopErr)
}

func stopRewriteWorker(worker readbrokerstudy.SandboxWorker) (bool, error) {
	terminateErr := worker.Terminate()
	done := make(chan error, 1)
	go func() { done <- worker.Wait() }()
	timer := time.NewTimer(creationWorkerJoinTimeout)
	defer timer.Stop()
	select {
	case <-done:
		if !worker.Joined() {
			return false, errors.Join(terminateErr, readbrokerstudy.ErrCleanupPending)
		}
		return true, terminateErr
	case <-timer.C:
		return false, errors.Join(terminateErr, readbrokerstudy.ErrCleanupPending)
	}
}

func openRewriteTarget(parent *os.File, leaf string, expected readbrokerstudy.FileIdentity) (*os.File, error) {
	fd, err := unix.Openat2(int(parent.Fd()), leaf, &unix.OpenHow{
		Flags:   unix.O_PATH | unix.O_CLOEXEC | unix.O_NOFOLLOW,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV,
	})
	if errors.Is(err, unix.ENOENT) {
		return nil, errRewriteTargetMissing
	}
	if err != nil {
		return nil, errors.Join(errRewriteTargetChanged, err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || identityFromUnixStat(&stat) != expected || stat.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return nil, errRewriteTargetChanged
	}
	return os.NewFile(uintptr(fd), "rewrite-retained-target"), nil
}

func openPinnedRewriteFile(pinned *os.File) (*os.File, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(pinned.Fd()), &stat); err != nil || !eligibleRewriteStat(stat) {
		return nil, errRewriteMetadata
	}
	proc, err := unix.Open("/proc/self/fd", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(proc)
	var fs unix.Statfs_t
	if unix.Fstatfs(proc, &fs) != nil || fs.Type != unix.PROC_SUPER_MAGIC {
		return nil, readbrokerstudy.ErrUnavailable
	}
	fd, err := unix.Openat(proc, strconv.FormatUint(uint64(pinned.Fd()), 10), unix.O_RDWR|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "rewrite-writable-target")
	if err := compareRewriteDescriptors(pinned, file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func compareRewriteDescriptors(first, second *os.File) error {
	const mask = unix.STATX_INO | unix.STATX_TYPE | unix.STATX_MNT_ID
	var before, after unix.Statx_t
	if unix.Statx(int(first.Fd()), "", unix.AT_EMPTY_PATH, mask, &before) != nil ||
		unix.Statx(int(second.Fd()), "", unix.AT_EMPTY_PATH, mask, &after) != nil ||
		before.Mask&mask != mask || after.Mask&mask != mask ||
		before.Ino != after.Ino || before.Mode&unix.S_IFMT != after.Mode&unix.S_IFMT ||
		before.Dev_major != after.Dev_major || before.Dev_minor != after.Dev_minor || before.Mnt_id != after.Mnt_id {
		return errRewriteTargetChanged
	}
	return nil
}

func eligibleRewriteStat(stat unix.Stat_t) bool {
	return stat.Mode&unix.S_IFMT == unix.S_IFREG && stat.Nlink == 1 &&
		stat.Uid == uint32(os.Geteuid()) && stat.Mode&(unix.S_ISUID|unix.S_ISGID|unix.S_ISVTX) == 0 &&
		stat.Size >= 0 && stat.Size <= readbrokerstudy.MaxReadBytes
}

func validateRewriteContents(file *os.File, base []byte) error {
	var before, after unix.Stat_t
	if unix.Fstat(int(file.Fd()), &before) != nil || !eligibleRewriteStat(before) {
		return errRewriteMetadata
	}
	for _, attr := range []string{"security.capability", "security.ima", "security.evm"} {
		if _, err := unix.Fgetxattr(int(file.Fd()), attr, nil); !errors.Is(err, unix.ENODATA) {
			return errRewriteMetadata
		}
	}
	var stat unix.Statx_t
	const forbidden = unix.STATX_ATTR_APPEND | unix.STATX_ATTR_IMMUTABLE | unix.STATX_ATTR_VERITY
	if unix.Statx(int(file.Fd()), "", unix.AT_EMPTY_PATH, unix.STATX_BASIC_STATS, &stat) != nil ||
		stat.Attributes_mask&forbidden != forbidden || stat.Attributes&forbidden != 0 {
		return errRewriteMetadata
	}
	if err := compareRewriteBytes(file, base); err != nil {
		return err
	}
	if unix.Fstat(int(file.Fd()), &after) != nil || !eligibleRewriteStat(after) ||
		before.Ctim != after.Ctim || before.Mtim != after.Mtim || before.Size != after.Size {
		return errRewriteTargetChanged
	}
	return nil
}

func compareRewriteBytes(file *os.File, expected []byte) error {
	content := make([]byte, len(expected)+1)
	defer clear(content)
	n, err := file.ReadAt(content, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("verify retained edit contents: %w", err)
	}
	if n != len(expected) || !bytes.Equal(content[:n], expected) {
		return errRewriteTargetChanged
	}
	return nil
}
