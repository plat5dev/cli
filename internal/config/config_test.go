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
  operator: v0.5.0
app:
  compose: ./docker-compose.yml
  web: http://localhost:3001/
`

func resolve(t *testing.T, yml string) (Resolved, error) {
	t.Helper()
	f, err := Parse([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	return Resolve(f, "/proj/plat5.dev.yml", Overrides{})
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
	if r.Path != filepath.Clean("/proj/plat5.dev.yml") || r.ConfigPath != filepath.Clean("/proj/plat5.yml") {
		t.Fatalf("paths: %q %q", r.Path, r.ConfigPath)
	}
	if got := strings.Join(r.AuthRedirectURIs(), ","); got != "http://localhost:3001/callback" {
		t.Fatalf("redirects: %s", got)
	}
	if got := strings.Join(r.AuthClients(), ","); got != "plat5" {
		t.Fatalf("clients: %s", got)
	}
}

func TestUnknownKeysAreErrors(t *testing.T) {
	for _, key := range []string{"routes", "roles", "upstreams", "billing"} {
		if _, err := Parse([]byte(key + ": x\n")); err == nil || !strings.Contains(err.Error(), `unknown key "`+key+`"`) {
			t.Errorf("%s: want unknown key error, got %v", key, err)
		}
	}
	if _, err := Parse([]byte("plat5_version: v0.8.0\n")); err == nil || !strings.Contains(err.Error(), `unknown key "plat5_version"`) {
		t.Fatalf("want unknown key error, got %v", err)
	}
	for yml, want := range map[string]string{
		fullYML + "modules: {billing: true}\n":                                                    "modules.billing",
		fullYML + "ports: {console: 5412}\n":                                                      "ports.console",
		fullYML + "source: {billing: ../billing}\n":                                               "source.billing",
		fullYML + "ports: {gateway: 5401}\n":                                                      "both 5401",
		fullYML + "ports: {operator_auth: 5400}\n":                                                "both 5400",
		"versions: {auth: v0.2.2}\n":                                                              "versions.plat5 is required",
		"versions: {plat5: v0.8.0, auth: v0.2.2}\n":                                               "versions.operator is required (plat5 init writes the latest tag, or turn modules.operator off)",
		"versions: {plat5: v0.8.0, auth: v0.2.2, operator: v0.5.0}\napp: {web: localhost:3001}\n": "app.web",
		fullYML + "operator: {console: {web: localhost:5412}}\n":                                  "operator.console.web",
		fullYML + "idp: {issuer: https://x.test}\n":                                               "turn modules.auth off",
	} {
		f, err := Parse([]byte(yml))
		if err == nil {
			_, err = Resolve(f, "/proj/plat5.dev.yml", Overrides{})
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want error containing %q, got %v", yml, want, err)
		}
	}
}

func TestModulesOffNeedFewerPins(t *testing.T) {
	r, err := resolve(t, `versions: {plat5: v0.8.0, auth: v0.2.2}
modules: {observability: false, operator: false}
`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Modules[ModuleObservability] || !r.Modules[ModuleAuth] {
		t.Fatalf("modules: %v", r.Modules)
	}
	if strings.Join(r.NeededVersions(), ",") != "plat5,auth" {
		t.Fatalf("needed: %v", r.NeededVersions())
	}
}

func TestAuthOffNeedsAnIdP(t *testing.T) {
	_, err := resolve(t, "versions: {plat5: v0.8.0}\nmodules: {auth: false, operator: false}\n")
	if err == nil || !strings.Contains(err.Error(), "idp.issuer") {
		t.Fatalf("want idp error, got %v", err)
	}
	r, err := resolve(t, `versions: {plat5: v0.8.0}
modules: {auth: false, operator: false}
idp:
  issuer: https://idp.test
  jwks_uri: https://idp.test/jwks
  audiences: [api]
  subject_claim: sub
`)
	if err != nil {
		t.Fatal(err)
	}
	if r.IdP.Issuer != "https://idp.test" || r.IdP.SubjectClaim != "sub" || strings.Join(r.NeededVersions(), ",") != "plat5" {
		t.Fatalf("%+v", r.IdP)
	}
}

func TestSourceReplacesAVersion(t *testing.T) {
	r, err := resolve(t, `versions: {auth: v0.2.2}
source: {plat5: ../plat5, operator: ../operator}
`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Source[VersionPlat5] != filepath.Clean("/plat5") || r.Source[VersionOperator] != filepath.Clean("/operator") {
		t.Fatalf("source: %v", r.Source)
	}
}

// The operator module: on by default, its IdP on the auth pin, its files and
// the console's origin from operator:.
func TestOperator(t *testing.T) {
	r, err := resolve(t, fullYML+`operator:
  routes: ./operator.routes.yml
  console:
    compose: ./operator-console/docker-compose.yml
    web: http://localhost:5412/
`)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Modules[ModuleOperator] || strings.Join(r.NeededVersions(), ",") != "plat5,auth,operator" {
		t.Fatalf("modules %v, needed %v", r.Modules, r.NeededVersions())
	}
	if r.OperatorRoutes != filepath.Clean("/proj/operator.routes.yml") || r.OperatorConsole != filepath.Clean("/proj/operator-console/docker-compose.yml") {
		t.Fatalf("files: %q %q", r.OperatorRoutes, r.OperatorConsole)
	}
	if r.OperatorWeb != "http://localhost:5412" || strings.Join(r.OperatorRedirectURIs(), ",") != "http://localhost:5412/callback" {
		t.Fatalf("console: %q %v", r.OperatorWeb, r.OperatorRedirectURIs())
	}
	if r.Ports[ports.Operator] != 5410 || r.Ports[ports.OperatorAuth] != 5411 {
		t.Fatalf("ports: %v", r.Ports)
	}

	// operator.origins adds to the console's origin, each with its /callback.
	r, err = resolve(t, fullYML+`operator:
  routes: ./operator.routes.yml
  console: {web: "http://localhost:5412"}
  origins: ["http://localhost:5173/", "http://localhost:5412"]
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.OperatorOrigins(), ","); got != "http://localhost:5412,http://localhost:5173" {
		t.Fatalf("origins: %s", got)
	}
	if got := strings.Join(r.OperatorRedirectURIs(), ","); got != "http://localhost:5412/callback,http://localhost:5173/callback" {
		t.Fatalf("redirect uris: %s", got)
	}

	// Without a console, only operator.origins.
	r, err = resolve(t, fullYML+`operator: {routes: ./operator.routes.yml, origins: ["http://localhost:5173"]}
`)
	if err != nil || strings.Join(r.OperatorOrigins(), ",") != "http://localhost:5173" {
		t.Fatalf("no console: %v %v", r.OperatorOrigins(), err)
	}

	// With auth off, the operator IdP still needs the auth pin.
	r, err = resolve(t, `versions: {plat5: v0.8.0, auth: v0.2.2, operator: v0.5.0}
modules: {auth: false}
idp: {issuer: "https://idp.test", jwks_uri: "https://idp.test/jwks", audiences: [api]}
`)
	if err != nil || strings.Join(r.NeededVersions(), ",") != "plat5,auth,operator" {
		t.Fatalf("auth off: %v %v", r.NeededVersions(), err)
	}
}

func TestOverrides(t *testing.T) {
	t.Setenv("PLAT5_ADMIN_TOKEN", "from-env")
	f, _ := Parse([]byte(fullYML))
	r, err := Resolve(f, "/proj/plat5.dev.yml", Overrides{ControlPlaneURL: "https://cp.example.com/"})
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
