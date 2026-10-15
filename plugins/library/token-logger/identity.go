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
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func canonicalProjectLocation(location string) (string, error) {
	location = strings.TrimSpace(location)
	if location == "" {
		return "", nil
	}
	absolute, err := filepath.Abs(location)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = filepath.Clean(resolved)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return canonicalPathCase(absolute), nil
}

func persistExplicitProjectLocation(tx *sql.Tx, project Project, now string) error {
	canonical, err := canonicalProjectLocation(project.Root)
	if err != nil || canonical == "" {
		return err
	}
	_, err = tx.Exec(`INSERT INTO project_locations(project_id, canonical_path, display_path, is_preferred, first_seen_at, last_seen_at, provenance)
		VALUES (?, ?, ?, CASE WHEN EXISTS(SELECT 1 FROM project_locations WHERE project_id = ?) THEN 0 ELSE 1 END, ?, ?, 'explicit local metadata')
		ON CONFLICT(project_id, canonical_path) DO UPDATE SET last_seen_at = excluded.last_seen_at`,
		project.ID, canonical, project.Root, project.ID, now, now)
	return err
}

func identityScope(adapter, client string) string {
	adapter = strings.TrimSpace(adapter)
	client = strings.TrimSpace(client)
	if adapter == "" {
		return ""
	}
	if client == "" {
		return adapter
	}
	return adapter + ":" + client
}

func stableLocalID(kind, scope, alias string) string {
	return kind + "-" + digest([]byte("komposyn-local-"+kind+"\x00"+scope+"\x00"+alias))
}

func reconcileImportedReferences(tx *sql.Tx, events []UsageEvent, projects []Project, sessions []Session) ([]UsageEvent, []Project, []Session, error) {
	projectIDs := make(map[string]string)
	sessionIDs := make(map[string]string)
	now := formatStoredTime(time.Now())

	for index := range projects {
		project := &projects[index]
		alias := strings.TrimSpace(project.ProviderAlias)
		if alias == "" {
			alias = strings.TrimSpace(project.ID)
		}
		scope := identityScope(project.SourceAdapter, "")
		if scope == "" || alias == "" {
			continue
		}
		localID, locationAccepted, err := resolveProject(tx, *project, scope, alias, now)
		if err != nil {
			return nil, nil, nil, err
		}
		if !locationAccepted {
			project.Root = ""
		}
		projectIDs[scope+"\x00"+project.ID] = localID
		projectIDs[scope+"\x00"+alias] = localID
		project.ID = localID
	}

	for index := range sessions {
		session := &sessions[index]
		alias := strings.TrimSpace(session.ProviderAlias)
		if alias == "" {
			alias = strings.TrimSpace(session.ID)
		}
		scope := identityScope(session.SourceAdapter, session.Client.Name)
		if scope == "" || alias == "" {
			continue
		}
		projectScope := identityScope(session.SourceAdapter, "")
		if local, ok := projectIDs[projectScope+"\x00"+session.ProjectID]; ok {
			session.ProjectID = local
		}
		localID, localProjectID, err := resolveSession(tx, *session, scope, alias, now)
		if err != nil {
			return nil, nil, nil, err
		}
		sessionIDs[scope+"\x00"+session.ID] = localID
		sessionIDs[scope+"\x00"+alias] = localID
		session.ID, session.ProjectID = localID, localProjectID
	}

	for index := range events {
		event := &events[index]
		projectScope := identityScope(event.Provenance.Adapter, "")
		if local, ok := projectIDs[projectScope+"\x00"+event.ProjectID]; ok {
			event.ProjectID = local
		}
		sessionScope := identityScope(event.Provenance.Adapter, event.Client.Name)
		if local, ok := sessionIDs[sessionScope+"\x00"+event.SessionID]; ok {
			event.SessionID = local
			for _, session := range sessions {
				if session.ID == local {
					event.ProjectID = session.ProjectID
					break
				}
			}
		}
	}
	return events, projects, sessions, nil
}

func resolveProject(tx *sql.Tx, project Project, scope, alias, now string) (string, bool, error) {
	var localID string
	err := tx.QueryRow("SELECT project_id FROM project_aliases WHERE scope = ? AND alias = ?", scope, alias).Scan(&localID)
	canonical, canonicalErr := canonicalProjectLocation(project.Root)
	if canonicalErr != nil {
		return "", false, canonicalErr
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	aliasAlreadyMapped := err == nil
	if errors.Is(err, sql.ErrNoRows) && canonical != "" {
		matches, err := projectsAtLocation(tx, canonical)
		if err != nil {
			return "", false, err
		}
		if len(matches) == 1 {
			localID = matches[0]
		} else if len(matches) > 1 {
			localID = stableLocalID("project", scope, alias)
			if err := recordReconciliationWarning(tx, "ambiguous-location", scope, alias, localID, "", "location matches multiple local projects; kept an isolated identity", now); err != nil {
				return "", false, err
			}
		}
	}
	if localID == "" {
		localID = stableLocalID("project", scope, alias)
	}
	locationAccepted := true
	if aliasAlreadyMapped && canonical != "" {
		matches, err := projectsAtLocation(tx, canonical)
		if err != nil {
			return "", false, err
		}
		for _, match := range matches {
			if match != localID {
				locationAccepted = false
				if err := recordReconciliationWarning(tx, "alias-location-conflict", scope, alias, localID, "", "established alias retained; location belongs to another local project", now); err != nil {
					return "", false, err
				}
				break
			}
		}
	}
	name := strings.TrimSpace(project.DisplayName)
	if name == "" {
		name = "Unnamed project"
	}
	if _, err := tx.Exec(`INSERT INTO projects(id, root, display_name, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		root = CASE WHEN projects.root = '' THEN excluded.root ELSE projects.root END,
		display_name = CASE WHEN projects.display_name = '' THEN excluded.display_name ELSE projects.display_name END`, localID, project.Root, name, now); err != nil {
		return "", false, err
	}
	if canonical != "" && locationAccepted {
		preferred := 0
		if aliasAlreadyMapped {
			if _, err := tx.Exec("UPDATE project_locations SET is_preferred = 0 WHERE project_id = ?", localID); err != nil {
				return "", false, err
			}
			preferred = 1
		}
		if _, err := tx.Exec(`INSERT INTO project_locations(project_id, canonical_path, display_path, is_preferred, first_seen_at, last_seen_at, provenance)
			VALUES (?, ?, ?, CASE WHEN ? = 1 OR NOT EXISTS(SELECT 1 FROM project_locations WHERE project_id = ?) THEN 1 ELSE 0 END, ?, ?, ?)
			ON CONFLICT(project_id, canonical_path) DO UPDATE SET last_seen_at = excluded.last_seen_at,
			is_preferred = CASE WHEN excluded.is_preferred = 1 THEN 1 ELSE project_locations.is_preferred END`, localID, canonical, project.Root, preferred, localID, now, now, project.AliasProvenance); err != nil {
			return "", false, err
		}
	}
	if _, err := tx.Exec(`INSERT INTO project_aliases(scope, alias, project_id, provenance, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(scope, alias) DO UPDATE SET last_seen_at = excluded.last_seen_at`, scope, alias, localID, project.AliasProvenance, now, now); err != nil {
		return "", false, err
	}
	return localID, locationAccepted, nil
}

func projectsAtLocation(tx *sql.Tx, canonical string) ([]string, error) {
	rows, err := tx.Query("SELECT DISTINCT project_id FROM project_locations WHERE canonical_path = ?", canonical)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var matches []string
	for rows.Next() {
		var match string
		if err := rows.Scan(&match); err != nil {
			return nil, err
		}
		matches = append(matches, match)
	}
	return matches, rows.Err()
}

func resolveSession(tx *sql.Tx, session Session, scope, alias, now string) (string, string, error) {
	var localID, establishedProjectID string
	err := tx.QueryRow("SELECT session_id, COALESCE(project_id, '') FROM session_aliases WHERE scope = ? AND alias = ?", scope, alias).Scan(&localID, &establishedProjectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", "", err
	}
	if errors.Is(err, sql.ErrNoRows) {
		localID = stableLocalID("session", scope, alias)
		establishedProjectID = session.ProjectID
	} else if establishedProjectID != "" && session.ProjectID != "" && establishedProjectID != session.ProjectID {
		if err := recordReconciliationWarning(tx, "session-project-conflict", scope, alias, establishedProjectID, localID, "established session project retained; conflicting import was not reassigned", now); err != nil {
			return "", "", err
		}
	} else if establishedProjectID == "" {
		establishedProjectID = session.ProjectID
	}
	if _, err := tx.Exec(`INSERT INTO sessions(id, project_id, client_name, client_version, client_mode, started_at, ended_at, source_metadata)
		VALUES (?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET client_version = excluded.client_version, client_mode = excluded.client_mode,
		started_at = COALESCE(sessions.started_at, excluded.started_at), ended_at = COALESCE(excluded.ended_at, sessions.ended_at),
		source_metadata = COALESCE(excluded.source_metadata, sessions.source_metadata)`, localID, establishedProjectID, session.Client.Name,
		session.Client.Version, session.Client.Mode, nullableTime(session.StartedAt), nullableTime(session.EndedAt), nullableJSON(session.SourceMetadata)); err != nil {
		return "", "", err
	}
	if _, err := tx.Exec(`INSERT INTO session_aliases(scope, alias, session_id, project_id, provenance, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?)
		ON CONFLICT(scope, alias) DO UPDATE SET last_seen_at = excluded.last_seen_at`, scope, alias, localID, establishedProjectID, session.SourceAdapter, now, now); err != nil {
		return "", "", err
	}
	return localID, establishedProjectID, nil
}

func recordReconciliationWarning(tx *sql.Tx, kind, scope, alias, projectID, sessionID, message, now string) error {
	aliasDigest := digest([]byte(alias))
	_, err := tx.Exec(`INSERT INTO reconciliation_warnings(kind, scope, alias_digest, project_id, session_id, message, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(kind, scope, alias_digest, project_id, session_id) DO UPDATE SET
		last_seen_at = excluded.last_seen_at, occurrence_count = occurrence_count + 1`, kind, scope, aliasDigest, projectID, sessionID, message, now, now)
	if err != nil {
		return fmt.Errorf("record reconciliation warning: %w", err)
	}
	return nil
}
