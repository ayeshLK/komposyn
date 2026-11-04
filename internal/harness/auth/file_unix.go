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

//go:build !windows

package auth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func ensurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("credential directory must be a real directory")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	return nil
}
func checkPrivateFile(_ string, info os.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("credential store permissions must be 0600")
	}
	return nil
}
func openPrivateFile(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
func lockPath(path string) (func(), error) {
	return lockPathContext(context.Background(), path)
}

func lockPathContext(ctx context.Context, path string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name := path + ".lock"
	f, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		f.Close()
		return nil, err
	}
	delay := 10 * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			_ = f.Close()
			return nil, err
		}
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EINTR) {
			_ = f.Close()
			return nil, err
		}
		if err := waitForLockRetry(ctx, delay); err != nil {
			_ = f.Close()
			return nil, err
		}
		if delay < 250*time.Millisecond {
			delay *= 2
			if delay > 250*time.Millisecond {
				delay = 250 * time.Millisecond
			}
		}
	}
}
func writeAtomic(path string, doc any) error {
	return writeAtomicProtected(context.Background(), path, doc, nil)
}

func writeAtomicProtected(ctx context.Context, path string, doc any, beforeWrite func(context.Context, *os.File) error) error {
	data, err := marshal(doc)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".auth-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	info, statErr := f.Stat()
	if statErr != nil {
		_ = f.Close()
		return statErr
	}
	defer func() {
		if current, e := os.Lstat(tmp); e == nil && os.SameFile(info, current) {
			_ = os.Remove(tmp)
		}
	}()
	if err = f.Chmod(0o600); err == nil {
		err = ctx.Err()
	}
	if err == nil && beforeWrite != nil {
		err = beforeWrite(ctx, f)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = verifyTempIdentity(tmp, info)
	}
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = verifyTempIdentity(tmp, info); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err == nil {
		err = d.Sync()
		_ = d.Close()
	}
	return err
}

func verifyTempIdentity(path string, expected os.FileInfo) error {
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if current.Mode()&os.ModeSymlink != 0 || !os.SameFile(expected, current) {
		return errors.New("protected temporary file identity changed")
	}
	return nil
}
