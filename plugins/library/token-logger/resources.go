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
	"sort"
	"time"
)

const (
	// UnattributedProjectID and UnattributedSessionID identify synthetic local
	// navigation buckets; they are never persisted as provider identities.
	UnattributedProjectID = "komposyn:unattributed-project"
	UnattributedSessionID = "komposyn:unattributed-session"
)

// ResourceFilter limits project and session summaries without changing their
// stable identities.
type ResourceFilter struct {
	From     time.Time
	To       time.Time
	Client   string
	Provider string
	Model    string
}

// ProjectResource is a local project and its usage summary.
type ProjectResource struct {
	ID          string        `json:"id"`
	DisplayName string        `json:"display_name"`
	Location    string        `json:"location,omitempty"`
	Locations   []string      `json:"locations"`
	Clients     []string      `json:"clients"`
	Models      []string      `json:"models"`
	FirstActive *time.Time    `json:"first_active,omitempty"`
	LastActive  *time.Time    `json:"last_active,omitempty"`
	Summary     UsageMeasures `json:"summary"`
	Warnings    int64         `json:"reconciliation_warning_count"`
}

// SessionResource is a stable local session within a project.
type SessionResource struct {
	ID          string        `json:"id"`
	ProjectID   string        `json:"project_id,omitempty"`
	Client      string        `json:"client"`
	Models      []string      `json:"models"`
	StartedAt   *time.Time    `json:"started_at,omitempty"`
	EndedAt     *time.Time    `json:"ended_at,omitempty"`
	FirstActive *time.Time    `json:"first_active,omitempty"`
	LastActive  *time.Time    `json:"last_active,omitempty"`
	Summary     UsageMeasures `json:"summary"`
}

type ProjectResourceList struct {
	FormatVersion int               `json:"format_version"`
	Projects      []ProjectResource `json:"projects"`
}

type SessionResourceList struct {
	FormatVersion int               `json:"format_version"`
	ProjectID     string            `json:"project_id"`
	Sessions      []SessionResource `json:"sessions"`
}

// ListProjects returns local projects with summaries for the requested range.
func (s *Store) ListProjects(filter ResourceFilter) (ProjectResourceList, error) {
	rows, err := s.db.Query(`SELECT id, display_name FROM projects ORDER BY lower(display_name), id`)
	if err != nil {
		return ProjectResourceList{}, err
	}
	result := ProjectResourceList{FormatVersion: 1, Projects: []ProjectResource{}}
	for rows.Next() {
		var project ProjectResource
		if err := rows.Scan(&project.ID, &project.DisplayName); err != nil {
			rows.Close()
			return ProjectResourceList{}, err
		}
		result.Projects = append(result.Projects, project)
	}
	if err := rows.Close(); err != nil {
		return ProjectResourceList{}, err
	}
	for index := range result.Projects {
		if err := s.populateProjectResource(&result.Projects[index], filter); err != nil {
			return ProjectResourceList{}, err
		}
	}
	unattributedEvents, err := s.unattributedEventsExist(filter, false)
	if err != nil {
		return ProjectResourceList{}, err
	}
	var unattributedSessions int
	if err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM sessions WHERE project_id IS NULL)").Scan(&unattributedSessions); err != nil {
		return ProjectResourceList{}, err
	}
	if unattributedEvents || unattributedSessions != 0 {
		project := ProjectResource{ID: UnattributedProjectID, DisplayName: "Unattributed"}
		if err := s.populateProjectResource(&project, filter); err != nil {
			return ProjectResourceList{}, err
		}
		result.Projects = append(result.Projects, project)
	}
	return result, nil
}

// ProjectResource returns one stable local project.
func (s *Store) ProjectResource(id string, filter ResourceFilter) (ProjectResource, error) {
	if id == UnattributedProjectID {
		project := ProjectResource{ID: id, DisplayName: "Unattributed"}
		if err := s.populateProjectResource(&project, filter); err != nil {
			return ProjectResource{}, err
		}
		return project, nil
	}
	var project ProjectResource
	if err := s.db.QueryRow("SELECT id, display_name FROM projects WHERE id = ?", id).Scan(&project.ID, &project.DisplayName); err != nil {
		return ProjectResource{}, err
	}
	if err := s.populateProjectResource(&project, filter); err != nil {
		return ProjectResource{}, err
	}
	return project, nil
}

func (s *Store) populateProjectResource(project *ProjectResource, filter ResourceFilter) error {
	if project.ID == UnattributedProjectID {
		report, err := s.Query(resourceUsageFilter(filter, project.ID, ""))
		if err != nil {
			return err
		}
		project.Summary = report.Summary
		project.FirstActive, project.LastActive, err = s.activityBounds("project_id", project.ID, filter)
		if err != nil {
			return err
		}
		project.Clients, project.Models, err = s.resourceDimensions("project_id", project.ID, filter)
		return err
	}
	locationRows, err := s.db.Query(`SELECT canonical_path, is_preferred FROM project_locations
		WHERE project_id = ? ORDER BY is_preferred DESC, canonical_path`, project.ID)
	if err != nil {
		return err
	}
	for locationRows.Next() {
		var location string
		var preferred bool
		if err := locationRows.Scan(&location, &preferred); err != nil {
			locationRows.Close()
			return err
		}
		project.Locations = append(project.Locations, location)
		if project.Location == "" || preferred {
			project.Location = location
		}
	}
	if err := locationRows.Close(); err != nil {
		return err
	}
	report, err := s.Query(resourceUsageFilter(filter, project.ID, ""))
	if err != nil {
		return err
	}
	project.Summary = report.Summary
	project.FirstActive, project.LastActive, err = s.activityBounds("project_id", project.ID, filter)
	if err != nil {
		return err
	}
	project.Clients, project.Models, err = s.resourceDimensions("project_id", project.ID, filter)
	if err != nil {
		return err
	}
	return s.db.QueryRow("SELECT COUNT(*) FROM reconciliation_warnings WHERE project_id = ?", project.ID).Scan(&project.Warnings)
}

// ListSessions returns sessions belonging to one local project.
func (s *Store) ListSessions(projectID string, filter ResourceFilter) (SessionResourceList, error) {
	if projectID == "" {
		return SessionResourceList{}, errors.New("project ID is required")
	}
	query := `SELECT id, COALESCE(project_id, ''), client_name, started_at, ended_at FROM sessions WHERE project_id = ? ORDER BY COALESCE(started_at, ''), id`
	args := []any{projectID}
	if projectID == UnattributedProjectID {
		query = `SELECT id, '', client_name, started_at, ended_at FROM sessions WHERE project_id IS NULL ORDER BY COALESCE(started_at, ''), id`
		args = nil
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return SessionResourceList{}, err
	}
	result := SessionResourceList{FormatVersion: 1, ProjectID: projectID, Sessions: []SessionResource{}}
	for rows.Next() {
		var session SessionResource
		var started, ended sql.NullString
		if err := rows.Scan(&session.ID, &session.ProjectID, &session.Client, &started, &ended); err != nil {
			rows.Close()
			return SessionResourceList{}, err
		}
		session.StartedAt, err = parseNullableStoredTime(started)
		if err != nil {
			return SessionResourceList{}, err
		}
		session.EndedAt, err = parseNullableStoredTime(ended)
		if err != nil {
			return SessionResourceList{}, err
		}
		if projectID == UnattributedProjectID {
			session.ProjectID = UnattributedProjectID
		}
		result.Sessions = append(result.Sessions, session)
	}
	if err := rows.Close(); err != nil {
		return SessionResourceList{}, err
	}
	if projectID == UnattributedProjectID {
		missingSessionEvents, err := s.unattributedEventsExist(filter, true)
		if err != nil {
			return SessionResourceList{}, err
		}
		if missingSessionEvents {
			result.Sessions = append(result.Sessions, SessionResource{ID: UnattributedSessionID, ProjectID: UnattributedProjectID, Client: "unattributed"})
		}
	}
	for index := range result.Sessions {
		session := &result.Sessions[index]
		report, err := s.Query(resourceUsageFilter(filter, projectID, session.ID))
		if err != nil {
			return SessionResourceList{}, err
		}
		session.Summary = report.Summary
		session.FirstActive, session.LastActive, err = s.activityBounds("session_id", session.ID, filter)
		if err != nil {
			return SessionResourceList{}, err
		}
		_, session.Models, err = s.resourceDimensions("session_id", session.ID, filter)
		if err != nil {
			return SessionResourceList{}, err
		}
	}
	return result, nil
}

func (s *Store) unattributedEventsExist(filter ResourceFilter, sessionMissing bool) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM usage_events WHERE project_id IS NULL AND occurred_at >= ? AND occurred_at < ?`
	args := []any{formatStoredTime(filter.From), formatStoredTime(filter.To)}
	if sessionMissing {
		query += " AND session_id IS NULL"
	}
	query, args = appendResourceConditions(query, args, filter)
	query += ")"
	var exists bool
	err := s.db.QueryRow(query, args...).Scan(&exists)
	return exists, err
}

// SessionResource returns one session, optionally verifying project ownership.
func (s *Store) SessionResource(id, projectID string, filter ResourceFilter) (SessionResource, error) {
	if id == UnattributedSessionID {
		if projectID != "" && projectID != UnattributedProjectID {
			return SessionResource{}, errors.New("session does not belong to the selected project")
		}
		list, err := s.ListSessions(UnattributedProjectID, filter)
		if err != nil {
			return SessionResource{}, err
		}
		for _, session := range list.Sessions {
			if session.ID == id {
				return session, nil
			}
		}
		return SessionResource{}, sql.ErrNoRows
	}
	var actualProject string
	if err := s.db.QueryRow("SELECT COALESCE(project_id, '') FROM sessions WHERE id = ?", id).Scan(&actualProject); err != nil {
		return SessionResource{}, err
	}
	if actualProject == "" {
		actualProject = UnattributedProjectID
	}
	if projectID != "" && actualProject != projectID {
		return SessionResource{}, errors.New("session does not belong to the selected project")
	}
	list, err := s.ListSessions(actualProject, filter)
	if err != nil {
		return SessionResource{}, err
	}
	for _, session := range list.Sessions {
		if session.ID == id {
			return session, nil
		}
	}
	return SessionResource{}, sql.ErrNoRows
}

func (s *Store) activityBounds(column, id string, filter ResourceFilter) (*time.Time, *time.Time, error) {
	condition := column + " = ?"
	args := []any{id}
	if id == UnattributedProjectID {
		condition, args = "project_id IS NULL", nil
	} else if id == UnattributedSessionID {
		condition, args = "project_id IS NULL AND session_id IS NULL", nil
	}
	query := "SELECT MIN(occurred_at), MAX(occurred_at) FROM usage_events WHERE " + condition + " AND occurred_at >= ? AND occurred_at < ?"
	args = append(args, formatStoredTime(filter.From), formatStoredTime(filter.To))
	query, args = appendResourceConditions(query, args, filter)
	var first, last sql.NullString
	if err := s.db.QueryRow(query, args...).Scan(&first, &last); err != nil {
		return nil, nil, err
	}
	firstTime, err := parseNullableStoredTime(first)
	if err != nil {
		return nil, nil, err
	}
	lastTime, err := parseNullableStoredTime(last)
	return firstTime, lastTime, err
}

func (s *Store) resourceDimensions(column, id string, filter ResourceFilter) ([]string, []string, error) {
	condition := column + " = ?"
	args := []any{id}
	if id == UnattributedProjectID {
		condition, args = "project_id IS NULL", nil
	} else if id == UnattributedSessionID {
		condition, args = "project_id IS NULL AND session_id IS NULL", nil
	}
	query := `SELECT DISTINCT client_name, CASE WHEN resolved_model <> '' THEN resolved_model ELSE requested_model END
		FROM usage_events WHERE ` + condition + ` AND occurred_at >= ? AND occurred_at < ?`
	args = append(args, formatStoredTime(filter.From), formatStoredTime(filter.To))
	query, args = appendResourceConditions(query, args, filter)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	clientSet, modelSet := map[string]struct{}{}, map[string]struct{}{}
	for rows.Next() {
		var client, model string
		if err := rows.Scan(&client, &model); err != nil {
			return nil, nil, err
		}
		if client != "" {
			clientSet[client] = struct{}{}
		}
		if model != "" {
			modelSet[model] = struct{}{}
		}
	}
	clients, models := mapKeys(clientSet), mapKeys(modelSet)
	return clients, models, rows.Err()
}

func resourceUsageFilter(filter ResourceFilter, projectID, sessionID string) UsageFilter {
	usage := UsageFilter{From: filter.From, To: filter.To, ProjectID: projectID, SessionID: sessionID, Client: filter.Client, Provider: filter.Provider, Model: filter.Model}
	if projectID == UnattributedProjectID {
		usage.ProjectID, usage.ProjectMissing = "", true
	}
	if sessionID == UnattributedSessionID {
		usage.SessionID, usage.SessionMissing = "", true
	}
	return usage
}

func appendResourceConditions(query string, args []any, filter ResourceFilter) (string, []any) {
	if filter.Client != "" {
		query += " AND client_name = ?"
		args = append(args, filter.Client)
	}
	if filter.Provider != "" {
		query += " AND provider_name = ?"
		args = append(args, filter.Provider)
	}
	if filter.Model != "" {
		query += " AND (resolved_model = ? OR requested_model = ?)"
		args = append(args, filter.Model, filter.Model)
	}
	return query, args
}

func mapKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func parseNullableStoredTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid || value.String == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}
