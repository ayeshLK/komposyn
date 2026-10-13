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

//go:build windows

package main

import (
	"encoding/binary"
	"io"
	"os"
	"syscall"
	"time"
	"unsafe"

	"github.com/chzyer/readline"
	"golang.org/x/sys/windows"
)

var peekConsoleInputW = syscall.NewLazyDLL("kernel32.dll").NewProc("PeekConsoleInputW")

// Keep readline's Windows RawReader and ANSIWriter behavior, while folding
// line breaks that are followed by more queued console input into the current
// command. Native console paste arrives as key events rather than bracketed
// paste markers.
func configureTerminalStreams(config *readline.Config, input *os.File, _, _ io.Writer) {
	config.Stdin = &windowsPasteGuardReader{
		source:       readline.NewRawReader(),
		inputHandle:  windows.Handle(input.Fd()),
		pendingInput: windowsConsoleHasPendingKeyDown,
		settleDelay:  10 * time.Millisecond,
	}
}

func enableTerminalFeatures(io.Writer)  {}
func disableTerminalFeatures(io.Writer) {}

type windowsPasteGuardReader struct {
	source       io.Reader
	inputHandle  windows.Handle
	pendingInput func(windows.Handle) (bool, error)
	settleDelay  time.Duration
	output       []byte
}

func (reader *windowsPasteGuardReader) Read(target []byte) (int, error) {
	if len(target) == 0 {
		return 0, nil
	}
	for len(reader.output) == 0 {
		chunk := make([]byte, 4096)
		count, err := reader.source.Read(chunk)
		if count == 0 {
			return 0, err
		}
		for _, value := range chunk[:count] {
			if value == '\r' || value == '\n' {
				if reader.settleDelay > 0 {
					time.Sleep(reader.settleDelay)
				}
				pending, pendingErr := reader.pendingInput(reader.inputHandle)
				if pendingErr != nil {
					return 0, pendingErr
				}
				if pending {
					value = ' '
				}
			}
			reader.output = append(reader.output, value)
		}
		if err != nil && len(reader.output) == 0 {
			return 0, err
		}
	}
	count := copy(target, reader.output)
	reader.output = reader.output[count:]
	return count, nil
}

func (reader *windowsPasteGuardReader) Close() error {
	if closer, ok := reader.source.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

type windowsInputRecord struct {
	eventType uint16
	padding   uint16
	event     [16]byte
}

func windowsConsoleHasPendingKeyDown(handle windows.Handle) (bool, error) {
	const maxRecords = 128
	var records [maxRecords]windowsInputRecord
	var read uint32
	result, _, callErr := peekConsoleInputW.Call(
		uintptr(handle), uintptr(unsafe.Pointer(&records[0])), maxRecords, uintptr(unsafe.Pointer(&read)),
	)
	if result == 0 {
		if callErr == nil || callErr == syscall.Errno(0) {
			callErr = syscall.EINVAL
		}
		return false, callErr
	}
	return windowsRecordsHavePendingKeyDown(records[:read]), nil
}

func windowsRecordsHavePendingKeyDown(records []windowsInputRecord) bool {
	for _, record := range records {
		if record.eventType == windows.KEY_EVENT && binary.LittleEndian.Uint32(record.event[:4]) != 0 {
			return true
		}
	}
	return false
}
