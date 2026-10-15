package main

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

// readPassword reads without echoing when stdin is a terminal, and falls back
// to a plain line read when it is not (piped input, some IDE consoles).
func readPassword(label string) string {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return prompt(label)
	}
	fmt.Print(label)
	b, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return ""
	}
	return string(b)
}
