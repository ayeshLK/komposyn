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
	if err := clearWindowsConsoleWith(12, ops); err != nil {
		t.Fatal(err)
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

func TestWindowsPasteGuardFoldsEnterBeforeQueuedInput(t *testing.T) {
	pendingResponses := []bool{true, false}
	pendingIndex := 0
	reader := &windowsPasteGuardReader{
		source: bytes.NewBufferString("first\rsecond\r"),
		pendingInput: func(windows.Handle) (bool, error) {
			pending := pendingResponses[pendingIndex]
			pendingIndex++
			return pending, nil
		},
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "first second\r" {
		t.Fatalf("guarded Windows input = %q", output)
	}
}

func TestWindowsInputRecordDetectionIgnoresKeyUp(t *testing.T) {
	var keyUp, keyDown windowsInputRecord
	keyUp.eventType = windows.KEY_EVENT
	keyDown.eventType = windows.KEY_EVENT
	binary.LittleEndian.PutUint32(keyUp.event[:4], 0)
	binary.LittleEndian.PutUint32(keyDown.event[:4], 1)
	if windowsRecordsHavePendingKeyDown([]windowsInputRecord{keyUp}) {
		t.Fatal("key-up event was treated as pending input")
	}
	if !windowsRecordsHavePendingKeyDown([]windowsInputRecord{keyUp, keyDown}) {
		t.Fatal("key-down event was not detected")
	}
}

func TestWindowsTerminalStreamsKeepReadlineNativeAdapters(t *testing.T) {
	config := &readline.Config{}
	configureTerminalStreams(config, os.Stdin, io.Discard, io.Discard)
	guard, ok := config.Stdin.(*windowsPasteGuardReader)
	if !ok {
		t.Fatalf("configured input type = %T", config.Stdin)
	}
	if _, ok := guard.source.(*readline.RawReader); !ok {
		t.Fatalf("underlying input adapter = %T", guard.source)
	}
}
