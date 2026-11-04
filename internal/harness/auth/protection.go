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

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const (
	SensitiveOwnerAuth     = "komposyn-auth-store"
	SensitiveOwnerSettings = "komposyn-settings-store"
	SensitiveClassAuth     = "credential"
	SensitiveClassSettings = "settings"
)

// SensitiveFileProtector is a host-owned lifecycle hook. The file argument is
// the already-open object whose identity must be pinned before a write. A nil
// file registers only the effective route, including when the leaf is absent.
// Implementations must fail closed if registration or worker cleanup fails.
// This contract carries no authorization decision or filesystem handle to a
// provider, UI plugin, or tool.
type SensitiveFileProtector interface {
	Protect(context.Context, string, string, string, *os.File) error
}

// RegisterSensitiveJSONStore reports an effective auth/settings JSON route
// and, if present, its safely opened object. It reads no file contents.
func RegisterSensitiveJSONStore(ctx context.Context, protector SensitiveFileProtector, owner, class, path string) error {
	if protector == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err = ensurePrivateDir(filepath.Dir(abs)); err != nil {
		return err
	}
	f, err := openPrivateFile(abs)
	if os.IsNotExist(err) {
		return protector.Protect(ctx, owner, class, abs, nil)
	}
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("configuration file must be a regular file")
	}
	if err = checkPrivateFile(abs, info); err != nil {
		return err
	}
	return protector.Protect(ctx, owner, class, abs, f)
}

// UpdateJSONProtected is UpdateJSON with an owner-provided barrier that runs
// after opening the empty replacement and before writing its serialized data.
func UpdateJSONProtected(ctx context.Context, path string, update func(bool, map[string]json.RawMessage) error, protector SensitiveFileProtector, owner, class string) error {
	return updateJSON(ctx, path, update, protector, owner, class)
}

func atomicWrite(ctx context.Context, path string, value any, owner, class string, protector SensitiveFileProtector) error {
	return writeAtomicProtected(ctx, path, value, func(ctx context.Context, temp *os.File) error {
		if protector == nil {
			return nil
		}
		return protector.Protect(ctx, owner, class, path, temp)
	})
}
