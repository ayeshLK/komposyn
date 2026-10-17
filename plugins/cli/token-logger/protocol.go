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
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"

	tokenlogger "github.com/ayeshLK/komposyn/plugins/library/token-logger"
)

const (
	protocolName            = "komposyn.token-logger"
	protocolVersion         = 1
	maxProtocolMessageBytes = 8 << 20
)

type protocolRequest struct {
	Protocol string          `json:"protocol,omitempty"`
	Version  int             `json:"version,omitempty"`
	ID       string          `json:"id,omitempty"`
	Method   string          `json:"method"`
	Params   json.RawMessage `json:"params,omitempty"`
}

type protocolResponse struct {
	Protocol string         `json:"protocol"`
	Version  int            `json:"version"`
	ID       string         `json:"id,omitempty"`
	OK       bool           `json:"ok"`
	Result   any            `json:"result,omitempty"`
	Error    *protocolError `json:"error,omitempty"`
}

type protocolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type initializeParams struct {
	DataDir string `json:"data_dir"`
}

type recordParams struct {
	Event   tokenlogger.UsageEvent `json:"event"`
	Project *tokenlogger.Project   `json:"project,omitempty"`
	Session *tokenlogger.Session   `json:"session,omitempty"`
}

type protocolServer struct {
	store   *tokenlogger.Store
	dataDir string
}

func runProtocol(input io.Reader, stdout, stderr io.Writer) int {
	server := &protocolServer{}
	defer server.close()

	encoder := json.NewEncoder(stdout)
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), maxProtocolMessageBytes)
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var request protocolRequest
		err := json.Unmarshal(scanner.Bytes(), &request)
		if err != nil {
			writeProtocolDiagnostic(stderr, fmt.Errorf("decode protocol request: %w", err))
			if encodeErr := encoder.Encode(errorResponse("", "invalid_json", "request is not valid JSON")); encodeErr != nil {
				return 2
			}
			continue
		}

		response, shutdown := server.handle(request, stderr)
		if err := encoder.Encode(response); err != nil {
			writeProtocolDiagnostic(stderr, fmt.Errorf("encode protocol response: %w", err))
			return 2
		}
		if shutdown {
			return 0
		}
	}
	if err := scanner.Err(); err != nil {
		writeProtocolDiagnostic(stderr, fmt.Errorf("read protocol request: %w", err))
		return 2
	}
	return 0
}

func (s *protocolServer) handle(request protocolRequest, stderr io.Writer) (protocolResponse, bool) {
	if request.Protocol != "" && request.Protocol != protocolName {
		return errorResponse(request.ID, "unsupported_protocol", "unsupported protocol"), false
	}
	if request.Version != 0 && request.Version != protocolVersion {
		return errorResponse(request.ID, "unsupported_version", "unsupported protocol version"), false
	}
	switch request.Method {
	case "initialize":
		return s.initialize(request, stderr)
	case "record":
		return s.record(request, stderr), false
	case "flush":
		if s.store == nil {
			return errorResponse(request.ID, "not_initialized", "initialize must be called first"), false
		}
		return successResponse(request.ID, map[string]any{"durable": true}), false
	case "health":
		return s.health(request.ID), false
	case "shutdown":
		if s.store == nil {
			return successResponse(request.ID, map[string]any{"stopped": true}), true
		}
		if err := s.store.Close(); err != nil {
			writeProtocolDiagnostic(stderr, fmt.Errorf("close store: %w", err))
			return errorResponse(request.ID, "shutdown_failed", "could not close logger store"), false
		}
		s.store = nil
		return successResponse(request.ID, map[string]any{"stopped": true}), true
	default:
		return errorResponse(request.ID, "unknown_method", "unknown protocol method"), false
	}
}

func (s *protocolServer) initialize(request protocolRequest, stderr io.Writer) (protocolResponse, bool) {
	var params initializeParams
	if len(request.Params) > 0 && string(request.Params) != "null" {
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return errorResponse(request.ID, "invalid_params", "initialize params are invalid"), false
		}
	}
	resolution, err := resolveDataDir(params.DataDir)
	if err != nil {
		writeProtocolDiagnostic(stderr, fmt.Errorf("resolve token logger data directory: %w", err))
		return errorResponse(request.ID, "initialize_failed", "could not resolve logger data directory; see harness diagnostics"), false
	}
	reportDataDirResolution(stderr, resolution)
	params.DataDir = resolution.Path
	store, err := tokenlogger.Open(filepath.Join(params.DataDir, "token-logger.db"))
	if err != nil {
		writeProtocolDiagnostic(stderr, fmt.Errorf("open logger store in %s: %w", params.DataDir, err))
		return errorResponse(request.ID, "initialize_failed", "could not open logger store; see harness diagnostics"), false
	}
	if s.store != nil {
		_ = s.store.Close()
	}
	s.store, s.dataDir = store, params.DataDir
	return successResponse(request.ID, map[string]any{
		"plugin_id":       "token-logger",
		"display_name":    "Komposyn Token Logger",
		"plugin_version":  tokenlogger.CoreVersion(),
		"cli_version":     cliVersion,
		"schema_versions": tokenlogger.SupportedEventSchemaVersions(),
		"capabilities":    []string{"usage-events", "local-reports"},
		"data_dir":        s.dataDir,
	}), false
}

func (s *protocolServer) record(request protocolRequest, stderr io.Writer) protocolResponse {
	if s.store == nil {
		return errorResponse(request.ID, "not_initialized", "initialize must be called first")
	}
	var params recordParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		return errorResponse(request.ID, "invalid_params", "record params are invalid")
	}
	if params.Event.EventID == "" {
		if err := json.Unmarshal(request.Params, &params.Event); err != nil {
			return errorResponse(request.ID, "invalid_params", "record event is invalid")
		}
	}
	projects := make([]tokenlogger.Project, 0, 1)
	sessions := make([]tokenlogger.Session, 0, 1)
	if params.Event.ProjectID != "" {
		if params.Project == nil {
			return errorResponse(request.ID, "missing_project_metadata", "project metadata is required when event.project_id is set")
		}
		if params.Project.ID != params.Event.ProjectID {
			return errorResponse(request.ID, "invalid_project_metadata", "project metadata ID must match event.project_id")
		}
		projects = append(projects, *params.Project)
	}
	if params.Event.SessionID != "" {
		if params.Session == nil {
			return errorResponse(request.ID, "missing_session_metadata", "session metadata is required when event.session_id is set")
		}
		if params.Session.ID != params.Event.SessionID {
			return errorResponse(request.ID, "invalid_session_metadata", "session metadata ID must match event.session_id")
		}
		if params.Session.ProjectID != "" {
			if params.Event.ProjectID != "" && params.Session.ProjectID != params.Event.ProjectID {
				return errorResponse(request.ID, "invalid_session_metadata", "session project_id must match event.project_id")
			}
			if params.Project == nil {
				return errorResponse(request.ID, "missing_project_metadata", "project metadata is required when session.project_id is set")
			}
			if params.Project.ID != params.Session.ProjectID {
				return errorResponse(request.ID, "invalid_project_metadata", "project metadata ID must match session.project_id")
			}
			if len(projects) == 0 {
				projects = append(projects, *params.Project)
			}
		}
		sessions = append(sessions, *params.Session)
	}
	receipt, err := s.store.AppendWithReferences([]tokenlogger.UsageEvent{params.Event}, projects, sessions)
	if err != nil {
		writeProtocolDiagnostic(stderr, err)
		return errorResponse(request.ID, "record_failed", "could not persist usage event")
	}
	return successResponse(request.ID, map[string]any{
		"imported":   receipt.Imported,
		"duplicates": receipt.Duplicates,
	})
}

func (s *protocolServer) health(id string) protocolResponse {
	result := map[string]any{"status": "uninitialized"}
	if s.store != nil {
		count, err := s.store.EventCount()
		if err != nil {
			return errorResponse(id, "health_failed", "could not query logger health")
		}
		result = map[string]any{"status": "healthy", "event_count": count, "data_dir": s.dataDir}
	}
	return successResponse(id, result)
}

func (s *protocolServer) close() {
	if s.store != nil {
		_ = s.store.Close()
		s.store = nil
	}
}

func successResponse(id string, result any) protocolResponse {
	return protocolResponse{Protocol: protocolName, Version: protocolVersion, ID: id, OK: true, Result: result}
}

func errorResponse(id, code, message string) protocolResponse {
	return protocolResponse{Protocol: protocolName, Version: protocolVersion, ID: id, Error: &protocolError{Code: code, Message: message}}
}

func writeProtocolDiagnostic(stderr io.Writer, err error) {
	fmt.Fprintf(stderr, "protocol diagnostic: %s\n", tokenlogger.SanitizeDiagnostic(err.Error()))
}
