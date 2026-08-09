package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRequiresYAML(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	_, err := Load(Flags{})
	if err == nil {
		t.Fatal("expected ErrNoProject")
	}
	if _, ok := err.(ErrNoProject); !ok {
		t.Fatalf("got %T %v", err, err)
	}
}

func TestLoadResolvesRelativePaths(t *testing.T) {
	root := t.TempDir()
	edge := filepath.Join(root, "plat5-compose")
	if err := os.MkdirAll(edge, 0o755); err != nil {
		t.Fatal(err)
	}
	authCompose := filepath.Join(root, "auth-compose")
	if err := os.MkdirAll(authCompose, 0o755); err != nil {
		t.Fatal(err)
	}
	yml := `project_id: demo
plat5_compose: ./plat5-compose
auth:
  enabled: true
auth_compose: ./auth-compose
routes:
  - ./routes.yml
  - ./svc/routes.yml
admin_token: secret
`
	if err := os.WriteFile(filepath.Join(root, "plat5.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "app")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(nested); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectID != "demo" {
		t.Fatalf("project_id %q", cfg.ProjectID)
	}
	if !samePath(cfg.Plat5Compose, edge) {
		t.Fatalf("plat5 %q want %q", cfg.Plat5Compose, edge)
	}
	if !cfg.AuthEnabled {
		t.Fatal("auth.enabled")
	}
	if !samePath(cfg.AuthCompose, authCompose) {
		t.Fatalf("auth %q want %q", cfg.AuthCompose, authCompose)
	}
	if cfg.AdminToken != "secret" {
		t.Fatalf("token %q", cfg.AdminToken)
	}
	if len(cfg.RouteFiles) != 2 {
		t.Fatalf("routes %v", cfg.RouteFiles)
	}
	if cfg.ComposeProject != "plat5-demo" {
		t.Fatalf("compose project %q", cfg.ComposeProject)
	}
}

func TestLoadImageModeDefaults(t *testing.T) {
	root := t.TempDir()
	yml := `project_id: app
routes:
  - ./routes.yml
`
	if err := os.WriteFile(filepath.Join(root, "plat5.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Plat5Compose != "" {
		t.Fatalf("expected empty plat5_compose, got %q", cfg.Plat5Compose)
	}
	if cfg.Plat5Version != "v0.1.2" {
		t.Fatalf("version %q", cfg.Plat5Version)
	}
	if cfg.AuthVersion != "v0.1.2" {
		t.Fatalf("auth version %q", cfg.AuthVersion)
	}
}

func TestLoadAuthVersion(t *testing.T) {
	root := t.TempDir()
	yml := `project_id: p
auth:
  enabled: true
  version: v9.9.9
`
	if err := os.WriteFile(filepath.Join(root, "plat5.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AuthVersion != "v9.9.9" {
		t.Fatalf("auth version %q", cfg.AuthVersion)
	}
	if cfg.Plat5Version != "v0.1.2" {
		t.Fatalf("plat5 version should stay default, got %q", cfg.Plat5Version)
	}
}

func TestLoadPortPins(t *testing.T) {
	root := t.TempDir()
	yml := `project_id: p
plat5_compose: ./e
ports:
  gateway: 6001
  registry: 6002
`
	if err := os.WriteFile(filepath.Join(root, "plat5.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.PortsExplicit.Gateway || cfg.Ports.Gateway != 6001 {
		t.Fatalf("gateway pin %+v", cfg.Ports)
	}
	if !cfg.PortsExplicit.Registry || cfg.Ports.Registry != 6002 {
		t.Fatalf("registry pin %+v", cfg.Ports)
	}
	if cfg.PortsExplicit.Auth {
		t.Fatal("auth should not be pinned")
	}
}

func TestSanitizeProjectID(t *testing.T) {
	if got := sanitizeProjectID("My App!"); got != "my-app" {
		t.Fatalf("got %q", got)
	}
}

func TestLoadUpstreams(t *testing.T) {
	root := t.TempDir()
	yml := `project_id: p
plat5_compose: ./e
upstreams:
  api: 3000
  other: localhost:4000
  remote: https://api.example.com
`
	if err := os.WriteFile(filepath.Join(root, "plat5.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Upstreams["api"] != "3000" {
		t.Fatalf("api %q", cfg.Upstreams["api"])
	}
	if cfg.Upstreams["other"] != "localhost:4000" {
		t.Fatalf("other %q", cfg.Upstreams["other"])
	}
	if cfg.Upstreams["remote"] != "https://api.example.com" {
		t.Fatalf("remote %q", cfg.Upstreams["remote"])
	}
}

func TestLoadUpstreamsInvalid(t *testing.T) {
	root := t.TempDir()
	yml := `project_id: p
upstreams:
  api: 0
`
	if err := os.WriteFile(filepath.Join(root, "plat5.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(Flags{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadOtelEndpoint(t *testing.T) {
	root := t.TempDir()
	yml := `project_id: p
plat5_compose: ./e
otel:
  endpoint: http://host.docker.internal:4318
`
	if err := os.WriteFile(filepath.Join(root, "plat5.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OtelEndpoint != "http://host.docker.internal:4318" {
		t.Fatalf("otel %q", cfg.OtelEndpoint)
	}
}

func TestLoadObservabilityAndOtelAutowire(t *testing.T) {
	root := t.TempDir()
	obs := filepath.Join(root, "obs-compose")
	if err := os.MkdirAll(obs, 0o755); err != nil {
		t.Fatal(err)
	}
	yml := `project_id: p
plat5_compose: ./e
observability_compose: ./obs-compose
observability:
  enabled: true
ports:
  otlp_http: 4418
`
	if err := os.WriteFile(filepath.Join(root, "plat5.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ObservabilityEnabled {
		t.Fatal("observability.enabled")
	}
	if !samePath(cfg.ObservabilityCompose, obs) {
		t.Fatalf("obs path %q want %q", cfg.ObservabilityCompose, obs)
	}
	if cfg.OtelEndpoint != "" {
		t.Fatalf("otel should be empty before ResolvePorts, got %q", cfg.OtelEndpoint)
	}
	if err := ResolvePorts(&cfg); err != nil {
		t.Fatal(err)
	}
	want := "http://host.docker.internal:4418"
	if cfg.OtelEndpoint != want {
		t.Fatalf("otel auto-wire got %q want %q", cfg.OtelEndpoint, want)
	}
	if cfg.ObservabilityComposeName != "plat5-p-observability" {
		t.Fatalf("compose name %q", cfg.ObservabilityComposeName)
	}
}

func TestOtelExplicitWinsOverObservability(t *testing.T) {
	root := t.TempDir()
	yml := `project_id: p
plat5_compose: ./e
observability:
  enabled: true
otel:
  endpoint: http://remote.example:4318
`
	if err := os.WriteFile(filepath.Join(root, "plat5.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ResolvePorts(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.OtelEndpoint != "http://remote.example:4318" {
		t.Fatalf("got %q", cfg.OtelEndpoint)
	}
}

func TestNeedsHostGateway(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"http://host.docker.internal:4318", true},
		{"http://HOST.DOCKER.INTERNAL:4318/v1/traces", true},
		{"http://127.0.0.1:4318", false},
		{"http://localhost:4318", false},
		{"https://otlp.example.com:443", false},
	}
	for _, tc := range cases {
		if got := NeedsHostGateway(tc.in); got != tc.want {
			t.Fatalf("%q: got %v want %v", tc.in, got, tc.want)
		}
	}
}

func samePath(a, b string) bool {
	a, _ = filepath.Abs(a)
	b, _ = filepath.Abs(b)
	if ae, err := filepath.EvalSymlinks(a); err == nil {
		a = ae
	}
	if be, err := filepath.EvalSymlinks(b); err == nil {
		b = be
	}
	return a == b
}
