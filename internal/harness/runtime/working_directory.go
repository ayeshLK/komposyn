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

package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrWorkingDirectoryArgument    = errors.New("working directory path is required; use /cd <path>")
	ErrWorkingDirectoryUnavailable = errors.New("working directory is unavailable; restart KompoSyn from an accessible directory")
	ErrWorkingDirectoryClosed      = errors.New("chat is closed; working directory cannot be changed")
	ErrWorkingDirectoryChangeStale = errors.New("working directory change was superseded by a newer operation")
)

// ChangeWorkingDirectory changes only the base used to resolve future
// relative read paths. It does not change the process working directory,
// eligible-target boundary, conversation, or existing read context.
func (c *Chat) ChangeWorkingDirectory(ctx context.Context, path string) error {
	if ctx == nil {
		return context.Canceled
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return ErrWorkingDirectoryArgument
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrWorkingDirectoryClosed
	}
	if c.active || c.credentialBusy {
		c.mu.Unlock()
		return ErrRequestActive
	}
	base := c.workingDirectory
	if base == "" {
		c.mu.Unlock()
		return ErrWorkingDirectoryUnavailable
	}
	c.workingDirectoryOp++
	operation := c.workingDirectoryOp
	host := c.filesystemRootHostLocked()
	c.mu.Unlock()

	resolved, err := resolveWorkingDirectory(path, base)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	canonical := ""
	if host != nil {
		canonical, err = host.ValidateFilesystemRoot(ctx, resolved)
	} else {
		canonical, err = CanonicalFilesystemRoot(ctx, resolved)
	}
	if err != nil {
		return err
	}
	if !filepath.IsAbs(canonical) || filepath.Clean(canonical) != canonical {
		return ErrFilesystemRootAbsolute
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrWorkingDirectoryClosed
	}
	if c.active || c.credentialBusy {
		return ErrRequestActive
	}
	if c.workingDirectoryOp != operation || c.workingDirectory != base {
		return ErrWorkingDirectoryChangeStale
	}
	c.workingDirectory = canonical
	c.workingDirectoryOp++
	return nil
}

func resolveWorkingDirectory(path, base string) (string, error) {
	if path == "" {
		return "", ErrWorkingDirectoryArgument
	}
	path = expandHomeDirectory(path)
	if !filepath.IsAbs(path) {
		if base == "" || !filepath.IsAbs(base) || filepath.Clean(base) != base {
			return "", ErrWorkingDirectoryUnavailable
		}
		path = filepath.Join(base, path)
	}
	return filepath.Clean(path), nil
}

func expandHomeDirectory(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~"+string(filepath.Separator)) && !(filepath.Separator == '\\' && strings.HasPrefix(path, "~/")) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	remainder := strings.TrimLeft(path[1:], `/\`)
	return filepath.Join(home, remainder)
}
