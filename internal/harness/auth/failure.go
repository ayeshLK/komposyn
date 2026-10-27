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

import "errors"

type FailureKind string

const (
	FailureConfiguration FailureKind = "configuration"
	FailureCanceled      FailureKind = "canceled"
	FailureTemporary     FailureKind = "temporary"
	FailurePermission    FailureKind = "permission_required"
	FailureInvalidClient FailureKind = "invalid_client"
	FailureTerminal      FailureKind = "terminal_authentication"
)

var ErrInvalidClientConfiguration = errors.New("authentication client configuration is invalid")

// Failure carries a stable category and sanitized guidance. The underlying
// error remains available to programmatic callers through Unwrap.
type Failure struct {
	Kind     FailureKind
	Guidance string
	Cause    error
}

func (e *Failure) Error() string {
	if e == nil {
		return "authentication failed"
	}
	if e.Guidance != "" {
		return e.Guidance
	}
	return "authentication failed"
}

func (e *Failure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func NewFailure(kind FailureKind, guidance string, cause error) error {
	return &Failure{Kind: kind, Guidance: guidance, Cause: cause}
}

func AsFailure(err error) (*Failure, bool) {
	var failure *Failure
	if errors.As(err, &failure) {
		return failure, true
	}
	return nil, false
}
