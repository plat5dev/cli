package config

import (
	"os"
	"path/filepath"
	"strings"
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
	if cfg.Plat5Version != "v0.1.9" {
		t.Fatalf("version %q", cfg.Plat5Version)
	}
	if cfg.AuthVersion != "v0.1.6" {
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
	if cfg.Plat5Version != "v0.1.9" {
		t.Fatalf("plat5 version should stay default, got %q", cfg.Plat5Version)
	}
}

func TestLoadAuthOAuthSurface(t *testing.T) {
	root := t.TempDir()
	yml := `project_id: p
auth:
  enabled: true
  allowed_clients:
    - my-app
    - " other "
  allowed_redirect_uris:
    - http://localhost:3000/callback
    - https://oauth.pstmn.io/v1/callback
  allowed_origins:
    - http://localhost:3000
  public_issuer_url: https://auth.example.com
ports:
  auth: 5100
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
	if len(cfg.AuthAllowedClients) != 2 || cfg.AuthAllowedClients[0] != "my-app" || cfg.AuthAllowedClients[1] != "other" {
		t.Fatalf("clients %v", cfg.AuthAllowedClients)
	}
	if len(cfg.AuthAllowedRedirectURIs) != 2 {
		t.Fatalf("redirects %v", cfg.AuthAllowedRedirectURIs)
	}
	if len(cfg.AuthAllowedOrigins) != 1 || cfg.AuthAllowedOrigins[0] != "http://localhost:3000" {
		t.Fatalf("origins %v", cfg.AuthAllowedOrigins)
	}
	if cfg.AuthPublicIssuerURL != "https://auth.example.com" {
		t.Fatalf("public issuer %q", cfg.AuthPublicIssuerURL)
	}
	if err := ResolvePorts(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.AuthPublicIssuerURL != "https://auth.example.com" {
		t.Fatalf("explicit public issuer must survive ResolvePorts, got %q", cfg.AuthPublicIssuerURL)
	}
}

func TestLoadAuthPublicIssuerDerived(t *testing.T) {
	root := t.TempDir()
	yml := `project_id: p
auth:
  enabled: true
ports:
  auth: 5100
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
	if cfg.AuthPublicIssuerURL != "http://localhost:5100" {
		t.Fatalf("derived public issuer %q", cfg.AuthPublicIssuerURL)
	}
	if len(cfg.AuthAllowedClients) != 0 {
		t.Fatalf("clients should be unset, got %v", cfg.AuthAllowedClients)
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

func TestLoadAPIKeyBrand(t *testing.T) {
	unsetAPIKeyBrand(t)

	root := t.TempDir()
	yml := `project_id: p
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
	if cfg.APIKeyBrand != DefaultAPIKeyBrand {
		t.Fatalf("default brand %q", cfg.APIKeyBrand)
	}

	if err := os.WriteFile(filepath.Join(root, "plat5.yml"), []byte("project_id: p\napikey_brand: acme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKeyBrand != "acme" {
		t.Fatalf("yml brand %q", cfg.APIKeyBrand)
	}

	t.Setenv("APIKEY_BRAND", "happ")
	cfg, err = Load(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKeyBrand != "happ" {
		t.Fatalf("env brand %q", cfg.APIKeyBrand)
	}

	t.Setenv("APIKEY_BRAND", "Acme")
	if _, err := Load(Flags{}); err == nil {
		t.Fatal("expected reject uppercase")
	}
}

func TestParseAPIKeyBrand(t *testing.T) {
	ok := []string{"plat5", "acme", "a", "a1", "happ"}
	for _, in := range ok {
		got, err := parseAPIKeyBrand(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got != in {
			t.Fatalf("%q: got %q", in, got)
		}
	}
	bad := []string{"", "   ", "Plat5", "acme-app", "1acme", "-x"}
	for _, in := range bad {
		if _, err := parseAPIKeyBrand(in); err == nil {
			t.Fatalf("%q: expected error", in)
		}
	}
}

func unsetAPIKeyBrand(t *testing.T) {
	t.Helper()
	orig, ok := os.LookupEnv("APIKEY_BRAND")
	if err := os.Unsetenv("APIKEY_BRAND"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv("APIKEY_BRAND", orig)
		} else {
			_ = os.Unsetenv("APIKEY_BRAND")
		}
	})
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

func TestLoadAuthThemeFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "theme.json"), []byte(`{"title":"Acme"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	yml := `project_id: p
auth:
  enabled: true
  theme_file: ./theme.json
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
	want := filepath.Join(root, "theme.json")
	if !samePath(cfg.AuthThemeFile, want) {
		t.Fatalf("theme_file %q want %q", cfg.AuthThemeFile, want)
	}

	abs := filepath.Join(root, "other.json")
	if err := os.WriteFile(abs, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	yml = "project_id: p\nauth:\n  enabled: true\n  theme_file: " + abs + "\n"
	if err := os.WriteFile(filepath.Join(root, "plat5.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(cfg.AuthThemeFile, abs) {
		t.Fatalf("abs theme_file %q want %q", cfg.AuthThemeFile, abs)
	}
}

func TestLoadAuthThemeFileOmitted(t *testing.T) {
	root := t.TempDir()
	yml := `project_id: p
auth:
  enabled: true
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
	if cfg.AuthThemeFile != "" {
		t.Fatalf("expected empty theme_file, got %q", cfg.AuthThemeFile)
	}
}

func TestCheckAuthThemeFile(t *testing.T) {
	if err := CheckAuthThemeFile(""); err != nil {
		t.Fatalf("empty should be ok: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "nope.json")
	err := CheckAuthThemeFile(missing)
	if err == nil {
		t.Fatal("expected missing-file error")
	}
	if !strings.Contains(err.Error(), "auth.theme_file: file not found:") {
		t.Fatalf("error %q", err)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error should include path, got %q", err)
	}
	dir := t.TempDir()
	err = CheckAuthThemeFile(dir)
	if err == nil {
		t.Fatal("expected directory error")
	}
	if !strings.Contains(err.Error(), "not a file") {
		t.Fatalf("error %q", err)
	}
	f := filepath.Join(dir, "theme.json")
	if err := os.WriteFile(f, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckAuthThemeFile(f); err != nil {
		t.Fatal(err)
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
