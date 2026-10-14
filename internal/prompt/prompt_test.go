package prompt

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	for in, want := range map[string]string{
		" ~/x ": filepath.Join(home, "x"),
		"~":     home,
		"./a":   "./a",
	} {
		if got := ExpandPath(in); got != want {
			t.Errorf("ExpandPath(%q) = %q, want %q", in, got, want)
		}
	}
}
