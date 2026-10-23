package cmd

import (
	"io"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWarnUngrantedLabels(t *testing.T) {
	services := `
api:
  url: http://api:3000
  routes:
    - path: /orgs/{organization}/stats
      scope: organization
      methods: [GET]
      required_labels: [stats:read]
    - path: /orgs/{organization}
      scope: organization
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
			roles: "owner: {labels: [\"*\"]}\nmember: {labels: [stats:raed]}\n",
			want:  []string{"A typo?", "stats:read (/orgs/{organization}/stats) and stats:raed (role member)", "owner only (no role grants these by name): org:delete (DELETE /orgs/{organization})"},
			not:   []string{"org:delete (DELETE /orgs/{organization}); stats:read"},
		},
		{
			name:  "typo in a route",
			roles: "owner: {labels: [\"*\"]}\nmember: {labels: [stats:rade]}\n",
			want:  []string{"stats:read (/orgs/{organization}/stats) and stats:rade (role member)"},
		},
		{
			name:  "owner-only is a note, not a typo",
			roles: "owner: {labels: [\"*\"]}\nmember: {labels: [stats:read, notes:write]}\n",
			want:  []string{"owner only (no role grants these by name): org:delete (DELETE /orgs/{organization})\n"},
			not:   []string{"warning", "typo", "notes:write"},
		},
		{
			name:  "nobody holds every label",
			roles: "member: {labels: [stats:read]}\n",
			want:  []string{"warning: labels no role grants, so no member can call these routes: org:delete (DELETE /orgs/{organization})", "Add them to a role in plat5.yml."},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			flat := map[string]any{"roles": yamlMap(t, c.roles), "services": yamlMap(t, services)}
			out := captureStdout(t, func() { warnUngrantedLabels(flat, "plat5.yml") })
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
}

func yamlMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
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
