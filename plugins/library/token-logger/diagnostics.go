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
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Diagnostic is a locally persisted, sanitized interactive error.
type Diagnostic struct {
	Fingerprint     string
	Message         string
	OccurrenceCount int
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
	Status          string
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
	sanitized := SanitizeDiagnostic(message)
	sum := sha256.Sum256([]byte(sanitized))
	fingerprint := hex.EncodeToString(sum[:])
	now := time.Now().UTC()
	_, err := s.db.Exec(`INSERT INTO diagnostics(fingerprint, message, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(fingerprint) DO UPDATE SET occurrence_count = occurrence_count + 1,
		last_seen_at = excluded.last_seen_at`, fingerprint, sanitized,
		formatStoredTime(now), formatStoredTime(now))
	if err != nil {
		return Diagnostic{}, fmt.Errorf("persist diagnostic: %w", err)
	}
	return s.Diagnostic(fingerprint)
}

// Diagnostic loads one local diagnostic by fingerprint.
func (s *Store) Diagnostic(fingerprint string) (Diagnostic, error) {
	var diagnostic Diagnostic
	var first, last string
	err := s.db.QueryRow(`SELECT fingerprint, message, occurrence_count, first_seen_at, last_seen_at, status
		FROM diagnostics WHERE fingerprint = ?`, fingerprint).Scan(&diagnostic.Fingerprint, &diagnostic.Message,
		&diagnostic.OccurrenceCount, &first, &last, &diagnostic.Status)
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
	rows, err := s.db.Query(`SELECT fingerprint, message, occurrence_count, first_seen_at, last_seen_at, status
		FROM diagnostics WHERE status = 'pending' ORDER BY last_seen_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var diagnostics []Diagnostic
	for rows.Next() {
		var diagnostic Diagnostic
		var first, last string
		if err := rows.Scan(&diagnostic.Fingerprint, &diagnostic.Message, &diagnostic.OccurrenceCount, &first, &last, &diagnostic.Status); err != nil {
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

// MarkDiagnosticReviewed suppresses repeated prompts after the user has
// explicitly reviewed or skipped a diagnostic.
func (s *Store) MarkDiagnosticReviewed(fingerprint, status string) error {
	if status != "reviewed" && status != "skipped" {
		return fmt.Errorf("unsupported diagnostic status %q", status)
	}
	_, err := s.db.Exec("UPDATE diagnostics SET status = ? WHERE fingerprint = ?", status, fingerprint)
	return err
}

// GitHubIssueDraftURL returns a prefilled issue draft without sending a
// request or including local paths, credentials, or source content.
func GitHubIssueDraftURL(diagnostic Diagnostic) string {
	shortFingerprint := diagnostic.Fingerprint
	if len(shortFingerprint) > 12 {
		shortFingerprint = shortFingerprint[:12]
	}
	title := "Komposyn Token Logger diagnostic: " + shortFingerprint
	body := fmt.Sprintf("Sanitized diagnostic fingerprint: %s\n\nMessage:\n%s\n\nOccurrences: %d", diagnostic.Fingerprint, diagnostic.Message, diagnostic.OccurrenceCount)
	return "https://github.com/ayeshLK/komposyn/issues/new?" + url.Values{
		"title": {title},
		"body":  {body},
	}.Encode()
}
