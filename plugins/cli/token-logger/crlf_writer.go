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
	"io"
	"os"
	"sync"
)

// crlfWriter normalizes bare line feeds to carriage-return/line-feed pairs
// so that application output returns to column zero on a terminal whose
// OPOST output processing has been disabled by term.MakeRaw, without
// duplicating carriage returns already present in the stream (such as the
// readline library's own cursor-control sequences). lastByte tracks state
// across Write calls so a line feed at the start of one call is handled
// correctly even if the preceding carriage return arrived in an earlier
// call. Readline redraws from its own goroutine while application code (and
// terminal teardown) can write from the main goroutine, so access is
// serialized with a mutex.
type crlfWriter struct {
	mu       sync.Mutex
	dest     io.Writer
	file     *os.File
	lastByte byte
}

// newCRLFWriter wraps a terminal file so writes through it are CRLF-safe.
func newCRLFWriter(file *os.File) *crlfWriter {
	return &crlfWriter{dest: file, file: file}
}

func (w *crlfWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]byte, 0, len(p)+8)
	previous := w.lastByte
	for _, b := range p {
		if b == '\n' && previous != '\r' {
			out = append(out, '\r')
		}
		out = append(out, b)
		previous = b
	}
	if _, err := w.dest.Write(out); err != nil {
		return 0, err
	}
	w.lastByte = p[len(p)-1]
	return len(p), nil
}

// TerminalWidth reports the current terminal column width backing this
// writer, or 0 if it cannot be determined.
func (w *crlfWriter) TerminalWidth() int {
	if w.file == nil {
		return 0
	}
	return terminalWidthOf(w.file)
}
