package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWarnUngrantedLabels(t *testing.T) {
	dir := t.TempDir()
	roles := filepath.Join(dir, "roles.yml")
	routes := filepath.Join(dir, "routes.yml")
	_ = os.WriteFile(roles, []byte("roles:\n  owner: [\"*\"]\n  admin: [stats:read, projects:write]\ncreator_role: owner\ndefault_role: admin\n"), 0o644)
	_ = os.WriteFile(routes, []byte(`services:
  api:
    organization:
      routes:
        - path: /org/stats
          methods: [GET]
          required_labels: [stats:raed]
        - path: /org/projects
          methods:
            GET:
            POST:
              required_labels: [projects:write, projects:admin]
`), 0o644)

	out := captureStdout(t, func() { warnUngrantedLabels(roles, []string{routes}) })
	if !strings.Contains(out, "only owner can call them: projects:admin (POST /org/projects); stats:raed (/org/stats)") {
		t.Fatalf("missing warnings:\n%s", out)
	}
	if strings.Contains(out, "projects:write") || strings.Contains(out, "stats:read ") {
		t.Fatalf("warned about a granted label:\n%s", out)
	}
	if out := captureStdout(t, func() { warnUngrantedLabels("", []string{routes}) }); out != "" {
		t.Fatalf("roles off must not warn: %s", out)
	}
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = old
	data, _ := io.ReadAll(r)
	return string(data)
}
