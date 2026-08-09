package xdg

import (
	"os"
	"path/filepath"
)

// ConfigHome returns $XDG_CONFIG_HOME or ~/.config (same on macOS and Linux).
func ConfigHome() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config"), nil
}

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

// Plat5ConfigDir is ~/.config/plat5 (or $XDG_CONFIG_HOME/plat5).
func Plat5ConfigDir() (string, error) {
	base, err := ConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "plat5"), nil
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

// CacheHome returns $XDG_CACHE_HOME or ~/.cache.
func CacheHome() (string, error) {
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache"), nil
}

// Plat5CacheDir is ~/.cache/plat5 (or $XDG_CACHE_HOME/plat5).
func Plat5CacheDir() (string, error) {
	base, err := CacheHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "plat5"), nil
}
