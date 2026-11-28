package config

import (
	"os"
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
		if !r.Modules[m] {
			t.Fatalf("module %s = %t, want true", m, r.Modules[m])
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
	for _, key := range []string{"services", "roles", "upstreams", "billing"} {
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
		fullYML + "routes: {permission_separator: /}\n":                                           "routes.permission_separator",
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
  config: ./operator.yml
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
	if r.OperatorConfig != filepath.Clean("/proj/operator.yml") || r.OperatorConsole != filepath.Clean("/proj/operator-console/docker-compose.yml") {
		t.Fatalf("files: %q %q", r.OperatorConfig, r.OperatorConsole)
	}
	if r.OperatorWeb != "http://localhost:5412" || strings.Join(r.OperatorRedirectURIs(), ",") != "http://localhost:5412/callback" {
		t.Fatalf("console: %q %v", r.OperatorWeb, r.OperatorRedirectURIs())
	}
	if r.Ports[ports.Operator] != 5410 || r.Ports[ports.OperatorAuth] != 5411 {
		t.Fatalf("ports: %v", r.Ports)
	}

	// operator.origins adds to the console's origin, each with its /callback.
	r, err = resolve(t, fullYML+`operator:
  config: ./operator.yml
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
	r, err = resolve(t, fullYML+`operator: {config: ./operator.yml, origins: ["http://localhost:5173"]}
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

func TestGatewayAndRoutesConfig(t *testing.T) {
	r, err := resolve(t, fullYML+`gateway:
  scope_claim: scope
  audit_mode: async
  rate_limit_enabled: false
  upstream_signing: true
  cors_allowed_origins: ["http://localhost:3001/", "https://app.example.com"]
  cors_allowed_headers: ["X-Trace", "X-Custom"]
routes:
  permission_separator: "_"
  exclude: ["POST /orgs", "DELETE /orgs/{organization}/invites/{invite_id}"]
`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Gateway.ScopeClaim != "scope" || r.Gateway.AuditMode != "async" || !r.Gateway.UpstreamSigning {
		t.Fatalf("gateway: %+v", r.Gateway)
	}
	if r.Gateway.RateLimitEnabled == nil || *r.Gateway.RateLimitEnabled {
		t.Fatalf("gateway toggles: %+v", r.Gateway)
	}
	if got := strings.Join(r.GatewayCORSOrigins(), ","); got != "http://localhost:3001,https://app.example.com" {
		t.Fatalf("cors origins: %q", got)
	}
	if got := strings.Join(r.GatewayCORSHeaders(), ","); got != "X-Trace,X-Custom" {
		t.Fatalf("cors headers: %q", got)
	}
	if r.Routes.PermissionSeparator != "_" || len(r.Routes.Exclude) != 2 {
		t.Fatalf("routes: %+v", r.Routes)
	}
	// When gateway.cors_allowed_origins is omitted, it defaults to app.web + auth.origins.
	def, err := resolve(t, fullYML+"auth:\n  origins: [\"http://localhost:5173/\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(def.GatewayCORSOrigins(), ","); got != "http://localhost:3001,http://localhost:5173" {
		t.Fatalf("default cors origins: %q", got)
	}
	if r, err := resolve(t, fullYML+"gateway: {audit_mode: off}\n"); err != nil || r.Gateway.AuditMode != "off" {
		t.Fatalf("audit_mode off: %+v %v", r.Gateway, err)
	}

	for yml, want := range map[string]string{
		fullYML + "gateway: {audit_mode: bogus}\n":             "gateway.audit_mode",
		fullYML + "gateway: {cors_allowed_origins: [bad]}\n":   "gateway.cors_allowed_origins",
		fullYML + "gateway: {cors_allowed_headers: [\" \"]}\n": "gateway.cors_allowed_headers",
		fullYML + "routes: {exclude: [\"BAD /orgs\"]}\n":       "routes.exclude",
		fullYML + "routes: {exclude: [\"GET orgs\"]}\n":        "routes.exclude",
	} {
		if _, err := resolve(t, yml); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want %q, got %v", yml, want, err)
		}
	}
}

func TestEnvFileAndExpansion(t *testing.T) {
	dir := t.TempDir()
	envContent := "# local secrets\nexport GRAFANA_TOKEN=\"tok_123\"\nAUTH0_DOMAIN='acme.us.auth0.com'\nINLINE=val # comment\n"
	if err := os.WriteFile(filepath.Join(dir, DefaultEnvFile), []byte(envContent), 0o600); err != nil {
		t.Fatal(err)
	}
	yml := `project_id: envdemo
versions: {plat5: v0.18.0}
modules: {auth: false, operator: false}
observability: {mode: alloy-only}
idp:
  issuer: "https://${AUTH0_DOMAIN}/"
  jwks_uri: "https://${AUTH0_DOMAIN}/.well-known/jwks.json"
  audiences: ["https://api.acme.com"]
  client_id: "${AUTH0_CLIENT_ID:-spa-default}"
otel:
  endpoint: "https://otlp.grafana.net/otlp"
  headers:
    Authorization: "Bearer ${GRAFANA_TOKEN}"
`
	f, err := Parse([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Resolve(f, filepath.Join(dir, FileName), Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if r.EnvFile != filepath.Join(dir, DefaultEnvFile) {
		t.Fatalf("EnvFile = %q", r.EnvFile)
	}
	if r.Env["INLINE"] != "val" || r.IdP.Issuer != "https://acme.us.auth0.com/" || r.IdP.ClientID != "spa-default" {
		t.Fatalf("expanded: env=%v idp=%+v", r.Env, r.IdP)
	}
	if r.ObservabilityMode != ObservabilityAlloyOnly || r.OtelHeadersEnv() != "Authorization=Bearer tok_123" {
		t.Fatalf("obs=%q headers=%q", r.ObservabilityMode, r.OtelHeadersEnv())
	}

	// Process env wins over .env.local.
	t.Setenv("GRAFANA_TOKEN", "from-process")
	r, err = Resolve(f, filepath.Join(dir, FileName), Overrides{})
	if err != nil || r.OtelHeaders["Authorization"] != "Bearer from-process" {
		t.Fatalf("process env override: %v %v", r.OtelHeaders, err)
	}

	// Unresolved ${VAR} without default is an error.
	fBad, _ := Parse([]byte("versions: {plat5: v0.18.0}\nmodules: {auth: false, operator: false}\nidp: {issuer: \"https://${MISSING_VAR}\", jwks_uri: \"https://x/jwks\", audiences: [a]}\n"))
	if _, err := Resolve(fBad, filepath.Join(dir, FileName), Overrides{}); err == nil || !strings.Contains(err.Error(), "MISSING_VAR") {
		t.Fatalf("want missing var error, got %v", err)
	}

	// Explicit missing env_file is an error.
	fMissing, _ := Parse([]byte("env_file: ./nope.env\n" + fullYML))
	if _, err := Resolve(fMissing, filepath.Join(dir, FileName), Overrides{}); err == nil || !strings.Contains(err.Error(), "env_file") {
		t.Fatalf("want env_file error, got %v", err)
	}
}

func TestOperatorIdPAndInfraAndObservability(t *testing.T) {
	r, err := resolve(t, `project_id: ent
versions: {plat5: v0.18.0, operator: v0.9.0}
modules: {auth: false}
infra:
  compose: ./docker-compose.infra.yml
idp:
  issuer: https://acme.us.auth0.com/
  jwks_uri: https://acme.us.auth0.com/.well-known/jwks.json
  audiences: [https://api.acme.com]
  client_id: spa-123
operator:
  config: ./operator.yml
  idp:
    issuer: https://acme.okta.com/oauth2/default
    jwks_uri: https://acme.okta.com/oauth2/default/v1/keys
    audiences: [plat5-operator]
    client_id: op-spa
observability:
  mode: off
`)
	if err != nil {
		t.Fatal(err)
	}
	// Neither customer nor operator needs the auth pin when both use external IdPs.
	if got := strings.Join(r.NeededVersions(), ","); got != "plat5,operator" {
		t.Fatalf("NeededVersions = %q, want plat5,operator", got)
	}
	if r.InfraCompose != filepath.Clean("/proj/docker-compose.infra.yml") || r.InfraComposeProject != "ent-infra" {
		t.Fatalf("infra: %q %q", r.InfraCompose, r.InfraComposeProject)
	}
	if r.OperatorIdP.OperatorIDClaim != "sub" || r.OperatorIdP.OperatorEmailClaim != "email" || r.OperatorIdP.ClientID != "op-spa" {
		t.Fatalf("operator.idp: %+v", r.OperatorIdP)
	}
	if r.Modules[ModuleObservability] || r.ObservabilityMode != ObservabilityOff {
		t.Fatalf("observability off: mod=%v mode=%q", r.Modules[ModuleObservability], r.ObservabilityMode)
	}

	// Wildcard audience is forbidden on operator.idp.
	if _, err := resolve(t, fullYML+`operator:
  idp:
    issuer: https://acme.okta.com
    jwks_uri: https://acme.okta.com/keys
    audiences: ["*"]
`); err == nil || !strings.Contains(err.Error(), `wildcard "*"`) {
		t.Fatalf("want wildcard rejection, got %v", err)
	}

	// Invalid observability.mode is rejected.
	if _, err := resolve(t, fullYML+"observability: {mode: bogus}\n"); err == nil || !strings.Contains(err.Error(), "observability.mode") {
		t.Fatalf("want observability.mode error, got %v", err)
	}
}
