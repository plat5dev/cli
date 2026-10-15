// Package prompt has the terminal helpers init needs beside its huh forms.
package prompt

import (
	"os"
	"path/filepath"
	"strings"
)

// Interactive reports whether stdin and stdout are terminals: someone to ask.
func Interactive() bool {
	return terminal(os.Stdin) && terminal(os.Stdout)
}

// StdoutTerminal reports whether stdout is a terminal, not a pipe or a file.
func StdoutTerminal() bool { return terminal(os.Stdout) }

func terminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// ExpandPath trims space and expands a leading ~/.
func ExpandPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
