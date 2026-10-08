package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWritePlat5Override(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "compose.override.yml")
	if err := WritePlat5Override(p, 5011, 5012, OverrideOpts{RolesFile: "/proj/roles.yml"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		`ports: !override`,
		`"5011:5001"`,
		`"5012:5002"`,
		`gateway:`,
		`route-registry:`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "extra_hosts") {
		t.Fatalf("unexpected extra_hosts without HostGateway:\n%s", s)
	}
}

func TestWritePlat5OverrideRequiresRolesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "compose.override.yml")
	if err := WritePlat5Override(p, 5011, 5012, OverrideOpts{}); err == nil {
		t.Fatal("expected an error without a roles file")
	}
}

func TestWritePlat5OverrideRolesFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "compose.override.yml")
	if err := WritePlat5Override(p, 5001, 5002, OverrideOpts{HostGateway: true, RolesFile: "/proj/roles.yml"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
			Volumes     []string          `yaml:"volumes"`
			ExtraHosts  []string          `yaml:"extra_hosts"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("override is not valid YAML: %v\n%s", err, data)
	}
	id := doc.Services["identity"]
	if id.Environment["ROLES_FILE"] != RolesContainerPath {
		t.Fatalf("ROLES_FILE: %+v\n%s", id.Environment, data)
	}
	if len(id.Volumes) != 1 || id.Volumes[0] != "/proj/roles.yml:"+RolesContainerPath+":ro" {
		t.Fatalf("volumes: %+v", id.Volumes)
	}
	if len(id.ExtraHosts) != 1 || len(doc.Services["route-registry"].ExtraHosts) != 1 {
		t.Fatalf("host gateway kept on identity and registry:\n%s", data)
	}
}

func TestWritePlat5OverrideHostGateway(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "compose.override.yml")
	if err := WritePlat5Override(p, 5001, 5002, OverrideOpts{HostGateway: true, RolesFile: "/proj/roles.yml"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		`extra_hosts:`,
		`host.docker.internal:host-gateway`,
		`identity:`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}

func TestWriteAuthOverride(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "compose.auth.override.yml")
	if err := WriteAuthOverride(p, 5100, OverrideOpts{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `"5100:5000"`) || !strings.Contains(s, `issuer:`) {
		t.Fatalf("unexpected:\n%s", s)
	}
	if strings.Contains(s, "extra_hosts") {
		t.Fatalf("unexpected extra_hosts:\n%s", s)
	}
	if strings.Contains(s, "volumes:") || strings.Contains(s, "AUTH_THEME_FILE") {
		t.Fatalf("omit theme_file must not mount or set AUTH_THEME_FILE:\n%s", s)
	}
}

func TestWriteAuthOverrideHostGateway(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "compose.auth.override.yml")
	if err := WriteAuthOverride(p, 5000, OverrideOpts{HostGateway: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "extra_hosts:") || !strings.Contains(s, "host.docker.internal:host-gateway") {
		t.Fatalf("unexpected:\n%s", s)
	}
}

func TestWriteAuthOverrideThemeFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "compose.auth.override.yml")
	host := "/abs/project/theme.json"
	if err := WriteAuthOverride(p, 5100, OverrideOpts{AuthThemeFile: host}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	wantVol := host + ":" + AuthThemeContainerPath + ":ro"
	for _, want := range []string{
		`volumes:`,
		wantVol,
		`AUTH_THEME_FILE: ` + AuthThemeContainerPath,
		AuthThemeContainerPath,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "AUTH_LOGO_URL") || strings.Contains(s, "AUTH_FAVICON_URL") {
		t.Fatalf("must not set logo/favicon env:\n%s", s)
	}
}

func TestWriteAuthOverrideThemeFileAndHostGateway(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "compose.auth.override.yml")
	host := "/tmp/theme.json"
	if err := WriteAuthOverride(p, 5000, OverrideOpts{HostGateway: true, AuthThemeFile: host}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		`extra_hosts:`,
		`host.docker.internal:host-gateway`,
		host + ":" + AuthThemeContainerPath + ":ro",
		`AUTH_THEME_FILE: ` + AuthThemeContainerPath,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}

func TestResolveDir(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte("name: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("empty path")
	}
	if _, err := ResolveDir(filepath.Join(root, "missing")); err == nil {
		t.Fatal("expected error")
	}
}

func TestPlat5NetworkName(t *testing.T) {
	if got := Plat5NetworkName("plat5-demo"); got != "plat5-demo_plat5" {
		t.Fatalf("network %q", got)
	}
}

func testOperatorOverride(dir string) OperatorOverride {
	return OperatorOverride{
		Port:           5014,
		IdPPort:        5557,
		IssuerURL:      "http://localhost:5557/dex",
		AllowedOrigins: []string{"http://localhost:5173", "https://console.example.com"},
		Plat5Network:   "plat5-demo_plat5",
		DexConfig:      filepath.Join(dir, "operator-dex.yml"),
	}
}

func TestWriteOperatorOverride(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "compose.operator.override.yml")
	if err := WriteOperatorOverride(p, testOperatorOverride(dir)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		`external: true`,
		`name: "plat5-demo_plat5"`,
		`"5014:5004"`,
		"networks:\n      - default\n      - plat5",
		`ROUTES_FILE: /routes.yml`,
		`AUTH_ISSUER: "http://localhost:5557/dex"`,
		`AUTH_JWKS_URI: http://dex:5556/dex/keys`,
		`AUTH_AUDIENCES: "operator-cli,operator-console"`,
		`ALLOWED_ORIGINS: "http://localhost:5173,https://console.example.com"`,
		`"5557:5556"`,
		`/operator-dex.yml:/etc/dex/config.yaml:ro"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Count(s, "ports: !override") != 2 {
		t.Fatalf("both services' ports must be replaced:\n%s", s)
	}
	if strings.Contains(s, "networks: !override") {
		t.Fatalf("service networks must be appended, not replaced:\n%s", s)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("override is not YAML: %v\n%s", err, s)
	}
}

func TestWriteOperatorOverrideRequires(t *testing.T) {
	dir := t.TempDir()
	noNet := testOperatorOverride(dir)
	noNet.Plat5Network = ""
	noDex := testOperatorOverride(dir)
	noDex.DexConfig = ""
	for _, o := range []OperatorOverride{noNet, noDex} {
		if err := WriteOperatorOverride(filepath.Join(dir, "x.yml"), o); err == nil {
			t.Fatalf("expected error for %+v", o)
		}
	}
}

func TestWriteOperatorDexConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "operator-dex.yml")
	origins := []string{"http://localhost:5173", "https://console.example.com/"}
	if err := WriteOperatorDexConfig(p, "http://localhost:5557/dex", "staff@example.com", origins); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Issuer string `yaml:"issuer"`
		Web    struct {
			HTTP           string   `yaml:"http"`
			AllowedOrigins []string `yaml:"allowedOrigins"`
		} `yaml:"web"`
		StaticPasswords []struct {
			Email string `yaml:"email"`
			Hash  string `yaml:"hash"`
		} `yaml:"staticPasswords"`
		StaticClients []struct {
			ID           string   `yaml:"id"`
			Secret       string   `yaml:"secret"`
			Public       bool     `yaml:"public"`
			RedirectURIs []string `yaml:"redirectURIs"`
		} `yaml:"staticClients"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("dex config is not YAML: %v\n%s", err, data)
	}
	if cfg.Issuer != "http://localhost:5557/dex" || cfg.Web.HTTP != "0.0.0.0:5556" {
		t.Fatalf("issuer/web %+v", cfg)
	}
	if strings.Join(cfg.Web.AllowedOrigins, ",") != "http://localhost:5173,https://console.example.com/" {
		t.Fatalf("cors %q", cfg.Web.AllowedOrigins)
	}
	if len(cfg.StaticPasswords) != 1 || cfg.StaticPasswords[0].Email != "staff@example.com" || !strings.HasPrefix(cfg.StaticPasswords[0].Hash, "$2a$") {
		t.Fatalf("passwords %+v", cfg.StaticPasswords)
	}
	if len(cfg.StaticClients) != 2 {
		t.Fatalf("clients %+v", cfg.StaticClients)
	}
	cli, console := cfg.StaticClients[0], cfg.StaticClients[1]
	if cli.ID != OperatorCLIClientID || cli.Secret == "" || cli.Public {
		t.Fatalf("cli client %+v", cli)
	}
	if console.ID != OperatorConsoleClientID || !console.Public || console.Secret != "" {
		t.Fatalf("console client %+v", console)
	}
	if strings.Join(console.RedirectURIs, ",") != "http://localhost:5173/callback,https://console.example.com/callback" {
		t.Fatalf("redirects %q", console.RedirectURIs)
	}
}

func TestWriteObservabilityOverride(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "compose.observability.override.yml")
	if err := WriteObservabilityOverride(p, ObservabilityPorts{
		Grafana: 3102, OTLPGRPC: 4417, OTLPHTTP: 4418, Alloy: 13345,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		`ports: !override`,
		`127.0.0.1:3102:3000`,
		`127.0.0.1:4417:4317`,
		`127.0.0.1:4418:4318`,
		`127.0.0.1:13345:12345`,
		`grafana:`,
		`alloy:`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}
