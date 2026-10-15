package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWarnUngrantedLabels(t *testing.T) {
	routes := `services:
  api:
    organization:
      routes:
        - path: /org/stats
          methods: [GET]
          required_labels: [stats:read]
        - path: /org
          methods:
            DELETE:
              required_labels: [org:delete]
`
	cases := []struct {
		name, roles string
		want, not   []string
	}{
		{
			name:  "typo in a role",
			roles: "roles:\n  owner: [\"*\"]\n  member: [stats:raed]\n",
			want:  []string{"A typo?", "stats:read (/org/stats) and stats:raed (role member)", "owner only (no role grants these by name): org:delete (DELETE /org)"},
			not:   []string{"owner only (no role grants these by name): org:delete (DELETE /org); stats:read"},
		},
		{
			name:  "typo in a route",
			roles: "roles:\n  owner: [\"*\"]\n  member: [stats:rade]\n",
			want:  []string{"stats:read (/org/stats) and stats:rade (role member)"},
		},
		{
			name:  "owner-only is a note, not a typo",
			roles: "roles:\n  owner: [\"*\"]\n  member: [stats:read, notes:write]\n",
			want:  []string{"owner only (no role grants these by name): org:delete (DELETE /org)\n"},
			not:   []string{"warning", "typo", "notes:write"},
		},
		{
			name:  "nobody holds every label",
			roles: "roles:\n  member: [stats:read]\n",
			want:  []string{"warning: labels no role grants, so no member can call these routes: org:delete (DELETE /org)"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			rolesPath := filepath.Join(dir, "roles.yml")
			routesPath := filepath.Join(dir, "routes.yml")
			_ = os.WriteFile(rolesPath, []byte(c.roles), 0o644)
			_ = os.WriteFile(routesPath, []byte(routes), 0o644)
			out := captureStdout(t, func() { warnUngrantedLabels(rolesPath, []string{routesPath}) })
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q in:\n%s", w, out)
				}
			}
			for _, n := range c.not {
				if strings.Contains(out, n) {
					t.Errorf("unexpected %q in:\n%s", n, out)
				}
			}
		})
	}

	dir := t.TempDir()
	routesPath := filepath.Join(dir, "routes.yml")
	_ = os.WriteFile(routesPath, []byte(routes), 0o644)
	if out := captureStdout(t, func() { warnUngrantedLabels("", []string{routesPath}) }); out != "" {
		t.Fatalf("roles off must not warn: %s", out)
	}
}

func TestLabelDistance(t *testing.T) {
	for _, c := range []struct {
		a, b string
		d    int
	}{
		{"stats:read", "stats:raed", 1},
		{"stats:read", "stats:rade", 2},
		{"org:read", "org:write", 4},
		{"a", "a", 0},
	} {
		if got := labelDistance(c.a, c.b); got != c.d {
			t.Errorf("%s/%s = %d, want %d", c.a, c.b, got, c.d)
		}
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
