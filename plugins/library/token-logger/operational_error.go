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
	"context"
	"errors"
	"io/fs"
	"os"

	sqlite3 "modernc.org/sqlite"
)

// OperationalError preserves the original local error chain while carrying a
// private, controlled classification. Use FailureDetails for safe projection;
// Error() may contain local paths, driver text, or other operational details.
type OperationalError struct {
	cause   error
	failure DiagnosticFailure
}

func (err *OperationalError) Error() string {
	if err == nil || err.cause == nil {
		return "<nil>"
	}
	return err.cause.Error()
}

func (err *OperationalError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

// FailureDetails returns a bounded safe projection only when err contains a
// core-classified operational failure. It never returns the cause or raw text.
func FailureDetails(err error) (DiagnosticFailure, bool) {
	if err == nil {
		return DiagnosticFailure{}, false
	}
	var operational *OperationalError
	if !errors.As(err, &operational) || operational == nil || operational.failure.Validate() != nil {
		return DiagnosticFailure{}, false
	}
	return operational.failure, true
}

func newOperationalError(cause error, failure DiagnosticFailure) error {
	if cause == nil {
		return nil
	}
	var existing *OperationalError
	if errors.As(cause, &existing) {
		return cause
	}
	if failure.Validate() != nil {
		return cause
	}
	return &OperationalError{cause: cause, failure: failure}
}

func classifyOpenFailure(err error) DiagnosticClassification {
	if err == nil {
		return DiagnosticClassification{}
	}
	var unsupported *UnsupportedSchemaError
	if errors.As(err, &unsupported) || errors.Is(err, ErrUnsupportedSchema) {
		return makeDiagnosticClassification(DiagnosticComponentSchema, DiagnosticOperationOpen,
			DiagnosticReasonSchemaUnsupported, DiagnosticAdapterUnavailable)
	}
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, os.ErrPermission) {
		return makeDiagnosticClassification(DiagnosticComponentStorage, DiagnosticOperationOpen,
			DiagnosticReasonPermissionDenied, DiagnosticAdapterUnavailable)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return makeDiagnosticClassification(DiagnosticComponentUnknown, DiagnosticOperationOpen,
			DiagnosticReasonDeadlineExceeded, DiagnosticAdapterUnavailable)
	}
	if errors.Is(err, context.Canceled) {
		return makeDiagnosticClassification(DiagnosticComponentUnknown, DiagnosticOperationOpen,
			DiagnosticReasonCancelled, DiagnosticAdapterUnavailable)
	}
	var sqliteError *sqlite3.Error
	if errors.As(err, &sqliteError) {
		var reason DiagnosticReason
		switch sqliteError.Code() & 0xff {
		case 5:
			reason = DiagnosticReasonStorageBusy
		case 6:
			reason = DiagnosticReasonStorageLocked
		case 13:
			reason = DiagnosticReasonStorageFull
		case 8:
			reason = DiagnosticReasonStorageReadonly
		case 11, 26:
			reason = DiagnosticReasonStorageCorrupt
		default:
			reason = DiagnosticReasonStorageFailure
		}
		return makeDiagnosticClassification(DiagnosticComponentStorage, DiagnosticOperationOpen,
			reason, DiagnosticAdapterUnavailable)
	}
	return makeDiagnosticClassification(DiagnosticComponentUnknown, DiagnosticOperationOpen,
		DiagnosticReasonFailureUnclassified, DiagnosticAdapterUnavailable)
}

func safeCoreVersion() string {
	version := CoreVersion()
	if validDiagnosticVersion(version) {
		return version
	}
	return "unknown"
}
