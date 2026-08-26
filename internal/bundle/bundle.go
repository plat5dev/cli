package bundle

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// DefaultVersion is the Plat5 runtime image tag when plat5_version is unset.
const DefaultVersion = "v0.1.8"

// DefaultAuthVersion is the Auth image tag when auth.version is unset.
const DefaultAuthVersion = "v0.1.5"

//go:embed plat5/docker-compose.yml auth/docker-compose.yml observability/docker-compose.yml observability/monitoring/* observability/dashboards/*
var content embed.FS

// MaterializePlat5 writes the embedded Plat5 image-mode stack under destDir.
func MaterializePlat5(destDir string) error {
	return materialize(destDir, "plat5")
}

// MaterializeAuth writes the embedded Auth image-mode stack under destDir.
func MaterializeAuth(destDir string) error {
	return materialize(destDir, "auth")
}

// MaterializeObservability writes the embedded observability stack under destDir.
func MaterializeObservability(destDir string) error {
	return materialize(destDir, "observability")
}

func materialize(destDir, root string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	return fs.WalkDir(content, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(destDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := content.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embed %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
