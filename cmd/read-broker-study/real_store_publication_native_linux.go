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
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
	"github.com/ayeshLK/komposyn/internal/harness/settings"
)

func runRealStorePublicationInvalidationCases(bwrap, workerPath, parent string) ([]caseResult, error) {
	rows := make([]caseResult, 0, 2)
	for _, storeType := range []string{"auth", "settings"} {
		row, err := runRealStorePublicationInvalidationCase(bwrap, workerPath, parent, storeType)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func runRealStorePublicationInvalidationCase(bwrap, workerPath, parent, storeType string) (row caseResult, runErr error) {
	name := "real_" + storeType + "_publication_invalidation_after_capture"
	root := filepath.Join(parent, name)
	if err := os.Mkdir(root, 0o700); err != nil {
		return caseResult{}, err
	}
	target := filepath.Join(root, "ordinary-captured.fixture")
	if err := os.WriteFile(target, []byte("synthetic ordinary bytes captured before owner policy changes\n"), 0o600); err != nil {
		return caseResult{}, err
	}
	storePath := filepath.Join(root, storeType+"-override.json")
	registry, authorizer, session, err := newStudySession(bwrap, workerPath, root, false)
	if err != nil {
		return caseResult{Name: name, Status: "unavailable", Classification: "unavailable", Expected: "actual store mutation changes policy after native worker capture and suppresses stale publication", Observed: "native worker/session setup unavailable", FixtureEffect: "no publication claim", AuthorityA: "no conclusion", AuthorityB: "owner lifecycle only", Detail: "trusted worker setup was unavailable"}, nil
	}
	sessionClosed := false
	defer func() {
		if !sessionClosed {
			if closeErr := closeStudySessionVerified(registry, session); closeErr != nil {
				row = markCleanupUnconfirmed(row, "owner publication case ran but worker join or study-session cleanup was not confirmed; fixture retained")
			}
		}
	}()
	protector := &realStoreObservationProtector{delegate: studyOwnerProtection{session: session, registry: registry}}
	var register func(context.Context) error
	var mutate func(context.Context) error
	switch storeType {
	case "auth":
		store := auth.Store{Path: storePath, Protection: protector}
		register = store.RegisterProtection
		mutate = func(context.Context) error {
			return store.AddAPIKey("synthetic-publication", "openai", "fixture", "synthetic-auth-value", nil)
		}
	case "settings":
		store := settings.Store{Path: storePath, Protection: protector}
		register = store.RegisterProtection
		mutate = func(ctx context.Context) error {
			return store.SaveContext(ctx, settings.Settings{Plugin: settings.DefaultUI})
		}
	default:
		return caseResult{}, errors.New("unsupported real store fixture")
	}
	if err := register(context.Background()); err != nil {
		return caseResult{Name: name, Status: "inconclusive", Classification: "inconclusive", Expected: "actual store route is reserved before the read fixture", Observed: "store startup registration failed", FixtureEffect: "no publication claim", AuthorityA: "no conclusion", AuthorityB: "owner lifecycle only", Detail: "synthetic startup registration failed"}, nil
	}
	invocation := name + "-read"
	if err := authorizer.ApproveOnce(invocation); err != nil {
		return caseResult{}, err
	}
	policyBefore := session.GenerationSnapshot()
	var mutationErr error
	var mutationCalled bool
	session.SetCheckpointHook(func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint != readbrokerstudy.CheckpointAfterCapture || mutationCalled {
			return nil
		}
		mutationCalled = true
		mutationErr = mutate(context.Background())
		return nil
	})
	readCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	result, readErr := session.Read(readCtx, root, target, invocation)
	cancel()
	session.SetCheckpointHook(nil)
	evidence, hasEvidence := evidenceFor(session, invocation)
	policyAfter := session.GenerationSnapshot()
	published := session.PublishedResults()
	storeIdentity, identityErr := identityAt(storePath)
	pinned := identityErr == nil && realStoreIdentityPinned(registry, storeIdentity)
	routeClass, routeProtected := registry.ClassifyRoute(storePath)
	workerJoined := hasEvidence && evidence.WorkerJoined
	staleSuppressed := readErr == nil && result.Outcome == readbrokerstudy.ReadCancelled && hasEvidence && !evidence.Published && len(published) == 0
	onceCount := authorizer.ConsumptionCount(invocation)
	pass := mutationCalled && mutationErr == nil && protector.emptyAtRegistration && protector.identityAtRegistration != (readbrokerstudy.FileIdentity{}) &&
		pinned && routeProtected && (storeType != "settings" || routeClass == readbrokerstudy.ClassSettings) &&
		policyAfter.Policy > policyBefore.Policy && workerJoined && hasEvidence && evidence.ReadStarted && onceCount == 1 && staleSuppressed

	realEvidence := &realStoreEvidence{
		StoreType: storeType, Operation: "registration and actual store mutation after captured worker result",
		EffectiveOverride: true, StartupRegistrationAttempted: true, StartupRegistrationCompleted: true,
		StoreMutationAttempted: mutationCalled, StoreMutationResult: realStoreMutationResult(mutationErr, mutationCalled),
		ReplacementIdentity: identityPtr(protector.identityAtRegistration), ReplacementEmptyAtRegistration: protector.emptyAtRegistration,
		WorkerCapabilityHeld:          hasEvidence && evidence.CapabilityHeld,
		CapabilityIdentityMatchesTemp: hasEvidence && evidence.CapabilityIdentity == protector.identityAtRegistration,
		WorkerJoinConfirmed:           workerJoined, StoreWriteStartedAfterJoin: workerJoined && mutationErr == nil,
		PublishedObjectMatchesRegistered: pinned && storeIdentity == protector.identityAtRegistration,
		EffectiveRouteReserved:           routeProtected, PolicyBefore: policyBefore, PolicyAfter: policyAfter,
		OnceGrantConsumptions: onceCount, StalePublicationSuppressed: staleSuppressed, SensitiveContentPresentInReport: false,
	}
	var capabilityIdentity *readbrokerstudy.FileIdentity
	if hasEvidence {
		capabilityIdentity = identityPtr(evidence.CapabilityIdentity)
	}
	row = caseResult{
		Name: name, Status: "inconclusive", Classification: "inconclusive",
		Expected: "a real native worker captures an ordinary fixture object; the actual auth/settings store registers its empty replacement and advances owner policy before publication; captured bytes are suppressed",
		Observed: string(result.Outcome), WorkerStarted: hasEvidence && evidence.WorkerStarted,
		WorkerReady: hasEvidence && evidence.WorkerReady, WorkerJoinConfirmed: workerJoined,
		CapabilitySent: hasEvidence && evidence.CapabilitySent, CapabilityHeld: hasEvidence && evidence.CapabilityHeld,
		CapabilityIdentity: capabilityIdentity, ReadStarted: hasEvidence && evidence.ReadStarted,
		Published: len(published) > 0, OnceGrantConsumptions: onceCount,
		GenerationsBefore: &policyBefore, GenerationsAfter: &policyAfter, WorkerProbe: probeFor(evidence),
		RealStore: realEvidence, FixtureEffect: "synthetic owner store updated after capture; no stale read result was published; values and paths omitted",
		AuthorityA: "no conclusion", AuthorityB: "owner policy invalidation and publication gate only",
	}
	if hasEvidence {
		row.WorkerOutcome = evidence.Outcome
		row.WorkerGenerations = &evidence.Generations
		row.WorkerEffect = evidence.Effect
		row.WorkerLimitation = evidence.Limitation
		row.ReadEvidencePresent = true
		row.ReadEvidenceCase = evidence.Case
		row.ResultBytesReturned = len(result.Bytes) > 0
	}
	if pass {
		row.Status, row.Classification = "measured_pass", "owner_lifecycle"
		row.Detail = "actual owner route and empty replacement were registered after native capture; worker direct join, policy generation change, one once-grant consumption, identity match, and empty publication sink were confirmed"
	} else {
		row.Detail = "actual store operation, post-capture generation change, worker join, registered object identity, once consumption, or stale-publication suppression was not established"
	}
	if closeErr := closeStudySessionVerified(registry, session); closeErr != nil {
		row = markCleanupUnconfirmed(row, "owner publication case ran but worker join or study-session cleanup was not confirmed; fixture retained")
	}
	sessionClosed = true
	return row, nil
}

type realStoreObservationProtector struct {
	delegate               studyOwnerProtection
	identityAtRegistration readbrokerstudy.FileIdentity
	emptyAtRegistration    bool
}

func (p *realStoreObservationProtector) Protect(ctx context.Context, owner, class, route string, object *os.File) error {
	if object != nil {
		p.identityAtRegistration, _ = studyIdentity(object)
		info, err := object.Stat()
		p.emptyAtRegistration = err == nil && info.Size() == 0
	}
	return p.delegate.Protect(ctx, owner, class, route, object)
}

func realStoreIdentityPinned(registry *readbrokerstudy.Registry, identity readbrokerstudy.FileIdentity) bool {
	for _, entry := range registry.View().Entries {
		if entry.Identity == identity && (entry.Class == readbrokerstudy.ClassCredential || entry.Class == readbrokerstudy.ClassSettings) {
			return true
		}
	}
	return false
}

func realStoreMutationResult(err error, attempted bool) string {
	if !attempted {
		return "not_attempted"
	}
	if err != nil {
		return "failed"
	}
	return "success"
}

var _ auth.SensitiveFileProtector = (*realStoreObservationProtector)(nil)
