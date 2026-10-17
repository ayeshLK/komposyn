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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Diagnostic is a locally persisted, sanitized interactive error.
type Diagnostic struct {
	Fingerprint     string    `json:"fingerprint"`
	Message         string    `json:"message"`
	Category        string    `json:"category"`
	CoreVersion     string    `json:"core_version,omitempty"`
	CLIVersion      string    `json:"cli_version,omitempty"`
	ProtocolVersion int       `json:"protocol_version,omitempty"`
	SchemaVersion   int       `json:"schema_version,omitempty"`
	OccurrenceCount int       `json:"occurrence_count"`
	FirstSeenAt     time.Time `json:"first_seen_at"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	Status          string    `json:"status"`
}

const (
	DiagnosticCategoryCLI               = "cli"
	DiagnosticCategoryProvider          = "provider"
	DiagnosticCategoryIngestion         = "ingestion"
	DiagnosticCategoryStorage           = "storage"
	DiagnosticCategoryMigration         = "migration"
	DiagnosticCategoryPricing           = "pricing"
	DiagnosticCategoryReporting         = "reporting"
	DiagnosticCategoryValidation        = "validation"
	DiagnosticCategorySecuritySensitive = "security-sensitive"
	DiagnosticCategoryUnknown           = "unknown"
)

// DiagnosticMetadata adds controlled source and version context without
// capturing arbitrary source content.
type DiagnosticMetadata struct {
	Category        string
	CoreVersion     string
	CLIVersion      string
	ProtocolVersion int
	SchemaVersion   int
}

// DiagnosticReport is a destination-neutral snapshot of pending diagnostics.
type DiagnosticReport struct {
	FormatVersion int          `json:"format_version"`
	CreatedAt     time.Time    `json:"created_at"`
	Diagnostics   []Diagnostic `json:"diagnostics"`
}

var (
	absolutePathPattern = regexp.MustCompile(`(?:[A-Za-z]:[\\/]|/)[^\s]+`)
	secretPattern       = regexp.MustCompile(`(?i)(token|secret|password|api[_-]?key)=([^\s]+)`)
)

// SanitizeDiagnostic strips paths and common secret-shaped values before a
// diagnostic is persisted or shown as a report candidate.
func SanitizeDiagnostic(message string) string {
	message = secretPattern.ReplaceAllString(message, "$1=[redacted]")
	message = absolutePathPattern.ReplaceAllString(message, "[path]")
	message = strings.TrimSpace(message)
	if message == "" {
		return "unknown error"
	}
	return message
}

// RecordDiagnostic persists a deduplicated sanitized diagnostic.
func (s *Store) RecordDiagnostic(message string) (Diagnostic, error) {
	return s.RecordDiagnosticWithMetadata(message, DiagnosticMetadata{Category: DiagnosticCategoryUnknown})
}

// RecordDiagnosticWithMetadata persists a sanitized diagnostic and its
// controlled classification/version metadata.
func (s *Store) RecordDiagnosticWithMetadata(message string, metadata DiagnosticMetadata) (Diagnostic, error) {
	sanitized := SanitizeDiagnostic(message)
	if !validDiagnosticCategory(metadata.Category) {
		metadata.Category = DiagnosticCategoryUnknown
	}
	sum := sha256.Sum256([]byte(sanitized))
	fingerprint := hex.EncodeToString(sum[:])
	now := time.Now().UTC()
	_, err := s.db.Exec(`INSERT INTO diagnostics(fingerprint, message, first_seen_at, last_seen_at, category,
		core_version, cli_version, protocol_version, schema_version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(fingerprint) DO UPDATE SET occurrence_count = occurrence_count + 1,
		last_seen_at = excluded.last_seen_at,
		category = CASE
			WHEN diagnostics.category = 'security-sensitive' OR excluded.category = 'security-sensitive' THEN 'security-sensitive'
			WHEN diagnostics.category = 'unknown' THEN excluded.category
			ELSE diagnostics.category END,
		core_version = CASE WHEN diagnostics.core_version = '' THEN excluded.core_version ELSE diagnostics.core_version END,
		cli_version = CASE WHEN diagnostics.cli_version = '' THEN excluded.cli_version ELSE diagnostics.cli_version END,
		protocol_version = CASE WHEN diagnostics.protocol_version = 0 THEN excluded.protocol_version ELSE diagnostics.protocol_version END,
		schema_version = CASE WHEN diagnostics.schema_version = 0 THEN excluded.schema_version ELSE diagnostics.schema_version END`, fingerprint, sanitized,
		formatStoredTime(now), formatStoredTime(now), metadata.Category, metadata.CoreVersion,
		metadata.CLIVersion, metadata.ProtocolVersion, metadata.SchemaVersion)
	if err != nil {
		return Diagnostic{}, fmt.Errorf("persist diagnostic: %w", err)
	}
	return s.Diagnostic(fingerprint)
}

// Diagnostic loads one local diagnostic by fingerprint.
func (s *Store) Diagnostic(fingerprint string) (Diagnostic, error) {
	var diagnostic Diagnostic
	var first, last string
	err := s.db.QueryRow(`SELECT fingerprint, message, category, core_version, cli_version, protocol_version, schema_version,
		occurrence_count, first_seen_at, last_seen_at, status
		FROM diagnostics WHERE fingerprint = ?`, fingerprint).Scan(&diagnostic.Fingerprint, &diagnostic.Message,
		&diagnostic.Category, &diagnostic.CoreVersion, &diagnostic.CLIVersion, &diagnostic.ProtocolVersion,
		&diagnostic.SchemaVersion, &diagnostic.OccurrenceCount, &first, &last, &diagnostic.Status)
	if err != nil {
		return Diagnostic{}, err
	}
	err = nil
	diagnostic.FirstSeenAt, err = time.Parse(time.RFC3339Nano, first)
	if err != nil {
		return Diagnostic{}, err
	}
	diagnostic.LastSeenAt, err = time.Parse(time.RFC3339Nano, last)
	return diagnostic, err
}

// PendingDiagnostics returns locally stored diagnostics awaiting user review.
func (s *Store) PendingDiagnostics() ([]Diagnostic, error) {
	rows, err := s.db.Query(`SELECT fingerprint, message, category, core_version, cli_version, protocol_version,
		schema_version, occurrence_count, first_seen_at, last_seen_at, status
		FROM diagnostics WHERE status = 'pending' ORDER BY last_seen_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var diagnostics []Diagnostic
	for rows.Next() {
		var diagnostic Diagnostic
		var first, last string
		if err := rows.Scan(&diagnostic.Fingerprint, &diagnostic.Message, &diagnostic.Category, &diagnostic.CoreVersion,
			&diagnostic.CLIVersion, &diagnostic.ProtocolVersion, &diagnostic.SchemaVersion,
			&diagnostic.OccurrenceCount, &first, &last, &diagnostic.Status); err != nil {
			return nil, err
		}
		var err error
		diagnostic.FirstSeenAt, err = time.Parse(time.RFC3339Nano, first)
		if err != nil {
			return nil, err
		}
		diagnostic.LastSeenAt, err = time.Parse(time.RFC3339Nano, last)
		if err != nil {
			return nil, err
		}
		diagnostics = append(diagnostics, diagnostic)
	}
	return diagnostics, rows.Err()
}

// DiagnosticReport returns a destination-neutral snapshot of diagnostics
// awaiting local review.
func (s *Store) DiagnosticReport() (DiagnosticReport, error) {
	diagnostics, err := s.PendingDiagnostics()
	if err != nil {
		return DiagnosticReport{}, err
	}
	return DiagnosticReport{FormatVersion: 1, CreatedAt: time.Now().UTC(), Diagnostics: diagnostics}, nil
}

// MarkDiagnosticReviewed suppresses repeated prompts after the user has
// explicitly reviewed or skipped a diagnostic.
func (s *Store) MarkDiagnosticReviewed(fingerprint, status string) error {
	if status != "reviewed" && status != "skipped" {
		return fmt.Errorf("unsupported diagnostic status %q", status)
	}
	_, err := s.db.Exec("UPDATE diagnostics SET status = ? WHERE fingerprint = ?", status, fingerprint)
	return err
}

func validDiagnosticCategory(category string) bool {
	switch category {
	case DiagnosticCategoryCLI, DiagnosticCategoryProvider, DiagnosticCategoryIngestion,
		DiagnosticCategoryStorage, DiagnosticCategoryMigration, DiagnosticCategoryPricing,
		DiagnosticCategoryReporting, DiagnosticCategoryValidation, DiagnosticCategorySecuritySensitive,
		DiagnosticCategoryUnknown:
		return true
	default:
		return false
	}
}
