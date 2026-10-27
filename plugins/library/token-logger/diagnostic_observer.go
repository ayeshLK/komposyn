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

package tokenlogger

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"time"
)

const (
	DefaultDiagnosticObserverCapacity = 64
	MaxDiagnosticObserverCapacity     = 256
)

var ErrInvalidDiagnosticObserverCapacity = errors.New("invalid diagnostic observer capacity")

// DiagnosticObserverStats reports lifetime mailbox admission/loss totals.
// Admitted counts successful nonblocking enqueues, even after the consumer
// drains them. Dropped counts invalid events or events rejected because the
// bounded mailbox was full. Each total saturates at MaxUint64 and then its
// corresponding flag remains true.
type DiagnosticObserverStats struct {
	Capacity          int    `json:"capacity"`
	Admitted          uint64 `json:"admitted"`
	Dropped           uint64 `json:"dropped"`
	AdmittedSaturated bool   `json:"admitted_saturated"`
	DroppedSaturated  bool   `json:"dropped_saturated"`
}

// DiagnosticObserver is a caller-owned, bounded, single-consumer mailbox.
// Core never closes its channel or starts a consumer/worker goroutine.
type DiagnosticObserver struct {
	events   chan DiagnosticEvent
	capacity int
	stats    diagnosticAtomicStats
}

type diagnosticAtomicStats struct {
	admitted          atomic.Uint64
	dropped           atomic.Uint64
	admittedSaturated atomic.Bool
	droppedSaturated  atomic.Bool
}

// NewDiagnosticObserver creates a bounded observer mailbox. Capacity zero
// selects 64; capacities 1 through 256 are accepted.
func NewDiagnosticObserver(capacity int) (*DiagnosticObserver, error) {
	if capacity == 0 {
		capacity = DefaultDiagnosticObserverCapacity
	}
	if capacity < 1 || capacity > MaxDiagnosticObserverCapacity {
		return nil, fmt.Errorf("%w: want 0 or 1..%d", ErrInvalidDiagnosticObserverCapacity, MaxDiagnosticObserverCapacity)
	}
	return &DiagnosticObserver{events: make(chan DiagnosticEvent, capacity), capacity: capacity}, nil
}

// Events returns the receive-only event stream. The caller owns draining and
// consumer teardown. Core does not close this channel.
func (observer *DiagnosticObserver) Events() <-chan DiagnosticEvent {
	if observer == nil {
		return nil
	}
	return observer.events
}

// Stats returns counters without draining the mailbox or touching SQLite.
func (observer *DiagnosticObserver) Stats() DiagnosticObserverStats {
	if observer == nil {
		return DiagnosticObserverStats{}
	}
	return DiagnosticObserverStats{Capacity: observer.capacity, Admitted: observer.stats.admitted.Load(),
		Dropped: observer.stats.dropped.Load(), AdmittedSaturated: observer.stats.admittedSaturated.Load(),
		DroppedSaturated: observer.stats.droppedSaturated.Load()}
}

func (observer *DiagnosticObserver) publish(event DiagnosticEvent) DiagnosticEmissionDisposition {
	if observer == nil {
		return DiagnosticEmissionUnavailable
	}
	if event.Validate() != nil {
		saturatingIncrement(&observer.stats.dropped, &observer.stats.droppedSaturated)
		return DiagnosticEmissionDropped
	}
	select {
	case observer.events <- event:
		saturatingIncrement(&observer.stats.admitted, &observer.stats.admittedSaturated)
		return DiagnosticEmissionQueued
	default:
		saturatingIncrement(&observer.stats.dropped, &observer.stats.droppedSaturated)
		return DiagnosticEmissionDropped
	}
}

func saturatingIncrement(counter *atomic.Uint64, saturated *atomic.Bool) {
	for {
		current := counter.Load()
		if current == math.MaxUint64 {
			saturated.Store(true)
			return
		}
		if counter.CompareAndSwap(current, current+1) {
			if current+1 == math.MaxUint64 {
				saturated.Store(true)
			}
			return
		}
	}
}

func newDiagnosticID() (DiagnosticID, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return DiagnosticID(hex.EncodeToString(raw[:])), nil
}

func observedOpenFailure(observer *DiagnosticObserver, cause error) error {
	var alreadyClassified *OperationalError
	if errors.As(cause, &alreadyClassified) {
		return cause
	}
	classification := classifyOpenFailure(cause)
	failure := DiagnosticFailure{Classification: classification, EmissionDisposition: DiagnosticEmissionDisabled}
	if observer == nil {
		return newOperationalError(cause, failure)
	}
	operationID, err := newDiagnosticID()
	if err != nil {
		failure.EmissionDisposition = DiagnosticEmissionUnavailable
		return newOperationalError(cause, failure)
	}
	failure.OperationID = operationID
	event := DiagnosticEvent{ContractVersion: DiagnosticContractVersion, CoreVersion: safeCoreVersion(),
		ObservedAt: time.Now().UTC(), Kind: DiagnosticEventOperationFailed, Classification: classification,
		HasClassification: true, OperationID: operationID, Subject: DiagnosticSubject{Availability: DiagnosticAvailabilityNotCollected},
		SourceSchemaVersion: unavailableMeasurement(), TargetSchemaVersion: unavailableMeasurement(),
		DurationMilliseconds: unavailableMeasurement(), RetryAttempt: unavailableMeasurement(), RetryCount: unavailableMeasurement(),
		Position: DiagnosticPosition{ByteOffset: unavailableMeasurement(), LineNumber: unavailableMeasurement()},
		Evidence: DiagnosticEvidence{Scope: DiagnosticEvidenceOperation, Availability: DiagnosticAvailabilityNotCollected,
			UnavailableReason: DiagnosticUnavailableNotCollected}, Counts: unavailableDiagnosticOutcomeCounts()}
	var unsupported *UnsupportedSchemaError
	if errors.As(cause, &unsupported) {
		event.SourceSchemaVersion = DiagnosticOptionalUint64{Value: uint64(unsupported.DatabaseVersion), Availability: DiagnosticAvailabilityAvailable}
		event.TargetSchemaVersion = DiagnosticOptionalUint64{Value: uint64(unsupported.CurrentVersion), Availability: DiagnosticAvailabilityAvailable}
	}
	failure.EmissionDisposition = observer.publish(event)
	return newOperationalError(cause, failure)
}

func unavailableMeasurement() DiagnosticOptionalUint64 {
	return DiagnosticOptionalUint64{Availability: DiagnosticAvailabilityNotCollected}
}

func unavailableDiagnosticOutcomeCounts() DiagnosticOutcomeCounts {
	return DiagnosticOutcomeCounts{Scope: DiagnosticCountOperation, Availability: DiagnosticAvailabilityNotCollected}
}
