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
	"errors"
)

var ErrOAuthPermissionRequired = errors.New("OAuth account permission is required")

type LoginResult struct {
	Profile   Profile
	ProfileID string
	Status    PublicStatus
	Err       error
}

type OAuthLoginResult = LoginResult

type LoginEventKind string

const (
	LoginStarted   LoginEventKind = "started"
	LoginBrowser   LoginEventKind = "opening_browser"
	LoginCompleted LoginEventKind = "completed"
)

// LoginEvent is transient progress only. Authorization URLs and credentials
// are never included; the injected browser capability receives the URL.
type LoginEvent struct {
	Kind      LoginEventKind
	Message   string
	Succeeded bool
}

type LoginAttempt struct {
	ID     string
	Events <-chan LoginEvent
	Done   <-chan LoginResult
}

type OAuthLoginAttempt = LoginAttempt

type DisconnectResult struct {
	RemoteRevocationConfirmed bool
	LocalRegistrationRemoved  bool
	LocalTokensRemoved        bool
}

type OAuthDisconnectResult = DisconnectResult

type OAuthResolver interface {
	Resolve(context.Context, string) (Profile, string, error)
}

// OAuthCoordinator is the provider-neutral auth lifecycle consumed by the
// temporary chat runtime. Provider-specific protocol details stay in the
// concrete auth implementation.
type LoginCoordinator interface {
	OAuthResolver
	StartLogin(context.Context, string) (OAuthLoginAttempt, error)
	CancelLogin(string) bool
	Disconnect(context.Context, string, bool) (OAuthDisconnectResult, error)
}

type OAuthCoordinator interface {
	LoginCoordinator
	OpenUsage() error
}

// BrowserOpener is the only capability the provider auth implementation uses
// to deliver its transient authorization URL. UI packages need not orchestrate
// OAuth protocol details or receive the URL in public state.
type BrowserOpener interface {
	Open(context.Context, string) error
}

type BrowserOpenerFunc func(context.Context, string) error

func (f BrowserOpenerFunc) Open(ctx context.Context, target string) error { return f(ctx, target) }
