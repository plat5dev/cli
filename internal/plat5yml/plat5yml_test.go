package plat5yml

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const plat5 = `version: 2
kinds:
  directory: { sign_in: idp }
  organization: { label: Organization, segment: orgs }
roles:
  admin: { labels: ["*"], bindable_at: [organization] }
default_role: admin
creator_role: admin
services:
  - routes/e10s.yml
  - routes/api.v2.yml
`

const e10s = `e10s:
  url: http://e10s:3000
  routes:
    - path: /orgs/{organization}/plans
      scope: organization
      upstream: /orgs/{subject.organization_id}/plans
      methods:
        GET: { required_labels: [plans:read] }
`

const apiV2 = `api.v2:
  url: http://api:3000
  routes:
    - { path: /health, scope: public, methods: [GET] }
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
	dir := write(t, map[string]string{"plat5.yml": plat5, "routes/e10s.yml": e10s, "routes/api.v2.yml": apiV2})
	c, err := Load(filepath.Join(dir, "plat5.yml"))
	if err != nil {
		t.Fatal(err)
	}
	services := c.Flat["services"].(map[string]any)
	if len(services) != 2 || services["e10s"] == nil || services["api.v2"] == nil {
		t.Fatalf("services: %v", services)
	}
	if problems, err := c.Validate(); err != nil || len(problems) != 0 {
		t.Fatalf("%v %v", problems, err)
	}
}

func TestErrorsNameTheirFile(t *testing.T) {
	bad := strings.Replace(e10s, "scope: organization", "scope: 7", 1)
	dir := write(t, map[string]string{
		"plat5.yml":         strings.Replace(plat5, "default_role: admin", "default_role: Admin", 1),
		"routes/e10s.yml":   bad,
		"routes/api.v2.yml": apiV2,
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
	for _, want := range []string{"plat5.yml: default_role:", "routes/e10s.yml: services.e10s.routes[0].scope:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("want %q in:\n%s", want, joined)
		}
	}
	if got := c.FileOf("services.api.v2.routes[0]"); got != "routes/api.v2.yml" {
		t.Errorf("FileOf dotted name: %s", got)
	}
	if got := c.FileOf("services.nope"); got != "plat5.yml" {
		t.Errorf("FileOf unknown: %s", got)
	}
}

func TestRoutesFilesHoldOneServiceEach(t *testing.T) {
	dir := write(t, map[string]string{"plat5.yml": plat5, "routes/e10s.yml": e10s + apiV2, "routes/api.v2.yml": apiV2})
	if _, err := Load(filepath.Join(dir, "plat5.yml")); err == nil || !strings.Contains(err.Error(), "exactly one service") {
		t.Fatalf("got %v", err)
	}
	dir = write(t, map[string]string{"plat5.yml": plat5, "routes/e10s.yml": apiV2, "routes/api.v2.yml": apiV2})
	if _, err := Load(filepath.Join(dir, "plat5.yml")); err == nil || !strings.Contains(err.Error(), "already in routes/e10s.yml") {
		t.Fatalf("got %v", err)
	}
}

// What config pull writes loads back to the same config.
func TestWriteRoundTrips(t *testing.T) {
	src := write(t, map[string]string{"plat5.yml": plat5, "routes/e10s.yml": e10s, "routes/api.v2.yml": apiV2})
	c, err := Load(filepath.Join(src, "plat5.yml"))
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	written, err := Write(out, c.Flat)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(written, []string{"plat5.yml", "routes/api.v2.yml", "routes/e10s.yml"}) {
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
	if !strings.HasPrefix(string(text), "version: 2\nkinds:") {
		t.Errorf("keys out of order:\n%s", text)
	}
}

func TestNodeOrdersKeysAndInlinesScalarLists(t *testing.T) {
	var v any
	_ = yaml.Unmarshal([]byte("routes: [{methods: [GET], scope: public, path: /h}]\nurl: http://a:1\n"), &v)
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(Node(v)); err != nil {
		t.Fatal(err)
	}
	want := "url: http://a:1\nroutes:\n  - path: /h\n    scope: public\n    methods: [GET]\n"
	if b.String() != want {
		t.Fatalf("got:\n%s", b.String())
	}
}
