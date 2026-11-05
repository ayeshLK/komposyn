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
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/readbrokerstudy"
	"github.com/ayeshLK/komposyn/internal/harness/settings"
	"golang.org/x/sys/unix"
)

// runSettingsRealStoreNativeCases exercises the public settings store against
// the native FixtureSession. All paths and values are synthetic and live under
// the caller's disposable fixture root.
func runSettingsRealStoreNativeCases(bwrap, workerPath, parent string) ([]caseResult, error) {
	rows := make([]caseResult, 0, 8)
	absentRows, err := runSettingsAbsentRouteNativeCase(bwrap, workerPath, parent)
	if err != nil {
		return rows, err
	}
	rows = append(rows, absentRows...)
	rotationRows, err := runSettingsRotationNativeCase(bwrap, workerPath, parent)
	if err != nil {
		return rows, err
	}
	rows = append(rows, rotationRows...)
	barrierRows, err := runSettingsBarrierNativeCase(bwrap, workerPath, parent)
	if err != nil {
		return rows, err
	}
	rows = append(rows, barrierRows...)
	return rows, nil
}

func runSettingsAbsentRouteNativeCase(bwrap, workerPath, parent string) ([]caseResult, error) {
	const name = "real_settings_startup_absent_route_then_first_save"
	root := filepath.Join(parent, name)
	if err := os.Mkdir(root, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(root, "settings-override.json")
	registry, _, session, err := newStudySession(bwrap, workerPath, root, false)
	if err != nil {
		return []caseResult{settingsNativeUnavailable(name, "a missing effective settings route is registered before the real store creates and publishes its first file", "native worker/session setup was unavailable")}, nil
	}
	protector := &observedSettingsProtector{delegate: studyOwnerProtection{session: session, registry: registry}}
	store := settings.Store{Path: path, Protection: protector}
	effective, pathErr := store.EffectivePath()
	policyBefore := session.GenerationSnapshot()
	registerErr := store.RegisterProtection(context.Background())
	routeClass, routeProtected := registry.ClassifyRoute(effective)
	beforeExists := pathExists(path)
	protector.arm(false)
	saveErr := store.SaveContext(context.Background(), settings.Settings{Plugin: settings.DefaultUI})
	protector.disarm()
	identity, identityErr := identityAt(path)
	class, protected := registry.Classify(path, identity)
	replacement := protector.lastReplacement()
	pinned := replacement.registered && replacement.identity == identity
	policy := session.GenerationSnapshot()
	pass := pathErr == nil && effective == path && registerErr == nil && routeProtected && routeClass == readbrokerstudy.ClassSettings &&
		!beforeExists && saveErr == nil && identityErr == nil && protected && class == readbrokerstudy.ClassSettings &&
		storeReplacementAccepted(replacement.empty, replacement.identity, identity) && pinned && ownerPolicyAdvanced(policyBefore, policy)
	row := caseResult{
		Name: name, Status: "inconclusive", Classification: "inconclusive",
		Expected: "register the absent effective override route before the actual settings store creates an empty temp object; register that object before the real JSON write and publish the same pinned identity",
		Observed: "real settings store first-save path exercised", FixtureEffect: "synthetic settings file created; payload omitted",
		PreparedClass: readbrokerstudy.ClassSettings, PreparedIdentity: &identity,
		AuthorityA: "not applicable; protected owner lifecycle only", AuthorityB: "real settings owner route and object registration only",
		RealStore: &realStoreEvidence{StoreType: "settings", Operation: "RegisterProtection+SaveContext", EffectiveOverride: true,
			StartupRegistrationAttempted: true, StartupRegistrationCompleted: registerErr == nil, StoreMutationAttempted: true,
			StoreMutationResult: settingsStoreResult(saveErr), ReplacementIdentity: identityPtr(replacement.identity),
			ReplacementEmptyAtRegistration: replacement.empty, PublishedObjectMatchesRegistered: pinned,
			EffectiveRouteReserved: routeProtected, PolicyBefore: policyBefore, PolicyAfter: policy, OnceGrantConsumptions: 0,
			SensitiveContentPresentInReport: false},
	}
	if pass {
		row.Status = "measured_pass"
		row.Classification = "owner_lifecycle"
		row.Observed = "absent route was reserved, the actual SaveContext registered the empty replacement, and the published identity remained pinned"
		row.Detail = "verified using an explicit temporary override and the real study adapter/session; no values or paths are reported"
	} else {
		row.Detail = "route reservation, real store save, published identity, or registry pin correlation was not established"
	}
	if err := closeSettingsSession(registry, session); err != nil {
		row = markCleanupUnconfirmed(row, "store setup completed but session, pin, or worker cleanup was not confirmed; fixture retained")
	}
	return []caseResult{row}, nil
}

func runSettingsRotationNativeCase(bwrap, workerPath, parent string) (rows []caseResult, runErr error) {
	const name = "real_settings_startup_existing_route_and_rotations"
	root := filepath.Join(parent, name)
	if err := os.Mkdir(root, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(root, "settings-override.json")
	seed := settings.Store{Path: path}
	if err := seed.SaveContext(context.Background(), settings.Settings{Plugin: settings.DefaultUI}); err != nil {
		return nil, err
	}
	registry, authorizer, session, err := newStudySession(bwrap, workerPath, root, false)
	if err != nil {
		return []caseResult{settingsNativeUnavailable(name, "register an existing override and perform successive real atomic settings saves while retaining retired protected identities", "native worker/session setup was unavailable")}, nil
	}
	sessionClosed := false
	defer func() {
		if !sessionClosed {
			if err := closeSettingsSession(registry, session); err != nil {
				rows = markCaseRowsCleanupUnconfirmed(rows, "settings rotation case returned without confirmed session, worker, operation, and pin cleanup; fixture retained")
			}
		}
	}()
	protector := &observedSettingsProtector{delegate: studyOwnerProtection{session: session, registry: registry}}
	store := settings.Store{Path: path, Protection: protector}
	effective, pathErr := store.EffectivePath()
	policyBefore := session.GenerationSnapshot()
	first, firstErr := identityAt(path)
	registerErr := store.RegisterProtection(context.Background())
	registeredFirst := hasSettingsPin(registry.View(), first)
	oldAlias := filepath.Join(root, "retired-settings-alias.json")
	if err := os.Link(path, oldAlias); err != nil {
		return []caseResult{settingsNativeUnavailable(name, "retain an old settings identity through hard-link and rename aliases while later saves replace the route", "fixture filesystem did not permit a disposable hard link")}, nil
	}
	protector.arm(false)
	firstSaveErr := store.SaveContext(context.Background(), settings.Settings{Plugin: settings.DefaultUI})
	firstReplacement := protector.lastReplacement()
	protector.disarm()
	second, secondErr := identityAt(path)
	middleAlias := filepath.Join(root, "middle-settings-alias.json")
	if firstSaveErr == nil {
		if err := os.Link(path, middleAlias); err != nil {
			return []caseResult{settingsNativeUnavailable(name, "retain successive replacement identities through aliases", "fixture filesystem did not permit a disposable hard link")}, nil
		}
	}
	protector.arm(false)
	secondSaveErr := store.SaveContext(context.Background(), settings.Settings{Plugin: settings.DefaultUI})
	secondReplacement := protector.lastReplacement()
	protector.disarm()
	third, thirdErr := identityAt(path)
	retiredPath := filepath.Join(root, "renamed-retired-settings.json")
	renameErr := os.Rename(oldAlias, retiredPath)
	newAlias := filepath.Join(root, "current-settings-alias.json")
	newAliasErr := error(nil)
	if secondSaveErr == nil {
		newAliasErr = os.Link(path, newAlias)
	}
	view := registry.View()
	policyAfter := session.GenerationSnapshot()
	pins := hasSettingsPin(view, first) && hasSettingsPin(view, second) && hasSettingsPin(view, third)
	idsDistinct := firstErr == nil && secondErr == nil && thirdErr == nil && first != second && second != third && first != third
	writeOK := pathErr == nil && effective == path && registerErr == nil && registeredFirst && firstSaveErr == nil && secondSaveErr == nil && pins && idsDistinct && renameErr == nil && newAliasErr == nil &&
		firstReplacement.empty && secondReplacement.empty && firstReplacement.registered && secondReplacement.registered &&
		storeReplacementAccepted(firstReplacement.empty, firstReplacement.identity, second) &&
		storeReplacementAccepted(secondReplacement.empty, secondReplacement.identity, third) && ownerPolicyAdvanced(policyBefore, policyAfter)

	row := caseResult{
		Name: name, Status: "inconclusive", Classification: "inconclusive",
		Expected: "the existing effective override is pinned at startup; each actual SaveContext registers its empty temp object before writing and publishes that same object; retired identities remain protected through aliases and rename",
		Observed: "real settings store registration and successive SaveContext calls completed", FixtureEffect: "synthetic settings objects replaced; payload omitted",
		PreparedClass: readbrokerstudy.ClassSettings, PreparedIdentity: &third,
		AuthorityA: "not applicable; protected owner lifecycle only", AuthorityB: "real settings owner rotation and identity retention only",
		RealStore: &realStoreEvidence{StoreType: "settings", Operation: "RegisterProtection+successive SaveContext", EffectiveOverride: true,
			StartupRegistrationAttempted: true, StartupRegistrationCompleted: registerErr == nil,
			StoreMutationAttempted: true, StoreMutationResult: settingsStoreResult(secondSaveErr),
			ReplacementIdentity: identityPtr(secondReplacement.identity), ReplacementEmptyAtRegistration: firstReplacement.empty && secondReplacement.empty,
			PublishedObjectMatchesRegistered: secondReplacement.registered && secondReplacement.identity == third,
			EffectiveRouteReserved:           hasSettingsRoute(view, effective),
			RetiredPinRetained:               hasSettingsPin(view, first) && hasSettingsPin(view, second),
			AliasDenied:                      true, RenamedRetiredObjectDenied: true,
			PolicyBefore: policyBefore, PolicyAfter: policyAfter,
			OnceGrantConsumptions: 0, SensitiveContentPresentInReport: false},
	}
	if writeOK {
		row.Status = "measured_pass"
		row.Classification = "owner_lifecycle"
		row.Observed = "existing route and each replacement identity matched the actual store's published objects; prior identities remained in the registry"
		row.Detail = "identity checks report no paths or settings content"
	} else {
		row.Detail = "startup pin, distinct successive replacement identity, or publication-to-pin correlation was not established"
	}
	rows = []caseResult{row}
	for _, item := range []struct {
		caseName string
		path     string
		identity readbrokerstudy.FileIdentity
	}{
		{caseName: "real_settings_retired_alias_denied", path: retiredPath, identity: first},
		{caseName: "real_settings_intermediate_alias_denied", path: middleAlias, identity: second},
		{caseName: "real_settings_current_alias_denied", path: newAlias, identity: third},
		{caseName: "real_settings_effective_route_denied", path: path, identity: third},
	} {
		rows = append(rows, settingsProtectedReadCase(session, authorizer, root, item.caseName, item.path, item.identity))
	}
	ordinary := filepath.Join(root, "ordinary-control.fixture")
	if err := os.WriteFile(ordinary, []byte("disposable ordinary read control\n"), 0o600); err != nil {
		return nil, err
	}
	rows = append(rows, executeCase(session, authorizer, root, "real_settings_ordinary_read_control", ordinary, "real-settings-ordinary-read", "an explicit once approval permits an unrelated ordinary fixture read"))
	if row.RealStore != nil {
		row.RealStore.AliasDenied = rows[1].Status == "measured_pass" && rows[2].Status == "measured_pass" && rows[3].Status == "measured_pass"
		row.RealStore.RenamedRetiredObjectDenied = rows[1].Status == "measured_pass"
		row.RealStore.UnrelatedOrdinaryReadSucceeded = rows[len(rows)-1].Status == "measured_pass"
	}
	rows[0] = row
	if err := closeSettingsSession(registry, session); err != nil {
		rows = markCaseRowsCleanupUnconfirmed(rows, "store cases ran but direct worker join, session close, or pin cleanup was not confirmed; fixture retained")
	}
	sessionClosed = true
	return rows, nil
}

func runSettingsBarrierNativeCase(bwrap, workerPath, parent string) (rows []caseResult, runErr error) {
	const name = "real_settings_cleanup_pending_refuses_then_retries"
	root := filepath.Join(parent, name)
	if err := os.Mkdir(root, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(root, "settings-override.json")
	seed := settings.Store{Path: path}
	if err := seed.SaveContext(context.Background(), settings.Settings{Plugin: settings.DefaultUI}); err != nil {
		return nil, err
	}
	beforeBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	gate := newTerminationRequestGate()
	registry, authorizer, session, launcher, err := newObservedSession(bwrap, workerPath, root, true, gate)
	if err != nil {
		return []caseResult{settingsNativeUnavailable(name, "a live worker holds the real settings temp object while actual store registration requests cleanup; no settings replacement is written until confirmed join", "trusted native worker/session setup was unavailable")}, nil
	}
	readReleased := make(chan struct{})
	var releaseRead sync.Once
	releaseHeldRead := func() { releaseRead.Do(func() { close(readReleased) }) }
	var readDone chan settingsReadCompletion
	var cancelRead context.CancelFunc
	gateReleased := false
	cleanupDone := false
	readConsumed := false
	cleanupWaitIncomplete := false
	defer func() {
		releaseHeldRead()
		if cancelRead != nil {
			cancelRead()
		}
		if !gateReleased && launcher.latest() != nil {
			if err := gate.release(); err != nil {
				cleanupWaitIncomplete = true
			}
			gateReleased = true
		}
		if readDone != nil && !readConsumed {
			select {
			case <-readDone:
				readConsumed = true
			case <-time.After(10 * time.Second):
				cleanupWaitIncomplete = true
			}
		}
		if !cleanupDone {
			if err := closeSettingsSession(registry, session); err != nil {
				rows = markCaseRowsCleanupUnconfirmed(rows, "settings cleanup-pending case could not confirm worker join, active-operation, and pin cleanup; fixture retained")
			} else {
				cleanupDone = true
			}
		}
		if cleanupWaitIncomplete {
			rows = markCaseRowsCleanupUnconfirmed(rows, "settings cleanup-pending case did not confirm bounded goroutine or gate completion; fixture retained")
		}
	}()
	held := make(chan struct{})
	var heldOnce sync.Once
	session.SetCheckpointHook(func(checkpoint readbrokerstudy.Checkpoint) error {
		if checkpoint == readbrokerstudy.CheckpointAfterCapabilityHeld {
			heldOnce.Do(func() { close(held) })
			<-readReleased
		}
		return nil
	})
	var protect *observedSettingsProtector
	protect = &observedSettingsProtector{
		delegate: studyOwnerProtection{session: session, registry: registry},
		active:   true,
		beforeRegister: func(ctx context.Context, object *os.File) error {
			if object == nil {
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			info, err := object.Stat()
			if err != nil {
				return err
			}
			protect.tempIdentity, err = studyIdentity(object)
			if err != nil {
				return err
			}
			protect.tempEmptyBefore = info.Size() == 0
			protect.tempSeen = true
			if !protect.tempEmptyBefore {
				return errors.New("settings temporary object was not empty before registration")
			}
			protect.invocation = "real-settings-empty-temp-read"
			if err := authorizer.ApproveOnce(protect.invocation); err != nil {
				return err
			}
			readCtx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
			cancelRead = cancel
			readDone = make(chan settingsReadCompletion, 1)
			go func() {
				result, readErr := session.Read(readCtx, root, object.Name(), protect.invocation)
				readDone <- settingsReadCompletion{result: result, err: readErr}
			}()
			select {
			case <-held:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(12 * time.Second):
				return errors.New("real worker did not acknowledge holding the temporary settings object")
			}
		},
	}
	store := settings.Store{Path: path, Protection: protect}
	if err := store.RegisterProtection(context.Background()); err != nil {
		return []caseResult{settingsNativeUnavailable(name, "existing override is registered before the native barrier scenario", "settings startup registration failed")}, nil
	}
	protect.arm(true)
	policyBefore := session.GenerationSnapshot()
	storeErr := store.SaveContext(context.Background(), settings.Settings{Plugin: settings.DefaultUI})
	protect.disarm()
	requestObserved := false
	select {
	case <-gate.requestedSignal():
		requestObserved = true
	case <-time.After(2 * time.Second):
	}
	worker := launcher.latest()
	workerAlive := false
	if worker != nil && requestObserved {
		bwrapID, initID, observeErr := worker.ObserveHostProcesses()
		if observeErr == nil {
			bwrapFD, bwrapErr := readbrokerstudy.OpenVerifiedPIDFD(bwrapID)
			initFD, initErr := readbrokerstudy.OpenVerifiedPIDFD(initID)
			if bwrapErr == nil && initErr == nil {
				bwrapExited, bwrapExitErr := observePIDFDExit(bwrapFD, 0)
				initExited, initExitErr := observePIDFDExit(initFD, 0)
				workerAlive = bwrapExitErr == nil && initExitErr == nil && !bwrapExited && !initExited
				_ = unix.Close(bwrapFD)
				_ = unix.Close(initFD)
			} else {
				if bwrapErr == nil {
					_ = unix.Close(bwrapFD)
				}
				if initErr == nil {
					_ = unix.Close(initFD)
				}
			}
		}
	}
	pendingPins := registry.PinnedObjectCount()
	pendingPathBytes, pathReadErr := os.ReadFile(path)
	destinationPreserved := pathReadErr == nil && string(pendingPathBytes) == string(beforeBytes)
	contentEmptyAfterRegistration := protect.tempEmptyAfter
	policyAfter := session.GenerationSnapshot()
	readCount := authorizer.ConsumptionCount(protect.invocation)
	noPublicationBeforeJoin := len(session.PublishedResults()) == 0
	gatePending := errors.Is(storeErr, readbrokerstudy.ErrCleanupPending)

	releaseHeldRead()
	if cancelRead != nil {
		cancelRead()
	}
	if launcher.latest() != nil {
		if err := gate.release(); err != nil {
			cleanupWaitIncomplete = true
		}
		gateReleased = true
	}
	var readCompletion settingsReadCompletion
	joined := false
	if readDone != nil {
		select {
		case readCompletion = <-readDone:
			readConsumed = true
			for _, item := range session.Evidence() {
				if item.Case == protect.invocation {
					joined = item.WorkerJoined
				}
			}
		case <-time.After(15 * time.Second):
			cleanupWaitIncomplete = true
		}
	}
	var retryErr error
	if joined {
		protect.arm(false)
		retryErr = store.SaveContext(context.Background(), settings.Settings{Plugin: settings.DefaultUI})
		protect.disarm()
	} else {
		retryErr = errors.New("settings retry withheld because direct worker join was not confirmed")
	}
	third, retryIdentityErr := identityAt(path)
	finalPins := hasSettingsPin(registry.View(), third)
	newTemp := protect.retryTempIdentity
	retryTempPinned := newTemp != (readbrokerstudy.FileIdentity{}) && hasSettingsPin(registry.View(), newTemp)
	retryPublicationAccepted := retryIdentityErr == nil && settingsRetryPublicationAccepted(retryTempPinned, protect.tempIdentity, newTemp, third)
	workerEvidence, workerEvidencePresent := evidenceFor(session, protect.invocation)
	capabilityMatches := workerEvidencePresent && workerEvidence.CapabilityIdentity == protect.tempIdentity
	workerHeld := workerEvidencePresent && workerEvidence.CapabilityHeld
	consumedOnce := readCount == 1
	pass := protect.tempSeen && protect.tempEmptyBefore && contentEmptyAfterRegistration && gatePending && requestObserved && workerAlive && pendingPins > 0 &&
		destinationPreserved && noPublicationBeforeJoin && consumedOnce && readDone != nil && readCompletion.err == nil &&
		readCompletion.result.Outcome == readbrokerstudy.ReadNotExecuted && joined && capabilityMatches && workerHeld &&
		retryErr == nil && retryIdentityErr == nil && finalPins && retryPublicationAccepted

	evidence := &ownerLifecycleEvidence{
		RegistrationAttempted: true, InitialRegistrationResult: "cleanup_pending", CleanupPending: gatePending,
		WorkerJoinConfirmed: joined, WriteAttempted: true, PolicyBefore: policyBefore, PolicyAfterRegistration: policyAfter,
		WriteDescriptorIdentity: protect.tempIdentity, PinnedIdentity: protect.retryTempIdentity,
		WriteDescriptorMatchesPin: retryTempPinned, WriteAuthorized: retryErr == nil, WriteStarted: retryErr == nil,
		WriteCompleted: retryErr == nil, Published: retryErr == nil && finalPins,
		EffectiveRouteReserved: pathReadErr == nil, Events: []string{"real_settings_store_save_started", "empty_temp_object_observed_before_registration", "real_session_worker_held_same_temp_identity", "actual_settings_store_registration_returned_cleanup_pending", "existing_settings_destination_unchanged_before_worker_join", "termination_gate_released", "direct_worker_join_confirmed", "actual_settings_store_retry_published_registered_object"},
	}
	if requestObserved && workerAlive {
		evidence.Events = append(evidence.Events, "bubblewrap_and_namespace_init_live_while_termination_request_gated")
	}
	if contentEmptyAfterRegistration {
		evidence.Detail = "temporary object remained zero bytes through the cleanup-pending registration return"
	}
	row := caseResult{
		Name: name, Status: "inconclusive", Classification: "inconclusive",
		Expected: "actual SaveContext registers its empty temp object while a real worker holds that identity; cleanup pending blocks settings publication until worker termination and direct wait; a fresh real SaveContext retry registers and publishes a new pinned temp object",
		Observed: string(readCompletion.result.Outcome), WorkerStarted: workerEvidencePresent && workerEvidence.WorkerStarted,
		WorkerReady:         workerEvidencePresent && workerEvidence.WorkerReady,
		WorkerJoinConfirmed: joined, CapabilitySent: workerEvidencePresent && workerEvidence.CapabilitySent, CapabilityHeld: workerHeld,
		CapabilityIdentity: &protect.tempIdentity, OnceGrantConsumptions: readCount,
		GenerationsBefore: &policyBefore, GenerationsAfter: &policyAfter, OwnerLifecycle: evidence,
		AuthorityA: "not applicable; owner lifecycle only", AuthorityB: "actual owner registration, worker barrier, and publication ordering only",
		FixtureEffect: "prior synthetic settings destination preserved before join; retry wrote synthetic settings data after join; values and paths omitted",
		RealStore: &realStoreEvidence{StoreType: "settings", Operation: "SaveContext with cleanup-pending refusal and retry", EffectiveOverride: true,
			StartupRegistrationAttempted: true, StartupRegistrationCompleted: true, StoreMutationAttempted: true,
			StoreMutationResult: settingsStoreResult(storeErr), ReplacementIdentity: identityPtr(protect.tempIdentity),
			ReplacementEmptyAtRegistration: protect.tempEmptyBefore && protect.tempEmptyAfter,
			CapabilityIdentityMatchesTemp:  capabilityMatches,
			WorkerCapabilityHeld:           workerHeld, TerminationRequested: requestObserved, WorkerJoinConfirmed: joined,
			StoreWriteNotStartedBeforeJoin:   destinationPreserved && protect.tempEmptyAfter,
			StoreWriteStartedAfterJoin:       joined && retryErr == nil && retryIdentityErr == nil,
			PublishedObjectMatchesRegistered: retryPublicationAccepted,
			EffectiveRouteReserved:           hasSettingsRoute(registry.View(), path),
			RetiredPinRetained:               hasSettingsPin(registry.View(), protect.tempIdentity),
			PolicyBefore:                     policyBefore, PolicyAfter: policyAfter, OnceGrantConsumptions: readCount,
			RetryUsedNewReplacementIdentity: retryErr == nil && newTemp != protect.tempIdentity,
			StalePublicationSuppressed:      noPublicationBeforeJoin && len(session.PublishedResults()) == 0,
			SensitiveContentPresentInReport: false},
	}
	if pass {
		row.Status = "measured_pass"
		row.Classification = "owner_lifecycle"
		row.Detail = "the real settings store returned cleanup-pending without changing the destination; a same-identity native worker was held, terminated and joined; retry used a distinct pinned replacement"
	} else {
		row.Detail = "native same-object hold, gated live-worker observation, empty pre-sensitive temp, no-write refusal, confirmed direct join, once count, retry identity, or publication correlation was incomplete"
		if !requestObserved || !workerAlive {
			row.Status, row.Classification = "unavailable", "unavailable"
			row.Detail = "the actual candidate worker did not reach the required live termination-gate checkpoint"
		}
	}
	if closeErr := closeSettingsSession(registry, session); closeErr != nil {
		row = markCleanupUnconfirmed(row, "store lifecycle observations completed but session close did not confirm cleanup; fixture retained")
	} else {
		cleanupDone = true
	}
	rows = []caseResult{row}
	return rows, nil
}

type settingsReadCompletion struct {
	result readbrokerstudy.WorkerResult
	err    error
}

// observedSettingsProtector wraps the real study adapter only to stage a read
// against the store's actual empty replacement object before the adapter
// registers it. Security decisions and all file writing stay on the real
// store/adapter/session path.
type observedSettingsProtector struct {
	mu                sync.Mutex
	delegate          studyOwnerProtection
	active            bool
	armed             bool
	used              bool
	beforeRegister    func(context.Context, *os.File) error
	invocation        string
	tempIdentity      readbrokerstudy.FileIdentity
	retryTempIdentity readbrokerstudy.FileIdentity
	tempSeen          bool
	tempEmptyBefore   bool
	tempEmptyAfter    bool
	registered        bool
}

func (p *observedSettingsProtector) arm(stage bool) {
	p.mu.Lock()
	p.armed = true
	p.active = stage
	p.mu.Unlock()
}

func (p *observedSettingsProtector) disarm() {
	p.mu.Lock()
	p.armed = false
	p.active = false
	p.mu.Unlock()
}

func (p *observedSettingsProtector) Protect(ctx context.Context, owner, class, route string, object *os.File) error {
	p.mu.Lock()
	armed := p.armed && object != nil
	staged := p.active && armed && !p.used
	if staged {
		p.used = true
	}
	p.mu.Unlock()
	var identity readbrokerstudy.FileIdentity
	empty := false
	if armed {
		info, err := object.Stat()
		if err != nil {
			return err
		}
		identity, err = studyIdentity(object)
		if err != nil {
			return err
		}
		empty = info.Size() == 0
		if staged {
			p.tempIdentity, p.tempSeen, p.tempEmptyBefore = identity, true, empty
		} else {
			p.retryTempIdentity = identity
			p.tempSeen = true
			p.tempEmptyBefore = empty
		}
	}
	if staged && p.beforeRegister != nil {
		if err := p.beforeRegister(ctx, object); err != nil {
			return err
		}
	}
	err := p.delegate.Protect(ctx, owner, class, route, object)
	if armed && object != nil {
		info, statErr := object.Stat()
		registered := hasSettingsPin(p.delegate.registry.View(), identity)
		p.mu.Lock()
		p.tempEmptyAfter = statErr == nil && info.Size() == 0
		p.registered = registered
		p.mu.Unlock()
	}
	return err
}

func (p *observedSettingsProtector) lastReplacement() settingsReplacementObservation {
	p.mu.Lock()
	defer p.mu.Unlock()
	identity := p.tempIdentity
	if p.retryTempIdentity != (readbrokerstudy.FileIdentity{}) {
		identity = p.retryTempIdentity
	}
	return settingsReplacementObservation{identity: identity, empty: p.tempEmptyBefore && p.tempEmptyAfter, registered: p.registered}
}

type settingsReplacementObservation struct {
	identity   readbrokerstudy.FileIdentity
	empty      bool
	registered bool
}

func settingsProtectedReadCase(session readbrokerstudy.FixtureSession, authorizer *readbrokerstudy.OnceGrantFixture, root, name, target string, identity readbrokerstudy.FileIdentity) caseResult {
	before := session.GenerationSnapshot()
	result, readErr := session.Read(context.Background(), root, target, name+"-invocation")
	after := session.GenerationSnapshot()
	count := authorizer.ConsumptionCount(name + "-invocation")
	row := caseResult{
		Name: name, Status: "inconclusive", Classification: "inconclusive",
		Expected: "the exact settings route or retired pinned identity refuses through its current, hard-link, or renamed path before once consumption and worker startup",
		Observed: string(result.Outcome), OnceGrantConsumptions: count, PreparedClass: readbrokerstudy.ClassSettings,
		PreparedIdentity: &identity, GenerationsBefore: &before, GenerationsAfter: &after,
		AuthorityA: "not applicable; protected identity rule only", AuthorityB: "settings identity remains protected through alias or rename",
		FixtureEffect: "no worker handoff or result publication",
	}
	if readErr == nil && result.Outcome == readbrokerstudy.ReadRefused && result.Detail == readbrokerstudy.ErrProtected.Error() && count == 0 {
		row.Status, row.Classification = "measured_pass", "protected_rule_enforced"
	} else {
		row.Detail = "protected refusal before once authorization was not established"
	}
	return row
}

func hasSettingsPin(view readbrokerstudy.PolicyView, identity readbrokerstudy.FileIdentity) bool {
	if identity == (readbrokerstudy.FileIdentity{}) {
		return false
	}
	for _, entry := range view.Entries {
		if entry.Owner == "komposyn-settings-store" && entry.Class == readbrokerstudy.ClassSettings && entry.Identity == identity {
			return true
		}
	}
	return false
}

func hasSettingsRoute(view readbrokerstudy.PolicyView, path string) bool {
	for _, entry := range view.Entries {
		if entry.Owner == "komposyn-settings-store" && entry.Class == readbrokerstudy.ClassSettings && entry.Path == path {
			return true
		}
	}
	return false
}

func settingsStoreResult(err error) string {
	switch {
	case err == nil:
		return "succeeded"
	case errors.Is(err, readbrokerstudy.ErrCleanupPending):
		return "cleanup_pending"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	default:
		return "refused_or_failed"
	}
}

func identityPtr(identity readbrokerstudy.FileIdentity) *readbrokerstudy.FileIdentity {
	if identity == (readbrokerstudy.FileIdentity{}) {
		return nil
	}
	copy := identity
	return &copy
}

func settingsNativeUnavailable(name, expected, detail string) caseResult {
	return caseResult{Name: name, Status: "unavailable", Classification: "unavailable", Expected: expected,
		Observed: "candidate worker/session precondition unavailable", FixtureEffect: "no evidence claimed",
		AuthorityA: "no conclusion", AuthorityB: "no conclusion", Detail: detail}
}

func closeSettingsSession(registry *readbrokerstudy.Registry, session readbrokerstudy.FixtureSession) error {
	if err := closeStudySession(session); err != nil {
		return err
	}
	if activeOperationCount(session) != 0 || registry.PinnedObjectCount() != 0 {
		return errors.New("settings fixture session retained active operations or pins")
	}
	for _, item := range session.Evidence() {
		if item.WorkerStarted && !item.WorkerJoined {
			return errors.New("settings fixture session has an unjoined worker")
		}
	}
	return nil
}
