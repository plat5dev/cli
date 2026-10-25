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
