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

// configureTerminalStreams wires readline's streams for a raw-mode terminal
// session and returns the writer application code must use for all further
// interactive output, together with the paste/command-mode toggle. Output is
// wrapped so bare line feeds return to column zero: term.MakeRaw disables
// OPOST on the controlling tty for the session, so without this, writes
// through the unwrapped stdout would advance a row without a carriage
// return.
func configureTerminalStreams(config *readline.Config, input *os.File, stdout, stderr io.Writer) (io.Writer, func(bool)) {
	config.Stdin = &pasteGuardReader{source: input}
	output := io.Writer(stdout)
	if file, ok := stdout.(*os.File); ok {
		output = newCRLFWriter(file)
	}
	config.Stdout = output
	config.Stderr = stderr
	return output, func(bool) {}
}

func enableTerminalFeatures(output io.Writer) {
	_, _ = io.WriteString(output, "\033[?2004h")
}

func disableTerminalFeatures(output io.Writer) {
	_, _ = io.WriteString(output, "\033[?2004l")
}

func interactiveInputHint() string { return "" }
