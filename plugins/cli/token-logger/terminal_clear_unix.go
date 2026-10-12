//go:build !windows

package main

import (
	"io"

	"github.com/chzyer/readline"
)

func clearTerminal(output io.Writer) error {
	_, err := readline.ClearScreen(output)
	return err
}
