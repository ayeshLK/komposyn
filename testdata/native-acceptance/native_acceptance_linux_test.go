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
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	rb "github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
	"github.com/ayeshLK/komposyn/internal/harness/settings"
)

// The test binary is also the exact fixed worker image. Bootstrap precedes
// testing initialization and never initializes credentials or providers.
func init() {
	if len(os.Args) > 1 && os.Args[1] == "--readbrokerstudy-bootstrap" {
		handled, code := WorkerBootstrap(os.Args[1:])
		if handled {
			os.Exit(code)
		}
	}
}

type nativeRecord struct {
	FirstUpdateCleanupPending        bool                  `json:"first_update_cleanup_pending"`
	FirstRefusalPreservedDestination bool                  `json:"first_refusal_preserved_destination"`
	FreshRetrySucceeded              bool                  `json:"fresh_retry_succeeded"`
	FreshRetryDistinctObject         bool                  `json:"fresh_retry_distinct_object"`
	RootTransitionCleanupPending     bool                  `json:"root_transition_cleanup_pending"`
	FreshReadAfterRootChange         bool                  `json:"fresh_read_after_root_change"`
	FailureKind                      string                `json:"failure_kind,omitempty"`
	EmptyReplacementObserved         bool                  `json:"empty_replacement_observed"`
	WorkerJoinedBeforeOwnerWrite     bool                  `json:"worker_joined_before_owner_write"`
	RetiredAliasRefused              bool                  `json:"retired_alias_refused"`
	VerifiedReturnedBytes            bool                  `json:"verified_returned_bytes"`
	Case                             string                `json:"case"`
	Status                           string                `json:"status"`
	CleanupConfirmed                 bool                  `json:"cleanup_confirmed"`
	Backend                          *rb.BackendInspection `json:"backend,omitempty"`
	Evidence                         []rb.ReadEvidence     `json:"evidence,omitempty"`
	Limitations                      []string              `json:"limitations"`
}

func nativeReport(t *testing.T) *nativeRecord {
	t.Helper()
	r := &nativeRecord{Case: t.Name(), Status: "measured_pass", Limitations: []string{"Test-only fixture rebinding of the deny-only authorizer; shipped generic reads remain refused.", "No provider network call; publication means the coordinator's private result sink.", "This run covers the reported bind mode and fixed worker only, not all descendants/platforms or natural unjoinable processes."}}
	t.Cleanup(func() {
		if t.Failed() {
			r.Status = "failed"
		}
		if dir := os.Getenv("KOMPOSYN_NATIVE_RECORDS"); dir != "" {
			b, err := json.MarshalIndent(r, "", "  ")
			if err == nil {
				err = os.WriteFile(filepath.Join(dir, t.Name()+".json"), append(b, '\n'), 0600)
			}
			if err != nil {
				t.Errorf("could not persist native evidence")
			}
		}
	})
	return r
}
func nativeContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	t.Cleanup(cancel)
	return ctx
}

type nativeFixture struct {
	tracked      *nativeTrackedLauncher
	service      *Service
	impl         *linuxImplementation
	auth         auth.Store
	settings     settings.Store
	grants       *rb.OnceGrantFixture
	root, target string
}

func newNativeFixture(t *testing.T, r *nativeRecord) *nativeFixture {
	t.Helper()
	backend := os.Getenv("KOMPOSYN_NATIVE_BWRAP")
	if backend == "" {
		backend = "/usr/bin/bwrap"
	}
	inspection := rb.InspectBubblewrap(backend)
	inspection.Path = "<configured-backend>"
	r.Backend = &inspection
	if !inspection.Trusted || inspection.Error != "" {
		r.Status = "unavailable"
		r.CleanupConfirmed = true
		t.Skip("trusted native backend unavailable; see evidence")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal("could not identify test worker")
	}
	launcher, err := rb.NewWorkerLauncherWithCapabilityHold(backend, exe)
	if err != nil {
		r.Status = "unavailable"
		r.CleanupConfirmed = true
		t.Skip("native launcher unavailable")
	}
	registry, err := rb.NewRegistry()
	if err != nil {
		launcher.Close()
		t.Fatal("registry creation failed")
	}
	tracked := &nativeTrackedLauncher{delegate: launcher}
	impl, err := composeLinuxImplementation(registry, tracked, nil)
	if err != nil {
		registry.Close()
		launcher.Close()
		t.Fatal("service composition failed")
	}
	// No read has been prepared/launched. Replace only the internal deny-only
	// coordinator in this test image with an explicitly once-approved fixture
	// coordinator. Both use the service's real registry and composition inventory.
	// The original idle coordinator owns no unique OS resources.
	grants := rb.NewOnceGrantFixture()
	session, err := rb.NewHostCoordinator(registry, rb.NewBroker(registry), tracked, grants, impl.inventory)
	if err != nil {
		impl.Close(context.Background())
		t.Fatal("fixture coordinator creation failed")
	}
	impl.session = session
	f := &nativeFixture{tracked: tracked, service: &Service{impl: impl}, impl: impl, grants: grants, root: t.TempDir()}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r.Evidence = session.Evidence()
		// Exclude raw device/inode identities and any unsanitized diagnostic strings.
		for i := range r.Evidence {
			r.Evidence[i].CapabilityIdentity = rb.FileIdentity{}
			r.Evidence[i].Probe.Details = nil
			r.Evidence[i].Effect = ""
			r.Evidence[i].Limitation = ""
		}
		err := f.service.Close(ctx)
		r.CleanupConfirmed = err == nil
		if err != nil {
			t.Errorf("native service cleanup unconfirmed")
		}
	})
	f.auth = auth.Store{Path: filepath.Join(f.root, "private-auth", "auth.json"), Protection: f.service}
	f.settings = settings.Store{Path: filepath.Join(f.root, "private-settings", "settings.json"), Protection: f.service}
	if err = f.auth.RegisterProtection(nativeContext(t)); err != nil {
		t.Fatal("auth protection registration failed")
	}
	if err = f.settings.RegisterProtection(nativeContext(t)); err != nil {
		t.Fatal("settings protection registration failed")
	}
	if err = f.auth.AddAPIKey("fixture", "openai", "fixture", "synthetic-native-key", nil); err != nil {
		t.Fatal("synthetic auth write failed")
	}
	if err = f.settings.SaveContext(nativeContext(t), settings.Settings{Plugin: settings.DefaultUI}); err != nil {
		t.Fatal("synthetic settings write failed")
	}
	f.target = filepath.Join(f.root, "ordinary.txt")
	if err = os.WriteFile(f.target, []byte("synthetic-native-read-control"), 0600); err != nil {
		t.Fatal("fixture write failed")
	}
	if err = session.SelectRoot(f.root); err != nil {
		t.Fatal("root selection failed")
	}
	return f
}
func (f *nativeFixture) read(t *testing.T, id, target string) rb.WorkerResult {
	t.Helper()
	if err := f.grants.ApproveOnce(id); err != nil {
		t.Fatal("once fixture approval failed")
	}
	r, err := f.impl.session.Read(nativeContext(t), f.root, target, id)
	if err != nil {
		t.Fatal("coordinator read call failed")
	}
	return r
}
func lastNativeEvidence(t *testing.T, f *nativeFixture) rb.ReadEvidence {
	t.Helper()
	e := f.impl.session.Evidence()
	if len(e) == 0 {
		t.Fatal("missing worker evidence")
	}
	return e[len(e)-1]
}
func TestNativeShippedGenericReadGate(t *testing.T) {
	r := nativeReport(t)
	s, err := NewHostService(os.Getenv("KOMPOSYN_NATIVE_BWRAP"))
	if err != nil {
		t.Fatal("host service creation failed")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r.CleanupConfirmed = s.Close(ctx) == nil
		if !r.CleanupConfirmed {
			t.Error("host service cleanup failed")
		}
	})
	if !errors.Is(s.PrepareGenericRead(nativeContext(t), t.TempDir(), "unclassified"), ErrUnknownRootData) {
		t.Fatal("shipped generic read gate opened")
	}
}
func TestNativeServicePositiveRead(t *testing.T) {
	r := nativeReport(t)
	f := newNativeFixture(t, r)
	result := f.read(t, "positive", f.target)
	e := lastNativeEvidence(t, f)
	if result.Outcome != rb.ReadSucceeded || !bytes.Equal(result.Bytes, []byte("synthetic-native-read-control")) || f.grants.ConsumptionCount("positive") != 1 || !e.WorkerStarted || !e.WorkerReady || !e.CapabilitySent || !e.CapabilityHeld || !e.ReadStarted || !e.WorkerJoined || !e.Published || len(f.impl.session.PublishedResults()) != 1 {
		t.Fatal("positive read lacked actual bytes or complete worker lifecycle")
	}
	r.VerifiedReturnedBytes = true
}
func TestNativeServiceProtectedRoutesAndAliases(t *testing.T) {
	r := nativeReport(t)
	f := newNativeFixture(t, r)
	authPath, _ := f.auth.EffectivePath()
	settingsPath, _ := f.settings.EffectivePath()
	alias := filepath.Join(f.root, "auth-alias")
	if err := os.Link(authPath, alias); err != nil {
		t.Fatal("alias fixture creation failed")
	}
	for i, target := range []string{authPath, settingsPath, alias} {
		id := []string{"auth-route", "settings-route", "auth-alias"}[i]
		result := f.read(t, id, target)
		if result.Outcome != rb.ReadRefused || result.Detail != rb.ErrProtected.Error() || len(result.Bytes) > 0 || len(f.impl.session.Evidence()) != 0 || f.grants.ConsumptionCount(id) != 0 {
			t.Fatal("protected target crossed refusal boundary")
		}
	}
}
func TestNativeServiceRotationJoinsHeldWorker(t *testing.T)     { nativeRotation(t, false) }
func TestNativeServiceAuthRotationJoinsHeldWorker(t *testing.T) { nativeRotation(t, true) }
func nativeRotation(t *testing.T, rotateAuth bool) {
	r := nativeReport(t)
	f := newNativeFixture(t, r)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.impl.session.SetCheckpointHook(func(p rb.Checkpoint) error {
		if p == rb.CheckpointAfterCapabilityHeld {
			once.Do(func() { close(entered) })
			select {
			case <-release:
			case <-time.After(15 * time.Second):
				return context.DeadlineExceeded
			}
		}
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := f.grants.ApproveOnce("rotation-held"); err != nil {
		t.Fatal("fixture approval failed")
	}
	done := make(chan rb.WorkerResult, 1)
	go func() { v, _ := f.impl.session.Read(ctx, f.root, f.target, "rotation-held"); done <- v }()
	released := false
	defer func() {
		if !released {
			close(release)
		}
		cancel()
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("worker never confirmed held capability")
	}
	// Keep an alias of the previously registered settings object.
	settingsPath, _ := f.settings.EffectivePath()
	if rotateAuth {
		settingsPath, _ = f.auth.EffectivePath()
	}
	retiredAlias := filepath.Join(f.root, "retired-settings-alias")
	if err := os.Link(settingsPath, retiredAlias); err != nil {
		t.Fatal("retired-alias fixture failed")
	}
	// Observe the actual service protector before the real store writes bytes.
	beforeBytes, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal("could not inspect synthetic destination before first attempt")
	}
	store := f.settings
	observer := &nativeObservedProtector{service: f.service, tracked: f.tracked, record: r}
	store.Protection = observer
	update := func() error {
		if rotateAuth {
			authStore := f.auth
			authStore.Protection = observer
			return authStore.AddAPIKey("rotation", "openai", "fixture-rotation", "synthetic-rotated-value", nil)
		}
		return store.SaveContext(ctx, settings.Settings{Plugin: settings.DefaultUI})
	}
	firstErr := update()
	r.FirstUpdateCleanupPending = errors.Is(firstErr, rb.ErrCleanupPending)
	afterRefusal, readErr := os.ReadFile(settingsPath)
	r.FirstRefusalPreservedDestination = readErr == nil && bytes.Equal(beforeBytes, afterRefusal)
	close(release)
	released = true
	var result rb.WorkerResult
	select {
	case result = <-done:
	case <-ctx.Done():
		t.Fatal("invalidated read did not join")
	}
	if !r.FirstUpdateCleanupPending || !r.FirstRefusalPreservedDestination {
		r.FailureKind = nativeErrorKind(firstErr)
		t.Fatal("first held-worker update did not produce cleanup-pending refusal with unchanged destination")
	}
	if !f.tracked.allJoined() {
		t.Fatal("worker remained unjoined before fresh owner retry")
	}
	if err = update(); err != nil {
		r.FailureKind = nativeErrorKind(err)
		t.Fatal("fresh owner update failed after confirmed join")
	}
	r.FreshRetrySucceeded = true
	if len(observer.opened) != 2 || os.SameFile(observer.opened[0], observer.opened[1]) {
		t.Fatal("retry did not register a distinct newly opened replacement")
	}
	r.FreshRetryDistinctObject = true
	e := lastNativeEvidence(t, f)
	if !e.CapabilityHeld || !e.WorkerJoined || e.ReadStarted || e.Published || len(result.Bytes) > 0 || len(f.impl.session.PublishedResults()) > 0 {
		t.Fatal("held read was released or stale data published during rotation")
	}
	if !r.EmptyReplacementObserved || !r.WorkerJoinedBeforeOwnerWrite {
		t.Fatal("missing empty-object or join-before-write observation")
	}
	result = f.read(t, "retired-settings", retiredAlias)
	if result.Outcome != rb.ReadRefused || result.Detail != rb.ErrProtected.Error() || f.grants.ConsumptionCount("retired-settings") != 0 {
		t.Fatal("retired settings alias lost protection")
	}
	r.RetiredAliasRefused = true

}
func TestNativeServicePostCaptureRevocation(t *testing.T) {
	r := nativeReport(t)
	f := newNativeFixture(t, r)
	f.impl.session.SetCheckpointHook(func(p rb.Checkpoint) error {
		if p == rb.CheckpointAfterCapture {
			return f.impl.session.Revoke("captured")
		}
		return nil
	})
	result := f.read(t, "captured", f.target)
	e := lastNativeEvidence(t, f)
	if !e.ReadStarted || !e.WorkerJoined || e.Published || len(result.Bytes) > 0 || len(f.impl.session.PublishedResults()) > 0 {
		t.Fatal("captured result escaped revocation publication gate")
	}
}

// This wrapper records real worker state; it never replaces worker behavior.
type nativeTrackedLauncher struct {
	mu       sync.Mutex
	delegate rb.WorkerLauncher
	workers  []rb.SandboxWorker
}

func (l *nativeTrackedLauncher) Start(ctx context.Context) (rb.SandboxWorker, error) {
	w, e := l.delegate.Start(ctx)
	if w != nil {
		l.mu.Lock()
		l.workers = append(l.workers, w)
		l.mu.Unlock()
	}
	return w, e
}
func (l *nativeTrackedLauncher) Close() error { return l.delegate.Close() }
func (l *nativeTrackedLauncher) allJoined() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.workers) == 0 {
		return false
	}
	for _, w := range l.workers {
		if !w.Joined() {
			return false
		}
	}
	return true
}

type nativeObservedProtector struct {
	opened  []os.FileInfo
	service *Service
	tracked *nativeTrackedLauncher
	record  *nativeRecord
}

func (p *nativeObservedProtector) Protect(ctx context.Context, owner, class, route string, object *os.File) error {
	if object == nil {
		return errors.New("rotation test did not supply an opened replacement")
	}
	st, e := object.Stat()
	if e != nil || st.Size() != 0 {
		return errors.New("replacement was not empty before registration")
	}
	p.opened = append(p.opened, st)
	if e = p.service.Protect(ctx, owner, class, route, object); e != nil {
		return e
	}
	st, e = object.Stat()
	if e != nil || st.Size() != 0 {
		return errors.New("replacement acquired bytes before registration returned")
	}
	if !p.tracked.allJoined() {
		return errors.New("actual worker was not joined before owner write")
	}
	p.record.EmptyReplacementObserved = true
	p.record.WorkerJoinedBeforeOwnerWrite = true
	return nil
}

func TestNativeServiceRootChangeWhileHeld(t *testing.T)    { nativeInterruptedHold(t, "root") }
func TestNativeServiceCancellationWhileHeld(t *testing.T)  { nativeInterruptedHold(t, "cancel") }
func TestNativeServiceShutdownRetryWhileHeld(t *testing.T) { nativeInterruptedHold(t, "shutdown") }
func nativeInterruptedHold(t *testing.T, action string) {
	r := nativeReport(t)
	f := newNativeFixture(t, r)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f.impl.session.SetCheckpointHook(func(p rb.Checkpoint) error {
		if p == rb.CheckpointAfterCapabilityHeld {
			once.Do(func() { close(entered) })
			select {
			case <-release:
			case <-time.After(15 * time.Second):
				return context.DeadlineExceeded
			}
		}
		return nil
	})
	if err := f.grants.ApproveOnce("held-lifecycle"); err != nil {
		t.Fatal("fixture approval failed")
	}
	done := make(chan rb.WorkerResult, 1)
	go func() { v, _ := f.impl.session.Read(ctx, f.root, f.target, "held-lifecycle"); done <- v }()
	released := false
	defer func() {
		if !released {
			close(release)
		}
		cancel()
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("worker did not reach held capability checkpoint")
	}
	var nextRoot string
	switch action {
	case "root":
		nextRoot = t.TempDir()
		rootErr := f.impl.session.SelectRoot(nextRoot)
		r.RootTransitionCleanupPending = errors.Is(rootErr, rb.ErrCleanupPending)
		if !r.RootTransitionCleanupPending {
			r.FailureKind = nativeErrorKind(rootErr)
			t.Fatal("held root transition did not report cleanup pending")
		}
	case "cancel":
		cancel()
	case "shutdown":
		stopped, stop := context.WithCancel(context.Background())
		stop()
		if err := f.service.Close(stopped); err == nil {
			t.Fatal("shutdown unexpectedly claimed cleanup before held operation settled")
		}
	}
	close(release)
	released = true
	var result rb.WorkerResult
	select {
	case result = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("interrupted held read did not settle")
	}
	e := lastNativeEvidence(t, f)
	if !e.CapabilityHeld || !e.WorkerJoined || e.ReadStarted || e.Published || len(result.Bytes) > 0 || len(f.impl.session.PublishedResults()) > 0 {
		t.Fatal("interrupted held worker read or published data, or remained unjoined")
	}
	if action == "root" {
		// The root transition already advanced the generation. Its pending barrier
		// clears only when the old operation actually finishes and the child joins.
		if !f.tracked.allJoined() || f.impl.session.ActiveOperationCount() != 0 {
			t.Fatal("root transition did not settle its old operation")
		}
		f.root = nextRoot
		f.target = filepath.Join(nextRoot, "ordinary-fresh-root.txt")
		if err := os.WriteFile(f.target, []byte("synthetic-native-read-control"), 0600); err != nil {
			t.Fatal("fresh-root fixture write failed")
		}
		result = f.read(t, "fresh-root-read", f.target)
		fresh := lastNativeEvidence(t, f)
		if result.Outcome != rb.ReadSucceeded || !bytes.Equal(result.Bytes, []byte("synthetic-native-read-control")) || !fresh.WorkerJoined || !fresh.Published {
			t.Fatal("fresh invocation after root transition did not succeed")
		}
		r.FreshReadAfterRootChange = true
	}
	if action == "shutdown" {
		cleanup, cancelCleanup := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelCleanup()
		if err := f.service.Close(cleanup); err != nil {
			t.Fatal("fresh shutdown retry failed to drain service")
		}
	}
}

func nativeErrorKind(err error) string {
	if err == nil {
		return "none"
	}
	switch {
	case errors.Is(err, rb.ErrCleanupPending):
		return "cleanup_pending"
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, rb.ErrInventoryIncomplete):
		return "inventory_incomplete"
	default:
		return "unexpected_error"
	}
}
