package plat5yml

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const plat5 = `kinds:
  organization: { path: "/orgs/{organization}", list_permission: organization:read }
roles:
  admin: { permissions: ["*"], bindable_at: [organization] }
creator_role: admin
rate_limits:
  standard: { requests: 60, window: 60s }
defaults:
  rate_limit: [standard]
services:
  - services/e10s.yml
  - services/api.billing.yml
`

const e10s = `e10s:
  url: http://e10s:3000
  scopes:
    organization:
      upstream_prefix: /orgs/{organization.id}
      routes:
        - path: /plans
          methods:
            GET: { required_permissions: [plans:read] }
`

const apiBilling = `api.billing:
  url: http://api:3000
  scopes:
    public:
      routes:
        - { path: /health, methods: [GET] }
`

func write(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadAssemblesOneFlatConfig(t *testing.T) {
	dir := write(t, map[string]string{"plat5.yml": plat5, "services/e10s.yml": e10s, "services/api.billing.yml": apiBilling})
	c, err := Load(filepath.Join(dir, "plat5.yml"))
	if err != nil {
		t.Fatal(err)
	}
	services := c.Flat["services"].(map[string]any)
	if len(services) != 2 || services["e10s"] == nil || services["api.billing"] == nil {
		t.Fatalf("services: %v", services)
	}
	if problems, err := c.Validate(); err != nil || len(problems) != 0 {
		t.Fatalf("%v %v", problems, err)
	}
}

func TestErrorsNameTheirFile(t *testing.T) {
	bad := strings.Replace(e10s, "upstream_prefix: /orgs/{organization.id}", "upstream_prefix: 7", 1)
	dir := write(t, map[string]string{
		"plat5.yml":                strings.Replace(plat5, "creator_role: admin", "creator_role: Admin", 1),
		"services/e10s.yml":        bad,
		"services/api.billing.yml": apiBilling,
	})
	c, err := Load(filepath.Join(dir, "plat5.yml"))
	if err != nil {
		t.Fatal(err)
	}
	problems, err := c.Validate()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"plat5.yml: creator_role:", "services/e10s.yml: services.e10s.scopes.organization.upstream_prefix:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("want %q in:\n%s", want, joined)
		}
	}
	if got := c.FileOf("services.api.billing.routes[0]"); got != "services/api.billing.yml" {
		t.Errorf("FileOf dotted name: %s", got)
	}
	if got := c.FileOf("services.nope"); got != "plat5.yml" {
		t.Errorf("FileOf unknown: %s", got)
	}
}

func TestServiceFilesHoldOneServiceEach(t *testing.T) {
	dir := write(t, map[string]string{"plat5.yml": plat5, "services/e10s.yml": e10s + apiBilling, "services/api.billing.yml": apiBilling})
	if _, err := Load(filepath.Join(dir, "plat5.yml")); err == nil || !strings.Contains(err.Error(), "exactly one service") {
		t.Fatalf("got %v", err)
	}
	dir = write(t, map[string]string{"plat5.yml": plat5, "services/e10s.yml": apiBilling, "services/api.billing.yml": apiBilling})
	if _, err := Load(filepath.Join(dir, "plat5.yml")); err == nil || !strings.Contains(err.Error(), "already in services/e10s.yml") {
		t.Fatalf("got %v", err)
	}
}

// What config pull writes loads back to the same config.
func TestWriteRoundTrips(t *testing.T) {
	src := write(t, map[string]string{"plat5.yml": plat5, "services/e10s.yml": e10s, "services/api.billing.yml": apiBilling})
	c, err := Load(filepath.Join(src, "plat5.yml"))
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	written, err := Write(out, c.Flat)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(written, []string{"plat5.yml", "services/api.billing.yml", "services/e10s.yml"}) {
		t.Fatalf("wrote %v", written)
	}
	back, err := Load(filepath.Join(out, "plat5.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Flat, c.Flat) {
		t.Fatalf("round trip differs:\n%v\n%v", back.Flat, c.Flat)
	}
	text, _ := os.ReadFile(filepath.Join(out, "plat5.yml"))
	if !strings.HasPrefix(string(text), "kinds:") {
		t.Errorf("keys out of order:\n%s", text)
	}
}

func TestNodeOrdersKeysAndInlinesScalarLists(t *testing.T) {
	var v any
	_ = yaml.Unmarshal([]byte("scopes: {public: {routes: [{methods: [GET], path: /h}]}}\nurl: http://a:1\n"), &v)
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(Node(v)); err != nil {
		t.Fatal(err)
	}
	want := "url: http://a:1\nscopes:\n  public:\n    routes:\n      - path: /h\n        methods: [GET]\n"
	if b.String() != want {
		t.Fatalf("got:\n%s", b.String())
	}
}

func TestLoadManyDeepMergeAndProvenance(t *testing.T) {
	base := plat5 + "cors:\n  allowed_origins: [\"http://localhost:3001\"]\n  allowed_headers: [\"x-trace\"]\n"
	prod := `cors:
  allowed_origins: ["https://app.example.com"]
services:
  e10s:
    url: http://e10s.prod.internal:3000
`
	eu := `services:
  e10s:
    url: http://e10s.eu.internal:3000
`
	dir := write(t, map[string]string{
		"plat5.yml":                base,
		"plat5.prod.yml":           prod,
		"env/eu.yml":               eu,
		"services/e10s.yml":        e10s,
		"services/api.billing.yml": apiBilling,
	})
	c, err := LoadMany(
		filepath.Join(dir, "plat5.yml"),
		filepath.Join(dir, "plat5.prod.yml"),
		filepath.Join(dir, "env/eu.yml"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if problems, err := c.Validate(); err != nil || len(problems) != 0 {
		t.Fatalf("%v %v", problems, err)
	}
	// List replaced, sibling key preserved.
	cors := c.Flat["cors"].(map[string]any)
	if !reflect.DeepEqual(cors["allowed_origins"], []any{"https://app.example.com"}) {
		t.Errorf("allowed_origins: %v", cors["allowed_origins"])
	}
	if !reflect.DeepEqual(cors["allowed_headers"], []any{"x-trace"}) {
		t.Errorf("allowed_headers lost: %v", cors["allowed_headers"])
	}
	// Service url overridden by last overlay, scopes kept from service file.
	e10sSvc := c.Flat["services"].(map[string]any)["e10s"].(map[string]any)
	if e10sSvc["url"] != "http://e10s.eu.internal:3000" {
		t.Errorf("url: %v", e10sSvc["url"])
	}
	if e10sSvc["scopes"] == nil {
		t.Errorf("scopes dropped during merge")
	}
	// Provenance tracks exact winning file per path.
	for path, want := range map[string]string{
		"creator_role":                                 "plat5.yml",
		"cors.allowed_headers":                         "plat5.yml",
		"cors.allowed_origins":                         "plat5.prod.yml",
		"cors.allowed_origins[0]":                      "plat5.prod.yml",
		"services.e10s.url":                            "env/eu.yml",
		"services.e10s.scopes.organization.routes[0]":  "services/e10s.yml",
		"services.api.billing.url":                     "services/api.billing.yml",
		"services.api.billing.scopes.public.routes[0]": "services/api.billing.yml",
	} {
		if got := c.FileOf(path); got != want {
			t.Errorf("FileOf(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestLoadManyNullDeletion(t *testing.T) {
	base := plat5 + "cors:\n  allowed_origins: [\"http://localhost:3001\"]\n"
	ov := `cors: null
services:
  api.billing: null
`
	dir := write(t, map[string]string{
		"plat5.yml":                base,
		"plat5.prod.yml":           ov,
		"services/e10s.yml":        e10s,
		"services/api.billing.yml": apiBilling,
	})
	c, err := LoadMany(filepath.Join(dir, "plat5.yml"), filepath.Join(dir, "plat5.prod.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Flat["cors"]; ok {
		t.Errorf("cors should be deleted: %v", c.Flat["cors"])
	}
	services := c.Flat["services"].(map[string]any)
	if _, ok := services["api.billing"]; ok {
		t.Errorf("api.billing should be deleted")
	}
	if _, ok := c.Files["api.billing"]; ok {
		t.Errorf("api.billing should be removed from Files")
	}
	if len(c.Files) != 1 || c.Files["e10s"] != "services/e10s.yml" {
		t.Errorf("Files: %v", c.Files)
	}
	if problems, err := c.Validate(); err != nil || len(problems) != 0 {
		t.Fatalf("%v %v", problems, err)
	}
}

func TestLoadManyOverlayServiceList(t *testing.T) {
	ov := `services:
  - extra/e10s.prod.yml
  - extra/metrics.yml
`
	e10sProd := `e10s:
  url: http://e10s.prod.internal:3000
`
	metrics := `metrics:
  url: http://metrics:3000
  scopes:
    public:
      routes:
        - { path: /metrics, methods: [GET] }
`
	dir := write(t, map[string]string{
		"plat5.yml":                plat5,
		"services/e10s.yml":        e10s,
		"services/api.billing.yml": apiBilling,
		"env/prod.yml":             ov,
		"env/extra/e10s.prod.yml":  e10sProd,
		"env/extra/metrics.yml":    metrics,
	})
	c, err := LoadMany(filepath.Join(dir, "plat5.yml"), filepath.Join(dir, "env/prod.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if problems, err := c.Validate(); err != nil || len(problems) != 0 {
		t.Fatalf("%v %v", problems, err)
	}
	services := c.Flat["services"].(map[string]any)
	if len(services) != 3 || len(c.Files) != 3 {
		t.Fatalf("services=%d files=%v", len(services), c.Files)
	}
	if got := c.FileOf("services.e10s.url"); got != "env/extra/e10s.prod.yml" {
		t.Errorf("e10s.url file = %q", got)
	}
	if got := c.FileOf("services.e10s.scopes.organization.upstream_prefix"); got != "services/e10s.yml" {
		t.Errorf("e10s.scopes file = %q", got)
	}
	if got := c.FileOf("services.metrics.url"); got != "env/extra/metrics.yml" {
		t.Errorf("metrics.url file = %q", got)
	}

	// Duplicate service names within one overlay list are rejected.
	dupDir := write(t, map[string]string{
		"plat5.yml":                plat5,
		"services/e10s.yml":        e10s,
		"services/api.billing.yml": apiBilling,
		"prod.yml":                 "services: [a.yml, b.yml]\n",
		"a.yml":                    metrics,
		"b.yml":                    metrics,
	})
	if _, err := LoadMany(filepath.Join(dupDir, "plat5.yml"), filepath.Join(dupDir, "prod.yml")); err == nil || !strings.Contains(err.Error(), "already in a.yml") {
		t.Fatalf("duplicate service in overlay: got %v", err)
	}
}

func TestLoadManySchemaErrorAttribution(t *testing.T) {
	badE10s := strings.Replace(e10s, "upstream_prefix: /orgs/{organization.id}", "upstream_prefix: 7", 1)
	badOverlay := `cors:
  allowed_origins: ["not-an-origin"]
services:
  api.billing:
    url: 123
`
	dir := write(t, map[string]string{
		"plat5.yml":                strings.Replace(plat5, "creator_role: admin", "creator_role: Admin", 1),
		"plat5.prod.yml":           badOverlay,
		"services/e10s.yml":        badE10s,
		"services/api.billing.yml": apiBilling,
	})
	c, err := LoadMany(filepath.Join(dir, "plat5.yml"), filepath.Join(dir, "plat5.prod.yml"))
	if err != nil {
		t.Fatal(err)
	}
	problems, err := c.Validate()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(problems, "\n")
	for _, want := range []string{
		"plat5.yml: creator_role:",
		"services/e10s.yml: services.e10s.scopes.organization.upstream_prefix:",
		"plat5.prod.yml: cors.allowed_origins[0]:",
		"plat5.prod.yml: services.api.billing.url:",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("want %q in:\n%s", want, joined)
		}
	}
}

func TestLoadManyErrors(t *testing.T) {
	if _, err := LoadMany(); err == nil {
		t.Fatal("LoadMany() with no paths should fail")
	}
	dir := write(t, map[string]string{
		"plat5.yml":                plat5,
		"services/e10s.yml":        e10s,
		"services/api.billing.yml": apiBilling,
		"bad.yml":                  "services: nope\n",
	})
	if _, err := LoadMany(filepath.Join(dir, "plat5.yml"), filepath.Join(dir, "bad.yml")); err == nil || !strings.Contains(err.Error(), "list of service files or a mapping") {
		t.Fatalf("bad overlay services: got %v", err)
	}
}
