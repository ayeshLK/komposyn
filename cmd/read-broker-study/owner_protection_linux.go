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

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
)

// studyOwnerProtection adapts the real auth/settings store hook to the
// research-only session. It is not wired into cmd/harness or production
// dispatch. A nil object registers the route; a non-nil object registers both
// that route and the exact temporary object before the owner writes it.
type studyOwnerProtection struct {
	session interface {
		RegisterRotation(string, []readbrokerstudy.OwnerRegistration) error
	}
	registry *readbrokerstudy.Registry
}

func (p studyOwnerProtection) Protect(ctx context.Context, owner, class, route string, object *os.File) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.session == nil || p.registry == nil {
		return errors.New("owner protection session is unavailable")
	}
	resourceClass, ok := studyResourceClass(class)
	if !ok {
		return errors.New("unsupported sensitive owner class")
	}
	route, err := filepath.Abs(route)
	if err != nil {
		return err
	}
	registrations := []readbrokerstudy.OwnerRegistration{{Owner: owner, Class: resourceClass, Path: route}}
	var expected readbrokerstudy.FileIdentity
	var objectPath string
	if object != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		objectPath, err = filepath.Abs(object.Name())
		if err != nil {
			return err
		}
		expected, err = studyIdentity(object)
		if err != nil {
			return err
		}
		info, err := object.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("sensitive owner object is not a regular file")
		}
		registrations = append(registrations, readbrokerstudy.OwnerRegistration{Owner: owner, Class: resourceClass, Path: objectPath})
	}
	if err := p.session.RegisterRotation(owner, registrations); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if object != nil {
		found := false
		for _, entry := range p.registry.View().Entries {
			if entry.Owner == owner && entry.Class == resourceClass && entry.Path == objectPath && entry.Identity == expected {
				found = true
				break
			}
		}
		if !found {
			return errors.New("registered sensitive object identity does not match the open owner descriptor")
		}
	}
	return nil
}

func studyResourceClass(class string) (readbrokerstudy.ResourceClass, bool) {
	switch class {
	case auth.SensitiveClassAuth:
		return readbrokerstudy.ClassCredential, true
	case auth.SensitiveClassSettings:
		return readbrokerstudy.ClassSettings, true
	default:
		return "", false
	}
}
