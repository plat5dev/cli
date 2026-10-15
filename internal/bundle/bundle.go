// Package bundle holds the local stack's compose files, embedded in the CLI so
// plat5 start needs no checkout. One file per module, all in one compose project:
// core.yml (Plat5 and the shared Postgres), auth.yml, operator.yml, observability.yml.
package bundle

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

//go:embed all:stack
var content embed.FS

// Files are the module compose files, by module name. "core" always runs.
var Files = map[string]string{
	"core":          "core.yml",
	"auth":          "auth.yml",
	"operator":      "operator.yml",
	"observability": "observability.yml",
}

// ConsoleFile is the operator console image: with the operator module, unless the
// project runs its own console (app.console).
const ConsoleFile = "operator-console.yml"

// Materialize writes the embedded stack under destDir, replacing what an older
// CLI wrote there. A file an older CLI embedded and this one doesn't (a renamed
// dashboard) is removed from the embedded subdirectories. destDir's top level
// also holds what start generates, so it is left alone.
func Materialize(destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	embedded := map[string]bool{}
	err := fs.WalkDir(content, "stack", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel("stack", path)
		if err != nil {
			return err
		}
		embedded[rel] = true
		target := filepath.Join(destDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := content.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embed %s: %w", path, err)
		}
		mode := os.FileMode(0o644)
		if filepath.Ext(path) == ".sh" {
			mode = 0o755
		}
		return os.WriteFile(target, data, mode)
	})
	if err != nil {
		return err
	}
	return filepath.WalkDir(destDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(destDir, path)
		if err != nil {
			return err
		}
		if filepath.Dir(rel) != "." && embedded[filepath.Dir(rel)] && !embedded[rel] {
			return os.Remove(path)
		}
		return nil
	})
}

// Services is every service name in the embedded compose files, all modules.
func Services() ([]string, error) {
	var names []string
	files := []string{ConsoleFile}
	for _, f := range Files {
		files = append(files, f)
	}
	for _, f := range files {
		data, err := content.ReadFile("stack/" + f)
		if err != nil {
			return nil, err
		}
		var doc struct {
			Services map[string]any `yaml:"services"`
		}
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for name := range doc.Services {
			names = append(names, name)
		}
	}
	return names, nil
}
