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
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRetainedAdmissionPreparesTheOpenedObjectWithoutDispatch(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.txt")
	if err := os.WriteFile(target, []byte("synthetic original object"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	request := AdmissionRequest{
		ConversationID: "conversation-fixture", TurnID: "turn-fixture", CallID: "call-fixture",
		Root: root, Target: target, RootGeneration: 1, Destination: "provider/model/profile-revision",
	}
	metadata, err := fixture.session.Admit(context.Background(), request)
	if err != nil {
		t.Fatalf("admit exact file: %v", err)
	}
	if metadata.Class != ClassOrdinary || metadata.Identity.Inode == 0 || metadata.Reference == nil || metadata.Binding == ([32]byte{}) {
		t.Fatalf("admission metadata is incomplete: %+v", metadata)
	}
	openedIdentity := metadata.Identity

	moved := filepath.Join(root, "moved-original.txt")
	if err := os.Rename(target, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("replacement object"), 0o600); err != nil {
		t.Fatal(err)
	}
	preparation, err := fixture.session.PrepareAdmitted(context.Background(), metadata.Reference, hex.EncodeToString(metadata.Binding[:]), metadata.Generations.Policy)
	if err != nil {
		t.Fatalf("prepare retained object after pathname replacement: %v", err)
	}
	preparedMetadata := preparation.Metadata()
	if preparedMetadata.Identity != openedIdentity || preparedMetadata.Target != target || preparedMetadata.Reference != metadata.Reference {
		t.Fatalf("preparation changed admitted object: %+v", preparedMetadata)
	}
	if fixture.launcher.starts != 0 || fixture.authorizer.ConsumptionCount("call-fixture") != 0 {
		t.Fatalf("Stage 3 started a worker or consumed study authorization: workers=%d once=%d", fixture.launcher.starts, fixture.authorizer.ConsumptionCount("call-fixture"))
	}
	if err := preparation.Close(context.Background()); err != nil {
		t.Fatalf("retire retained admission: %v", err)
	}
	if fixture.session.(*fixtureSession).admissionCount() != 0 {
		t.Fatal("confirmed retirement left a retained admission record")
	}
}

func TestRetainedAdmissionRequiresCurrentRootAndOwnerCoverage(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.txt")
	if err := os.WriteFile(target, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, root)
	request := AdmissionRequest{ConversationID: "c", TurnID: "t", CallID: "call", Root: root, Target: target, RootGeneration: 1, Destination: "provider/model/profile"}
	metadata, err := fixture.session.Admit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.session.SelectRootGeneration(context.Background(), root, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.session.PrepareAdmitted(context.Background(), metadata.Reference, hex.EncodeToString(metadata.Binding[:]), metadata.Generations.Policy); !errors.Is(err, ErrAdmissionStale) {
		t.Fatalf("preparation with stale root generation=%v", err)
	}
	if fixture.session.(*fixtureSession).admissionCount() != 0 {
		t.Fatal("root transition did not retire the host-only admission")
	}
	request.RootGeneration = 2
	metadata, err = fixture.session.Admit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	ownerUpdate := OwnerRegistration{Owner: "fixture-auth-inventory", Class: ClassCredential, Path: filepath.Join(root, ".auth-next-route")}
	if err := fixture.session.RegisterRotation(ownerUpdate.Owner, []OwnerRegistration{ownerUpdate}); err != nil {
		t.Fatalf("owner transition: %v", err)
	}
	if _, err := fixture.session.PrepareAdmitted(context.Background(), metadata.Reference, hex.EncodeToString(metadata.Binding[:]), metadata.Generations.Policy); !errors.Is(err, ErrAdmissionStale) {
		t.Fatalf("preparation with stale owner coverage=%v", err)
	}
	if fixture.session.(*fixtureSession).admissionCount() != 0 {
		t.Fatal("owner transition did not retire the pending admission")
	}
	if fixture.launcher.starts != 0 {
		t.Fatalf("owner lifecycle test unexpectedly started %d workers", fixture.launcher.starts)
	}
}

func TestAdmissionCancellationAndDeadlineRemainEffectiveAfterLookup(t *testing.T) {
	for _, test := range []struct {
		name    string
		timeout time.Duration
		cancel  bool
	}{
		{name: "caller cancellation", cancel: true},
		{name: "five minute policy deadline represented by trusted short test bound", timeout: 20 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "ordinary.txt")
			if err := os.WriteFile(target, []byte("synthetic"), 0o600); err != nil {
				t.Fatal(err)
			}
			fixture := newSessionFixture(t, root)
			session := fixture.session.(*fixtureSession)
			if test.timeout > 0 {
				session.admissionTimeout = test.timeout
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := AdmissionRequest{ConversationID: "c", TurnID: "t", CallID: "call", Root: root, Target: target, RootGeneration: 1, Destination: "provider/model/profile"}
			metadata, err := session.Admit(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			session.mu.Lock()
			record := session.admissions[metadata.Reference.id]
			lifetimeDone := record.lifetime.Done()
			session.mu.Unlock()
			if test.cancel {
				cancel()
			}
			select {
			case <-lifetimeDone:
			case <-time.After(time.Second):
				t.Fatal("admission lifetime did not end")
			}
			if _, err := session.PrepareAdmitted(context.Background(), metadata.Reference, hex.EncodeToString(metadata.Binding[:]), metadata.Generations.Policy); !errors.Is(err, ErrAdmissionStale) {
				t.Fatalf("preparation after lifetime end=%v, want stale", err)
			}
			deadline := time.Now().Add(time.Second)
			for session.admissionCount() != 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if got := session.admissionCount(); got != 0 {
				t.Fatalf("expired admission was not retired: %d records remain", got)
			}
		})
	}
}

type blockingAdmissionBroker struct {
	base    ReadBroker
	entered chan struct{}
	release chan struct{}
}

func (b *blockingAdmissionBroker) PrepareRead(root, target string, generation GenerationSet) (PreparedRead, error) {
	close(b.entered)
	<-b.release
	return b.base.PrepareRead(root, target, generation)
}

func (b *blockingAdmissionBroker) Revalidate(prepared PreparedRead) error {
	return b.base.Revalidate(prepared)
}

func TestTimedOutAdmissionRetainsOpeningObligationAndClosesLateFD(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "ordinary.txt")
	if err := os.WriteFile(target, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	broker := &blockingAdmissionBroker{base: NewBroker(registry), entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	authorizer := NewOnceGrantFixture()
	launcher := &fakeLauncher{worker: &fakeWorker{}}
	session, err := NewHostCoordinator(registry, broker, launcher, authorizer, NewSyntheticStudyInventory())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(broker.release) })
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = session.Close(ctx)
	})
	if err := session.SelectRoot(root); err != nil {
		t.Fatal(err)
	}
	if err := installTestOwnerCoverage(session, root); err != nil {
		t.Fatal(err)
	}
	request := AdmissionRequest{ConversationID: "c", TurnID: "t", CallID: "call", Root: root, Target: target, RootGeneration: 1, Destination: "provider/model/profile"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := session.Admit(ctx, request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed admission error=%v", err)
	}
	select {
	case <-broker.entered:
	default:
		t.Fatal("bounded lookup was not started")
	}
	if got := session.(*fixtureSession).admissionCount(); got != 1 {
		t.Fatalf("timed lookup obligation count=%d, want retained opening record", got)
	}
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer drainCancel()
	if err := session.InvalidateAdmissions(drainCtx); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("bounded owner drain=%v, want cleanup pending", err)
	}
	releaseOnce.Do(func() { close(broker.release) })
	deadline := time.Now().Add(time.Second)
	for session.(*fixtureSession).admissionCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := session.(*fixtureSession).admissionCount(); got != 0 {
		t.Fatalf("late lookup did not retire after acknowledgement; retained=%d", got)
	}
	if launcher.starts != 0 || authorizer.ConsumptionCount("call") != 0 {
		t.Fatal("timeout/late-FD cleanup started a worker or consumed authorization")
	}
}
