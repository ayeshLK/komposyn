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
	"github.com/mattn/go-isatty"
)

// configureTerminalStreams wires readline's streams for a raw-mode terminal
// session and returns the writers application code must use for all further
// interactive output, together with the paste/command-mode toggle. Both
// stdout and stderr are wrapped so bare line feeds return to column zero:
// term.MakeRaw disables OPOST on the controlling tty for the session (stdin,
// stdout, and stderr are usually the same tty), so without this, writes
// through either unwrapped stream would advance a row without a carriage
// return.
func configureTerminalStreams(config *readline.Config, input *os.File, stdout, stderr io.Writer) (io.Writer, io.Writer, func(bool)) {
	config.Stdin = &pasteGuardReader{source: input}
	output := crlfSafeWriter(input, stdout)
	errOutput := crlfSafeWriter(input, stderr)
	config.Stdout = output
	config.Stderr = errOutput
	return output, errOutput, func(bool) {}
}

// crlfSafeWriter wraps w only when it refers to the same terminal device as
// the raw input. A redirected file/pipe or a different terminal did not have
// its output processing changed by term.MakeRaw and must remain byte-for-byte
// untouched.
func crlfSafeWriter(input *os.File, w io.Writer) io.Writer {
	file, ok := w.(*os.File)
	if !ok || !(isatty.IsTerminal(file.Fd()) || isatty.IsCygwinTerminal(file.Fd())) {
		return w
	}
	inputInfo, inputErr := input.Stat()
	outputInfo, outputErr := file.Stat()
	if inputErr != nil || outputErr != nil || !os.SameFile(inputInfo, outputInfo) {
		return w
	}
	return newCRLFWriter(file)
}

func enableTerminalFeatures(output io.Writer) {
	_, _ = io.WriteString(output, "\033[?2004h")
}

func disableTerminalFeatures(output io.Writer) {
	_, _ = io.WriteString(output, "\033[?2004l")
}

func interactiveInputHint() string { return "" }
