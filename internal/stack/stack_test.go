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
versions: {plat5: v0.7.0, auth: v0.2.0}
app: {web: "http://localhost:3001"}
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
	if strings.Join(s.Modules, ",") != "auth,observability" {
		t.Fatalf("%v", s.Modules)
	}
	files := strings.Join(s.Runner().Files, ",")
	if files != "core.yml,auth.yml,observability.yml,override.yml" {
		t.Fatalf("files: %s", files)
	}
	s = newStack(t, allOn, "observability")
	if s.Has("observability") || !s.Has("auth") {
		t.Fatalf("%v", s.Modules)
	}
	files = strings.Join(s.Runner().Files, ",")
	if files != "core.yml,auth.yml,override.yml" {
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
modules: {auth: false, observability: false}
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
		"all":  newStack(t, allOn+"source: {plat5: ../plat5}\n"),
		"core": newStack(t, allOn, "observability"),
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
