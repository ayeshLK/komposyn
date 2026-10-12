//go:build windows

package main

import (
	"io"

	"github.com/chzyer/readline"
)

func clearTerminal(output io.Writer) error {
	return readline.ClearScreen(output)
}
