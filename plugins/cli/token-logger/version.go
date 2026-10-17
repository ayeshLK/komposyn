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

import tokenlogger "github.com/ayeshLK/komposyn/plugins/library/token-logger"

// cliVersion is injected independently from the library version by release
// builds. The combined release currently assigns both the same tag value.
var cliVersion = "0.0.0-dev"

func versionOutput() string {
	return "komposyn-lg " + cliVersion + " (core " + tokenlogger.CoreVersion() + ")"
}
