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
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"reflect"
	"testing"

	"github.com/chzyer/readline"
	"golang.org/x/sys/windows"
)

func TestWindowsClearUsesNativeConsoleBufferOperations(t *testing.T) {
	ops := &recordingWindowsConsoleClearer{
		info: windows.ConsoleScreenBufferInfo{
			Size:       windows.Coord{X: 120, Y: 100},
			Attributes: 7,
			Window:     windows.SmallRect{Left: 2, Top: 10, Right: 81, Bottom: 49},
		},
	}
	previousHandle := getWindowsStdoutHandle
	previousOps := windowsConsoleClearOpsForOS
	getWindowsStdoutHandle = func() (windows.Handle, error) { return 12, nil }
	windowsConsoleClearOpsForOS = ops
	defer func() {
		getWindowsStdoutHandle = previousHandle
		windowsConsoleClearOpsForOS = previousOps
	}()

	var configuredWriter bytes.Buffer
	if err := clearTerminal(&configuredWriter); err != nil {
		t.Fatal(err)
	}
	if configuredWriter.Len() != 0 {
		t.Fatalf("native clear wrote escape bytes through the configured writer: %q", configuredWriter.String())
	}
	expectedCalls := []string{"info"}
	for range 40 {
		expectedCalls = append(expectedCalls, "characters", "attributes")
	}
	expectedCalls = append(expectedCalls, "cursor")
	if !reflect.DeepEqual(ops.calls, expectedCalls) {
		t.Fatalf("console calls = %v", ops.calls)
	}
	if ops.cells != 80 || ops.attributes != 7 || ops.cursor != (windows.Coord{X: 2, Y: 10}) {
		t.Fatalf("clear parameters: last row width=%d attributes=%d cursor=%+v", ops.cells, ops.attributes, ops.cursor)
	}
	if len(ops.positions) != 40 || ops.positions[0] != (windows.Coord{X: 2, Y: 10}) || ops.positions[39] != (windows.Coord{X: 2, Y: 49}) {
		t.Fatalf("visible row positions = %v", ops.positions)
	}
}

type recordingWindowsConsoleClearer struct {
	info       windows.ConsoleScreenBufferInfo
	calls      []string
	cells      uintptr
	attributes uint16
	positions  []windows.Coord
	cursor     windows.Coord
}

func (ops *recordingWindowsConsoleClearer) screenBufferInfo(windows.Handle) (windows.ConsoleScreenBufferInfo, error) {
	ops.calls = append(ops.calls, "info")
	return ops.info, nil
}

func (ops *recordingWindowsConsoleClearer) fillCharacters(_ windows.Handle, _ uint16, cells uintptr, position windows.Coord) error {
	ops.calls = append(ops.calls, "characters")
	ops.cells = cells
	ops.positions = append(ops.positions, position)
	return nil
}

func (ops *recordingWindowsConsoleClearer) fillAttributes(_ windows.Handle, attributes uint16, _ uintptr, _ windows.Coord) error {
	ops.calls = append(ops.calls, "attributes")
	ops.attributes = attributes
	return nil
}

func (ops *recordingWindowsConsoleClearer) setCursor(_ windows.Handle, origin windows.Coord) error {
	ops.calls = append(ops.calls, "cursor")
	ops.cursor = origin
	return nil
}

func TestWindowsConsoleKeyReaderRequiresCtrlEnterToSubmit(t *testing.T) {
	var records []windowsInputRecord
	for _, char := range "first" {
		records = append(records, windowsTestKeyRecord(uint16(char), uint16(char), 0))
	}
	records = append(records, windowsTestKeyRecord(windows.VK_RETURN, '\r', 0))
	for _, char := range "second" {
		records = append(records, windowsTestKeyRecord(uint16(char), uint16(char), 0))
	}
	records = append(records, windowsTestKeyRecord(windows.VK_RETURN, '\r', windows.LEFT_CTRL_PRESSED))
	index := 0
	reader := &windowsConsoleKeyReader{
		readRecord: func(windows.Handle) (windowsInputRecord, error) {
			if index == len(records) {
				return windowsInputRecord{}, io.EOF
			}
			record := records[index]
			index++
			return record, nil
		},
	}
	reader.SetCommandMode(true)
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "first second\r" {
		t.Fatalf("Windows console input = %q", output)
	}
	if got := rune(readline.CharEnter); got != rune(output[len(output)-1]) {
		t.Fatalf("Ctrl+Enter rune = %d; want %d", output[len(output)-1], got)
	}
	plainEnter, ok := windowsConsoleRecordRune(windowsTestKeyRecord(windows.VK_RETURN, '\r', 0), false)
	if !ok || plainEnter != readline.CharEnter {
		t.Fatalf("plain Enter in a prompt = %d, %v; want submit rune %d", plainEnter, ok, readline.CharEnter)
	}
}

func TestWindowsConsoleReaderKeepsEnterSubmissionForNonCommandPrompts(t *testing.T) {
	records := []windowsInputRecord{
		windowsTestKeyRecord(windows.VK_RETURN, '\r', 0),
		windowsTestKeyRecord(windows.VK_RETURN, '\r', 0),
	}
	index := 0
	reader := &windowsConsoleKeyReader{
		readRecord: func(windows.Handle) (windowsInputRecord, error) {
			if index == len(records) {
				return windowsInputRecord{}, io.EOF
			}
			record := records[index]
			index++
			return record, nil
		},
	}
	reader.SetCommandMode(true)
	commandEnter := make([]byte, 1)
	if _, err := reader.Read(commandEnter); err != nil {
		t.Fatal(err)
	}
	if commandEnter[0] != ' ' {
		t.Fatalf("plain Enter in command mode = %q; want space", commandEnter[0])
	}
	reader.SetCommandMode(false)
	promptEnter := make([]byte, 1)
	if _, err := reader.Read(promptEnter); err != nil {
		t.Fatal(err)
	}
	if promptEnter[0] != '\r' {
		t.Fatalf("plain Enter in prompt mode = %q; want carriage return", promptEnter[0])
	}
}

func TestWindowsConsoleControlKeys(t *testing.T) {
	tests := []struct {
		name string
		key  uint16
		char uint16
		want rune
	}{
		{name: "Ctrl+C", key: 'C', char: 'C', want: readline.CharInterrupt},
		{name: "Ctrl+D", key: 'D', char: 'D', want: readline.CharDelete},
		{name: "Ctrl+L", key: 'L', char: 'L', want: readline.CharCtrlL},
		{name: "plain C", key: 'C', char: 'C', want: 'C'},
		{name: "plain D", key: 'D', char: 'D', want: 'D'},
		{name: "plain L", key: 'L', char: 'L', want: 'L'},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			modifiers := uint32(0)
			if len(test.name) >= 4 && test.name[:4] == "Ctrl" {
				modifiers = windows.LEFT_CTRL_PRESSED
			}
			got, ok := windowsConsoleRecordRune(windowsTestKeyRecord(test.key, test.char, modifiers), true)
			if !ok || got != test.want {
				t.Fatalf("translated rune = %d, %v; want %d", got, ok, test.want)
			}
		})
	}
}

func TestWindowsConsoleKeyReaderMapsEditingKeysAndRepeatCount(t *testing.T) {
	records := []windowsInputRecord{
		windowsTestKeyRecordWithRepeat('A', 'a', 0, 3),
		windowsTestKeyRecord(windows.VK_HOME, 0, 0),
		windowsTestKeyRecord(windows.VK_END, 0, 0),
		windowsTestKeyRecord(windows.VK_DELETE, 0, 0),
	}
	index := 0
	reader := &windowsConsoleKeyReader{
		readRecord: func(windows.Handle) (windowsInputRecord, error) {
			if index == len(records) {
				return windowsInputRecord{}, io.EOF
			}
			record := records[index]
			index++
			return record, nil
		},
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "aaa"+string([]byte{1, 5, 4}) {
		t.Fatalf("translated editing input = %q", output)
	}
}

func windowsTestKeyRecord(key, char uint16, modifiers uint32) windowsInputRecord {
	return windowsTestKeyRecordWithRepeat(key, char, modifiers, 1)
}

func windowsTestKeyRecordWithRepeat(key, char uint16, modifiers uint32, repeat uint16) windowsInputRecord {
	var record windowsInputRecord
	record.eventType = windows.KEY_EVENT
	binary.LittleEndian.PutUint32(record.event[:4], 1)
	binary.LittleEndian.PutUint16(record.event[4:6], repeat)
	binary.LittleEndian.PutUint16(record.event[6:8], key)
	binary.LittleEndian.PutUint16(record.event[10:12], char)
	binary.LittleEndian.PutUint32(record.event[12:16], modifiers)
	return record
}

func TestWindowsTerminalStreamsKeepReadlineNativeAdapters(t *testing.T) {
	config := &readline.Config{}
	output, setCommandMode := configureTerminalStreams(config, os.Stdin, io.Discard, io.Discard)
	if output != io.Writer(io.Discard) {
		t.Fatalf("configured output = %#v, want the unwrapped writer unchanged", output)
	}
	reader, ok := config.Stdin.(*windowsConsoleKeyReader)
	if !ok {
		t.Fatalf("configured input type = %T", config.Stdin)
	}
	if reader.inputHandle != windows.Handle(os.Stdin.Fd()) {
		t.Fatalf("configured console input handle = %v", reader.inputHandle)
	}
	setCommandMode(true)
	if !reader.commandMode {
		t.Fatal("command mode was not applied to the Windows reader")
	}
	setCommandMode(false)
	if reader.commandMode {
		t.Fatal("prompt mode did not restore Enter submission")
	}
}
