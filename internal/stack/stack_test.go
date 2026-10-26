package stack

import (
	"encoding/json"
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
	f, err := config.Parse([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	r, err := config.Resolve(f, filepath.Join(dir, config.FileName), config.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

const allOn = `project_id: demo
versions: {plat5: v0.7.0, auth: v0.2.0, operator: v0.5.0}
app: {web: "http://localhost:3001"}
operator: {routes: ./operator.routes.yml}
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
	if strings.Join(s.Modules, ",") != "auth,observability,operator" {
		t.Fatalf("%v", s.Modules)
	}
	files := strings.Join(s.Runner().Files, ",")
	if files != "core.yml,auth.yml,observability.yml,operator.yml,override.yml" {
		t.Fatalf("files: %s", files)
	}
	s = newStack(t, allOn, "observability")
	if s.Has("observability") || !s.Has("auth") {
		t.Fatalf("%v", s.Modules)
	}
	files = strings.Join(s.Runner().Files, ",")
	if files != "core.yml,auth.yml,operator.yml,override.yml" {
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
		"CONTROL_PLANE_PORT":          "5402",
		"PLAT5_VERSION":               "v0.7.0",
		"IDP_ISSUER":                  "http://localhost:5401",
		"IDP_JWKS_URI":                "http://auth:5000/.well-known/jwks.json",
		"IDP_AUDIENCES":               "plat5",
		"AUTH_REDIRECT_URIS":          "http://localhost:3001/callback",
		"IDP_SUBJECT_CLAIM":           "properties.user_id",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://alloy:4318",
		"AUTH_DEV_USERS":              "dev@example.com:password",
	} {
		if e[k] != want {
			t.Errorf("%s = %q, want %q", k, e[k], want)
		}
	}
}

func TestEnvWithOwnIdP(t *testing.T) {
	s := newStack(t, `versions: {plat5: v0.8.0}
modules: {auth: false, observability: false, operator: false}
idp: {issuer: "https://idp.test", jwks_uri: "https://idp.test/jwks", audiences: [api]}
`)
	e := env(s)
	if e["IDP_ISSUER"] != "https://idp.test" || e["IDP_SUBJECT_CLAIM"] != "sub" || e["OTEL_EXPORTER_OTLP_ENDPOINT"] != "" {
		t.Fatalf("%v", e)
	}
}

func TestOverride(t *testing.T) {
	s := newStack(t, allOn+"source: {plat5: ../plat5, auth: ../auth}\notel: {endpoint: \"http://host.docker.internal:4318\"}\n")
	s.OnHost = []string{"web", "api"}
	o := s.override()
	for _, want := range []string{
		"auth-theme.json:/config/theme.json:ro",
		"image: plat5-source/gateway:demo",
		"image: plat5-source/auth:demo",
		"additional_contexts:\n        schema: \"" + filepath.Join(s.Config.Dir, "../plat5/schema") + "\"",
		"host.docker.internal:host-gateway",
		"      - \"api:host-gateway\"\n      - \"web:host-gateway\"\n",
	} {
		if !strings.Contains(o, want) {
			t.Errorf("override missing %q:\n%s", want, o)
		}
	}
	// Only the control plane compiles the schema in.
	if strings.Count(o, "additional_contexts:") != 1 {
		t.Errorf("want the schema context on the control plane only:\n%s", o)
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
	for _, want := range []string{
		"PLAT5_GATEWAY_URL=http://localhost:5400", "PLAT5_AUTH_URL=http://localhost:5401", "OTEL_EXPORTER_OTLP_ENDPOINT=http://host.docker.internal:4318",
		"PLAT5_OPERATOR_URL=http://localhost:5410", "PLAT5_OPERATOR_AUTH_URL=http://localhost:5411", "PLAT5_OPERATOR_CLIENT_ID=operator-console",
	} {
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
		"all":          newStack(t, allOn+"source: {plat5: ../plat5}\n"),
		"core":         newStack(t, allOn, "observability"),
		"no-operator":  newStack(t, allOn, "operator"),
		"operator-src": newStack(t, allOn+"source: {operator: ../operator, auth: ../auth}\n"),
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

func TestAppServices(t *testing.T) {
	s := newStack(t, allOn)
	networks := map[string][]string{"api": {"default"}, "db": {"default"}}
	got, err := s.appServices(networks, []string{"api", "identity", "billing", "api"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "api" {
		t.Fatalf("joined: %v", got)
	}
	if _, err := s.appServices(map[string][]string{"gateway": nil}, []string{"gateway"}); err == nil {
		t.Fatal("a service named like a Plat5 one must not join")
	}
}

func TestAppOverride(t *testing.T) {
	networks := map[string][]string{"api": {"backend", "default"}, "web": nil}
	got := appOverride("plat5-demo", []string{"api"}, networks)
	for _, want := range []string{
		"  api:\n    networks:\n      backend: {}\n      default: {}\n      plat5-stack: {}\n",
		"networks:\n  plat5-stack:\n    name: plat5-demo_plat5\n    external: true\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("override missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "web:") {
		t.Fatalf("web is no url host and must not join:\n%s", got)
	}
}

// withConsole is allOn with the operator console, in a project dir whose route
// file and console compose file exist.
func withConsole(t *testing.T, extra string, exclude ...string) *Stack {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "operator.routes.yml"), "services: {}\n")
	writeFile(t, filepath.Join(dir, "operator-console", "docker-compose.yml"), "services:\n  operator-console:\n    image: busybox\n")
	yml := strings.Replace(allOn, "operator: {routes: ./operator.routes.yml}\n",
		"operator:\n  routes: ./operator.routes.yml\n  console: {compose: ./operator-console/docker-compose.yml, web: \"http://localhost:5412\"}\n", 1) + extra
	f, err := config.Parse([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	r, err := config.Resolve(f, filepath.Join(dir, config.FileName), config.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s, err := New(r, exclude)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The operator module: its ports, the operator IdP's allowlists from the
// console's origin, its dev login, and its pin.
func TestOperatorEnv(t *testing.T) {
	s := withConsole(t, "")
	e := env(s)
	for k, want := range map[string]string{
		"OPERATOR_VERSION":           "v0.5.0",
		"AUTH_VERSION":               "v0.2.0",
		"OPERATOR_PORT":              "5410",
		"OPERATOR_AUTH_PORT":         "5411",
		"OPERATOR_ORIGINS":           "http://localhost:5412",
		"OPERATOR_REDIRECT_URIS":     "http://localhost:5412/callback",
		"OPERATOR_DEV_USERS":         "operator@example.com:password",
		"OPERATOR_AUTH_DISPLAY_NAME": "demo operator",
	} {
		if e[k] != want {
			t.Errorf("%s = %q, want %q", k, e[k], want)
		}
	}
	got := s.Ports()
	if got["operator"] != 5410 || got["operator_auth"] != 5411 {
		t.Fatalf("ports: %v", got)
	}
	if err := s.CheckOperator(); err != nil {
		t.Fatal(err)
	}
	if !s.RunsOperatorConsole() {
		t.Fatal("the console runs with the module")
	}
	if skip, err := s.SkippedAppServices(); err != nil || len(skip) != 0 {
		t.Fatalf("with the module, nothing is skipped: %v %v", skip, err)
	}
}

// -x operator and modules.operator: false run none of it, and leave the
// console's services out of the app.
func TestOperatorOff(t *testing.T) {
	for name, s := range map[string]*Stack{
		"-x operator":      withConsole(t, "", "operator"),
		"modules.operator": withConsole(t, "modules: {operator: false}\n"),
	} {
		if s.Has("operator") || strings.Contains(strings.Join(s.Runner().Files, ","), "operator.yml") {
			t.Errorf("%s: %v %v", name, s.Modules, s.Runner().Files)
		}
		if _, ok := s.Ports()["operator"]; ok {
			t.Errorf("%s: operator port checked: %v", name, s.Ports())
		}
		if s.RunsOperatorConsole() || s.CheckOperator() != nil {
			t.Errorf("%s: the console runs, or the module is checked", name)
		}
		if strings.Contains(s.override(), "operator") {
			t.Errorf("%s: override names operator:\n%s", name, s.override())
		}
		if _, err := exec.LookPath("docker"); err != nil {
			continue
		}
		skip, err := s.SkippedAppServices()
		if err != nil || strings.Join(skip, ",") != "operator-console" {
			t.Errorf("%s: skipped %v %v", name, skip, err)
		}
	}
}

func TestCheckOperator(t *testing.T) {
	s := newStack(t, strings.Replace(allOn, "operator: {routes: ./operator.routes.yml}\n", "", 1))
	if err := s.CheckOperator(); err == nil || !strings.Contains(err.Error(), "operator.routes") {
		t.Fatalf("no routes: %v", err)
	}
	s = newStack(t, allOn)
	if err := s.CheckOperator(); err == nil || !strings.Contains(err.Error(), "file not found") {
		t.Fatalf("missing routes file: %v", err)
	}
}

// The override mounts operator.routes.yml as the gateway's ROUTES_FILE, labels
// it with the file's hash so a changed list recreates the gateway, themes the
// operator IdP, and builds operator's images from a checkout.
func TestOperatorOverride(t *testing.T) {
	s := withConsole(t, "source: {operator: ../operator, auth: ../auth}\n")
	o := s.override()
	for _, want := range []string{
		filepath.Join(s.Config.Dir, "operator.routes.yml") + ":/etc/operator/routes.yml:ro",
		"operator-auth-theme.json:/config/theme.json:ro",
		"dev.plat5.routes-sha256: ",
		"image: plat5-source/operator:demo",
		"image: plat5-source/operator-audit:demo",
		"image: plat5-source/operator-audit-migrate:demo",
		"image: plat5-source/operator-auth:demo",
		"      target: operator-audit\n",
	} {
		if !strings.Contains(o, want) {
			t.Errorf("override missing %q:\n%s", want, o)
		}
	}
	if strings.Count(o, "\n  operator:\n") != 1 || env(s)["OPERATOR_VERSION"] != "source" {
		t.Fatalf("service repeated, or the pin not replaced:\n%s", o)
	}
}

// The generated compose project runs operator as operator's compose does: the
// route file mounted, identity on Plat5's network, the operator IdP's issuer,
// and operator-audit on the shared Postgres's operator database.
func TestOperatorComposeConfig(t *testing.T) {
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	s := withConsole(t, "")
	if err := s.Prepare(); err != nil {
		t.Fatal(err)
	}
	r := s.Runner()
	args := []string{"compose", "-p", r.ProjectName, "--project-directory", r.Dir}
	for _, f := range r.Files {
		args = append(args, "-f", f)
	}
	cmd := exec.Command("docker", append(args, "config", "--format", "json")...)
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(), r.Env...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("docker compose config: %v", err)
	}
	var cfg struct {
		Services map[string]struct {
			Image       string            `json:"image"`
			Environment map[string]string `json:"environment"`
			Networks    map[string]any    `json:"networks"`
			Volumes     []struct {
				Source, Target string
			} `json:"volumes"`
			Ports []struct {
				Target    int    `json:"target"`
				Published string `json:"published"`
			} `json:"ports"`
		} `json:"services"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	op := cfg.Services["operator"]
	if op.Image != "ghcr.io/plat5dev/operator:v0.5.0" || cfg.Services["operator-audit"].Image != "ghcr.io/plat5dev/operator-audit:v0.5.0" || cfg.Services["operator-auth"].Image != "ghcr.io/plat5dev/auth:v0.2.0" {
		t.Errorf("images: %s %s %s", op.Image, cfg.Services["operator-audit"].Image, cfg.Services["operator-auth"].Image)
	}
	for k, want := range map[string]string{
		"ROUTES_FILE":     "/etc/operator/routes.yml",
		"IDENTITY_URL":    "http://identity:3000",
		"AUTH_ISSUER":     "http://localhost:5411",
		"AUTH_AUDIENCES":  "operator-console",
		"ALLOWED_ORIGINS": "http://localhost:5412",
		"AUDIT_URL":       "http://operator-audit:8005",
	} {
		if op.Environment[k] != want {
			t.Errorf("operator %s = %q, want %q", k, op.Environment[k], want)
		}
	}
	if _, ok := op.Networks["plat5"]; !ok {
		t.Errorf("operator is not on Plat5's network: %v", op.Networks)
	}
	mounted := false
	for _, v := range op.Volumes {
		mounted = mounted || (v.Target == "/etc/operator/routes.yml" && v.Source == filepath.Join(s.Config.Dir, "operator.routes.yml"))
	}
	if !mounted || len(op.Ports) != 1 || op.Ports[0].Published != "5410" || op.Ports[0].Target != 5004 {
		t.Errorf("operator volumes %+v, ports %+v", op.Volumes, op.Ports)
	}
	if !strings.Contains(cfg.Services["operator-audit"].Environment["DATABASE_URL"], "@postgres:5432/operator?") {
		t.Errorf("operator-audit's database: %v", cfg.Services["operator-audit"].Environment)
	}
	auth := cfg.Services["operator-auth"].Environment
	if auth["PUBLIC_ISSUER_URL"] != "http://localhost:5411" || auth["AUTH_ALLOWED_REDIRECT_URIS"] != "http://localhost:5412/callback" || auth["AUTH_TOKEN_EMAIL"] != "true" || auth["AUTH_THEME_FILE"] != "/config/theme.json" {
		t.Errorf("operator-auth: %v", auth)
	}
}
