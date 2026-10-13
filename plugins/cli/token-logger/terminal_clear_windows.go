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
	"io"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	fillConsoleOutputCharacterW = syscall.NewLazyDLL("kernel32.dll").NewProc("FillConsoleOutputCharacterW")
	fillConsoleOutputAttribute  = syscall.NewLazyDLL("kernel32.dll").NewProc("FillConsoleOutputAttribute")
)

func clearTerminal(io.Writer) error {
	// readline's Windows ANSIWriter does not safely handle cursor-home CSI H.
	// Clear the visible window through the console API, leaving scrollback rows
	// outside the viewport untouched.
	handle, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	if err != nil {
		return err
	}
	return clearWindowsConsole(handle)
}

func clearWindowsConsole(handle windows.Handle) error {
	return clearWindowsConsoleWith(handle, windowsConsoleClearOps{})
}

type windowsConsoleClearer interface {
	screenBufferInfo(windows.Handle) (windows.ConsoleScreenBufferInfo, error)
	fillCharacters(windows.Handle, uint16, uintptr, windows.Coord) error
	fillAttributes(windows.Handle, uint16, uintptr, windows.Coord) error
	setCursor(windows.Handle, windows.Coord) error
}

type windowsConsoleClearOps struct{}

func (windowsConsoleClearOps) screenBufferInfo(handle windows.Handle) (windows.ConsoleScreenBufferInfo, error) {
	var info windows.ConsoleScreenBufferInfo
	err := windows.GetConsoleScreenBufferInfo(handle, &info)
	return info, err
}

func (windowsConsoleClearOps) fillCharacters(handle windows.Handle, character uint16, cells uintptr, origin windows.Coord) error {
	var written uint32
	result, _, callErr := fillConsoleOutputCharacterW.Call(
		uintptr(handle), uintptr(character), cells, uintptr(unsafe.Pointer(&origin)), uintptr(unsafe.Pointer(&written)),
	)
	if result == 0 {
		if callErr == nil || callErr == syscall.Errno(0) {
			callErr = syscall.EINVAL
		}
		return callErr
	}
	return nil
}

func (windowsConsoleClearOps) fillAttributes(handle windows.Handle, attributes uint16, cells uintptr, origin windows.Coord) error {
	var written uint32
	result, _, callErr := fillConsoleOutputAttribute.Call(
		uintptr(handle), uintptr(attributes), cells, uintptr(unsafe.Pointer(&origin)), uintptr(unsafe.Pointer(&written)),
	)
	if result == 0 {
		if callErr == nil || callErr == syscall.Errno(0) {
			callErr = syscall.EINVAL
		}
		return callErr
	}
	return nil
}

func (windowsConsoleClearOps) setCursor(handle windows.Handle, position windows.Coord) error {
	return windows.SetConsoleCursorPosition(handle, position)
}

func clearWindowsConsoleWith(handle windows.Handle, ops windowsConsoleClearer) error {
	info, err := ops.screenBufferInfo(handle)
	if err != nil {
		return err
	}
	origin := windows.Coord{X: info.Window.Left, Y: info.Window.Top}
	width := uintptr(info.Window.Right-info.Window.Left) + 1
	for row := info.Window.Top; row <= info.Window.Bottom; row++ {
		position := windows.Coord{X: info.Window.Left, Y: row}
		if err := ops.fillCharacters(handle, uint16(' '), width, position); err != nil {
			return err
		}
		if err := ops.fillAttributes(handle, info.Attributes, width, position); err != nil {
			return err
		}
	}
	return ops.setCursor(handle, origin)
}
