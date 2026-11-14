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

package sandboxstudy

import (
	"fmt"
	"os"
	"path/filepath"
)

func setupRootRelocation(scope, suiteRoot string) (func() (string, error), error) {
	return func() (string, error) {
		moved := filepath.Join(suiteRoot, "moved-root")
		if err := os.Rename(scope, moved); err != nil {
			return "", err
		}
		if err := os.Mkdir(scope, 0o700); err != nil {
			return "", err
		}
		return "renamed the bound root and replaced its old pathname", nil
	}, nil
}

func setupRootRelocationRead(scope, suiteRoot string) (func() (string, error), error) {
	if err := os.WriteFile(filepath.Join(scope, "secret.txt"), []byte("read fixture"), 0o600); err != nil {
		return nil, err
	}
	return func() (string, error) {
		moved := filepath.Join(suiteRoot, "moved-root")
		if err := os.Rename(scope, moved); err != nil {
			return "", err
		}
		if err := os.Mkdir(scope, 0o700); err != nil {
			return "", err
		}
		return "renamed root after worker opened the file", nil
	}, nil
}

func setupParentRelocation(scope, suiteRoot string) (func() (string, error), error) {
	parent := filepath.Join(scope, "parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		return nil, err
	}
	return func() (string, error) {
		if err := os.Rename(parent, filepath.Join(suiteRoot, "moved-parent")); err != nil {
			return "", err
		}
		if err := os.Mkdir(parent, 0o700); err != nil {
			return "", err
		}
		return "moved the opened parent outside the approved root and replaced its pathname", nil
	}, nil
}

func setupParentDelete(scope, suiteRoot string) (func() (string, error), error) {
	parent := filepath.Join(scope, "parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(parent, "target.txt"), []byte("approved delete entry"), 0o600); err != nil {
		return nil, err
	}
	return func() (string, error) {
		if err := os.Rename(parent, filepath.Join(suiteRoot, "moved-parent")); err != nil {
			return "", err
		}
		if err := os.Mkdir(parent, 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(parent, "target.txt"), []byte("replacement entry"), 0o600); err != nil {
			return "", err
		}
		return "moved validated parent and substituted a new entry at the approved pathname", nil
	}, nil
}

func setupTargetRelocation(scope, suiteRoot string) (func() (string, error), error) {
	if err := os.WriteFile(filepath.Join(scope, "target.txt"), []byte("before"), 0o600); err != nil {
		return nil, err
	}
	return func() (string, error) {
		return "", os.Rename(filepath.Join(scope, "target.txt"), filepath.Join(suiteRoot, "moved-target"))
	}, nil
}

func setupExistingHardlink(scope, suiteRoot string) (func() (string, error), error) {
	if err := os.WriteFile(filepath.Join(scope, "target.txt"), []byte("before"), 0o600); err != nil {
		return nil, err
	}
	if err := os.Link(filepath.Join(scope, "target.txt"), filepath.Join(suiteRoot, "alias.txt")); err != nil {
		return nil, err
	}
	return func() (string, error) { return "used a pre-existing outside hard-link alias", nil }, nil
}

func setupLateHardlink(scope, suiteRoot string) (func() (string, error), error) {
	if err := os.WriteFile(filepath.Join(scope, "target.txt"), []byte("before"), 0o600); err != nil {
		return nil, err
	}
	return func() (string, error) {
		return "", os.Link(filepath.Join(scope, "target.txt"), filepath.Join(suiteRoot, "alias.txt"))
	}, nil
}

func setupDeleteSubstitution(scope, suiteRoot string) (func() (string, error), error) {
	if err := os.WriteFile(filepath.Join(scope, "target.txt"), []byte("approved entry"), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(suiteRoot, "secret.txt"), []byte("outside fixture"), 0o600); err != nil {
		return nil, err
	}
	return func() (string, error) {
		target := filepath.Join(scope, "target.txt")
		if err := os.Rename(target, filepath.Join(suiteRoot, "displaced.txt")); err != nil {
			return "", err
		}
		if err := os.Symlink(filepath.Join(suiteRoot, "secret.txt"), target); err != nil {
			return "", err
		}
		return "replaced the deletion name with a symlink after the worker opened the parent", nil
	}, nil
}

func setupRecursiveMove(scope, suiteRoot string) (func() (string, error), error) {
	child := filepath.Join(scope, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(child, "item.txt"), []byte("recursive fixture"), 0o600); err != nil {
		return nil, err
	}
	return func() (string, error) {
		return "", os.Rename(child, filepath.Join(suiteRoot, "moved-child"))
	}, nil
}

func setupRenameOut(scope, _ string) (func() (string, error), error) {
	if err := os.WriteFile(filepath.Join(scope, "source.txt"), []byte("rename fixture"), 0o600); err != nil {
		return nil, err
	}
	return func() (string, error) { return "no host mutation before cross-scope rename", nil }, nil
}

func setupSymlinkEscape(scope, suiteRoot string) (func() (string, error), error) {
	if err := os.WriteFile(filepath.Join(suiteRoot, "secret.txt"), []byte("outside fixture"), 0o600); err != nil {
		return nil, err
	}
	if err := os.Symlink(filepath.Join(suiteRoot, "secret.txt"), filepath.Join(scope, "link.txt")); err != nil {
		return nil, err
	}
	return func() (string, error) { return "attempted absolute symlink traversal", nil }, nil
}

func setupProtectedAlias(scope, suiteRoot string) (func() (string, error), error) {
	protected := filepath.Join(scope, "protected")
	if err := os.Mkdir(protected, 0o700); err != nil {
		return nil, err
	}
	secret := filepath.Join(protected, "secret.txt")
	if err := os.WriteFile(secret, []byte("protected fixture"), 0o600); err != nil {
		return nil, err
	}
	if err := os.Link(secret, filepath.Join(scope, "protected-alias.txt")); err != nil {
		return nil, err
	}
	return func() (string, error) { return fmt.Sprintf("read via alias %q", "protected-alias.txt"), nil }, nil
}

func setupNoop(string, string) (func() (string, error), error) {
	return func() (string, error) { return "no host mutation before release", nil }, nil
}

func prepareCreateParent(scope string) error {
	return os.Mkdir(filepath.Join(scope, "parent"), 0o700)
}

func setupCreateEmptyParent(scope, _ string) (func() (string, error), error) {
	if err := prepareCreateParent(scope); err != nil {
		return nil, err
	}
	return func() (string, error) { return "no mutation before release", nil }, nil
}

func setupCreateParentRelocation(scope, outside string, actor fixtureActor) (func() (string, error), error) {
	if err := prepareCreateParent(scope); err != nil {
		return nil, err
	}
	return func() (string, error) {
		if err := actor("move-parent", scope, outside); err != nil {
			return "", err
		}
		return "independent same-user actor moved the retained parent and installed a replacement", nil
	}, nil
}

func setupCreateCollision(scope, outside string, actor fixtureActor) (func() (string, error), error) {
	if err := prepareCreateParent(scope); err != nil {
		return nil, err
	}
	return func() (string, error) {
		if err := actor("insert-collision", scope, outside); err != nil {
			return "", err
		}
		return "independent same-user actor inserted the destination after parent admission", nil
	}, nil
}

func setupCreateSymlinkSubstitution(scope, outside string, actor fixtureActor) (func() (string, error), error) {
	if err := prepareCreateParent(scope); err != nil {
		return nil, err
	}
	return func() (string, error) {
		if err := actor("insert-symlink", scope, outside); err != nil {
			return "", err
		}
		return "independent same-user actor substituted a symlink at the absent destination", nil
	}, nil
}

func setupCreatePartial(scope, outside string, actor fixtureActor) (func() (string, error), error) {
	if err := prepareCreateParent(scope); err != nil {
		return nil, err
	}
	return func() (string, error) {
		if err := actor("noop", scope, outside); err != nil {
			return "", err
		}
		return "no mutation before release", nil
	}, nil
}

func setupCreateUnknown(scope, outside string, actor fixtureActor) (func() (string, error), error) {
	if err := prepareCreateParent(scope); err != nil {
		return nil, err
	}
	return func() (string, error) {
		if err := actor("noop", scope, outside); err != nil {
			return "", err
		}
		return "no mutation before release", nil
	}, nil
}

func setupCreateLateAlias(scope, outside string, actor fixtureActor) (func() (string, error), error) {
	if err := prepareCreateParent(scope); err != nil {
		return nil, err
	}
	if err := os.Mkdir(filepath.Join(outside, "protected"), 0o700); err != nil {
		return nil, err
	}
	return func() (string, error) {
		if err := actor("noop", scope, outside); err != nil {
			return "", err
		}
		return "worker has parent descriptor; post-create actor checkpoint follows", nil
	}, nil
}

func setupCancel(string, string) (func() (string, error), error) {
	return func() (string, error) { return "", errCancelBeforeRelease }, nil
}
