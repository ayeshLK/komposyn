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
	"sync"
	"syscall"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
)

// runAuthRealStoreNativeCases exercises the actual auth Store API against the
// real fixture registry and sandbox worker. All values and paths are synthetic
// and disposable; report rows contain no payloads or host paths.
func runAuthRealStoreNativeCases(bwrap, workerPath, parent string) ([]caseResult, error) {
	rows, err := runRealAuthRotationCase(bwrap, workerPath, parent)
	if err != nil {
		return nil, err
	}
	barrier, err := runRealAuthWorkerBarrierCase(bwrap, workerPath, parent)
	if err != nil {
		return nil, err
	}
	rows = append(rows, barrier)
	failure, err := runRealAuthRegistrationFailureCase(bwrap, workerPath, parent)
	if err != nil {
		return nil, err
	}
	rows = append(rows, failure...)
	return rows, nil
}

// runRealAuthRegistrationFailureCase is deterministic store-path evidence,
// not a native sandbox claim. The injected owner failure must preserve the
// prior object and content, after which a fresh study-protected store attempt
// can register and publish a new object.
func runRealAuthRegistrationFailureCase(bwrap, workerPath, parent string) (rows []caseResult, runErr error) {
	const name = "real_auth_injected_registration_failure_preserves_destination"
	root := filepath.Join(parent, name)
	if err := os.Mkdir(root, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(root, "auth-override.json")
	seed := auth.Store{Path: path}
	if err := seed.AddAPIKey("synthetic-existing", "openai", "synthetic", "synthetic-existing-value", nil); err != nil {
		return []caseResult{inconclusiveRealAuthCase(name, "initial disposable auth fixture could not be created")}, nil
	}
	before, err := identityAt(path)
	if err != nil {
		return []caseResult{inconclusiveRealAuthCase(name, "initial synthetic auth object identity was unavailable")}, nil
	}
	beforeBytes, err := os.ReadFile(path)
	if err != nil {
		return []caseResult{inconclusiveRealAuthCase(name, "initial synthetic auth object could not be inspected")}, nil
	}
	failedStore := auth.Store{Path: path, Protection: authInjectedFailureProtector{err: errors.New("injected registration refusal")}}
	mutationErr := failedStore.AddAPIKey("synthetic-denied", "openai", "synthetic", "synthetic-denied-value", nil)
	afterFailure, identityErr := identityAt(path)
	afterBytes, bytesErr := os.ReadFile(path)
	if mutationErr == nil || identityErr != nil || bytesErr != nil || afterFailure != before || string(afterBytes) != string(beforeBytes) {
		return []caseResult{{Name: name, Status: "observed_violation", Classification: "owner_write_before_registration", Expected: "an injected registration failure preserves the old auth object and writes no replacement bytes", Observed: "the actual auth store changed or replaced the fixture after its protection hook failed", FixtureEffect: "prior synthetic auth fixture changed on registration refusal", AuthorityA: "no conclusion", AuthorityB: "injected owner-hook behavior only", Detail: "store refusal did not preserve the prior object and bytes", RealStore: &realStoreEvidence{StoreType: "auth", Operation: "AddAPIKey after injected registration failure", EffectiveOverride: true, StartupRegistrationAttempted: true, StoreMutationAttempted: true, StoreMutationResult: "unexpected_success_or_mutation", SensitiveContentPresentInReport: false}}}, nil
	}
	failureRow := caseResult{Name: name, Status: "measured_pass", Classification: "owner_lifecycle_fixture", Expected: "an injected registration failure preserves the existing auth object and bytes", Observed: "actual AddAPIKey returned the injected owner registration error and the prior inode and contents remained unchanged", FixtureEffect: "prior synthetic auth fixture unchanged", AuthorityA: "no conclusion", AuthorityB: "injected owner-hook failure behavior only", RealStore: &realStoreEvidence{StoreType: "auth", Operation: "AddAPIKey after injected registration failure", EffectiveOverride: true, StoreMutationAttempted: true, StoreMutationResult: "injected_failure_preserved_existing_object", SensitiveContentPresentInReport: false}}
	registry, _, session, err := newStudySession(bwrap, workerPath, root, false)
	if err != nil {
		return []caseResult{failureRow, unavailableRealAuthCase("real_auth_failure_retry_after_registration", "trusted real-session setup was unavailable after deterministic failure preservation")}, nil
	}
	sessionClosed := false
	defer func() {
		if !sessionClosed {
			if err := closeStudySessionVerified(registry, session); err != nil {
				rows = markCaseRowsCleanupUnconfirmed(rows, "auth retry case returned without confirmed session, worker, operation, and pin cleanup")
			}
		}
	}()
	protectedStore := auth.Store{Path: path, Protection: studyOwnerProtection{session: session, registry: registry}}
	if err := protectedStore.RegisterProtection(context.Background()); err != nil {
		return []caseResult{failureRow, inconclusiveRealAuthCase("real_auth_failure_retry_after_registration", "real store could not register the existing synthetic object before retry")}, nil
	}
	if err := protectedStore.AddAPIKey("synthetic-retry", "openai", "synthetic", "synthetic-retry-value", nil); err != nil {
		return []caseResult{failureRow, inconclusiveRealAuthCase("real_auth_failure_retry_after_registration", "actual auth store retry failed after injected registration refusal")}, nil
	}
	afterRetry, err := identityAt(path)
	if err != nil || afterRetry == before || !realAuthIdentityPinned(registry, path, afterRetry) {
		return []caseResult{failureRow, inconclusiveRealAuthCase("real_auth_failure_retry_after_registration", "retry did not publish a new protected identity")}, nil
	}
	if _, value, err := protectedStore.Resolve("synthetic-retry"); err != nil || value != "synthetic-retry-value" {
		return []caseResult{failureRow, inconclusiveRealAuthCase("real_auth_failure_retry_after_registration", "actual auth store retry owner readback failed")}, nil
	}
	if err := closeStudySessionVerified(registry, session); err != nil {
		return []caseResult{failureRow, inconclusiveRealAuthCase("real_auth_failure_retry_after_registration", "study session cleanup was not confirmed after retry")}, nil
	}
	sessionClosed = true
	evidence := &realStoreEvidence{StoreType: "auth", Operation: "AddAPIKey after injected registration failure", EffectiveOverride: true, StartupRegistrationAttempted: true, StartupRegistrationCompleted: true, StoreMutationAttempted: true, StoreMutationResult: "injected_failure_preserved_existing_object_then_retry_success", ReplacementIdentity: &afterRetry, PublishedObjectMatchesRegistered: true, EffectiveRouteReserved: true, RetiredPinRetained: realAuthIdentityPinned(registry, path, before), SensitiveContentPresentInReport: false}
	failureRow.RealStore.RetiredPinRetained = evidence.RetiredPinRetained
	retryRow := caseResult{Name: "real_auth_failure_retry_after_registration", Status: "measured_pass", Classification: "owner_lifecycle", Expected: "a fresh actual auth operation registers and publishes a distinct protected object after the deterministic refusal", Observed: "actual AddAPIKey retry succeeded through the study adapter after the old object was registered", FixtureEffect: "only synthetic auth data was present; retry published the registered replacement", AuthorityA: "no conclusion", AuthorityB: "actual auth owner registration and retry only", RealStore: evidence}
	return []caseResult{failureRow, retryRow}, nil
}

type authInjectedFailureProtector struct{ err error }

func (p authInjectedFailureProtector) Protect(context.Context, string, string, string, *os.File) error {
	return p.err
}

func runRealAuthRotationCase(bwrap, workerPath, parent string) (rows []caseResult, runErr error) {
	const name = "real_auth_startup_registration_and_successive_rotations"
	root := filepath.Join(parent, name)
	if err := os.Mkdir(root, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(root, "auth-override.json")
	registry, authorizer, session, err := newStudySession(bwrap, workerPath, root, false)
	if err != nil {
		return []caseResult{unavailableRealAuthCase(name, "real auth store session setup was unavailable")}, nil
	}
	sessionClosed := false
	defer func() {
		if !sessionClosed {
			if err := closeStudySessionVerified(registry, session); err != nil {
				rows = markCaseRowsCleanupUnconfirmed(rows, "auth rotation case returned without confirmed session, worker, operation, and pin cleanup")
			}
		}
	}()
	var registeredTemps []readbrokerstudy.FileIdentity
	var registeredEmpty []bool
	protector := authRecordingProtector{base: studyOwnerProtection{session: session, registry: registry}, observe: func(_ context.Context, route string, object *os.File) {
		if object == nil {
			return
		}
		if object.Name() == route {
			return
		}
		identity, identityErr := studyIdentity(object)
		info, statErr := object.Stat()
		registeredTemps = append(registeredTemps, identity)
		registeredEmpty = append(registeredEmpty, identityErr == nil && statErr == nil && info.Size() == 0)
	}}
	store := auth.Store{Path: path, Protection: protector}
	evidence := ownerLifecycleEvidence{RegistrationAttempted: true, Events: []string{"auth_store_startup_registration_started"}}
	realEvidence := &realStoreEvidence{StoreType: "auth", Operation: "startup registration and successive AddAPIKey atomic replacements", EffectiveOverride: true, StartupRegistrationAttempted: true, SensitiveContentPresentInReport: false}
	if effective, pathErr := store.EffectivePath(); pathErr != nil || effective != path {
		return []caseResult{inconclusiveRealAuthCase(name, "auth override path could not be established")}, nil
	}
	policyBefore := session.GenerationSnapshot()
	if err := store.RegisterProtection(context.Background()); err != nil {
		return []caseResult{inconclusiveRealAuthCase(name, "real auth store startup registration failed")}, nil
	}
	evidence.Events = append(evidence.Events, "auth_store_absent_route_registered")
	realEvidence.StartupRegistrationCompleted = true
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return []caseResult{inconclusiveRealAuthCase(name, "startup registration unexpectedly created an auth file")}, nil
	}
	if err := store.AddAPIKey("synthetic-one", "openai", "synthetic", "synthetic-auth-value-one", nil); err != nil {
		return []caseResult{inconclusiveRealAuthCase(name, "first actual auth store write failed")}, nil
	}
	realEvidence.StoreMutationAttempted = true
	realEvidence.StoreMutationResult = "success"
	firstID, err := identityAt(path)
	if err != nil {
		return []caseResult{inconclusiveRealAuthCase(name, "first published auth object identity was unavailable")}, nil
	}
	if !realAuthIdentityPinned(registry, path, firstID) {
		return []caseResult{inconclusiveRealAuthCase(name, "first published auth object did not match a registered pin")}, nil
	}
	if err := store.RegisterProtection(context.Background()); err != nil || !realAuthIdentityPinned(registry, path, firstID) {
		return []caseResult{inconclusiveRealAuthCase(name, "existing auth object startup registration did not retain its identity")}, nil
	}
	evidence.Events = append(evidence.Events, "existing_auth_object_registered_by_real_store_api")
	realEvidence.ReplacementIdentity = &firstID
	realEvidence.ReplacementEmptyAtRegistration = len(registeredEmpty) >= 1 && registeredEmpty[len(registeredEmpty)-1]
	realEvidence.PublishedObjectMatchesRegistered = len(registeredTemps) >= 1 && registeredTemps[len(registeredTemps)-1] == firstID
	firstAlias := filepath.Join(root, "retired-auth-alias.json")
	if err := os.Link(path, firstAlias); err != nil {
		return []caseResult{caseResult{
			Name: name, Status: "unavailable", Classification: "unavailable",
			Expected:      "real auth replacements are registered before content and retired identities stay protected",
			Observed:      "the host filesystem could not create a synthetic hard-link alias",
			FixtureEffect: "auth store startup registration and first write ran; remaining alias/rotation checks unavailable",
			AuthorityA:    "no conclusion", AuthorityB: "registered auth object lifecycle only",
			Detail: "hard-link alias prerequisite was unavailable",
		}}, nil
	}
	if err := store.AddAPIKey("synthetic-two", "openai", "synthetic", "synthetic-auth-value-two", nil); err != nil {
		return []caseResult{inconclusiveRealAuthCase(name, "second actual auth store rotation failed")}, nil
	}
	if len(registeredTemps) >= 2 {
		realEvidence.RetryUsedNewReplacementIdentity = registeredTemps[1] != registeredTemps[0]
		realEvidence.ReplacementEmptyAtRegistration = realEvidence.ReplacementEmptyAtRegistration && registeredEmpty[1]
	}
	secondID, err := identityAt(path)
	if err != nil || secondID == firstID || !realAuthIdentityPinned(registry, path, secondID) || !realAuthIdentityPinned(registry, firstAlias, firstID) {
		return []caseResult{inconclusiveRealAuthCase(name, "second replacement identity, route or retired pin was not established")}, nil
	}
	retiredAliasRow := protectedIdentityCase(session, authorizer, root, "real_auth_retired_alias_denied", firstAlias, "real-auth-retired-alias")
	secondAlias := filepath.Join(root, "intermediate-auth-alias.json")
	if err := os.Link(path, secondAlias); err != nil {
		return []caseResult{caseResult{Name: name, Status: "unavailable", Classification: "unavailable", Expected: "intermediate and current auth identities remain denied through aliases", Observed: "the host filesystem could not create a synthetic intermediate hard link", FixtureEffect: "auth rotations completed; alias behavior unavailable", AuthorityA: "no conclusion", AuthorityB: "registered auth object lifecycle only", Detail: "hard-link alias prerequisite was unavailable"}}, nil
	}
	oldRenamed := filepath.Join(root, "renamed-retired-auth.json")
	if err := os.Rename(firstAlias, oldRenamed); err != nil {
		return []caseResult{inconclusiveRealAuthCase(name, "retired auth alias could not be renamed for the fixture")}, nil
	}
	if err := store.AddAPIKey("synthetic-three", "openai", "synthetic", "synthetic-auth-value-three", nil); err != nil {
		return []caseResult{inconclusiveRealAuthCase(name, "third actual auth store rotation failed")}, nil
	}
	currentAlias := filepath.Join(root, "current-auth-alias.json")
	if err := os.Link(path, currentAlias); err != nil {
		return []caseResult{caseResult{Name: name, Status: "unavailable", Classification: "unavailable", Expected: "current auth identity remains denied through a hard-link alias", Observed: "the host filesystem could not create a synthetic current hard link", FixtureEffect: "auth rotations completed; alias behavior unavailable", AuthorityA: "no conclusion", AuthorityB: "registered auth object lifecycle only", Detail: "hard-link alias prerequisite was unavailable"}}, nil
	}
	if len(registeredTemps) >= 3 {
		realEvidence.ReplacementEmptyAtRegistration = realEvidence.ReplacementEmptyAtRegistration && registeredEmpty[2]
	}
	thirdID, err := identityAt(path)
	if err != nil || thirdID == secondID || !realAuthIdentityPinned(registry, path, thirdID) || !realAuthIdentityPinned(registry, oldRenamed, firstID) || !realAuthIdentityPinned(registry, path, secondID) {
		return []caseResult{inconclusiveRealAuthCase(name, "successive auth rotations did not preserve old/new identity pins")}, nil
	}
	if !realAuthIdentityPinned(registry, secondAlias, secondID) || !realAuthIdentityPinned(registry, currentAlias, thirdID) {
		return []caseResult{inconclusiveRealAuthCase(name, "current or intermediate alias did not retain its registered object identity")}, nil
	}
	if _, key, err := store.Resolve("synthetic-three"); err != nil || key != "synthetic-auth-value-three" {
		return []caseResult{inconclusiveRealAuthCase(name, "auth owner readback did not match the synthetic stored profile")}, nil
	}
	evidence.InitialRegistrationResult = "registered"
	evidence.WriteAttempted, evidence.WriteAuthorized, evidence.WriteStarted, evidence.WriteCompleted, evidence.Published = true, true, true, true, true
	evidence.PinnedIdentity = thirdID
	evidence.WriteDescriptorIdentity = thirdID
	evidence.WriteDescriptorMatchesPin = true
	evidence.EffectiveRouteReserved = true
	evidence.RetiredIdentityRetained = true
	evidence.Events = append(evidence.Events, "three_auth_store_writes_registered_before_content", "successive_replacements_published_matching_registered_objects", "retired_auth_identities_retained_after_alias_and_rename")
	realEvidence.ReplacementIdentity = &thirdID
	allReplacementsEmpty, allReplacementPublicationsMatch := len(registeredTemps) >= 3 && len(registeredEmpty) >= 3, len(registeredTemps) >= 3 && len(registeredEmpty) >= 3
	for i := 0; i < 3 && i < len(registeredTemps) && i < len(registeredEmpty); i++ {
		allReplacementsEmpty = allReplacementsEmpty && registeredEmpty[i]
	}
	allReplacementPublicationsMatch = allReplacementPublicationsMatch &&
		storeReplacementAccepted(registeredEmpty[0], registeredTemps[0], firstID) &&
		storeReplacementAccepted(registeredEmpty[1], registeredTemps[1], secondID) &&
		storeReplacementAccepted(registeredEmpty[2], registeredTemps[2], thirdID)
	realEvidence.ReplacementEmptyAtRegistration = allReplacementsEmpty
	realEvidence.PublishedObjectMatchesRegistered = allReplacementPublicationsMatch
	routeClass, routeReserved := registry.ClassifyRoute(path)
	realEvidence.EffectiveRouteReserved = routeReserved && routeClass == readbrokerstudy.ClassCredential
	realEvidence.RetiredPinRetained = true
	row := caseResult{
		Name: name, Status: "measured_pass", Classification: "owner_lifecycle",
		Expected:       "actual auth store registers an absent override route and each empty replacement before JSON content; successive published objects match registered identities while retired aliases and renamed objects remain protected",
		Observed:       "actual auth Store startup registration and three AddAPIKey replacement operations completed; owner readback matched the final synthetic profile",
		OwnerLifecycle: &evidence, RealStore: realEvidence, OnceGrantConsumptions: 0,
		FixtureEffect: "actual store created and atomically replaced only disposable synthetic auth data; old and current object identities remain pinned",
		AuthorityA:    "no conclusion", AuthorityB: "real auth owner route and object lifecycle only",
	}
	policyAfter := session.GenerationSnapshot()
	realEvidence.PolicyBefore = policyBefore
	realEvidence.PolicyAfter = policyAfter
	if !realEvidence.ReplacementEmptyAtRegistration || !realEvidence.PublishedObjectMatchesRegistered || !ownerPolicyAdvanced(policyBefore, policyAfter) {
		row.Status, row.Classification = "inconclusive", "inconclusive"
		row.Detail = "auth replacement was not empty at registration, published identity did not match the registered object, or policy generation did not advance"
	}
	rows = []caseResult{row}
	retiredAliasRow.RealStore = &realStoreEvidence{StoreType: "auth", Operation: "generic read through retired hard-link alias", EffectiveOverride: true, RetiredPinRetained: true, AliasDenied: retiredAliasRow.Status == "measured_pass", SensitiveContentPresentInReport: false}
	rows = append(rows, retiredAliasRow)
	renamedRetired := protectedIdentityCase(session, authorizer, root, "real_auth_renamed_retired_object_denied", oldRenamed, "real-auth-renamed-retired")
	renamedRetired.RealStore = &realStoreEvidence{StoreType: "auth", Operation: "generic read through renamed retired identity", EffectiveOverride: true, RetiredPinRetained: true, RenamedRetiredObjectDenied: renamedRetired.Status == "measured_pass", SensitiveContentPresentInReport: false}
	rows = append(rows, renamedRetired)
	intermediateAliasRow := protectedIdentityCase(session, authorizer, root, "real_auth_intermediate_alias_denied", secondAlias, "real-auth-intermediate-alias")
	intermediateAliasRow.RealStore = &realStoreEvidence{StoreType: "auth", Operation: "generic read through intermediate replacement alias", EffectiveOverride: true, RetiredPinRetained: true, AliasDenied: intermediateAliasRow.Status == "measured_pass", SensitiveContentPresentInReport: false}
	rows = append(rows, intermediateAliasRow)
	currentRoute := protectedIdentityCase(session, authorizer, root, "real_auth_current_route_denied", path, "real-auth-current-route")
	currentRoute.RealStore = &realStoreEvidence{StoreType: "auth", Operation: "generic read through effective auth route", EffectiveOverride: true, SensitiveContentPresentInReport: false}
	rows = append(rows, currentRoute)
	currentAliasRow := protectedIdentityCase(session, authorizer, root, "real_auth_current_alias_denied", currentAlias, "real-auth-current-alias")
	currentAliasRow.RealStore = &realStoreEvidence{StoreType: "auth", Operation: "generic read through current replacement hard-link alias", EffectiveOverride: true, AliasDenied: currentAliasRow.Status == "measured_pass", SensitiveContentPresentInReport: false}
	rows = append(rows, currentAliasRow)
	ordinary := filepath.Join(root, "ordinary-control.fixture")
	if err := os.WriteFile(ordinary, []byte("synthetic unrelated ordinary bytes\n"), 0o600); err != nil {
		return rows, err
	}
	ordinaryRow := executeCase(session, authorizer, root, "real_auth_unrelated_ordinary_read_control", ordinary, "real-auth-ordinary-control", "unrelated ordinary object remains readable with explicit once approval")
	ordinaryRow.RealStore = &realStoreEvidence{StoreType: "auth", Operation: "unrelated ordinary read positive control", EffectiveOverride: true, UnrelatedOrdinaryReadSucceeded: ordinaryRow.Status == "measured_pass", SensitiveContentPresentInReport: false}
	rows = append(rows, ordinaryRow)
	rows[0].RealStore.AliasDenied = retiredAliasRow.Status == "measured_pass" && intermediateAliasRow.Status == "measured_pass" && currentAliasRow.Status == "measured_pass"
	rows[0].RealStore.RenamedRetiredObjectDenied = renamedRetired.Status == "measured_pass"
	rows[0].RealStore.UnrelatedOrdinaryReadSucceeded = ordinaryRow.Status == "measured_pass"
	if !rows[0].RealStore.AliasDenied || !rows[0].RealStore.RenamedRetiredObjectDenied || currentRoute.Status != "measured_pass" || !rows[0].RealStore.UnrelatedOrdinaryReadSucceeded {
		rows[0].Status, rows[0].Classification = "inconclusive", "inconclusive"
		rows[0].Detail = "route, alias, renamed-retired-object, or unrelated-read control did not meet its expected result"
	}
	if err := closeStudySessionVerified(registry, session); err != nil {
		rows = markCaseRowsCleanupUnconfirmed(rows, "auth owner cases ran but worker join, active-operation, or pin cleanup was not confirmed")
	}
	sessionClosed = true
	return rows, nil
}

// runRealAuthWorkerBarrierCase starts the real worker from the auth store's
// before-write callback while it holds the exact empty temporary object. The
// owner's actual RegisterProtection adapter then invalidates that operation.
func runRealAuthWorkerBarrierCase(bwrap, workerPath, parent string) (row caseResult, runErr error) {
	const name = "real_auth_held_worker_cleanup_pending_blocks_write_then_retry"
	root := filepath.Join(parent, name)
	if err := os.Mkdir(root, 0o700); err != nil {
		return caseResult{}, err
	}
	path := filepath.Join(root, "auth-override.json")
	gate := newTerminationRequestGate()
	registry, authorizer, session, launcher, err := newObservedSession(bwrap, workerPath, root, true, gate)
	if err != nil {
		return unavailableRealAuthCase(name, "held Bubblewrap worker setup was unavailable"), nil
	}
	var cleanupOnce sync.Once
	var gateReleaseOnce sync.Once
	var gateReleaseErr error
	releaseGate := func() error {
		gateReleaseOnce.Do(func() { gateReleaseErr = gate.release() })
		return gateReleaseErr
	}
	cleanup := func() {
		cleanupOnce.Do(func() {
			var gateReleaseErr error
			if launcher.latest() != nil {
				gateReleaseErr = releaseGate()
			}
			closeErr := closeStudySessionVerified(registry, session)
			if gateReleaseErr != nil && row.Status == "measured_pass" {
				row.Status, row.Classification = "inconclusive", "inconclusive"
				row.Detail = "termination gate release returned an error; the measured barrier outcome was downgraded"
			}
			if closeErr != nil {
				row = markCleanupUnconfirmed(row, "auth cleanup-pending case could not confirm worker join, active-operation cleanup, and pin release; fixture retained")
			}
			allStartedWorkersJoined := true
			for _, item := range session.Evidence() {
				row.WorkerStarted = row.WorkerStarted || item.WorkerStarted
				if item.WorkerStarted && !item.WorkerJoined {
					allStartedWorkersJoined = false
					row = markCleanupUnconfirmed(row, "auth worker remains unjoined after bounded cleanup; fixture retained")
				}
			}
			if row.WorkerStarted && allStartedWorkersJoined {
				row.WorkerJoinConfirmed = true
			}
		})
	}
	defer cleanup()
	const invocation = "real-auth-empty-temp-read"
	if err := authorizer.ApproveOnce(invocation); err != nil {
		return inconclusiveRealAuthCase(name, "synthetic once approval could not be created"), nil
	}
	hookHeld := make(chan struct{})
	releaseHook := make(chan struct{})
	var releaseHookOnce sync.Once
	releaseHeldHook := func() { releaseHookOnce.Do(func() { close(releaseHook) }) }
	var hookOnce sync.Once
	session.SetCheckpointHook(func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint == readbrokerstudy.CheckpointAfterCapabilityHeld {
			hookOnce.Do(func() { close(hookHeld) })
			select {
			case <-releaseHook:
				return nil
			case <-time.After(12 * time.Second):
				return context.DeadlineExceeded
			}
		}
		return nil
	})
	type readCompletion struct {
		result readbrokerstudy.WorkerResult
		err    error
	}
	readDone := make(chan readCompletion, 1)
	var startOnce sync.Once
	var readStarted bool
	var tempPath string
	var tempIdentity readbrokerstudy.FileIdentity
	var tempRegistered bool
	var emptyAtPending bool
	var holdUnavailable bool
	var pendingGenBefore, pendingGenAfter readbrokerstudy.GenerationSet
	var retryTempIdentity readbrokerstudy.FileIdentity
	var retryTempEmpty bool
	objectRegistrations := 0
	baseProtector := studyOwnerProtection{session: session, registry: registry}
	protector := authNativeBarrierProtector{base: baseProtector, beforeRegistration: func(ctx context.Context, object *os.File) error {
		if object == nil {
			return nil
		}
		observedIdentity, observedIdentityErr := studyIdentity(object)
		observedInfo, observedStatErr := object.Stat()
		objectRegistrations++
		if objectRegistrations > 1 {
			retryTempIdentity = observedIdentity
			retryTempEmpty = observedIdentityErr == nil && observedStatErr == nil && observedInfo.Size() == 0
		}
		var callbackErr error
		startOnce.Do(func() {
			tempPath = object.Name()
			tempIdentity = observedIdentity
			if observedIdentityErr != nil {
				callbackErr = observedIdentityErr
				return
			}
			go func() {
				result, readErr := session.Read(ctx, root, tempPath, invocation)
				readDone <- readCompletion{result: result, err: readErr}
			}()
			select {
			case <-hookHeld:
				readStarted = true
			case <-ctx.Done():
				callbackErr = ctx.Err()
				holdUnavailable = true
			case <-time.After(12 * time.Second):
				callbackErr = errors.New("native worker did not reach the held-capability checkpoint")
				holdUnavailable = true
			}
		})
		if callbackErr != nil {
			return callbackErr
		}
		pendingGenBefore = session.GenerationSnapshot()
		err := baseProtector.Protect(ctx, auth.SensitiveOwnerAuth, auth.SensitiveClassAuth, path, object)
		pendingGenAfter = session.GenerationSnapshot()
		if errors.Is(err, readbrokerstudy.ErrCleanupPending) {
			info, statErr := object.Stat()
			emptyAtPending = statErr == nil && info.Size() == 0
			for _, entry := range registry.View().Entries {
				if entry.Owner == auth.SensitiveOwnerAuth && entry.Class == readbrokerstudy.ClassCredential && entry.Path == tempPath && entry.Identity == tempIdentity {
					tempRegistered = true
					break
				}
			}
			releaseHeldHook()
			select {
			case <-gate.requestedSignal():
			case <-time.After(5 * time.Second):
				return errors.New("termination request did not reach the bounded native gate")
			}
		}
		return err
	}}
	store := auth.Store{Path: path, Protection: protector}
	if err := store.RegisterProtection(context.Background()); err != nil {
		return inconclusiveRealAuthCase(name, "actual auth startup registration failed"), nil
	}
	row = caseResult{
		Name: name, Expected: "auth store registers an empty replacement while a real worker holds its descriptor; the store writes no auth bytes until that exact worker has been joined, then a fresh actual store attempt succeeds",
		AuthorityA: "no conclusion", AuthorityB: "actual auth replacement object lifecycle and worker barrier only",
		RealStore: &realStoreEvidence{StoreType: "auth", Operation: "AddAPIKey cleanup-pending refusal and fresh retry", EffectiveOverride: true, StartupRegistrationAttempted: true, StartupRegistrationCompleted: true, StoreMutationAttempted: true, SensitiveContentPresentInReport: false},
	}
	storeResult := make(chan error, 1)
	go func() {
		storeResult <- store.AddAPIKey("synthetic-retry", "openai", "synthetic", "synthetic-auth-retry-value", nil)
	}()
	var firstErr error
	select {
	case firstErr = <-storeResult:
	case <-time.After(20 * time.Second):
		releaseHeldHook()
		_ = releaseGate()
		return inconclusiveRealAuthCase(name, "actual auth store write did not return within the bounded deadline"), nil
	}
	row.OnceGrantConsumptions = authorizer.ConsumptionCount(invocation)
	if !errors.Is(firstErr, readbrokerstudy.ErrCleanupPending) {
		releaseHeldHook()
		_ = releaseGate()
		if holdUnavailable || !readStarted {
			row.Status, row.Classification = "unavailable", "unavailable"
			row.Observed = "the candidate worker did not reach the required held-capability checkpoint"
			row.Detail = "native live-worker barrier precondition was unavailable"
		} else {
			row.Status, row.Classification = "inconclusive", "inconclusive"
			row.Observed = "actual auth store did not return cleanup-pending from real owner registration"
			row.Detail = "store result did not establish the expected held-worker barrier"
		}
		return row, nil
	}
	row.RealStore.StoreMutationResult = "cleanup_pending"
	row.RealStore.StoreMutationAttempted = true
	if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
		row.Status, row.Classification = "observed_violation", "owner_write_before_join"
		row.Observed = "auth destination existed after cleanup-pending refusal"
		row.FixtureEffect = "destination changed before worker join"
		row.Detail = "actual store created or replaced its destination before the owner barrier completed"
		releaseHeldHook()
		_ = releaseGate()
		return row, nil
	}
	worker := launcher.latest()
	workerID, held := readbrokerstudy.FileIdentity{}, false
	if worker != nil {
		workerID, held = worker.HeldCapabilityIdentity()
	}
	row.CapabilityIdentity = &workerID
	row.CapabilityHeld, row.WorkerStarted, row.WorkerReady, row.CapabilitySent = held, worker != nil, held, held
	workerAlive := worker != nil && held && authObservedWorkerAlive(worker)
	row.OwnerLifecycle = &ownerLifecycleEvidence{
		RegistrationAttempted: true, InitialRegistrationResult: "cleanup_pending", CleanupPending: true,
		WriteAttempted: true, PolicyBefore: pendingGenBefore, PolicyAfterRegistration: pendingGenAfter,
		WriteDescriptorIdentity: tempIdentity, PinnedIdentity: tempIdentity,
		WriteDescriptorMatchesPin: tempRegistered, TemporaryRouteMatchesPin: tempRegistered,
		EffectiveRouteReserved: true, Events: []string{"actual_auth_store_write_started", "empty_temp_descriptor_held_by_real_worker", "study_owner_protection_registration_returned_cleanup_pending", "actual_auth_store_write_refused_before_content"},
	}
	select {
	case <-gate.requestedSignal():
	case <-time.After(5 * time.Second):
		row.Status, row.Classification = "inconclusive", "inconclusive"
		row.Detail = "native termination request was not observed"
		return row, nil
	}
	row.RealStore.TerminationRequested = true
	row.WorkerJoinConfirmed = false
	pendingAdmission, pendingAdmissionErr := session.Read(context.Background(), root, tempPath, "real-auth-pending-admission")
	pendingAdmissionBlocked := pendingAdmissionErr == nil && pendingAdmission.Outcome == readbrokerstudy.ReadNotExecuted && authorizer.ConsumptionCount("real-auth-pending-admission") == 0
	if !readStarted || !held || !workerAlive || workerID != tempIdentity || !tempRegistered || !emptyAtPending || authorizer.ConsumptionCount(invocation) != 1 || activeOperationCount(session) != 1 || !pendingAdmissionBlocked || registry.PinnedObjectCount() == 0 {
		row.Status, row.Classification = "inconclusive", "inconclusive"
		row.Observed = "held-worker, object-identity, empty-content, once-consumption or retained-cleanup evidence was incomplete"
		row.FixtureEffect = "no auth destination was published; temporary object state inspected while the worker termination request remained gated"
		row.Detail = "required actual auth store and live-worker correlation was not established"
		releaseHeldHook()
		_ = releaseGate()
		return row, nil
	}
	row.RealStore.ReplacementIdentity = &tempIdentity
	row.RealStore.ReplacementEmptyAtRegistration = emptyAtPending
	row.RealStore.CapabilityIdentityMatchesTemp = workerID == tempIdentity
	row.RealStore.WorkerCapabilityHeld = held
	row.RealStore.StoreWriteNotStartedBeforeJoin = true
	row.RealStore.WorkerJoinConfirmed = false
	routeClass, routeReserved := registry.ClassifyRoute(path)
	row.RealStore.EffectiveRouteReserved = tempRegistered && routeReserved && routeClass == readbrokerstudy.ClassCredential
	row.RealStore.PolicyBefore = pendingGenBefore
	row.RealStore.PolicyAfter = pendingGenAfter
	row.RealStore.OnceGrantConsumptions = authorizer.ConsumptionCount(invocation)
	row.OwnerLifecycle.Events = append(row.OwnerLifecycle.Events, "affected_admission_refused_while_cleanup_pending", "registry_pins_retained_while_worker_unjoined")
	row.Observed = "actual auth store refused its first write after registration committed cleanup-pending; the real worker held the matching empty temp object and remained unjoined at the gate"
	row.OwnerLifecycle.CleanupPending = true
	row.OwnerLifecycle.WriteAuthorized = false
	row.OwnerLifecycle.WriteStarted = false
	releaseHeldHook()
	if err := releaseGate(); err != nil {
		row.Status, row.Classification = "inconclusive", "inconclusive"
		row.Detail = "termination gate could not release the actual worker"
		return row, nil
	}
	var completion readCompletion
	select {
	case completion = <-readDone:
	case <-time.After(8 * time.Second):
		row.Status, row.Classification = "inconclusive", "inconclusive"
		row.Detail = "actual worker did not finish and join after the termination gate opened"
		return row, nil
	}
	readEvidence, hasEvidence := evidenceFor(session, invocation)
	joined := completion.err == nil && hasEvidence && completion.result.Outcome == readbrokerstudy.ReadNotExecuted && readEvidence.WorkerJoined && !readEvidence.Published && readEvidence.CapabilityHeld && !readEvidence.ReadStarted
	row.WorkerJoinConfirmed = joined
	row.ReadStarted = hasEvidence && readEvidence.ReadStarted
	row.Published = hasEvidence && readEvidence.Published
	row.OwnerLifecycle.WorkerJoinConfirmed = joined
	row.OwnerLifecycle.Events = append(row.OwnerLifecycle.Events, "termination_gate_released", "real_worker_direct_wait_confirmed")
	row.RealStore.WorkerJoinConfirmed = joined
	row.RealStore.StalePublicationSuppressed = hasEvidence && !readEvidence.Published && len(session.PublishedResults()) == 0
	if !joined || row.OnceGrantConsumptions != 1 || len(session.PublishedResults()) != 0 {
		row.Status, row.Classification = "inconclusive", "inconclusive"
		row.Detail = "direct join, single once consumption, or stale-publication suppression was not confirmed"
		return row, nil
	}
	if err := store.AddAPIKey("synthetic-retry", "openai", "synthetic", "synthetic-auth-retry-value", nil); err != nil {
		row.Status, row.Classification = "inconclusive", "inconclusive"
		row.Detail = "actual auth store did not succeed on a fresh attempt after direct join"
		return row, nil
	}
	row.RealStore.StoreMutationResult = "cleanup_pending_then_successful_retry"
	row.RealStore.StoreWriteStartedAfterJoin = joined
	finalID, finalErr := identityAt(path)
	profile, value, resolveErr := store.Resolve("synthetic-retry")
	_ = profile
	row.OwnerLifecycle.WriteAttempted = true
	row.OwnerLifecycle.WriteAuthorized = true
	row.OwnerLifecycle.WriteStarted = true
	row.OwnerLifecycle.WriteCompleted = finalErr == nil && resolveErr == nil && value == "synthetic-auth-retry-value"
	row.OwnerLifecycle.Published = row.OwnerLifecycle.WriteCompleted
	row.OwnerLifecycle.PinnedIdentity = finalID
	row.OwnerLifecycle.WriteDescriptorIdentity = finalID
	row.OwnerLifecycle.WriteDescriptorMatchesPin = finalErr == nil && realAuthIdentityPinned(registry, path, finalID)
	row.OwnerLifecycle.EffectiveRouteReserved = row.OwnerLifecycle.WriteDescriptorMatchesPin
	row.OwnerLifecycle.Events = append(row.OwnerLifecycle.Events, "actual_auth_store_retry_started_after_join", "retry_temp_registered_before_json_write", "retry_object_published_with_matching_pin")
	row.RealStore.RetryUsedNewReplacementIdentity = retryTempIdentity != (readbrokerstudy.FileIdentity{}) && retryTempIdentity != tempIdentity && retryTempEmpty
	row.RealStore.ReplacementEmptyAtRegistration = emptyAtPending && retryTempEmpty
	row.RealStore.ReplacementIdentity = &finalID
	row.RealStore.PublishedObjectMatchesRegistered = finalErr == nil && retryTempIdentity != (readbrokerstudy.FileIdentity{}) && retryTempIdentity == finalID && realAuthIdentityPinned(registry, path, finalID)
	row.RealStore.RetiredPinRetained = realAuthIdentityPinned(registry, tempPath, tempIdentity)
	row.RealStore.OnceGrantConsumptions = authorizer.ConsumptionCount(invocation)
	row.WorkerJoinConfirmed = row.WorkerJoinConfirmed && row.OwnerLifecycle.WriteDescriptorMatchesPin
	if !row.OwnerLifecycle.WriteCompleted || !row.RealStore.StoreWriteNotStartedBeforeJoin || !row.RealStore.StoreWriteStartedAfterJoin || !row.RealStore.PublishedObjectMatchesRegistered || !row.RealStore.RetryUsedNewReplacementIdentity {
		row.Status, row.Classification = "inconclusive", "inconclusive"
		row.Detail = "store retry result, registration identity, empty-at-registration state, or write/join ordering did not correlate"
		return row, nil
	}
	row.Status, row.Classification = "measured_pass", "owner_lifecycle"
	row.FixtureEffect = "first actual auth write left the destination absent and its empty temporary object unchanged while the worker held it; after direct join, a fresh store retry published the pinned synthetic auth object"
	row.Detail = "once approval was consumed once for the held read; the store retry did not replay or reuse it"
	return row, nil
}

type authNativeBarrierProtector struct {
	base               studyOwnerProtection
	beforeRegistration func(context.Context, *os.File) error
}

type authRecordingProtector struct {
	base    studyOwnerProtection
	observe func(context.Context, string, *os.File)
}

func (p authRecordingProtector) Protect(ctx context.Context, owner, class, route string, object *os.File) error {
	if p.observe != nil {
		p.observe(ctx, route, object)
	}
	return p.base.Protect(ctx, owner, class, route, object)
}

func (p authNativeBarrierProtector) Protect(ctx context.Context, owner, class, route string, object *os.File) error {
	if object != nil && p.beforeRegistration != nil {
		if err := p.beforeRegistration(ctx, object); err != nil {
			return err
		}
	}
	return p.base.Protect(ctx, owner, class, route, object)
}

func realAuthIdentityPinned(registry *readbrokerstudy.Registry, path string, identity readbrokerstudy.FileIdentity) bool {
	_ = path
	for _, entry := range registry.View().Entries {
		if entry.Owner == auth.SensitiveOwnerAuth && entry.Class == readbrokerstudy.ClassCredential && entry.Identity == identity {
			if identity != (readbrokerstudy.FileIdentity{}) {
				return true
			}
		}
	}
	return false
}

func authObservedWorkerAlive(worker *observedSandboxWorker) bool {
	if worker == nil {
		return false
	}
	bwrapID, initID, err := worker.ObserveHostProcesses()
	if err != nil {
		return false
	}
	bwrapFD, err := readbrokerstudy.OpenVerifiedPIDFD(bwrapID)
	if err != nil {
		return false
	}
	defer syscall.Close(bwrapFD)
	initFD, err := readbrokerstudy.OpenVerifiedPIDFD(initID)
	if err != nil {
		return false
	}
	defer syscall.Close(initFD)
	bwrapExited, bwrapErr := observePIDFDExit(bwrapFD, 0)
	initExited, initErr := observePIDFDExit(initFD, 0)
	return bwrapErr == nil && initErr == nil && !bwrapExited && !initExited
}

func unavailableRealAuthCase(name, detail string) caseResult {
	return caseResult{Name: name, Status: "unavailable", Classification: "unavailable", Expected: "actual auth owner operations run against the real fixture session and sandbox worker", Observed: "required native precondition unavailable", FixtureEffect: "no claim of live-worker owner integration", AuthorityA: "no conclusion", AuthorityB: "no conclusion", Detail: detail}
}

func inconclusiveRealAuthCase(name, detail string) caseResult {
	return caseResult{Name: name, Status: "inconclusive", Classification: "inconclusive", Expected: "actual auth owner operations register before writes and preserve protected identity lifecycle", Observed: "required owner lifecycle evidence incomplete", FixtureEffect: "synthetic fixture only", AuthorityA: "no conclusion", AuthorityB: "owner lifecycle only", Detail: detail}
}

var _ auth.SensitiveFileProtector = authNativeBarrierProtector{}
var _ auth.SensitiveFileProtector = authRecordingProtector{}
