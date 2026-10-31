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

package tview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

type clipboardWriter interface {
	Write(context.Context, string) error
}

type clipboardQueue struct {
	mu      sync.Mutex
	pending chan string
	write   func(context.Context, string) error
	osc52   func(string)
	result  func(string)
}

func newClipboardQueue(ctx context.Context, writer clipboardWriter, osc52 func(string), result func(string)) *clipboardQueue {
	if writer == nil {
		writer = nativeClipboard{}
	}
	q := &clipboardQueue{pending: make(chan string, 1), write: writer.Write, osc52: osc52, result: result}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case text := <-q.pending:
				writeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
				err := q.write(writeCtx, text)
				cancel()
				message := "Copied using the native clipboard helper.\n"
				if err != nil {
					if q.osc52 != nil {
						q.osc52(text)
						message = "Clipboard request sent to the terminal. The terminal or multiplexer may block it; use Ctrl+T for terminal selection if it does not appear.\n"
					} else {
						message = "Clipboard write failed; use Ctrl+T for terminal selection.\n"
					}
				}
				if q.result != nil {
					q.result(message)
				}
			}
		}
	}()
	return q
}

func (q *clipboardQueue) enqueue(text string) bool {
	if text == "" || q.pending == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	select {
	case q.pending <- text:
		return true
	default:
		// A write in flight is allowed to finish. Replace the single pending
		// item so the most recent explicit copy is always the final write.
		select {
		case <-q.pending:
		default:
		}
		select {
		case q.pending <- text:
			return true
		default:
			return false
		}
	}
}

type nativeClipboard struct{}

func (nativeClipboard) Write(ctx context.Context, text string) error {
	if remoteSession() {
		return errors.New("remote session uses terminal clipboard protocol")
	}
	var candidates [][]string
	switch runtime.GOOS {
	case "darwin":
		candidates = [][]string{{"pbcopy"}}
	case "windows":
		candidates = [][]string{{"powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "[Console]::InputEncoding = New-Object System.Text.UTF8Encoding; $text = [Console]::In.ReadToEnd(); Set-Clipboard -Value $text"}}
	case "linux":
		if isWSL() {
			candidates = [][]string{{"clip.exe"}, {"powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "[Console]::InputEncoding = New-Object System.Text.UTF8Encoding; $text = [Console]::In.ReadToEnd(); Set-Clipboard -Value $text"}}
		} else if os.Getenv("WAYLAND_DISPLAY") != "" {
			candidates = [][]string{{"wl-copy", "--type", "text/plain;charset=utf-8"}, {"xclip", "-selection", "clipboard", "-in"}, {"xsel", "--clipboard", "--input"}}
		} else {
			candidates = [][]string{{"xclip", "-selection", "clipboard", "-in"}, {"xsel", "--clipboard", "--input"}, {"wl-copy", "--type", "text/plain;charset=utf-8"}}
		}
	default:
		return errors.New("no native clipboard backend")
	}
	var failures []string
	for _, candidate := range candidates {
		if _, err := exec.LookPath(candidate[0]); err != nil {
			continue
		}
		cmd := exec.CommandContext(ctx, candidate[0], candidate[1:]...)
		cmd.Stdin = strings.NewReader(text)
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		cmd.WaitDelay = 250 * time.Millisecond
		if err := cmd.Run(); err == nil {
			return nil
		} else {
			failures = append(failures, fmt.Sprintf("%s failed", candidate[0]))
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if len(failures) == 0 {
		return errors.New("no native clipboard helper available")
	}
	return errors.New(strings.Join(failures, "; "))
}

func remoteSession() bool {
	return os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != ""
}

func isWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
		return true
	}
	data, err := os.ReadFile("/proc/version")
	return err == nil && strings.Contains(strings.ToLower(string(data)), "microsoft")
}
