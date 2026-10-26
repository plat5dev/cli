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
versions: {plat5: v0.8.1, auth: v0.2.2, operator: v0.5.0}
app: {compose: ./docker-compose.yml, web: "http://localhost:3001"}
operator:
  routes: ./operator.routes.yml
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

// The summary prints the operator gateway, its IdP and the console with the
// operator login, and a dev token's request on the dev org by id.
func TestSummaryOperator(t *testing.T) {
	st := state.State{AppCompose: "/x/docker-compose.yml", Dev: &state.Dev{
		Email: config.DevEmail, Key: "plat5-k-2-abc",
		Nodes: []state.DevNode{{Kind: "organization", ID: "0193-org", Slug: "dev", Path: "/orgs/dev"}},
	}}
	out := captureStdout(t, func() { printSummary(summaryStack(t), st) })
	for _, want := range []string{
		"Operator\n",
		"  Gateway        http://localhost:5410  routes: operator.routes.yml\n",
		"  IdP            http://localhost:5411  operator@example.com / password\n",
		"  Console        http://localhost:5412  operator@example.com / password\n",
		"TOKEN=$(curl -s http://localhost:5411/dev/token -H 'Content-Type: application/json' \\\n",
		`-d '{"email":"operator@example.com","client_id":"operator-console"}' | jq -r .access_token)`,
		`curl http://localhost:5410/nodes/0193-org -H "Authorization: Bearer $TOKEN"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}

	out = captureStdout(t, func() { printSummary(summaryStack(t, "operator"), st) })
	if strings.Contains(out, "5410") || strings.Contains(out, "5412") {
		t.Errorf("-x operator: the summary names the operator plane:\n%s", out)
	}
}
