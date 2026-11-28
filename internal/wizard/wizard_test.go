package wizard

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/plat5dev/cli/internal/plugin"
	"github.com/plat5dev/cli/internal/registry"
)

func loadTestCatalog(t *testing.T) *Catalog {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	if _, err := os.Stat(filepath.Join(root, "registry", "registry.yml")); err != nil {
		t.Skip("no sibling registry checkout")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cat, err := LoadCatalog(&registry.Resolver{SourcesDir: root, CLIVersion: "0.20.0"})
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func sendKey(m Model, k string) Model {
	var msg tea.KeyMsg
	switch k {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "space":
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	next, _ := m.Update(msg)
	return next.(Model)
}

func TestWizardQuickstartFlowAndBackNav(t *testing.T) {
	cat := loadTestCatalog(t)
	m := NewModel(cat, Options{Dir: "demo-app"})
	if m.step != stepMode {
		t.Fatalf("expected stepMode when Dir is preset, got %v", m.step)
	}
	// Pick first recipe (quickstart) → confirm → go back → pick custom.
	m = sendKey(m, "enter")
	if m.step != stepConfirm || m.recipe != "quickstart" {
		t.Fatalf("after enter on recipe: step=%v recipe=%q", m.step, m.recipe)
	}
	m = sendKey(m, "esc")
	if m.step != stepMode {
		t.Fatalf("after esc from confirm: step=%v", m.step)
	}
	// Navigate down to "custom" (last item) and enter.
	for i := 0; i < len(m.choices); i++ {
		m = sendKey(m, "down")
	}
	m = sendKey(m, "enter")
	if m.step != stepPreset || !m.customMode {
		t.Fatalf("expected stepPreset in custom mode, got step=%v custom=%v", m.step, m.customMode)
	}
	// Walk through custom steps: preset (saas) → auth (down to auth0) → authz (down to cerbos)
	// → services (toggle go-fiber-api on) → consoles → operator (plat5-auth) → op-authz (disabled)
	// → observability (local-lgtm) → ci/deploy (toggle github-actions) → confirm.
	m = sendKey(m, "enter") // preset
	m = sendKey(m, "down")  // auth0
	m = sendKey(m, "enter")
	m = sendKey(m, "down") // cerbos
	m = sendKey(m, "enter")
	m = sendKey(m, "down")  // go-fiber-api
	m = sendKey(m, "space") // toggle go-fiber-api
	m = sendKey(m, "enter") // services done
	m = sendKey(m, "enter") // consoles done
	m = sendKey(m, "enter") // operator: plat5-auth
	m = sendKey(m, "enter") // operator authz: disabled
	m = sendKey(m, "enter") // observability: local-lgtm
	m = sendKey(m, "space") // toggle github-actions
	m = sendKey(m, "enter") // ci/deploy done
	if m.step != stepConfirm {
		t.Fatalf("expected stepConfirm, got %v", m.step)
	}
	// Render wide and narrow views without panicking.
	m.width = 120
	wide := m.View()
	if !strings.Contains(wide, "Stack preview") || !strings.Contains(wide, "auth0") || !strings.Contains(wide, "cerbos") {
		t.Fatalf("wide view missing preview:\n%s", wide)
	}
	m.width = 70
	_ = m.View()

	m = sendKey(m, "enter")
	if !m.done {
		t.Fatal("expected done=true after confirming")
	}
	res := m.Result()
	if res.Dir != "demo-app" || res.Slots[plugin.SlotAuth][0] != "auth0" || res.Slots[plugin.SlotAuthZ][0] != "cerbos" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if len(res.Slots[plugin.SlotService]) != 2 || len(res.Slots[plugin.SlotCI]) != 1 {
		t.Fatalf("multi-select mismatch: services=%v ci=%v", res.Slots[plugin.SlotService], res.Slots[plugin.SlotCI])
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRunVerifyCheck(t *testing.T) {
	p := &plugin.Plugin{
		Manifest: plugin.Manifest{
			Name: "auth0",
			Verify: &plugin.Verify{
				HTTPGet:      "https://[[.Prompt.domain]]/.well-known/jwks.json",
				ExpectStatus: 200,
				Label:        "Verifying Auth0 JWKS",
			},
		},
		Prompts: map[string]string{"domain": "acme.us.auth0.com"},
	}
	var gotURL string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`{"keys":[]}`)),
		}, nil
	})}
	out := RunVerifyCheck(context.Background(), p, client)
	if !out.OK || gotURL != "https://acme.us.auth0.com/.well-known/jwks.json" {
		t.Fatalf("verify outcome: %+v url=%q", out, gotURL)
	}
}
