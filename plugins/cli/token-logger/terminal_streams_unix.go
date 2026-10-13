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

//go:build !windows

package main

import (
	"io"
	"os"

	"github.com/chzyer/readline"
)

func configureTerminalStreams(config *readline.Config, input *os.File, stdout, stderr io.Writer) {
	config.Stdin = &pasteGuardReader{source: input}
	config.Stdout = stdout
	config.Stderr = stderr
}

func enableTerminalFeatures(output io.Writer) {
	_, _ = io.WriteString(output, "\033[?2004h")
}

func disableTerminalFeatures(output io.Writer) {
	_, _ = io.WriteString(output, "\033[?2004l")
}
