package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/stack"
	"github.com/plat5dev/cli/internal/state"
)

func summaryStack(t *testing.T, exclude ...string) *stack.Stack {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	f, err := config.Parse([]byte(`project_id: demo
versions: {plat5: v0.9.0, auth: v0.2.2, operator: v0.5.0}
app: {compose: ./docker-compose.yml, web: "http://localhost:3001"}
operator:
  config: ./operator.yml
  console: {compose: ./operator-console/docker-compose.yml, web: "http://localhost:5412"}
`))
	if err != nil {
		t.Fatal(err)
	}
	r, err := config.Resolve(f, filepath.Join(t.TempDir(), config.FileName), config.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := stack.New(r, exclude)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The summary leads with the consoles and their logins, then Plat5's links in
// one block, then the dev person's key: no curl recipes, no dev nodes.
func TestSummary(t *testing.T) {
	st := state.State{AppCompose: "/x/docker-compose.yml", Dev: &state.Dev{
		Email: config.DevEmail, Key: "plat5-k-2-abc",
		Nodes: []state.DevNode{{Kind: "organization", ID: "0193-org", Slug: "dev", Path: "/orgs/dev"}},
	}}
	out := captureStdout(t, func() { printSummary(summaryStack(t), st) })
	want := `demo is running.

  Customer console  http://localhost:3001  dev@example.com / password
  Operator console  http://localhost:5412  operator@example.com / password

Plat5
  Gateway           http://localhost:5400
  Auth              http://localhost:5401  sign-up codes: plat5 logs auth | grep code
  Control plane     http://localhost:5402  admin token dev-admin-token
  Operator gateway  http://localhost:5410  config: operator.yml
  Operator IdP      http://localhost:5411
  Grafana           http://localhost:5403  Plat5 folder: Service Health, Auth, Debug

Dev person  dev@example.com
  Personal key      plat5-k-2-abc          acts as them in every tree

`
	if out != want {
		t.Errorf("summary:\n%s\nwant:\n%s", out, want)
	}

	out = captureStdout(t, func() { printSummary(summaryStack(t, "operator"), st) })
	if strings.Contains(out, "5410") || strings.Contains(out, "5412") {
		t.Errorf("-x operator: the summary names the operator plane:\n%s", out)
	}

	// Without the apps running, there's no console to lead with, nor to open.
	out = captureStdout(t, func() { printSummary(summaryStack(t), state.State{}) })
	if !strings.HasPrefix(out, "demo is running.\n\nPlat5\n") {
		t.Errorf("no apps:\n%s", out)
	}
	if out := captureStdout(t, func() { printOpen(summaryStack(t), state.State{}) }); out != "" {
		t.Errorf("no apps, open: %q", out)
	}
	if out := captureStdout(t, func() { printOpen(summaryStack(t), st) }); out != "Open http://localhost:3001 and sign in as dev@example.com / password.\n" {
		t.Errorf("open: %q", out)
	}
}
