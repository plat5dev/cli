package xdg

import (
	"os"
	"path/filepath"
)

// StateHome returns $XDG_STATE_HOME or ~/.local/state (same on macOS and Linux).
func StateHome() (string, error) {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state"), nil
}

// Plat5StateDir is ~/.local/state/plat5 (or $XDG_STATE_HOME/plat5).
func Plat5StateDir() (string, error) {
	base, err := StateHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "plat5"), nil
}

// ProjectDir is state home for one project_id.
func ProjectDir(projectID string) (string, error) {
	base, err := Plat5StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "projects", projectID), nil
}
