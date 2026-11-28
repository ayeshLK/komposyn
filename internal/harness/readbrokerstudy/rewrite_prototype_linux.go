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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	frameRewriteInput   byte = 'i'
	frameRewriteHeld    byte = 'h'
	frameRewriteRelease byte = 'l'
	frameRewriteResult  byte = 'r'
)

type RewriteOutcome byte

const (
	RewriteUnknown RewriteOutcome = iota
	RewriteNoEffect
	RewritePartial
	RewriteComplete
)

// RewritePrototypeWorker holds one approved file and its proposed bytes.
// Only ReleaseRewrite permits a content mutation.
type RewritePrototypeWorker interface {
	HoldRewrite(context.Context, *os.File, FileIdentity, []byte) error
	ReleaseRewrite(context.Context) error
	WaitRewrite(context.Context) (RewriteOutcome, error)
}

func (w *sandboxWorker) HoldRewrite(ctx context.Context, file *os.File, identity FileIdentity, content []byte) error {
	if w == nil || ctx == nil || file == nil || w.operation != AdmissionRewritePrototype || !validRewriteText(content) {
		return ErrProtocol
	}
	if _, ok := rewriteDescriptor(int(file.Fd()), identity); !ok {
		return ErrStaleReceipt
	}
	w.mu.Lock()
	if !w.ready || w.sent || w.finished {
		w.mu.Unlock()
		return ErrProtocol
	}
	if err := ctx.Err(); err != nil {
		w.mu.Unlock()
		return err
	}
	w.sent = true
	w.expected = identity
	message := append(identityFrame(frameRewriteInput, identity), content...)
	n, err := unix.SendmsgN(int(w.control.Fd()), message, unix.UnixRights(int(file.Fd())), nil, unix.MSG_DONTWAIT|unix.MSG_NOSIGNAL)
	w.mu.Unlock()
	clear(message)
	if err != nil || n != len(message) {
		return ErrUnavailable
	}
	packet, err := recvPacket(ctx, int(w.control.Fd()), 64)
	if err != nil || len(packet) != 21 || packet[0] != frameRewriteHeld || !sameIdentity(packet[1:], identity) {
		return errors.Join(ErrProtocol, err)
	}
	w.mu.Lock()
	w.rewriteHeld = true
	w.mu.Unlock()
	return nil
}

func (w *sandboxWorker) ReleaseRewrite(ctx context.Context) error {
	w.mu.Lock()
	if !w.rewriteHeld || w.finished {
		w.mu.Unlock()
		return ErrProtocol
	}
	if err := ctx.Err(); err != nil {
		w.mu.Unlock()
		return err
	}
	w.finished = true
	identity := w.expected
	w.mu.Unlock()

	return sendPacket(int(w.control.Fd()), identityFrame(frameRewriteRelease, identity))
}

func (w *sandboxWorker) WaitRewrite(ctx context.Context) (RewriteOutcome, error) {
	w.mu.Lock()
	finished, identity := w.finished, w.expected
	w.mu.Unlock()
	if !finished {
		return RewriteNoEffect, ErrProtocol
	}
	packet, err := recvPacket(ctx, int(w.control.Fd()), 64)
	if err != nil || len(packet) != 22 || packet[0] != frameRewriteResult || !sameIdentity(packet[2:], identity) {
		return RewriteUnknown, errors.Join(ErrProtocol, err)
	}
	outcome := RewriteOutcome(packet[1])
	if outcome > RewriteComplete {
		return RewriteUnknown, ErrProtocol
	}
	return outcome, nil
}

func rewriteDescriptor(fd int, expected FileIdentity) (unix.Stat_t, bool) {
	var stat unix.Stat_t
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil || flags&unix.O_ACCMODE != unix.O_RDWR || flags&(unix.O_APPEND|unix.O_PATH) != 0 ||
		unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink > 1 ||
		stat.Mode&(unix.S_ISUID|unix.S_ISGID|unix.S_ISVTX) != 0 ||
		stat.Size < 0 || stat.Size > MaxReadBytes || identityFromStat(&stat) != expected {
		return stat, false
	}
	return stat, true
}

func validRewriteText(content []byte) bool {
	return len(content) <= MaxReadBytes && utf8.Valid(content) && bytes.IndexByte(content, 0) < 0
}

func rewriteTransferredFile(control, fd int, identityBytes, content []byte) int {
	defer clear(content)
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || !validRewriteText(content) {
		return sendRefusal(control)
	}
	identity := identityFromStat(&stat)
	if !sameIdentity(identityBytes, identity) {
		return sendRefusal(control)
	}
	if _, ok := rewriteDescriptor(fd, identity); !ok {
		return sendRefusal(control)
	}
	if sendPacket(control, identityFrame(frameRewriteHeld, identity)) != nil {
		return 72
	}
	packet, err := recvPacket(context.Background(), control, 64)
	if err != nil || len(packet) != 21 || packet[0] != frameRewriteRelease || !sameIdentity(packet[1:], identity) {
		return sendRefusal(control)
	}
	stat, ok := rewriteDescriptor(fd, identity)
	outcome := RewriteNoEffect
	if ok {
		outcome = rewriteContents(fd, stat.Size, content)
	}
	result := append([]byte{frameRewriteResult, byte(outcome)}, identityBytes...)
	if sendPacket(control, result) != nil {
		return 72
	}
	return 0
}

func rewriteContents(fd int, oldSize int64, content []byte) RewriteOutcome {
	if int64(len(content)) == oldSize {
		existing := make([]byte, len(content))
		if err := readRewriteBytes(fd, existing); err != nil {
			return RewriteNoEffect
		}
		if bytes.Equal(existing, content) {
			return RewriteNoEffect
		}
	}
	for offset := 0; offset < len(content); {
		n, err := unix.Pwrite(fd, content[offset:], int64(offset))
		if err != nil || n <= 0 {
			if offset > 0 {
				return RewritePartial
			}
			return RewriteUnknown
		}
		offset += n
	}
	if int64(len(content)) < oldSize {
		if err := unix.Ftruncate(fd, int64(len(content))); err != nil {
			if len(content) > 0 {
				return RewritePartial
			}
			return RewriteUnknown
		}
	}
	return RewriteComplete
}

func readRewriteBytes(fd int, data []byte) error {
	for offset := 0; offset < len(data); {
		n, err := unix.Pread(fd, data[offset:], int64(offset))
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrUnexpectedEOF
		}
		offset += n
	}
	return nil
}

// RewriteFileRevision identifies the mounted inode and its observed metadata
// revision without including access time, which an approved read may change.
func RewriteFileRevision(file *os.File) string {
	var stat unix.Statx_t
	const mask = unix.STATX_BASIC_STATS | unix.STATX_BTIME | unix.STATX_MNT_ID
	if unix.Statx(int(file.Fd()), "", unix.AT_EMPTY_PATH, mask, &stat) != nil || stat.Mask&mask != mask {
		return ""
	}
	values := fmt.Sprintf("%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d",
		stat.Dev_major, stat.Dev_minor, stat.Mnt_id, stat.Ino, stat.Btime.Sec, stat.Btime.Nsec,
		stat.Ctime.Sec, stat.Ctime.Nsec, stat.Mtime.Sec, stat.Mtime.Nsec,
		stat.Size, stat.Mode, stat.Uid, stat.Gid, stat.Nlink)
	digest := sha256.Sum256([]byte(values))
	return hex.EncodeToString(digest[:])
}

func rewriteReadRevision(read *preparedRead) string {
	read.mu.Lock()
	defer read.mu.Unlock()
	if read.closed || read.file == nil {
		return ""
	}
	return RewriteFileRevision(read.file)
}
