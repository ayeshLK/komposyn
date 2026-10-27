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

package auth

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestOAuthProfilesValidateAgainstPublishedSchema(t *testing.T) {
	schemaPath, err := filepath.Abs(filepath.Join("..", "..", "..", "schema", "harness-auth.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := jsonschema.NewCompiler().Compile(schemaPath)
	if err != nil {
		t.Fatalf("compile auth schema: %v", err)
	}

	store := Store{Path: filepath.Join(t.TempDir(), "auth.json")}
	connected := addExpiringStoreOAuthProfile(t, store, time.Now().Add(time.Hour))
	disconnected := cloneProfile(connected)
	disconnected.Auth.OAuth.AccessToken = ""
	disconnected.Auth.OAuth.RefreshToken = ""
	disconnected.Auth.OAuth.TokenType = ""
	disconnected.Auth.OAuth.ExpiresAt = time.Time{}
	disconnected.Auth.OAuth.ReconnectRequired = true
	unknown := cloneProfile(connected)
	unknown.Auth.OAuth.ReconnectRequired = true
	unknown.Auth.OAuth.RefreshOutcomeUnknown = true

	for _, profile := range []struct {
		name    string
		profile Profile
	}{
		{name: "connected", profile: connected},
		{name: "disconnected", profile: disconnected},
		{name: "refresh outcome unknown", profile: unknown},
	} {
		t.Run(profile.name, func(t *testing.T) {
			if err := validateProfile(profile.profile); err != nil {
				t.Fatalf("profile is invalid: %v", err)
			}
			doc := document{SchemaVersion: SchemaVersion, HostID: profile.profile.Auth.OAuth.HostID, Credentials: []Profile{profile.profile}}
			data, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("decode serialized profile: %v", err)
			}
			if err := schema.Validate(instance); err != nil {
				t.Fatalf("serialized auth profile violates published schema: %v\n%s", err, data)
			}
		})
	}
}
