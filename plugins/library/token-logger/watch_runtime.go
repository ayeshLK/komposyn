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
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrCollectorActive = errors.New("collector is already active")
var ErrCollectorLeaseLost = errors.New("collector lease is no longer owned by this instance")
var ErrInvalidCollectorSource = errors.New("invalid collector source")

type RegisteredSource struct {
	ID            string     `json:"id"`
	Path          string     `json:"path"`
	CanonicalPath string     `json:"canonical_path"`
	Enabled       bool       `json:"enabled"`
	Status        string     `json:"status"`
	LastError     string     `json:"last_error,omitempty"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
}

type SourceRegistrationResult struct {
	Source  RegisteredSource `json:"source"`
	Changed bool             `json:"changed"`
}

type CollectorRuntimeStatus struct {
	FormatVersion      int           `json:"format_version"`
	State              string        `json:"state"`
	InstanceID         string        `json:"instance_id,omitempty"`
	PID                int           `json:"pid,omitempty"`
	ExecutableVersion  string        `json:"executable_version,omitempty"`
	Mode               string        `json:"mode,omitempty"`
	Interval           time.Duration `json:"-"`
	IntervalMillis     int64         `json:"interval_millis"`
	StartedAt          *time.Time    `json:"started_at,omitempty"`
	HeartbeatAt        *time.Time    `json:"heartbeat_at,omitempty"`
	LastPollAt         *time.Time    `json:"last_poll_at,omitempty"`
	LastCompletedAt    *time.Time    `json:"last_completed_at,omitempty"`
	StoppedAt          *time.Time    `json:"stopped_at,omitempty"`
	StopRequestedAt    *time.Time    `json:"stop_requested_at,omitempty"`
	Imported           int64         `json:"imported"`
	Duplicates         int64         `json:"duplicates"`
	Outcomes           OutcomeCounts `json:"outcomes"`
	Pending            bool          `json:"pending"`
	SourceCount        int           `json:"source_count"`
	DiscoveredSources  int           `json:"discovered_sources"`
	DiscoveryFailures  int           `json:"discovery_failures"`
	RecoveredFromStale bool          `json:"recovered_from_stale"`
	LastError          string        `json:"last_error,omitempty"`
	Stale              bool          `json:"stale"`
}

func NewCollectorInstanceID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("create collector instance identity: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func canonicalRegisteredPath(path string, requireExisting bool) (string, string, error) {
	if strings.TrimSpace(path) == "" {
		return "", "", errors.New("source path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("resolve source path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if requireExisting {
		info, err := os.Stat(absolute)
		if err != nil {
			return "", "", fmt.Errorf("inspect source: %w", err)
		}
		if info.IsDir() {
			file, err := os.Open(absolute)
			if err != nil {
				return "", "", fmt.Errorf("read source directory: %w", err)
			}
			file.Close()
		} else {
			file, err := os.Open(absolute)
			if err != nil {
				return "", "", fmt.Errorf("read source file: %w", err)
			}
			file.Close()
		}
	}
	canonical := absolute
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		canonical = filepath.Clean(resolved)
	} else if requireExisting || !errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("resolve source symlinks: %w", err)
	}
	canonical = canonicalPathCase(canonical)
	return absolute, canonical, nil
}

func (s *Store) RegisterCollectorSource(path string, requireExisting bool) (SourceRegistrationResult, error) {
	display, canonical, err := canonicalRegisteredPath(path, requireExisting)
	if err != nil {
		return SourceRegistrationResult{}, fmt.Errorf("%w: %v", ErrInvalidCollectorSource, err)
	}
	now := formatStoredTime(time.Now())
	tx, err := s.db.Begin()
	if err != nil {
		return SourceRegistrationResult{}, err
	}
	defer tx.Rollback()
	var existing RegisteredSource
	var enabled int
	err = tx.QueryRow(`SELECT id, display_path, canonical_path, enabled, latest_status, latest_error, last_checked_at
		FROM collector_sources WHERE canonical_path = ?`, canonical).Scan(&existing.ID, &existing.Path, &existing.CanonicalPath, &enabled, &existing.Status, &existing.LastError, nullableTimeScanner(&existing.LastCheckedAt))
	if err == nil {
		existing.Enabled = enabled != 0
		changed := !existing.Enabled
		if _, err := tx.Exec(`UPDATE collector_sources SET enabled = 1, updated_at = ? WHERE id = ?`, now, existing.ID); err != nil {
			return SourceRegistrationResult{}, err
		}
		existing.Enabled = true
		if err := tx.Commit(); err != nil {
			return SourceRegistrationResult{}, err
		}
		return SourceRegistrationResult{Source: existing, Changed: changed}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SourceRegistrationResult{}, err
	}
	id, err := NewCollectorInstanceID()
	if err != nil {
		return SourceRegistrationResult{}, err
	}
	if _, err := tx.Exec(`INSERT INTO collector_sources(id, canonical_path, display_path, enabled, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, ?)`, id, canonical, display, now, now); err != nil {
		return SourceRegistrationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SourceRegistrationResult{}, err
	}
	return SourceRegistrationResult{Source: RegisteredSource{ID: id, Path: display, CanonicalPath: canonical, Enabled: true, Status: "unknown"}, Changed: true}, nil
}

func (s *Store) RemoveCollectorSource(selector string) (RegisteredSource, bool, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return RegisteredSource{}, false, errors.New("source or ID is required")
	}
	canonical := ""
	if _, value, err := canonicalRegisteredPath(selector, false); err == nil {
		canonical = value
	}
	var source RegisteredSource
	var enabled int
	err := s.db.QueryRow(`SELECT id, display_path, canonical_path, enabled, latest_status, latest_error, last_checked_at
		FROM collector_sources WHERE id = ? OR canonical_path = ?`, selector, canonical).Scan(&source.ID, &source.Path, &source.CanonicalPath, &enabled, &source.Status, &source.LastError, nullableTimeScanner(&source.LastCheckedAt))
	if errors.Is(err, sql.ErrNoRows) {
		return RegisteredSource{}, false, nil
	}
	if err != nil {
		return RegisteredSource{}, false, err
	}
	source.Enabled = enabled != 0
	if !source.Enabled {
		return source, false, nil
	}
	_, err = s.db.Exec(`UPDATE collector_sources SET enabled = 0, updated_at = ? WHERE id = ?`, formatStoredTime(time.Now()), source.ID)
	return source, err == nil, err
}

func (s *Store) ListCollectorSources(enabledOnly bool) ([]RegisteredSource, error) {
	query := `SELECT id, display_path, canonical_path, enabled, latest_status, latest_error, last_checked_at FROM collector_sources`
	if enabledOnly {
		query += ` WHERE enabled = 1`
	}
	query += ` ORDER BY canonical_path, id`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []RegisteredSource{}
	for rows.Next() {
		var source RegisteredSource
		var enabled int
		if err := rows.Scan(&source.ID, &source.Path, &source.CanonicalPath, &enabled, &source.Status, &source.LastError, nullableTimeScanner(&source.LastCheckedAt)); err != nil {
			return nil, err
		}
		source.Enabled = enabled != 0
		result = append(result, source)
	}
	return result, rows.Err()
}

type nullableTimeValue struct{ target **time.Time }

func nullableTimeScanner(target **time.Time) *nullableTimeValue {
	return &nullableTimeValue{target: target}
}
func (value *nullableTimeValue) Scan(src any) error {
	if src == nil {
		*value.target = nil
		return nil
	}
	text, ok := src.(string)
	if !ok {
		if bytes, bytesOK := src.([]byte); bytesOK {
			text = string(bytes)
		} else {
			return fmt.Errorf("invalid stored time %T", src)
		}
	}
	parsed, err := time.Parse(storedTimeLayout, text)
	if err != nil {
		return err
	}
	parsed = parsed.UTC()
	*value.target = &parsed
	return nil
}

func (s *Store) UpdateCollectorSourceHealth(id, status, message string) error {
	if message != "" {
		message = SanitizeDiagnostic(message)
	}
	_, err := s.db.Exec(`UPDATE collector_sources SET latest_status = ?, latest_error = ?, last_checked_at = ?, updated_at = ? WHERE id = ?`,
		status, message, formatStoredTime(time.Now()), formatStoredTime(time.Now()), id)
	return err
}

func (s *Store) AcquireCollectorLease(instanceID string, pid int, version, mode string, interval, staleAfter time.Duration) (bool, error) {
	if instanceID == "" {
		return false, errors.New("collector instance ID is required")
	}
	if interval <= 0 {
		return false, errors.New("collector interval must be positive")
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var currentID, state string
	var heartbeat sql.NullString
	if err := tx.QueryRow(`SELECT instance_id, state, heartbeat_at FROM collector_runtime WHERE singleton = 1`).Scan(&currentID, &state, &heartbeat); err != nil {
		return false, err
	}
	recovered := false
	if state == "starting" || state == "running" || state == "degraded" || state == "stopping" {
		last, parseErr := parseNullableStoredTime(heartbeat)
		if parseErr != nil {
			return false, parseErr
		}
		if last != nil && now.Sub(*last) <= staleAfter {
			return false, ErrCollectorActive
		}
		recovered = true
	}
	nowText := formatStoredTime(now)
	if recovered {
		if _, err := tx.Exec(`UPDATE collector_runs SET stopped_at = COALESCE(stopped_at, ?), terminal_state = 'stale',
			last_error = 'collector heartbeat expired' WHERE instance_id = ?`, nowText, currentID); err != nil {
			return false, err
		}
	}
	_, err = tx.Exec(`UPDATE collector_runtime SET instance_id = ?, pid = ?, executable_version = ?, mode = ?, state = 'starting', interval_millis = ?,
		started_at = ?, heartbeat_at = ?, last_poll_at = NULL, last_completed_at = NULL, stopped_at = NULL, stop_requested_at = NULL,
		imported = 0, duplicates = 0, ignored_non_usage = 0, unchanged_snapshot = 0, deferred_incomplete_tail = 0,
		unsupported_usage = 0, malformed_record = 0, failed_source = 0, pending = 0, source_count = 0,
		discovered_sources = 0, discovery_failures = 0, recovered_from_stale = ?, last_error = '' WHERE singleton = 1`, instanceID, pid, version, mode, interval.Milliseconds(), nowText, nowText, recovered)
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(`INSERT INTO collector_runs(instance_id, pid, executable_version, mode, interval_millis, started_at)
		VALUES (?, ?, ?, ?, ?, ?)`, instanceID, pid, version, mode, interval.Milliseconds(), nowText)
	if err != nil {
		return false, err
	}
	return recovered, tx.Commit()
}

func (s *Store) MarkCollectorReady(instanceID string) error {
	result, err := s.db.Exec(`UPDATE collector_runtime SET state = 'running', heartbeat_at = ? WHERE singleton = 1 AND instance_id = ?`, formatStoredTime(time.Now()), instanceID)
	return leaseResult(result, err)
}

func (s *Store) HeartbeatCollector(instanceID string) (bool, error) {
	result, err := s.db.Exec(`UPDATE collector_runtime SET heartbeat_at = ? WHERE singleton = 1 AND instance_id = ?`, formatStoredTime(time.Now()), instanceID)
	if err := leaseResult(result, err); err != nil {
		return false, err
	}
	var requested sql.NullString
	if err := s.db.QueryRow(`SELECT stop_requested_at FROM collector_runtime WHERE singleton = 1 AND instance_id = ?`, instanceID).Scan(&requested); err != nil {
		return false, err
	}
	return requested.Valid, nil
}

func leaseResult(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrCollectorLeaseLost
	}
	return nil
}

func (s *Store) BeginCollectorPoll(instanceID string, sourceCount int) error {
	result, err := s.db.Exec(`UPDATE collector_runtime SET state = 'running', heartbeat_at = ?, last_poll_at = ?, source_count = ?
		WHERE singleton = 1 AND instance_id = ?`, formatStoredTime(time.Now()), formatStoredTime(time.Now()), sourceCount, instanceID)
	return leaseResult(result, err)
}

func (s *Store) CompleteCollectorPoll(instanceID string, result ImportResult, health CollectorHealth, pollErr error) error {
	state, message := "running", ""
	if pollErr != nil || result.Outcomes.Incomplete() {
		state = "degraded"
	}
	if pollErr != nil {
		message = SanitizeDiagnostic(pollErr.Error())
	}
	completedAt := time.Now()
	pollAt := health.LastPollAt
	if pollAt.IsZero() {
		pollAt = completedAt
	}
	updated, err := s.db.Exec(`UPDATE collector_runtime SET state = ?, heartbeat_at = ?, last_poll_at = ?, last_completed_at = ?,
		imported = imported + ?, duplicates = duplicates + ?, ignored_non_usage = ignored_non_usage + ?,
		unchanged_snapshot = unchanged_snapshot + ?, deferred_incomplete_tail = deferred_incomplete_tail + ?,
		unsupported_usage = unsupported_usage + ?, malformed_record = malformed_record + ?, failed_source = failed_source + ?,
		pending = ?, discovered_sources = ?, discovery_failures = ?, last_error = ? WHERE singleton = 1 AND instance_id = ?`,
		state, formatStoredTime(completedAt), formatStoredTime(pollAt), formatStoredTime(completedAt), result.Receipt.Imported, result.Receipt.Duplicates,
		result.Outcomes.IgnoredNonUsage, result.Outcomes.UnchangedSnapshot, result.Outcomes.DeferredIncompleteTail,
		result.Outcomes.UnsupportedUsage, result.Outcomes.MalformedRecord, result.Outcomes.FailedSource,
		result.Outcomes.Pending(), health.Sources, health.DiscoveryFailures, message, instanceID)
	return leaseResult(updated, err)
}

func (s *Store) RequestCollectorStop() (bool, error) {
	result, err := s.db.Exec(`UPDATE collector_runtime SET state = 'stopping', stop_requested_at = COALESCE(stop_requested_at, ?)
		WHERE singleton = 1 AND state IN ('starting', 'running', 'degraded', 'stopping')`, formatStoredTime(time.Now()))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s *Store) ReleaseCollectorLease(instanceID, terminalState, message string) error {
	now := formatStoredTime(time.Now())
	if message != "" {
		message = SanitizeDiagnostic(message)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE collector_runtime SET state = ?, heartbeat_at = ?, stopped_at = ?, last_error = ?
		WHERE singleton = 1 AND instance_id = ?`, terminalState, now, now, message, instanceID)
	if err := leaseResult(result, err); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE collector_runs SET stopped_at = ?, terminal_state = ?, last_error = ? WHERE instance_id = ?`, now, terminalState, message, instanceID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CollectorRuntimeStatus(staleAfter time.Duration) (CollectorRuntimeStatus, error) {
	var status CollectorRuntimeStatus
	var started, heartbeat, poll, completed, stopped, requested sql.NullString
	var pending, recovered int
	err := s.db.QueryRow(`SELECT state, instance_id, pid, executable_version, mode, interval_millis, started_at, heartbeat_at,
		last_poll_at, last_completed_at, stopped_at, stop_requested_at, imported, duplicates, ignored_non_usage,
		unchanged_snapshot, deferred_incomplete_tail, unsupported_usage, malformed_record, failed_source, pending,
		source_count, discovered_sources, discovery_failures, recovered_from_stale, last_error FROM collector_runtime WHERE singleton = 1`).Scan(
		&status.State, &status.InstanceID, &status.PID, &status.ExecutableVersion, &status.Mode, &status.IntervalMillis,
		&started, &heartbeat, &poll, &completed, &stopped, &requested, &status.Imported, &status.Duplicates,
		&status.Outcomes.IgnoredNonUsage, &status.Outcomes.UnchangedSnapshot, &status.Outcomes.DeferredIncompleteTail,
		&status.Outcomes.UnsupportedUsage, &status.Outcomes.MalformedRecord, &status.Outcomes.FailedSource, &pending,
		&status.SourceCount, &status.DiscoveredSources, &status.DiscoveryFailures, &recovered, &status.LastError)
	if err != nil {
		return status, err
	}
	status.FormatVersion, status.Pending = 1, pending != 0
	status.RecoveredFromStale = recovered != 0
	status.Interval = time.Duration(status.IntervalMillis) * time.Millisecond
	for _, item := range []struct {
		value  sql.NullString
		target **time.Time
	}{{started, &status.StartedAt}, {heartbeat, &status.HeartbeatAt}, {poll, &status.LastPollAt}, {completed, &status.LastCompletedAt}, {stopped, &status.StoppedAt}, {requested, &status.StopRequestedAt}} {
		parsed, err := parseNullableStoredTime(item.value)
		if err != nil {
			return status, err
		}
		*item.target = parsed
	}
	if staleAfter > 0 && (status.State == "starting" || status.State == "running" || status.State == "degraded" || status.State == "stopping") && (status.HeartbeatAt == nil || time.Since(*status.HeartbeatAt) > staleAfter) {
		status.State, status.Stale = "stale", true
	}
	return status, nil
}
