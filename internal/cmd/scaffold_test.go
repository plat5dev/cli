package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/template"
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

// A starter with kind: api lands in api/, the web app and console beside it,
// the project files at the root, and plat5.yml resolves to all of it.
func TestScaffold(t *testing.T) {
	root := t.TempDir()
	templates := filepath.Join(root, "templates")
	writeFiles(t, filepath.Join(templates, "starter"), map[string]string{
		"plat5.template.yml":  "name: starter\nkind: api\nupstreams:\n  api: \"3000\"\nroutes:\n  - ./routes.identity.yml\n  - ./routes.yml\napp:\n  compose: docker-compose.yml\n",
		"routes.identity.yml": "services: {}\n",
		"routes.yml":          "services: {}\n",
		"roles.yml":           "roles:\n  owner: [\"*\"]\n",
		"docker-compose.yml":  "services:\n  api: {}\n",
		"src/main.ts":         "// api\n",
		".github/ci.yml":      "on: push\n",
	})
	writeFiles(t, filepath.Join(templates, "template-web"), map[string]string{
		"plat5.template.yml": "name: web\nkind: web\nsubstitute: [src/appName.ts]\napp:\n  compose: docker-compose.yml\n  web: http://localhost:3001\n",
		"docker-compose.yml": "services:\n  web: {}\n",
		"src/appName.ts":     "export const appName = \"{{project_id}}\"\n",
		".env.local":         "SECRET=1\n",
	})
	writeFiles(t, filepath.Join(templates, "operator-console"), map[string]string{
		"plat5.template.yml": "name: console\nkind: console\napp:\n  compose: docker-compose.yml\n  web: http://localhost:5412\n",
		"docker-compose.yml": "services:\n  console: {}\n",
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
		"api/src/main.ts", "api/docker-compose.yml", "web/docker-compose.yml", "console/docker-compose.yml",
		"routes.yml", "routes.identity.yml", "roles.yml", "docker-compose.yml", "operator.routes.yml", "README.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("missing %s", want)
		}
	}
	for _, gone := range []string{"api/routes.yml", "api/roles.yml", "api/.github", "web/.env.local"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); err == nil {
			t.Errorf("%s should not be there", gone)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "web/src/appName.ts")); !strings.Contains(string(b), `"demo"`) {
		t.Errorf("web app name not substituted: %s", b)
	}
	compose, _ := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	for _, inc := range []string{"- api/docker-compose.yml", "- web/docker-compose.yml", "- console/docker-compose.yml"} {
		if !strings.Contains(string(compose), inc) {
			t.Errorf("root compose lacks %q:\n%s", inc, compose)
		}
	}
	opRoutes, _ := os.ReadFile(filepath.Join(dir, "operator.routes.yml"))
	if !strings.Contains(string(opRoutes), "identity:") || !strings.Contains(string(opRoutes), "# api:") {
		t.Errorf("operator.routes.yml:\n%s", opRoutes)
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
	if r.AppCompose != filepath.Join(dir, "docker-compose.yml") || r.AppWeb != "http://localhost:3001" || r.AppConsole != "http://localhost:5412" {
		t.Errorf("app: %q %q %q", r.AppCompose, r.AppWeb, r.AppConsole)
	}
	if r.OperatorRoutesFile != filepath.Join(dir, "operator.routes.yml") {
		t.Errorf("operator.routes: %q", r.OperatorRoutesFile)
	}
	if got := r.OperatorConsoleOrigins(); len(got) != 1 || got[0] != "http://localhost:5412" {
		t.Errorf("console origins %v", got)
	}

	// --no-web --no-console: the API alone.
	starter2, _ := template.Resolve(opts, "starter")
	dir2 := filepath.Join(root, "lean")
	ch.noWeb, ch.noConsole = true, true
	lay2, err := scaffold(dir2, "lean", ch, pins, opts, starter2)
	if err != nil {
		t.Fatal(err)
	}
	if lay2.web != nil || lay2.console != nil {
		t.Fatal("apps added despite --no-web --no-console")
	}
	if c, _ := os.ReadFile(filepath.Join(dir2, "docker-compose.yml")); strings.Contains(string(c), "web/") || strings.Contains(string(c), "console/") {
		t.Errorf("lean compose: %s", c)
	}
	readme, _ := os.ReadFile(filepath.Join(dir2, "README.md"))
	if strings.Contains(string(readme), "web/src") || strings.Contains(string(readme), "console/src") {
		t.Errorf("README mentions apps that aren't there")
	}
}
