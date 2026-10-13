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
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"github.com/chzyer/readline"
	"golang.org/x/sys/windows"
)

var readConsoleInputW = syscall.NewLazyDLL("kernel32.dll").NewProc("ReadConsoleInputW")

// Windows console key events do not mark which Enter keys came from a paste.
// Treat plain Enter as command text and require Ctrl+Enter to submit, so pasted
// line breaks can never submit a command batch.
func configureTerminalStreams(config *readline.Config, input *os.File, _, _ io.Writer) {
	config.Stdin = &windowsConsoleKeyReader{
		inputHandle: windows.Handle(input.Fd()),
		readRecord:  readWindowsConsoleInputRecord,
	}
}

func enableTerminalFeatures(io.Writer)  {}
func disableTerminalFeatures(io.Writer) {}

type windowsConsoleKeyReader struct {
	inputHandle   windows.Handle
	readRecord    func(windows.Handle) (windowsInputRecord, error)
	output        []byte
	highSurrogate uint16
}

func (reader *windowsConsoleKeyReader) Read(target []byte) (int, error) {
	if len(target) == 0 {
		return 0, nil
	}
	for len(reader.output) == 0 {
		record, err := reader.readRecord(reader.inputHandle)
		if err != nil {
			return 0, err
		}
		char, ok := windowsConsoleRecordRune(record)
		if !ok {
			continue
		}
		if char >= 0xD800 && char <= 0xDBFF {
			reader.highSurrogate = uint16(char)
			continue
		}
		if char >= 0xDC00 && char <= 0xDFFF && reader.highSurrogate != 0 {
			char = rune(utf16.DecodeRune(rune(reader.highSurrogate), char))
			reader.highSurrogate = 0
		} else if reader.highSurrogate != 0 {
			reader.output = utf8.AppendRune(reader.output, utf8.RuneError)
			reader.highSurrogate = 0
		}
		reader.output = utf8.AppendRune(reader.output, char)
	}
	count := copy(target, reader.output)
	reader.output = reader.output[count:]
	return count, nil
}

func (reader *windowsConsoleKeyReader) Close() error { return nil }

func windowsConsoleRecordRune(record windowsInputRecord) (rune, bool) {
	if record.eventType != windows.KEY_EVENT || binary.LittleEndian.Uint32(record.event[:4]) == 0 {
		return 0, false
	}
	key := binary.LittleEndian.Uint16(record.event[6:8])
	char := rune(binary.LittleEndian.Uint16(record.event[10:12]))
	modifiers := binary.LittleEndian.Uint32(record.event[12:16])
	control := modifiers&(windows.LEFT_CTRL_PRESSED|windows.RIGHT_CTRL_PRESSED) != 0
	if control {
		switch key {
		case 'C':
			return readline.CharInterrupt, true
		case 'D':
			return readline.CharDelete, true
		case 'L':
			return readline.CharCtrlL, true
		case 'A':
			return readline.CharLineStart, true
		case 'E':
			return readline.CharLineEnd, true
		case 'R':
			return readline.CharBckSearch, true
		case 'S':
			return readline.CharFwdSearch, true
		case windows.VK_RETURN:
			return readline.CharEnter, true
		}
	}
	if key == windows.VK_RETURN {
		return ' ', true
	}
	if char != 0 {
		return char, true
	}
	switch key {
	case windows.VK_LEFT:
		return readline.CharBackward, true
	case windows.VK_RIGHT:
		return readline.CharForward, true
	case windows.VK_UP:
		return readline.CharPrev, true
	case windows.VK_DOWN:
		return readline.CharNext, true
	case windows.VK_BACK:
		return readline.CharBackspace, true
	default:
		return 0, false
	}
}

type windowsInputRecord struct {
	eventType uint16
	padding   uint16
	event     [16]byte
}

func readWindowsConsoleInputRecord(handle windows.Handle) (windowsInputRecord, error) {
	var record windowsInputRecord
	var read uint32
	result, _, callErr := readConsoleInputW.Call(
		uintptr(handle), uintptr(unsafe.Pointer(&record)), 1, uintptr(unsafe.Pointer(&read)),
	)
	if result == 0 || read != 1 {
		if callErr == nil || callErr == syscall.Errno(0) {
			callErr = syscall.EINVAL
		}
		return windowsInputRecord{}, callErr
	}
	return record, nil
}
