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

package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tokenlogger "github.com/ayeshLK/komposyn/plugins/library/token-logger"
	"github.com/mattn/go-runewidth"
)

type resourceOptions struct {
	dataDir   string
	format    string
	projectID string
	filter    tokenlogger.ResourceFilter
}

type sessionDetail struct {
	FormatVersion int                         `json:"format_version"`
	Session       tokenlogger.SessionResource `json:"session"`
	UsageByModel  tokenlogger.UsageReport     `json:"usage_by_model"`
}

func runProjectResources(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "list" && args[0] != "show") {
		fmt.Fprintln(stderr, "usage: komposyn-lg projects {list [options]|show <project-id> [options]}")
		return 1
	}
	action, id, optionArgs := args[0], "", args[1:]
	if action == "show" {
		if len(optionArgs) == 0 || strings.HasPrefix(optionArgs[0], "--") {
			fmt.Fprintln(stderr, "projects show requires a project ID")
			return 1
		}
		id, optionArgs = optionArgs[0], optionArgs[1:]
	}
	options, err := parseResourceOptions(optionArgs, action == "list")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	store, closeStore, ok := openResourceStore(options.dataDir, stderr)
	if !ok {
		return 2
	}
	defer closeStore()
	if action == "show" {
		project, err := store.ProjectResource(id, options.filter)
		if err != nil {
			fmt.Fprintf(stderr, "load project %q: %v\n", id, err)
			return 2
		}
		if options.format == "json" {
			err = writeResourceJSON(stdout, struct {
				FormatVersion int                         `json:"format_version"`
				Project       tokenlogger.ProjectResource `json:"project"`
			}{1, project})
		} else {
			err = writeProjectTable(stdout, []tokenlogger.ProjectResource{project})
		}
		if err != nil {
			fmt.Fprintf(stderr, "write project: %v\n", err)
			return 2
		}
		return 0
	}
	list, err := store.ListProjects(options.filter)
	if err != nil {
		fmt.Fprintf(stderr, "list projects: %v\n", err)
		return 2
	}
	if err := writeProjectResources(stdout, list, options.format); err != nil {
		fmt.Fprintf(stderr, "write projects: %v\n", err)
		return 2
	}
	return 0
}

func runSessionResources(args []string, stdout, stderr io.Writer) int {
	action := args[0]
	id, optionArgs := "", args[1:]
	if action == "show" {
		if len(optionArgs) == 0 || strings.HasPrefix(optionArgs[0], "--") {
			fmt.Fprintln(stderr, "sessions show requires a session ID")
			return 1
		}
		id, optionArgs = optionArgs[0], optionArgs[1:]
	}
	options, err := parseResourceOptions(optionArgs, true)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if action == "list" && options.projectID == "" {
		fmt.Fprintln(stderr, "sessions list requires --project <project-id>")
		return 1
	}
	store, closeStore, ok := openResourceStore(options.dataDir, stderr)
	if !ok {
		return 2
	}
	defer closeStore()
	if action == "show" {
		session, err := store.SessionResource(id, options.projectID, options.filter)
		if err != nil {
			fmt.Fprintf(stderr, "load session %q: %v\n", id, err)
			return 2
		}
		usageFilter := tokenlogger.UsageFilter{From: options.filter.From, To: options.filter.To, ProjectID: session.ProjectID, SessionID: session.ID, Client: options.filter.Client, Provider: options.filter.Provider, Model: options.filter.Model, GroupBy: []string{tokenlogger.GroupModel, tokenlogger.GroupProvider}}
		if session.ProjectID == tokenlogger.UnattributedProjectID {
			usageFilter.ProjectID, usageFilter.ProjectMissing = "", true
		}
		if session.ID == tokenlogger.UnattributedSessionID {
			usageFilter.SessionID, usageFilter.SessionMissing = "", true
		}
		report, err := store.Query(usageFilter)
		if err != nil {
			fmt.Fprintf(stderr, "query session usage: %v\n", err)
			return 2
		}
		if options.format == "json" {
			err = writeResourceJSON(stdout, sessionDetail{1, session, report})
		} else if options.format == "csv" {
			err = writeSessionDetailCSV(stdout, session, report)
		} else {
			err = writeSessionTable(stdout, []tokenlogger.SessionResource{session})
			if err == nil {
				err = tokenlogger.WriteTableWidth(stdout, report, terminalWidthOf(stdout))
			}
		}
		if err != nil {
			fmt.Fprintf(stderr, "write session: %v\n", err)
			return 2
		}
		return 0
	}
	list, err := store.ListSessions(options.projectID, options.filter)
	if err != nil {
		fmt.Fprintf(stderr, "list sessions: %v\n", err)
		return 2
	}
	if err := writeSessionResources(stdout, list, options.format); err != nil {
		fmt.Fprintf(stderr, "write sessions: %v\n", err)
		return 2
	}
	return 0
}

func parseResourceOptions(args []string, csvAllowed bool) (resourceOptions, error) {
	now := time.Now().UTC()
	options := resourceOptions{format: "table", filter: tokenlogger.ResourceFilter{From: now.AddDate(0, 0, -7), To: now}}
	for index := 0; index < len(args); index += 2 {
		if index+1 >= len(args) {
			return options, fmt.Errorf("missing value for %s", args[index])
		}
		value := args[index+1]
		switch args[index] {
		case "--data-dir":
			options.dataDir = value
		case "--project":
			options.projectID = value
		case "--format":
			if value != "table" && value != "json" && (!csvAllowed || value != "csv") {
				return options, fmt.Errorf("unsupported format %q", value)
			}
			options.format = value
		case "--from":
			parsed, err := parseTime(value)
			if err != nil {
				return options, fmt.Errorf("invalid --from: %w", err)
			}
			options.filter.From = parsed
		case "--to":
			parsed, err := parseTime(value)
			if err != nil {
				return options, fmt.Errorf("invalid --to: %w", err)
			}
			options.filter.To = parsed
		case "--client":
			options.filter.Client = value
		case "--provider":
			options.filter.Provider = value
		case "--model":
			options.filter.Model = value
		case "--group-by":
			if value != "model" {
				return options, errors.New("sessions show supports only --group-by model")
			}
		default:
			return options, fmt.Errorf("unknown option %q", args[index])
		}
	}
	if !options.filter.To.After(options.filter.From) {
		return options, errors.New("resource range must be increasing")
	}
	return options, nil
}

func openResourceStore(dataDir string, stderr io.Writer) (*tokenlogger.Store, func(), bool) {
	resolution, err := resolveDataDir(dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "resolve token logger data directory: %v\n", err)
		return nil, nil, false
	}
	reportDataDirResolution(stderr, resolution)
	store, err := tokenlogger.Open(filepath.Join(resolution.Path, "token-logger.db"))
	if err != nil {
		fmt.Fprintf(stderr, "open token logger database in %s: %v\n", resolution.Path, err)
		return nil, nil, false
	}
	return store, func() { _ = store.Close() }, true
}

func writeProjectResources(output io.Writer, list tokenlogger.ProjectResourceList, format string) error {
	switch format {
	case "json":
		return writeResourceJSON(output, list)
	case "csv":
		writer := csv.NewWriter(output)
		if err := writer.Write(resourceCSVHeader("project")); err != nil {
			return err
		}
		for _, project := range list.Projects {
			if err := writer.Write(resourceCSVRow(project.ID, project.DisplayName, project.Location, project.Clients, project.Models, project.FirstActive, project.LastActive, project.Summary)); err != nil {
				return err
			}
		}
		writer.Flush()
		return writer.Error()
	default:
		return writeProjectTable(output, list.Projects)
	}
}

func writeSessionResources(output io.Writer, list tokenlogger.SessionResourceList, format string) error {
	switch format {
	case "json":
		return writeResourceJSON(output, list)
	case "csv":
		writer := csv.NewWriter(output)
		if err := writer.Write(resourceCSVHeader("session")); err != nil {
			return err
		}
		for _, session := range list.Sessions {
			if err := writer.Write(resourceCSVRow(session.ID, session.Client, session.ProjectID, []string{session.Client}, session.Models, session.FirstActive, session.LastActive, session.Summary)); err != nil {
				return err
			}
		}
		writer.Flush()
		return writer.Error()
	default:
		return writeSessionTable(output, list.Sessions)
	}
}

func writeResourceJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeProjectTable(output io.Writer, projects []tokenlogger.ProjectResource) error {
	rows := [][]string{{"#", "PROJECT", "LOCATION", "ID", "SESSIONS", "REQUESTS", "TOKENS", "WARNINGS", "LAST ACTIVE"}}
	for index, project := range projects {
		rows = append(rows, []string{strconv.Itoa(index + 1), tokenlogger.FormatHumanLabel(project.DisplayName), humanLocation(project.Location), shortResourceID(project.ID), strconv.FormatInt(project.Summary.SessionCount, 10), strconv.FormatInt(project.Summary.RequestCount, 10), tokenlogger.FormatHumanTokens(project.Summary.TotalTokens), strconv.FormatInt(project.Warnings, 10), resourceTime(project.LastActive)})
	}
	return writeResourceRows(output, rows)
}

func writeSessionTable(output io.Writer, sessions []tokenlogger.SessionResource) error {
	rows := [][]string{{"#", "SESSION", "CLIENT", "STARTED", "LAST ACTIVE", "MODELS", "REQUESTS", "TOKENS"}}
	for index, session := range sessions {
		started := session.StartedAt
		if started == nil {
			started = session.FirstActive
		}
		rows = append(rows, []string{strconv.Itoa(index + 1), shortResourceID(session.ID), tokenlogger.FormatHumanLabel(session.Client), resourceTime(started), resourceTime(session.LastActive), strconv.Itoa(len(session.Models)), strconv.FormatInt(session.Summary.RequestCount, 10), tokenlogger.FormatHumanTokens(session.Summary.TotalTokens)})
	}
	return writeResourceRows(output, rows)
}

func writeInteractiveModelTable(output io.Writer, report tokenlogger.UsageReport) error {
	rows := [][]string{{"#", "MODEL", "PROVIDER", "REQUESTS", "INPUT", "CACHED", "CACHE WRITE", "OUTPUT", "REASONING", "TOTAL", "ESTIMATE", "REPORTED", "STATUS"}}
	for index, row := range report.Rows {
		model := row.Dimensions[tokenlogger.GroupModel]
		if model == "" {
			model = "(unattributed)"
		}
		provider := row.Dimensions[tokenlogger.GroupProvider]
		if provider == "" {
			provider = "(unattributed)"
		}
		estimate, reported, status := tokenlogger.FormatHumanCost(row.UsageMeasures)
		rows = append(rows, []string{strconv.Itoa(index + 1), tokenlogger.FormatHumanLabel(model), tokenlogger.FormatHumanLabel(provider), strconv.FormatInt(row.RequestCount, 10), tokenlogger.FormatHumanTokens(row.InputTokens), tokenlogger.FormatHumanTokens(row.CachedInputTokens), tokenlogger.FormatHumanTokens(row.CacheWriteInputTokens), tokenlogger.FormatHumanTokens(row.OutputTokens), tokenlogger.FormatHumanTokens(row.ReasoningOutputTokens), tokenlogger.FormatHumanTokens(row.TotalTokens), tokenlogger.FormatHumanText(estimate), tokenlogger.FormatHumanText(reported), tokenlogger.FormatHumanText(status)})
	}
	if err := writeResourceRows(output, rows); err != nil {
		return err
	}
	return tokenlogger.WriteHumanPricingCoverage(output, report.PricingCoverage)
}

func writeResourceRows(output io.Writer, rows [][]string) error {
	if len(rows) == 1 {
		_, err := fmt.Fprintln(output, "NO RESOURCE DATA")
		return err
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for index, cell := range row {
			if width := runewidth.StringWidth(cell); width > widths[index] {
				widths[index] = width
			}
		}
	}
	for _, row := range rows {
		for index, cell := range row {
			if _, err := io.WriteString(output, cell); err != nil {
				return err
			}
			if index+1 < len(row) {
				if _, err := io.WriteString(output, strings.Repeat(" ", widths[index]-runewidth.StringWidth(cell)+2)); err != nil {
					return err
				}
			}
		}
		if _, err := io.WriteString(output, "\n"); err != nil {
			return err
		}
	}
	return nil
}

func humanLocation(location string) string {
	if location == "" {
		return "location unavailable"
	}
	home, err := os.UserHomeDir()
	if err == nil && (location == home || strings.HasPrefix(location, home+string(filepath.Separator))) {
		location = "~" + strings.TrimPrefix(location, home)
	}
	return tokenlogger.FormatHumanLabel(location)
}

func shortResourceID(id string) string {
	return tokenlogger.FormatHumanLabel(id)
}

func writeSessionDetailCSV(output io.Writer, session tokenlogger.SessionResource, report tokenlogger.UsageReport) error {
	writer := csv.NewWriter(output)
	header := []string{"format_version", "session_id", "project_id", "client", "model", "provider", "request_count", "session_count", "input_tokens", "cached_input_tokens", "cache_write_input_tokens", "output_tokens", "reasoning_output_tokens", "total_tokens", "calculated_cost_micros", "calculated_currency", "provider_reported_cost_micros", "provider_reported_currency", "unknown_cost_requests", "estimated_cost_requests", "known_zero_cost_requests", "cost_unknown_reason", "calculation_basis", "pricing_provider", "pricing_version", "pricing_source"}
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, row := range report.Rows {
		m := row.UsageMeasures
		values := []string{"1", session.ID, session.ProjectID, session.Client, row.Dimensions[tokenlogger.GroupModel], row.Dimensions[tokenlogger.GroupProvider], strconv.FormatInt(m.RequestCount, 10), strconv.FormatInt(m.SessionCount, 10), strconv.FormatInt(m.InputTokens, 10), strconv.FormatInt(m.CachedInputTokens, 10), strconv.FormatInt(m.CacheWriteInputTokens, 10), strconv.FormatInt(m.OutputTokens, 10), strconv.FormatInt(m.ReasoningOutputTokens, 10), strconv.FormatInt(m.TotalTokens, 10), strconv.FormatInt(m.CalculatedCostMicros, 10), m.CalculatedCurrency, strconv.FormatInt(m.ProviderReportedCostMicros, 10), m.ProviderReportedCurrency, strconv.FormatInt(m.UnknownCostRequests, 10), strconv.FormatInt(m.EstimatedCostRequests, 10), strconv.FormatInt(m.KnownZeroCostRequests, 10), m.CostUnknownReason, m.CalculationBasis, m.PricingProvider, m.PricingVersion, m.PricingSource}
		if err := writer.Write(values); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func resourceTime(value *time.Time) string {
	if value == nil {
		return "unavailable"
	}
	return value.UTC().Format("2006-01-02 15:04Z")
}

func resourceCSVHeader(kind string) []string {
	return []string{"format_version", kind + "_id", "name", "location_or_project_id", "clients", "models", "first_active", "last_active", "session_count", "request_count", "input_tokens", "cached_input_tokens", "cache_write_input_tokens", "output_tokens", "reasoning_output_tokens", "total_tokens", "calculated_cost_micros", "calculated_currency", "unknown_cost_requests", "cost_status"}
}

func resourceCSVRow(id, name, location string, clients, models []string, first, last *time.Time, summary tokenlogger.UsageMeasures) []string {
	return []string{"1", id, name, location, strings.Join(clients, ";"), strings.Join(models, ";"), csvTime(first), csvTime(last), strconv.FormatInt(summary.SessionCount, 10), strconv.FormatInt(summary.RequestCount, 10), strconv.FormatInt(summary.InputTokens, 10), strconv.FormatInt(summary.CachedInputTokens, 10), strconv.FormatInt(summary.CacheWriteInputTokens, 10), strconv.FormatInt(summary.OutputTokens, 10), strconv.FormatInt(summary.ReasoningOutputTokens, 10), strconv.FormatInt(summary.TotalTokens, 10), strconv.FormatInt(summary.CalculatedCostMicros, 10), summary.CalculatedCurrency, strconv.FormatInt(summary.UnknownCostRequests, 10), summary.CostUnknownReason}
}

func csvTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
