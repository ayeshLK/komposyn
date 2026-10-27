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

// Package openai owns the Sign in with ChatGPT authorization and token
// lifecycle for the experimental temporary chat.
package openai

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/coreos/go-oidc/v3/oidc"
)

const (
	issuerURL            = "https://auth.openai.com"
	authorizeURL         = "https://auth.openai.com/api/accounts/authorize"
	tokenURL             = "https://auth.openai.com/api/accounts/oauth/token"
	resourceURL          = "https://api.openai.com/v1"
	UsageURL             = "https://chatgpt.com/settings/usage"
	callbackPath         = "/auth/callback"
	registrationClientID = "dynamic_agent_client"
)

var (
	ErrLoginCanceled          = errors.New("OpenAI sign-in was canceled")
	ErrLoginDenied            = errors.New("OpenAI sign-in was declined")
	ErrPlanUsageDisabled      = auth.ErrOAuthPermissionRequired
	ErrAuthClosed             = errors.New("OpenAI authentication manager is closed")
	errAmbiguousTokenExchange = errors.New("OAuth token exchange outcome is ambiguous")
)

const (
	defaultRefreshTimeout = 15 * time.Second
	defaultLockWait       = 30 * time.Second
)

type Manager struct {
	Store          auth.Store
	client         *http.Client
	browser        func(string) error
	listen         func() (net.Listener, error)
	now            func() time.Time
	timeout        time.Duration
	issuer         string
	authorize      string
	token          string
	resource       string
	verifyIdentity func(context.Context, string, string, string) (verifiedIdentity, error)
	attemptMu      sync.Mutex
	attemptID      string
	attemptCancel  context.CancelFunc
	refreshTimeout time.Duration
	lockWait       time.Duration
	refreshMu      sync.Mutex
	refreshClosed  bool
	refreshActive  int
	refreshOps     map[*refreshOperation]struct{}
	refreshDrained chan struct{}
}

type refreshOperation struct {
	ctx             context.Context
	cancel          context.CancelFunc
	mu              sync.Mutex
	exchangeStarted bool
}

type refreshResult struct {
	profile auth.Profile
	access  string
	err     error
}

func (op *refreshOperation) beginExchange(timeout time.Duration) (context.Context, context.CancelFunc, error) {
	op.mu.Lock()
	defer op.mu.Unlock()
	if err := op.ctx.Err(); err != nil {
		return nil, nil, err
	}
	op.exchangeStarted = true
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	return ctx, cancel, nil
}

func (op *refreshOperation) cancelBeforeExchange() {
	op.mu.Lock()
	defer op.mu.Unlock()
	if !op.exchangeStarted {
		op.cancel()
	}
}

var _ auth.OAuthCoordinator = (*Manager)(nil)

type LoginResult = auth.OAuthLoginResult
type LoginAttempt = auth.OAuthLoginAttempt

func New(store auth.Store) *Manager {
	m := &Manager{
		Store:          store,
		client:         &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		browser:        openBrowser,
		listen:         func() (net.Listener, error) { return net.Listen("tcp4", "127.0.0.1:0") },
		now:            time.Now,
		timeout:        5 * time.Minute,
		issuer:         issuerURL,
		authorize:      authorizeURL,
		token:          tokenURL,
		resource:       resourceURL,
		refreshTimeout: defaultRefreshTimeout,
		lockWait:       defaultLockWait,
		refreshOps:     make(map[*refreshOperation]struct{}),
		refreshDrained: make(chan struct{}),
	}
	close(m.refreshDrained)
	m.verifyIdentity = m.validateIdentity
	return m
}

// StartLogin starts a uniquely identified browser authorization attempt. Its
// ID is distinct from the account/profile ID and can be used to cancel safely.
func (m *Manager) StartLogin(parent context.Context, profileID string) (LoginAttempt, error) {
	id, err := randomURLToken(18)
	if err != nil {
		return LoginAttempt{}, errors.New("could not initialize secure sign-in state")
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan auth.OAuthLoginResult, 1)
	m.attemptMu.Lock()
	if m.attemptID != "" {
		m.attemptMu.Unlock()
		cancel()
		return LoginAttempt{}, errors.New("an OpenAI sign-in attempt is already active")
	}
	m.attemptID, m.attemptCancel = id, cancel
	m.attemptMu.Unlock()
	go func() {
		profile, loginErr := m.login(ctx, profileID)
		done <- auth.OAuthLoginResult{Profile: profile, Err: loginErr}
		close(done)
		m.attemptMu.Lock()
		if m.attemptID == id {
			m.attemptID, m.attemptCancel = "", nil
		}
		m.attemptMu.Unlock()
		cancel()
	}()
	return LoginAttempt{ID: id, Done: done}, nil
}

func (m *Manager) CancelLogin(attemptID string) bool {
	m.attemptMu.Lock()
	defer m.attemptMu.Unlock()
	if attemptID == "" || m.attemptID != attemptID || m.attemptCancel == nil {
		return false
	}
	m.attemptCancel()
	return true
}

// Login is a synchronous wrapper for callers that already run in a worker.
func (m *Manager) Login(ctx context.Context, profileID string) (auth.Profile, error) {
	attempt, err := m.StartLogin(ctx, profileID)
	if err != nil {
		return auth.Profile{}, err
	}
	select {
	case result := <-attempt.Done:
		return result.Profile, result.Err
	case <-ctx.Done():
		m.CancelLogin(attempt.ID)
		return auth.Profile{}, ErrLoginCanceled
	}
}

// login performs browser authorization. It commits a candidate only after
// callback, token exchange, and OIDC identity validation all succeed.
func (m *Manager) login(parent context.Context, profileID string) (auth.Profile, error) {
	ctx, cancel := context.WithTimeout(parent, m.timeout)
	defer cancel()
	hostID, err := m.Store.EnsureHostID()
	if err != nil {
		return auth.Profile{}, errors.New("could not prepare local OpenAI host identity")
	}

	var existing auth.Profile
	newRegistration := profileID == ""
	clientID := registrationClientID
	var idHint, loginHint string
	if !newRegistration {
		existing, err = m.Store.Get(profileID)
		if err != nil || existing.Provider != "openai" || existing.Auth.Type != "oauth" || existing.Auth.OAuth == nil {
			return auth.Profile{}, errors.New("selected OpenAI registration is unavailable")
		}
		clientID = existing.Auth.OAuth.ClientID
		idHint = existing.Auth.OAuth.IDToken
		loginHint = existing.Auth.OAuth.Email
	}

	listener, err := m.listen()
	if err != nil {
		return auth.Profile{}, errors.New("could not start the local sign-in callback; check whether a local port is available")
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", port, callbackPath)
	state, err := randomURLToken(32)
	if err != nil {
		return auth.Profile{}, errors.New("could not initialize secure sign-in state")
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		return auth.Profile{}, errors.New("could not initialize secure sign-in state")
	}
	verifier, err := randomURLToken(32)
	if err != nil {
		return auth.Profile{}, errors.New("could not initialize secure sign-in state")
	}
	challengeSum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeSum[:])

	values := url.Values{}
	values.Set("client_id", clientID)
	if newRegistration {
		values.Set("client_id", registrationClientID)
		values.Set("agent_name_hint", "KompoSyn")
	}
	values.Set("ext_agent_host_id", hostID)
	if !newRegistration && idHint != "" {
		values.Set("id_token_hint", idHint)
	}
	if !newRegistration && loginHint != "" {
		values.Set("login_hint", loginHint)
	}
	values.Set("response_type", "code")
	values.Set("redirect_uri", redirectURI)
	values.Set("scope", "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct")
	values.Set("resource", m.resource)
	values.Set("state", state)
	values.Set("nonce", nonce)
	values.Set("code_challenge_method", "S256")
	values.Set("code_challenge", challenge)
	authorizationURL := m.authorize + "?" + values.Encode()

	type callback struct{ code, clientID, state, oauthError string }
	callbackCh := make(chan callback, 1)
	var callbackOnce sync.Once
	callbackServer := &http.Server{ReadHeaderTimeout: 3 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != callbackPath || r.RemoteAddr == "" || r.Host != fmt.Sprintf("127.0.0.1:%d", port) {
			http.Error(w, "Invalid authorization callback.", http.StatusBadRequest)
			return
		}
		remote, _, splitErr := net.SplitHostPort(r.RemoteAddr)
		if splitErr != nil || net.ParseIP(remote) == nil || !net.ParseIP(remote).IsLoopback() {
			http.Error(w, "Invalid authorization callback.", http.StatusBadRequest)
			return
		}
		if ctx.Err() != nil {
			http.Error(w, "This sign-in attempt is no longer active.", http.StatusGone)
			return
		}
		query := r.URL.Query()
		result := callback{code: query.Get("code"), clientID: query.Get("client_id"), state: query.Get("state"), oauthError: query.Get("error")}
		if len(query["state"]) != 1 || len(query["code"]) > 1 || len(query["client_id"]) > 1 || len(query["error"]) > 1 {
			http.Error(w, "Invalid authorization callback.", http.StatusBadRequest)
			return
		}
		callbackOnce.Do(func() { callbackCh <- result })
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "KompoSyn received the sign-in response. You can return to the terminal.\n")
	})}
	serveDone := make(chan struct{})
	go func() { defer close(serveDone); _ = callbackServer.Serve(listener) }()
	defer func() {
		shutdown, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = callbackServer.Shutdown(shutdown)
		<-serveDone
	}()
	if err := ctx.Err(); err != nil {
		return auth.Profile{}, ErrLoginCanceled
	}
	if err := m.browser(authorizationURL); err != nil {
		return auth.Profile{}, errors.New("could not open the system browser; configure a default browser and retry sign-in")
	}

	var returned callback
	select {
	case <-ctx.Done():
		if errors.Is(parent.Err(), context.Canceled) {
			return auth.Profile{}, ErrLoginCanceled
		}
		return auth.Profile{}, errors.New("OpenAI sign-in timed out; start a new attempt")
	case returned = <-callbackCh:
	}
	if returned.state == "" || returned.state != state {
		return auth.Profile{}, errors.New("OpenAI sign-in callback did not match this attempt")
	}
	if returned.oauthError != "" {
		if returned.oauthError == "access_denied" {
			return auth.Profile{}, ErrLoginDenied
		}
		return auth.Profile{}, errors.New("OpenAI authorization did not complete")
	}
	if returned.code == "" {
		return auth.Profile{}, errors.New("OpenAI sign-in callback omitted its authorization code")
	}
	issuedClientID := returned.clientID
	if issuedClientID == "" && !newRegistration {
		issuedClientID = clientID
	}
	if issuedClientID == "" || (!newRegistration && issuedClientID != clientID) || issuedClientID == registrationClientID {
		return auth.Profile{}, errors.New("OpenAI returned an invalid registration identity")
	}
	tokens, err := m.exchangeCode(ctx, issuedClientID, returned.code, verifier, redirectURI)
	if err != nil {
		return auth.Profile{}, err
	}
	identity, err := m.verifyIdentity(ctx, issuedClientID, tokens.IDToken, nonce)
	if err != nil {
		return auth.Profile{}, errors.New("OpenAI identity token could not be validated")
	}
	if identity.subject == "" {
		return auth.Profile{}, errors.New("OpenAI identity token omitted its subject")
	}
	if !newRegistration && identity.subject != existing.Auth.OAuth.Subject {
		return auth.Profile{}, errors.New("OpenAI returned a different account; the saved registration was not changed")
	}

	credential := &auth.OAuthCredential{
		ClientID: issuedClientID, Issuer: m.issuer, Subject: identity.subject,
		Email: identity.email, HostID: hostID, IDToken: tokens.IDToken,
		AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken,
		TokenType: tokens.TokenType, Scopes: tokens.Scopes, ExpiresAt: tokens.ExpiresAt,
	}
	profile := auth.Profile{Provider: "openai", Label: identity.email, Auth: auth.AuthConfig{Type: "oauth", OAuth: credential}}
	if profile.Label == "" {
		profile.Label = "ChatGPT account"
	}
	if newRegistration {
		profile.ID, err = newProfileID()
		if err != nil {
			return auth.Profile{}, errors.New("could not create local OpenAI profile identity")
		}
		profile.Revision, err = auth.NewOAuthRevision()
		if err != nil {
			return auth.Profile{}, err
		}
		if ctx.Err() != nil {
			return auth.Profile{}, ErrLoginCanceled
		}
		if err = m.Store.AddOAuthContext(ctx, profile); err != nil {
			if ctx.Err() != nil {
				return auth.Profile{}, ErrLoginCanceled
			}
			return auth.Profile{}, errors.New("validated OpenAI registration could not be saved")
		}
	} else {
		profile.ID, profile.Label, profile.Revision = existing.ID, existing.Label, existing.Revision
		credential.LifecycleGeneration = existing.Auth.OAuth.LifecycleGeneration
		if profile.Label == "" {
			profile.Label = identity.email
		}
		if ctx.Err() != nil {
			return auth.Profile{}, ErrLoginCanceled
		}
		if err = m.Store.ReplaceOAuthContext(ctx, profile.ID, existing.Revision, profile); err != nil {
			if ctx.Err() != nil {
				return auth.Profile{}, ErrLoginCanceled
			}
			return auth.Profile{}, errors.New("saved OpenAI registration changed before sign-in completed")
		}
		profile.Auth.OAuth.LifecycleGeneration++
	}
	return profile, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	StatusCode   int    `json:"-"`
}

func (m *Manager) exchangeCode(ctx context.Context, clientID, code, verifier, redirectURI string) (auth.OAuthTokens, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", clientID)
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", redirectURI)
	form.Set("resource", m.resource)
	response, err := m.postForm(ctx, m.token, form)
	if err != nil {
		return auth.OAuthTokens{}, err
	}
	if response.Error != "" {
		return auth.OAuthTokens{}, errors.New("OpenAI authorization code was rejected; start a new sign-in")
	}
	return m.tokensFromResponse(response)
}

func (m *Manager) tokensFromResponse(r tokenResponse) (auth.OAuthTokens, error) {
	if r.AccessToken == "" || r.RefreshToken == "" || r.IDToken == "" || !strings.EqualFold(r.TokenType, "Bearer") || r.ExpiresIn <= 0 {
		return auth.OAuthTokens{}, errors.New("OpenAI returned an incomplete OAuth token response")
	}
	scopes := strings.Fields(r.Scope)
	return auth.OAuthTokens{IDToken: r.IDToken, AccessToken: r.AccessToken, RefreshToken: r.RefreshToken, TokenType: "Bearer", Scopes: scopes, ExpiresAt: m.now().Add(time.Duration(r.ExpiresIn) * time.Second)}, nil
}

func (m *Manager) postForm(ctx context.Context, endpoint string, values url.Values) (tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return tokenResponse{}, errors.New("could not prepare OpenAI authentication request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.client.Do(req)
	if err != nil {
		return tokenResponse{}, errors.Join(errAmbiguousTokenExchange, errors.New("OpenAI authentication service could not be reached"))
	}
	defer resp.Body.Close()
	var parsed tokenResponse
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&parsed); err != nil {
		if resp.StatusCode >= http.StatusInternalServerError || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
			return tokenResponse{}, errors.Join(errAmbiguousTokenExchange, errors.New("OpenAI returned an invalid authentication response"))
		}
		return tokenResponse{}, fmt.Errorf("OpenAI authentication failed (HTTP %d)", resp.StatusCode)
	}
	parsed.StatusCode = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode >= http.StatusInternalServerError {
			return parsed, nil
		}
		if parsed.Error != "" {
			return parsed, nil
		}
		return tokenResponse{}, fmt.Errorf("OpenAI authentication failed (HTTP %d)", resp.StatusCode)
	}
	return parsed, nil
}

type verifiedIdentity struct{ subject, email string }

func (m *Manager) validateIdentity(ctx context.Context, clientID, raw, nonce string) (verifiedIdentity, error) {
	if raw == "" {
		return verifiedIdentity{}, errors.New("identity token is empty")
	}
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, m.client), m.issuer)
	if err != nil {
		return verifiedIdentity{}, err
	}
	token, err := provider.Verifier(&oidc.Config{ClientID: clientID}).Verify(ctx, raw)
	if err != nil {
		return verifiedIdentity{}, err
	}
	if token.Nonce != nonce {
		return verifiedIdentity{}, errors.New("identity token nonce did not match")
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := token.Claims(&claims); err != nil {
		return verifiedIdentity{}, err
	}
	email := ""
	if claims.EmailVerified {
		email = claims.Email
	}
	return verifiedIdentity{subject: token.Subject, email: email}, nil
}

// Resolve refreshes an expiring OAuth profile and returns the currently valid
// access token only to the caller performing one provider operation. Refresh
// work is owned by the manager: callers may stop waiting at any point, but a
// token exchange that has started continues under its independent timeout and
// persists its replacement before the auth-file lock is released.
func (m *Manager) Resolve(ctx context.Context, profileID string) (auth.Profile, string, error) {
	if err := ctx.Err(); err != nil {
		return auth.Profile{}, "", err
	}
	resultCh, err := m.startRefresh(ctx, profileID)
	if err != nil {
		return auth.Profile{}, "", err
	}
	if err := ctx.Err(); err != nil {
		return auth.Profile{}, "", err
	}
	select {
	case <-ctx.Done():
		return auth.Profile{}, "", ctx.Err()
	case result := <-resultCh:
		if err := ctx.Err(); err != nil {
			return auth.Profile{}, "", err
		}
		return result.profile, result.access, result.err
	}
}

func (m *Manager) startRefresh(ctx context.Context, profileID string) (<-chan refreshResult, error) {
	lockCtx, cancel := context.WithTimeout(ctx, m.lockWait)
	op := &refreshOperation{ctx: lockCtx, cancel: cancel}
	resultCh := make(chan refreshResult, 1)

	m.refreshMu.Lock()
	if m.refreshClosed {
		m.refreshMu.Unlock()
		cancel()
		return nil, ErrAuthClosed
	}
	if m.refreshActive == 0 {
		m.refreshDrained = make(chan struct{})
	}
	m.refreshActive++
	m.refreshOps[op] = struct{}{}
	m.refreshMu.Unlock()

	go func() {
		defer m.finishRefresh(op)
		profile, err := m.Store.RefreshOAuthContext(op.ctx, profileID, m.now, func(current auth.OAuthCredential) (auth.OAuthTokens, error) {
			return m.refreshTokens(op, current)
		})
		if err != nil {
			resultCh <- refreshResult{err: err}
			return
		}
		if profile.Auth.OAuth == nil || !hasOAuthScope(profile.Auth.OAuth.Scopes, "chatgpt.tokens.use.direct") {
			resultCh <- refreshResult{profile: profile, err: ErrPlanUsageDisabled}
			return
		}
		resultCh <- refreshResult{profile: profile, access: profile.Auth.OAuth.AccessToken}
	}()
	return resultCh, nil
}

func (m *Manager) refreshTokens(op *refreshOperation, current auth.OAuthCredential) (auth.OAuthTokens, error) {
	ctx, cancel, err := op.beginExchange(m.refreshTimeout)
	if err != nil {
		return auth.OAuthTokens{}, err
	}
	defer cancel()
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", current.ClientID)
	form.Set("refresh_token", current.RefreshToken)
	form.Set("resource", m.resource)
	response, err := m.postForm(ctx, m.token, form)
	if err != nil {
		if errors.Is(err, errAmbiguousTokenExchange) {
			return auth.OAuthTokens{}, errors.Join(auth.ErrOAuthRefreshOutcomeUnknown, err)
		}
		return auth.OAuthTokens{}, err
	}
	if response.StatusCode >= http.StatusInternalServerError && response.Error != "server_error" && response.Error != "temporarily_unavailable" {
		return auth.OAuthTokens{}, auth.ErrOAuthRefreshOutcomeUnknown
	}
	switch response.Error {
	case "invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused":
		return auth.OAuthTokens{}, auth.ErrOAuthReconnectRequired
	case "invalid_client":
		return auth.OAuthTokens{}, errors.New("OpenAI OAuth client configuration is invalid")
	case "":
	default:
		return auth.OAuthTokens{}, errors.New("OpenAI could not refresh this account")
	}
	response.IDToken = "" // retain the last validated ID token as a login hint
	tokens, err := m.tokensFromRefresh(response, m.now())
	if err != nil {
		return auth.OAuthTokens{}, errors.Join(auth.ErrOAuthRefreshOutcomeUnknown, err)
	}
	return tokens, nil
}

// Close rejects new refresh operations, cancels operations still waiting to
// start an exchange, and waits for exchanges already in progress to persist or
// reach their independent timeout.
func (m *Manager) Close(ctx context.Context) error {
	m.refreshMu.Lock()
	m.refreshClosed = true
	for op := range m.refreshOps {
		op.cancelBeforeExchange()
	}
	drained := m.refreshDrained
	m.refreshMu.Unlock()
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) finishRefresh(op *refreshOperation) {
	m.refreshMu.Lock()
	delete(m.refreshOps, op)
	m.refreshActive--
	if m.refreshActive == 0 {
		close(m.refreshDrained)
	}
	m.refreshMu.Unlock()
	op.cancel()
}

func (m *Manager) tokensFromRefresh(r tokenResponse, now time.Time) (auth.OAuthTokens, error) {
	if r.AccessToken == "" || r.RefreshToken == "" || !strings.EqualFold(r.TokenType, "Bearer") || r.ExpiresIn <= 0 {
		return auth.OAuthTokens{}, errors.New("OpenAI returned an incomplete token refresh")
	}
	return auth.OAuthTokens{AccessToken: r.AccessToken, RefreshToken: r.RefreshToken, TokenType: "Bearer", Scopes: strings.Fields(r.Scope), ExpiresAt: now.Add(time.Duration(r.ExpiresIn) * time.Second)}, nil
}

type DisconnectResult = auth.OAuthDisconnectResult

// Disconnect attempts remote refresh-token revocation, then always removes
// local tokens. removeRegistration controls whether the client mapping remains.
func (m *Manager) Disconnect(ctx context.Context, profileID string, removeRegistration bool) (DisconnectResult, error) {
	profile, err := m.Store.Get(profileID)
	if err != nil {
		return DisconnectResult{}, err
	}
	if profile.Auth.Type != "oauth" || profile.Auth.OAuth == nil {
		return DisconnectResult{}, errors.New("profile is not an OpenAI OAuth registration")
	}
	remoteConfirmed := false
	remoteErr := errors.New("remote revocation was not confirmed")
	if profile.Auth.OAuth.RefreshToken != "" {
		remoteConfirmed = m.revoke(ctx, *profile.Auth.OAuth)
		if remoteConfirmed {
			remoteErr = nil
		} else {
			remoteErr = errors.New("OpenAI remote revocation was not confirmed; disconnect the app in ChatGPT Settings if needed")
		}
	} else {
		remoteErr = nil
	}
	if removeRegistration {
		err = m.Store.Remove(profileID)
	} else {
		err = m.Store.ClearOAuthTokens(profileID)
	}
	if err != nil {
		return DisconnectResult{RemoteRevocationConfirmed: remoteConfirmed, LocalRegistrationRemoved: false}, err
	}
	result := DisconnectResult{RemoteRevocationConfirmed: remoteConfirmed, LocalRegistrationRemoved: removeRegistration, LocalTokensRemoved: true}
	if remoteErr != nil {
		return result, remoteErr
	}
	return result, nil
}

func (m *Manager) OpenUsage() error {
	if err := m.browser(UsageURL); err != nil {
		return errors.New("could not open ChatGPT usage settings in the system browser")
	}
	return nil
}

func (m *Manager) revoke(ctx context.Context, credential auth.OAuthCredential) bool {
	wellKnown := strings.TrimRight(m.issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnown, nil)
	if err != nil {
		return false
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return false
	}
	var discovery struct {
		Issuer             string `json:"issuer"`
		RevocationEndpoint string `json:"revocation_endpoint"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&discovery)
	_ = resp.Body.Close()
	if decodeErr != nil || resp.StatusCode != http.StatusOK || discovery.Issuer != m.issuer || discovery.RevocationEndpoint == "" {
		return false
	}
	endpoint, err := url.Parse(discovery.RevocationEndpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil {
		return false
	}
	form := url.Values{"token": {credential.RefreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {credential.ClientID}}
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(form.Encode()))
		if err != nil {
			return false
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response, callErr := m.client.Do(req)
		if callErr == nil {
			status := response.StatusCode
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			if status == http.StatusOK {
				return true
			}
			if status < 500 {
				return false
			}
		}
		if attempt == 2 {
			return false
		}
		delay := time.Duration(attempt+1) * 250 * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
	return false
}

func hasOAuthScope(scopes []string, target string) bool {
	for _, scope := range scopes {
		if scope == target {
			return true
		}
	}
	return false
}

func randomURLToken(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func newProfileID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "openai-chatgpt-" + base64.RawURLEncoding.EncodeToString(b), nil
}

func openBrowser(target string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command, args = "open", []string{target}
	case "windows":
		command, args = "rundll32", []string{"url.dll,FileProtocolHandler", target}
	default:
		command, args = "xdg-open", []string{target}
	}
	cmd := exec.Command(command, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
