package stack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plat5dev/cli/internal/config"
)

func resolved(t *testing.T, yml string) config.Resolved {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "roles.yml"), []byte("roles: {owner: [\"*\"]}\ncreator_role: owner\ndefault_role: owner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := config.Parse([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	r, err := config.Resolve(f, filepath.Join(dir, "plat5.yml"), config.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

const allOn = `project_id: demo
versions: {plat5: v0.7.0, auth: v0.2.0, operator: v0.4.0, operator_console: v0.1.0}
app: {web: "http://localhost:3001"}
roles: ./roles.yml
`

func newStack(t *testing.T, yml string, exclude ...string) *Stack {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s, err := New(resolved(t, yml), exclude)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func env(s *Stack) map[string]string {
	m := map[string]string{}
	for _, kv := range s.Env() {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

func TestModulesAndExclude(t *testing.T) {
	s := newStack(t, allOn)
	if strings.Join(s.Modules, ",") != "auth,operator,observability" {
		t.Fatalf("%v", s.Modules)
	}
	s = newStack(t, allOn, "operator")
	if s.Has("operator") || !s.Has("auth") {
		t.Fatalf("%v", s.Modules)
	}
	files := strings.Join(s.Runner().Files, ",")
	if files != "core.yml,auth.yml,observability.yml,override.yml" {
		t.Fatalf("files: %s", files)
	}
	if _, err := New(s.Config, []string{"grafana"}); err == nil {
		t.Fatal("unknown module must be an error")
	}
	if _, err := New(s.Config, []string{"auth"}); err == nil || !strings.Contains(err.Error(), "idp:") {
		t.Fatalf("excluding auth without an idp: must be an error, got %v", err)
	}
}

func TestEnvWiresLocalAuth(t *testing.T) {
	e := env(newStack(t, allOn))
	for k, want := range map[string]string{
		"GATEWAY_PORT":                "5400",
		"STAFF_AUTH_PORT":             "5411",
		"PLAT5_VERSION":               "v0.7.0",
		"OPERATOR_CONSOLE_VERSION":    "v0.1.0",
		"IDP_ISSUER":                  "http://localhost:5401",
		"IDP_JWKS_URI":                "http://auth:5000/.well-known/jwks.json",
		"IDP_AUDIENCES":               "plat5",
		"AUTH_REDIRECT_URIS":          "http://localhost:3001/callback",
		"OPERATOR_ORIGINS":            "http://localhost:5412",
		"OPERATOR_REDIRECT_URIS":      "http://localhost:5412/callback",
		"ROLES_ENABLED":               "true",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://alloy:4318",
		"AUTH_DEV_USERS":              "dev@example.com:password",
		"STAFF_DEV_USERS":             "staff@example.com:password",
	} {
		if e[k] != want {
			t.Errorf("%s = %q, want %q", k, e[k], want)
		}
	}
}

func TestEnvWithOwnIdP(t *testing.T) {
	s := newStack(t, `versions: {plat5: v1}
modules: {auth: false, operator: false, observability: false}
idp: {issuer: "https://idp.test", jwks_uri: "https://idp.test/jwks", audiences: [api]}
`)
	e := env(s)
	if e["IDP_ISSUER"] != "https://idp.test" || e["IDP_USER_ID_CLAIM"] != "sub" || e["OTEL_EXPORTER_OTLP_ENDPOINT"] != "" || e["ROLES_ENABLED"] != "false" {
		t.Fatalf("%v", e)
	}
}

func TestOverride(t *testing.T) {
	s := newStack(t, allOn+"source: {plat5: ../plat5, operator: ../operator}\notel: {endpoint: \"http://host.docker.internal:4318\"}\n")
	o := s.override()
	for _, want := range []string{
		"SEED_ROLES_FILE: \"/seed-roles/roles.yml\"",
		"/seed-roles/roles.yml:ro",
		"staff-theme.json:/config/theme.json:ro",
		"image: plat5-source/gateway:demo",
		"target: operator-audit",
		"host.docker.internal:host-gateway",
	} {
		if !strings.Contains(o, want) {
			t.Errorf("override missing %q:\n%s", want, o)
		}
	}
	// Each service appears once: a duplicate key is invalid YAML.
	if strings.Count(o, "\n  auth:\n") != 1 || strings.Count(o, "\n  gateway:\n") != 1 {
		t.Fatalf("service repeated:\n%s", o)
	}
	if !s.Builds() || env(s)["PLAT5_VERSION"] != "source" {
		t.Fatal("source checkouts build")
	}
}

func TestAppEnv(t *testing.T) {
	s := newStack(t, allOn)
	got := strings.Join(s.AppEnv(), " ")
	for _, want := range []string{"PLAT5_GATEWAY_URL=http://localhost:5400", "PLAT5_AUTH_URL=http://localhost:5401", "OTEL_EXPORTER_OTLP_ENDPOINT=http://host.docker.internal:4318"} {
		if !strings.Contains(got, want) {
			t.Errorf("app env missing %s: %s", want, got)
		}
	}
	if s.AppRunner() != nil {
		t.Fatal("no app.compose, no app runner")
	}
}

// The real files, env, and override make a valid compose project.
func TestComposeConfig(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	for name, s := range map[string]*Stack{
		"all":    newStack(t, allOn+"source: {plat5: ../plat5}\n"),
		"core":   newStack(t, allOn, "operator", "observability"),
		"no-ops": newStack(t, allOn, "operator"),
	} {
		if err := s.Prepare(); err != nil {
			t.Fatal(err)
		}
		r := s.Runner()
		r.Quiet = true
		args := append(r.Files[:0:0], "compose", "-p", r.ProjectName, "--project-directory", r.Dir)
		for _, f := range r.Files {
			args = append(args, "-f", f)
		}
		cmd := exec.Command("docker", append(args, "config", "--quiet")...)
		cmd.Dir = r.Dir
		cmd.Env = append(os.Environ(), r.Env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: docker compose config: %v\n%s\n--- override.yml\n%s", name, err, out, s.override())
		}
	}
}

func TestOperatorRoutesFile(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cfg := resolved(t, allOn+"upstreams: {api: 3010}\noperator: {routes: ./operator-routes.yml}\n")
	routes := `upstreams:
  identity:
    url: http://identity:3000
    routes:
      - path: /organizations
        methods: [GET]
  api:
    routes:
      - path: /organizations/{organization_id}/features
        methods: [GET]
`
	if err := os.WriteFile(filepath.Join(cfg.ConfigDir, "operator-routes.yml"), []byte(routes), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Prepare(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(s.Dir, "operator-routes.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"url: http://host.docker.internal:3010", "url: http://identity:3000", "/organizations/{organization_id}/features"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("operator routes missing %q:\n%s", want, got)
		}
	}
	o := s.override()
	for _, want := range []string{"ROUTES_FILE: \"/config/routes.yml\"", "operator-routes.yml:/config/routes.yml:ro", "PLAT5_ROUTES_SHA256: \"" + fileSHA256(filepath.Join(s.Dir, "operator-routes.yml")) + "\""} {
		if !strings.Contains(o, want) {
			t.Errorf("override missing %q:\n%s", want, o)
		}
	}
	if !strings.Contains(o[strings.Index(o, "\n  operator:\n"):], "host.docker.internal:host-gateway") {
		t.Errorf("operator needs the host gateway:\n%s", o)
	}

	// Without the operator module, nothing is written or mounted.
	off, err := New(cfg, []string{"operator"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(off.override(), "ROUTES_FILE") {
		t.Fatal("operator off: no routes override")
	}
}
