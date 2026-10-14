package cmd

import (
	"errors"
	"path/filepath"
)

var errNoApp = errors.New("no app: set app.compose in plat5.yml")

func dirOf(p string) string { return filepath.Dir(p) }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
