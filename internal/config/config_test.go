package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/plat5dev/cli/internal/ports"
)

const fullYML = `project_id: My App
versions:
  plat5: v0.7.0
  auth: v0.2.0
  operator: v0.4.0
  operator_console: v0.1.0
app:
  compose: ./docker-compose.yml
  web: http://localhost:3001/
upstreams:
  api: 3000
routes:
  - ./routes.yml
roles: ./roles.yml
`

func resolve(t *testing.T, yml string) (Resolved, error) {
	t.Helper()
	f, err := Parse([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	return Resolve(f, "/proj/plat5.yml", Overrides{})
}

func TestResolveDefaults(t *testing.T) {
	r, err := resolve(t, fullYML)
	if err != nil {
		t.Fatal(err)
	}
	if r.ProjectID != "my-app" || r.ComposeProject != "plat5-my-app" {
		t.Fatalf("project: %q %q", r.ProjectID, r.ComposeProject)
	}
	for _, m := range Modules {
		if !r.Modules[m] {
			t.Fatalf("module %s should default on", m)
		}
	}
	if r.Ports[ports.Gateway] != 5400 || r.GatewayURL() != "http://localhost:5400" {
		t.Fatalf("gateway: %v", r.Ports)
	}
	if r.ControlPlaneURL != "http://localhost:5402" || r.AdminToken != DefaultAdminToken {
		t.Fatalf("control plane: %s %s", r.ControlPlaneURL, r.AdminToken)
	}
	if r.AppCompose != filepath.Clean("/proj/docker-compose.yml") || r.AppWeb != "http://localhost:3001" {
		t.Fatalf("app: %q %q", r.AppCompose, r.AppWeb)
	}
	if r.RolesFile != filepath.Clean("/proj/roles.yml") {
		t.Fatalf("roles: %q", r.RolesFile)
	}
	if got := strings.Join(r.AuthRedirectURIs(), ","); got != "http://localhost:3001/callback" {
		t.Fatalf("redirects: %s", got)
	}
	if got := strings.Join(r.AuthClients(), ","); got != "plat5" {
		t.Fatalf("clients: %s", got)
	}
	if got := strings.Join(r.OperatorConsoleOrigins(), ","); got != "http://localhost:5412" {
		t.Fatalf("console origins: %s", got)
	}
}

func TestUnknownKeysAreErrors(t *testing.T) {
	if _, err := Parse([]byte("plat5_version: v1\n")); err == nil || !strings.Contains(err.Error(), `unknown key "plat5_version"`) {
		t.Fatalf("want unknown key error, got %v", err)
	}
	for yml, want := range map[string]string{
		fullYML + "modules: {grafana: true}\n": "modules.grafana",
		fullYML + "ports: {registry: 5000}\n":  "ports.registry",
		fullYML + "source: {web: ../web}\n":    "source.web",
		fullYML + "ports: {gateway: 5401}\n":   "both 5401",
		"versions: {auth: v1}\n":               "versions.plat5 is required",
		"versions: {plat5: v1, auth: v2, operator: v3, operator_console: v4}\napp: {web: localhost:3001}\n": "app.web",
		fullYML + "idp: {issuer: https://x.test}\n":                                                         "turn modules.auth off",
	} {
		f, err := Parse([]byte(yml))
		if err == nil {
			_, err = Resolve(f, "/proj/plat5.yml", Overrides{})
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want error containing %q, got %v", yml, want, err)
		}
	}
}

func TestModulesOffNeedFewerPins(t *testing.T) {
	r, err := resolve(t, `versions: {plat5: v1, auth: v2}
modules: {operator: false, observability: false}
`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Modules[ModuleOperator] || !r.Modules[ModuleAuth] {
		t.Fatalf("modules: %v", r.Modules)
	}
	if strings.Join(r.NeededVersions(), ",") != "plat5,auth" {
		t.Fatalf("needed: %v", r.NeededVersions())
	}
}

func TestAuthOffNeedsAnIdP(t *testing.T) {
	_, err := resolve(t, "versions: {plat5: v1}\nmodules: {auth: false, operator: false}\n")
	if err == nil || !strings.Contains(err.Error(), "idp.issuer") {
		t.Fatalf("want idp error, got %v", err)
	}
	r, err := resolve(t, `versions: {plat5: v1}
modules: {auth: false, operator: false}
idp:
  issuer: https://idp.test
  jwks_uri: https://idp.test/jwks
  audiences: [api]
`)
	if err != nil {
		t.Fatal(err)
	}
	if r.IdP.Issuer != "https://idp.test" {
		t.Fatalf("%+v", r.IdP)
	}
}

func TestSourceReplacesAVersion(t *testing.T) {
	r, err := resolve(t, `versions: {auth: v2, operator: v3, operator_console: v4}
source: {plat5: ../plat5}
`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Source[VersionPlat5] != filepath.Clean("/plat5") {
		t.Fatalf("source: %v", r.Source)
	}
}

func TestOverrides(t *testing.T) {
	t.Setenv("PLAT5_ADMIN_TOKEN", "from-env")
	f, _ := Parse([]byte(fullYML))
	r, err := Resolve(f, "/proj/plat5.yml", Overrides{ControlPlaneURL: "https://cp.example.com/"})
	if err != nil {
		t.Fatal(err)
	}
	if r.ControlPlaneURL != "https://cp.example.com" || r.AdminToken != "from-env" {
		t.Fatalf("%s %s", r.ControlPlaneURL, r.AdminToken)
	}
}

func TestAPIKeyBrand(t *testing.T) {
	for _, bad := range []string{"Plat5", "9lives", "a-b"} {
		if _, err := parseAPIKeyBrand(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	if b, err := parseAPIKeyBrand("acme2"); err != nil || b != "acme2" {
		t.Fatalf("%q %v", b, err)
	}
}

func TestNeedsHostGateway(t *testing.T) {
	if !NeedsHostGateway("http://host.docker.internal:4318") || NeedsHostGateway("http://alloy:4318") {
		t.Fatal("host gateway detection")
	}
}
