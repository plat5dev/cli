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
	return Resolve(f, "/proj/plat5.local.yml", Overrides{})
}

func TestResolveDefaults(t *testing.T) {
	r, err := resolve(t, fullYML)
	if err != nil {
		t.Fatal(err)
	}
	if r.ProjectID != "myapp" || r.ComposeProject != "plat5-myapp" || r.AppComposeProject != "myapp" {
		t.Fatalf("project: %q %q %q", r.ProjectID, r.ComposeProject, r.AppComposeProject)
	}
	for _, m := range Modules {
		if r.Modules[m] != DefaultModuleOn(m) {
			t.Fatalf("module %s = %t, want %t", m, r.Modules[m], DefaultModuleOn(m))
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
	if r.Path != filepath.Clean("/proj/plat5.local.yml") || r.ConfigPath != filepath.Clean("/proj/plat5.yml") {
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
		fullYML + "idp: {jwks_uri: https://x.test/jwks}\n":                                        "turn modules.auth off",
		fullYML + "idp: {audiences: [api]}\n":                                                     "turn modules.auth off",
		fullYML + "idp: {subject_claim: sub}\n":                                                   "turn modules.auth off",
		fullYML + "idp: {name_claims: [name]}\n":                                                  "turn modules.auth off",
		fullYML + "idp: {name_claims: []}\n":                                                      "turn modules.auth off",
	} {
		f, err := Parse([]byte(yml))
		if err == nil {
			_, err = Resolve(f, "/proj/plat5.local.yml", Overrides{})
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
	if r.IdP.NameClaims != nil || r.IdP.NameClaimsEnv() != DefaultIdPNameClaims {
		t.Fatalf("omitted name_claims: %v %q", r.IdP.NameClaims, r.IdP.NameClaimsEnv())
	}

	// Explicit empty list [] disables name seeding.
	r, err = resolve(t, `versions: {plat5: v0.8.0}
modules: {auth: false, operator: false}
idp:
  issuer: https://idp.test
  jwks_uri: https://idp.test/jwks
  audiences: [api]
  name_claims: []
`)
	if err != nil {
		t.Fatal(err)
	}
	if r.IdP.NameClaims == nil || len(*r.IdP.NameClaims) != 0 || r.IdP.NameClaimsEnv() != "" {
		t.Fatalf("empty name_claims: %v %q", r.IdP.NameClaims, r.IdP.NameClaimsEnv())
	}

	// Non-empty list trims entries and joins with commas.
	r, err = resolve(t, `versions: {plat5: v0.8.0}
modules: {auth: false, operator: false}
idp:
  issuer: https://idp.test
  jwks_uri: https://idp.test/jwks
  audiences: [api]
  name_claims: [" given_name ", "nickname"]
`)
	if err != nil {
		t.Fatal(err)
	}
	if r.IdP.NameClaimsEnv() != "given_name,nickname" {
		t.Fatalf("custom name_claims: %q", r.IdP.NameClaimsEnv())
	}

	// Empty entries inside name_claims are rejected.
	if _, err := resolve(t, `versions: {plat5: v0.8.0}
modules: {auth: false, operator: false}
idp:
  issuer: https://idp.test
  jwks_uri: https://idp.test/jwks
  audiences: [api]
  name_claims: ["name", "  "]
`); err == nil || !strings.Contains(err.Error(), "idp.name_claims") {
		t.Fatalf("empty claim entry: got %v", err)
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
	r, err := Resolve(f, "/proj/plat5.local.yml", Overrides{ControlPlaneURL: "https://cp.example.com/"})
	if err != nil {
		t.Fatal(err)
	}
	if r.ControlPlaneURL != "https://cp.example.com" || r.AdminToken != "from-env" {
		t.Fatalf("%s %s", r.ControlPlaneURL, r.AdminToken)
	}
}

func TestSecretBrand(t *testing.T) {
	for _, bad := range []string{"Plat5", "9lives", "a-b"} {
		if _, err := parseSecretBrand(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	if b, err := parseSecretBrand("acme2"); err != nil || b != "acme2" {
		t.Fatalf("%q %v", b, err)
	}
}

func TestNeedsHostGateway(t *testing.T) {
	if !NeedsHostGateway("http://host.docker.internal:4318") || NeedsHostGateway("http://alloy:4318") {
		t.Fatal("host gateway detection")
	}
}

// A project id is a name as docker compose normalises it: lowercased, other
// characters dropped, no leading - or _ (compose's -p refuses one), at most 64.
// So a folder's default id is the name compose gives an app in that folder.
func TestSanitizeProjectID(t *testing.T) {
	for in, want := range map[string]string{
		"my-app":                "my-app",
		"My.App":                "myapp",
		"my app":                "myapp",
		"_lead":                 "lead",
		"-lead":                 "lead",
		"trail-":                "trail-",
		"a__b":                  "a__b",
		"Über":                  "ber",
		"x.y-z":                 "xy-z",
		"!!!":                   "",
		strings.Repeat("a", 70): strings.Repeat("a", 64),
	} {
		if got := SanitizeProjectID(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestAuthorizerAndGatewayConfig(t *testing.T) {
	r, err := resolve(t, fullYML+`modules:
  authorizer: true
authorizer:
  manifest: ./topaz/manifest.yaml
gateway:
  scope_claim: scope
  policy_claims: [amr, idp_connection]
  audit_mode: async
  audit_enabled: true
  rate_limit_enabled: false
  upstream_signing: true
`)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Modules[ModuleAuthorizer] {
		t.Fatal("authorizer module should be on")
	}
	if r.Authorizer.Provider != AuthorizerProviderTopaz || r.Authorizer.Image != DefaultTopazImage {
		t.Fatalf("authorizer defaults: %+v", r.Authorizer)
	}
	if r.Authorizer.Manifest != filepath.Clean("/proj/topaz/manifest.yaml") {
		t.Fatalf("manifest: %q", r.Authorizer.Manifest)
	}
	if r.Ports[ports.Topaz] != 5420 || r.Ports[ports.TopazDirectory] != 5421 {
		t.Fatalf("topaz ports: %v", r.Ports)
	}
	if r.Gateway.ScopeClaim != "scope" || strings.Join(r.Gateway.PolicyClaims, ",") != "amr,idp_connection" || r.Gateway.AuditMode != "async" || !r.Gateway.UpstreamSigning {
		t.Fatalf("gateway: %+v", r.Gateway)
	}
	if r.Gateway.AuditEnabled == nil || !*r.Gateway.AuditEnabled || r.Gateway.RateLimitEnabled == nil || *r.Gateway.RateLimitEnabled {
		t.Fatalf("gateway toggles: %+v", r.Gateway)
	}

	for yml, want := range map[string]string{
		fullYML + "authorizer: {provider: opa}\n":                                   "authorizer.provider",
		fullYML + "gateway: {audit_mode: bogus}\n":                                  "gateway.audit_mode",
		fullYML + "gateway: {policy_claims: [amr, \" \"]}\n":                        "gateway.policy_claims",
		fullYML + "gateway: {policy_claims: [a,b,c,d,e,f,g,h,i,j,k,l,m,n,o,p,q]}\n": "at most 16",
	} {
		if _, err := resolve(t, yml); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want %q, got %v", yml, want, err)
		}
	}
}
