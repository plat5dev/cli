package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/template"
	"gopkg.in/yaml.v3"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A starter with kind: api lands in api/, the two consoles beside it, the project
// files at the root, its operator routes in operator.routes.yml, and plat5.yml
// resolves to all of it.
func TestScaffold(t *testing.T) {
	root := t.TempDir()
	templates := filepath.Join(root, "templates")
	writeFiles(t, filepath.Join(templates, "starter"), map[string]string{
		"plat5.template.yml":  "name: starter\nkind: api\nupstreams:\n  api: \"3000\"\nroutes:\n  - ./routes.identity.yml\n  - ./routes.yml\noperator_routes: ./routes.operator.yml\napp:\n  compose: docker-compose.yml\n",
		"routes.operator.yml": "# staff\nupstreams:\n  api:\n    routes:\n      - path: /organizations/{organization_id}/projects\n        methods: [GET]\n",
		"routes.identity.yml": "services: {}\n",
		"routes.yml":          "services: {}\n",
		"roles.yml":           "roles:\n  owner: [\"*\"]\n",
		"docker-compose.yml":  "services:\n  api: {}\n",
		"src/main.ts":         "// api\n",
		".github/ci.yml":      "on: push\n",
	})
	writeFiles(t, filepath.Join(templates, "template-customer-console"), map[string]string{
		"plat5.template.yml": "name: customer-console\nkind: customer-console\nsubstitute: [src/appName.ts]\napp:\n  compose: docker-compose.yml\n  web: http://localhost:3001\n",
		"docker-compose.yml": "services:\n  customer-console: {}\n",
		"src/appName.ts":     "export const appName = \"{{project_id}}\"\n",
		".env.local":         "SECRET=1\n",
	})
	writeFiles(t, filepath.Join(templates, "template-operator-console"), map[string]string{
		"plat5.template.yml": "name: operator-console\nkind: operator-console\napp:\n  compose: docker-compose.yml\n  web: http://localhost:5412\n",
		"docker-compose.yml": "services:\n  operator-console: {}\n",
	})
	operator := filepath.Join(root, "operator")
	writeFiles(t, operator, map[string]string{"routes.yml": "upstreams:\n  identity:\n    url: http://identity:3000\n    routes: []\n"})

	list, err := template.List(templates)
	if err != nil || len(list) != 1 || list[0].Name != "starter" {
		t.Fatalf("only the starter is listed: %v %v", list, err)
	}

	opts := template.ResolveOptions{LocalRoot: templates}
	starter, err := template.Resolve(opts, "starter")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "demo")
	ch := initChoices{modules: allModules(), sources: map[string]string{config.VersionOperator: operator}}
	lay, err := scaffold(dir, "demo", ch, pins, opts, starter)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"api/src/main.ts", "api/docker-compose.yml", "customer-console/docker-compose.yml", "operator-console/docker-compose.yml",
		"routes.yml", "routes.identity.yml", "roles.yml", "docker-compose.yml", "operator.routes.yml", "README.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("missing %s", want)
		}
	}
	for _, gone := range []string{"api/routes.yml", "api/roles.yml", "api/routes.operator.yml", "api/.github", "customer-console/.env.local"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); err == nil {
			t.Errorf("%s should not be there", gone)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "customer-console/src/appName.ts")); !strings.Contains(string(b), `"demo"`) {
		t.Errorf("customer console app name not substituted: %s", b)
	}
	compose, _ := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	for _, inc := range []string{"- api/docker-compose.yml", "- customer-console/docker-compose.yml", "- operator-console/docker-compose.yml"} {
		if !strings.Contains(string(compose), inc) {
			t.Errorf("root compose lacks %q:\n%s", inc, compose)
		}
	}
	opRoutes, _ := os.ReadFile(filepath.Join(dir, "operator.routes.yml"))
	var opDoc struct {
		Upstreams map[string]struct {
			Routes []struct{ Path string }
		}
	}
	if err := yaml.Unmarshal(opRoutes, &opDoc); err != nil {
		t.Fatalf("%v\n%s", err, opRoutes)
	}
	if _, ok := opDoc.Upstreams["identity"]; !ok {
		t.Errorf("operator.routes.yml lost the operator's own upstreams:\n%s", opRoutes)
	}
	if api := opDoc.Upstreams["api"].Routes; len(api) != 1 || api[0].Path != "/organizations/{organization_id}/projects" {
		t.Errorf("operator.routes.yml lacks the starter's api routes:\n%s", opRoutes)
	}
	if strings.Contains(string(opRoutes), "# api:") {
		t.Errorf("operator.routes.yml keeps the commented api stub beside the starter's:\n%s", opRoutes)
	}

	yml := renderPlat5YML("demo", ch, pins, lay)
	f, err := config.Parse([]byte(yml))
	if err != nil {
		t.Fatalf("%v\n%s", err, yml)
	}
	r, err := config.Resolve(f, filepath.Join(dir, "plat5.yml"), config.Overrides{})
	if err != nil {
		t.Fatalf("%v\n%s", err, yml)
	}
	if r.AppCompose != filepath.Join(dir, "docker-compose.yml") || r.AppWeb != "http://localhost:3001" || r.AppOperatorConsole != "http://localhost:5412" {
		t.Errorf("app: %q %q %q", r.AppCompose, r.AppWeb, r.AppOperatorConsole)
	}
	if r.OperatorRoutesFile != filepath.Join(dir, "operator.routes.yml") {
		t.Errorf("operator.routes: %q", r.OperatorRoutesFile)
	}
	if got := r.OperatorConsoleOrigins(); len(got) != 1 || got[0] != "http://localhost:5412" {
		t.Errorf("console origins %v", got)
	}

	// --no-customer-console --no-operator-console: the API alone.
	starter2, _ := template.Resolve(opts, "starter")
	dir2 := filepath.Join(root, "lean")
	ch.noCustomerConsole, ch.noOperatorConsole = true, true
	lay2, err := scaffold(dir2, "lean", ch, pins, opts, starter2)
	if err != nil {
		t.Fatal(err)
	}
	if lay2.customerConsole != nil || lay2.operatorConsole != nil {
		t.Fatal("apps added despite --no-customer-console --no-operator-console")
	}
	if c, _ := os.ReadFile(filepath.Join(dir2, "docker-compose.yml")); strings.Contains(string(c), "console/") {
		t.Errorf("lean compose: %s", c)
	}
	readme, _ := os.ReadFile(filepath.Join(dir2, "README.md"))
	if strings.Contains(string(readme), "console/src") {
		t.Errorf("README mentions apps that aren't there")
	}
}

// A starter without operator_routes leaves the commented api stub; one whose
// routes name an upstream the operator already has is refused.
func TestOperatorRoutes(t *testing.T) {
	operator := t.TempDir()
	writeFiles(t, operator, map[string]string{"routes.yml": "upstreams:\n  identity:\n    url: http://identity:3000\n    routes: []\n"})

	stub, err := operatorRoutes(operator, "", nil)
	if err != nil || !strings.Contains(string(stub), "# api:") {
		t.Fatalf("no starter routes: the stub stays: %v\n%s", err, stub)
	}

	dup := &template.Template{Dir: t.TempDir(), Manifest: template.Manifest{OperatorRoutes: "./routes.operator.yml"}}
	writeFiles(t, dup.Dir, map[string]string{"routes.operator.yml": "upstreams:\n  identity:\n    routes: []\n"})
	api, err := starterOperatorRoutes(dup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operatorRoutes(operator, "", api); err == nil {
		t.Fatal("a starter upstream named like the operator's is refused")
	}

	bad := &template.Template{Dir: t.TempDir(), Manifest: template.Manifest{OperatorRoutes: "routes.operator.yml"}}
	writeFiles(t, bad.Dir, map[string]string{"routes.operator.yml": "services:\n  api: {}\n"})
	if _, err := starterOperatorRoutes(bad); err == nil {
		t.Fatal("a starter file without upstreams: is refused")
	}
}
