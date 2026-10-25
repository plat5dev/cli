package cmd

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/plat5yml"
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

// fixtureTemplates writes a templates directory: an API starter in
// template-go-fiber-api and the customer console beside it.
func fixtureTemplates(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFiles(t, filepath.Join(root, "template-go-fiber-api"), map[string]string{
		"plat5.template.yml":  "name: go-fiber-api\nkind: api\nservice: services/api.yml\noperator_routes: ./routes.operator.yml\nsubstitute: [go.mod]\napp:\n  compose: docker-compose.yml\nnext:\n  - go run .\n",
		"plat5.yml":           "# the starter's own\n",
		"plat5.dev.yml":       "project_id: go-fiber-api\n",
		"services/api.yml":    "api:\n  url: http://api:3000\n  scopes:\n    project:\n      upstream_prefix: /projects/{project.id}\n      routes:\n        - path: /tasks\n          methods:\n            GET: { required_permissions: [project:read] }\n",
		"services/audit.yml":  "audit:\n  url: http://audit:3002\n",
		"routes.operator.yml": "# operator\nservices:\n  api:\n    url: http://api:3000\n    routes:\n      - path: /projects/{project_id}/tasks\n        node: project_id\n        methods: [GET]\n",
		"docker-compose.yml":  "services:\n  api: {}\n",
		"go.mod":              "module {{project_id}}\n",
		"main.go":             "package main\n",
		".github/ci.yml":      "on: push\n",
	})
	writeFiles(t, filepath.Join(root, "template-customer-console"), map[string]string{
		"plat5.template.yml": "name: customer-console\nkind: customer-console\nsubstitute: [src/appName.ts]\napp:\n  compose: docker-compose.yml\n  web: http://localhost:3001\nexclude: [.github]\n",
		"docker-compose.yml": "services:\n  customer-console: {}\n",
		"src/appName.ts":     "export const appName = \"{{project_id}}\"\n",
		".env.local":         "SECRET=1\n",
	})
	return root
}

// runStarterInit runs plat5 init dir -y with a starter from the templates
// directory, pinned so nothing is fetched.
func runStarterInit(t *testing.T, templates, starter, dir string, noConsole bool) error {
	t.Helper()
	initTemplate, initTemplatesDir, initYes, initNoCustomerConsole = starter, templates, true, noConsole
	initVersions = []string{"plat5=v0.8.0", "auth=v0.2.2"}
	defer func() {
		initTemplate, initTemplatesDir, initYes, initNoCustomerConsole = noTemplate, "", false, false
		initVersions = nil
	}()
	var err error
	captureStdout(t, func() { err = runInit(initCmd, []string{dir}) })
	return err
}

// A starter lands in api/, the customer console beside it, the starter's
// service file in services/ and plat5.yml with the console's origin, a compose
// file that includes each app's, operator.routes.yml with the starter's
// services, and a plat5.dev.yml that runs it all.
func TestInitStarter(t *testing.T) {
	templates := fixtureTemplates(t)
	dir := filepath.Join(t.TempDir(), "demo")
	if err := runStarterInit(t, templates, "go-fiber-api", dir, false); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]bool{
		"api/main.go":                         true,
		"api/docker-compose.yml":              true,
		"customer-console/docker-compose.yml": true,
		"services/api.yml":                    true,
		"services/identity.yml":               true,
		"services/audit.yml":                  true,
		"README.md":                           true,
		"api/plat5.yml":                       false,
		"api/plat5.dev.yml":                   false,
		"api/services":                        false,
		"api/plat5.template.yml":              false,
		"api/routes.operator.yml":             false,
		"api/.github":                         false,
		"customer-console/.env.local":         false,
	} {
		_, err := os.Stat(filepath.Join(dir, path))
		if got := err == nil; got != want {
			t.Errorf("%s there: %v, want %v", path, got, want)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "api/go.mod")); string(b) != "module demo\n" {
		t.Errorf("api/go.mod not substituted: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "customer-console/src/appName.ts")); !strings.Contains(string(b), `"demo"`) {
		t.Errorf("console app name not substituted: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "services/audit.yml")); !bytes.Equal(b, mustInitFile(t, "initfiles/services/audit.yml")) {
		t.Errorf("services/audit.yml is the starter's, not init's:\n%s", b)
	}

	cfg, err := plat5yml.Load(filepath.Join(dir, "plat5.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if problems, err := cfg.Validate(); err != nil || len(problems) > 0 {
		t.Fatalf("%v %v", problems, err)
	}
	if cfg.Files["api"] != "services/api.yml" {
		t.Errorf("services: %v", cfg.Files)
	}
	cors, _ := cfg.Flat["cors"].(map[string]any)
	if !reflect.DeepEqual(cors["allowed_origins"], []any{"http://localhost:3001"}) {
		t.Errorf("cors: %v", cfg.Flat["cors"])
	}

	compose, _ := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if !strings.Contains(string(compose), "include:\n  - api/docker-compose.yml\n  - customer-console/docker-compose.yml\n") {
		t.Errorf("compose:\n%s", compose)
	}

	var ops struct {
		Services map[string]struct {
			Routes []struct{ Path string }
		}
	}
	routes, _ := os.ReadFile(filepath.Join(dir, operatorRoutesFile))
	if err := yaml.Unmarshal(routes, &ops); err != nil {
		t.Fatalf("%v\n%s", err, routes)
	}
	if _, ok := ops.Services["identity"]; !ok {
		t.Errorf("operator.routes.yml lost operator's own services:\n%s", routes)
	}
	if api := ops.Services["api"].Routes; len(api) != 1 || api[0].Path != "/projects/{project_id}/tasks" {
		t.Errorf("operator.routes.yml lacks the starter's api routes:\n%s", routes)
	}

	dev, _ := os.ReadFile(filepath.Join(dir, config.FileName))
	f, err := config.Parse(dev)
	if err != nil {
		t.Fatal(err)
	}
	r, err := config.Resolve(f, filepath.Join(dir, config.FileName), config.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if r.AppCompose != filepath.Join(dir, "docker-compose.yml") || r.AppWeb != "http://localhost:3001" {
		t.Errorf("app: %q %q", r.AppCompose, r.AppWeb)
	}

	readme, _ := os.ReadFile(filepath.Join(dir, "README.md"))
	for _, want := range []string{"# demo", "`services/api.yml`", "http://localhost:3001", "go run ."} {
		if !strings.Contains(string(readme), want) {
			t.Errorf("README lacks %q:\n%s", want, readme)
		}
	}

	// A second init into the same project refuses before writing anything.
	err = runStarterInit(t, templates, "go-fiber-api", dir, false)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("init over a project: %v", err)
	}
}

// --no-customer-console: the API alone, with no origin to allow.
func TestInitStarterNoCustomerConsole(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lean")
	if err := runStarterInit(t, fixtureTemplates(t), "go-fiber-api", dir, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, customerConsoleDir)); err == nil {
		t.Error("customer-console/ written")
	}
	for file, unwanted := range map[string]string{
		"docker-compose.yml": "customer-console",
		"plat5.yml":          "cors",
		config.FileName:      "web:",
		"README.md":          "customer-console/",
	} {
		if b, _ := os.ReadFile(filepath.Join(dir, file)); strings.Contains(string(b), unwanted) {
			t.Errorf("%s mentions %s:\n%s", file, unwanted, b)
		}
	}
}

// -y without --template is Plat5 alone: the config and plat5.dev.yml, no apps.
func TestInitYesIsPlat5Alone(t *testing.T) {
	if def := initCmd.Flags().Lookup("template").DefValue; def != noTemplate {
		t.Fatalf("--template defaults to %q", def)
	}
	dir := filepath.Join(t.TempDir(), "alone")
	if err := runStarterInit(t, fixtureTemplates(t), noTemplate, dir, false); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			got[filepath.ToSlash(rel)] = true
		}
		return err
	})
	want := map[string]bool{"plat5.yml": true, "services/identity.yml": true, "services/audit.yml": true, config.FileName: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wrote %v, want %v", got, want)
	}
}

func TestInitUnknownStarter(t *testing.T) {
	err := runStarterInit(t, fixtureTemplates(t), "nope", filepath.Join(t.TempDir(), "x"), false)
	if err == nil || !strings.Contains(err.Error(), "go-fiber-api") {
		t.Fatalf("want an error naming the starters, got %v", err)
	}
}

func TestOperatorRoutes(t *testing.T) {
	shipped := []byte("services:\n  identity:\n    url: http://identity:3000\n    routes: []\n")
	for name, tc := range map[string]struct {
		starter, err string
	}{
		"none":        {"", ""},
		"api":         {"# mine\nservices:\n  api:\n    url: http://api:3000\n    routes: []\n", ""},
		"named twice": {"services:\n  identity:\n    routes: []\n", "don't fit"},
		"not alone":   {"services:\n  api: {}\nupstreams: {}\n", "services: block alone"},
		"empty":       {"services: {}\n", "services: block alone"},
	} {
		var starter []byte
		if tc.starter != "" {
			starter = []byte(tc.starter)
		}
		out, err := operatorRoutes(shipped, "demo", starter)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s: got %v, want %q", name, err, tc.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !bytes.Contains(out, shipped) {
			t.Errorf("%s: shipped list missing:\n%s", name, out)
		}
	}
}

// The embedded copy of operator's route list is operator's routes.yml. Checked
// when operator is checked out beside the CLI, as in plat5dev.
func TestOperatorRoutesMatchOperator(t *testing.T) {
	theirs, err := os.ReadFile(filepath.Join("..", "..", "..", "operator", "routes.yml"))
	if err != nil {
		t.Skipf("no operator checkout beside the CLI: %v", err)
	}
	if !bytes.Equal(theirs, operatorShippedRoutes) {
		t.Error("internal/cmd/operatorfiles/routes.yml differs from operator's routes.yml: copy it over")
	}
}

// Each first-party starter, checked out beside the CLI as in plat5dev, makes a
// project whose config passes the schema and whose operator routes fit.
func TestInitFirstPartyStarters(t *testing.T) {
	templates, _ := filepath.Abs(filepath.Join("..", "..", ".."))
	for _, o := range append(template.Official, template.CustomerConsole) {
		if _, err := os.Stat(filepath.Join(templates, o.Folder(), template.ManifestName)); err != nil {
			t.Skipf("no %s checkout beside the CLI: %v", o.Folder(), err)
		}
	}
	for _, o := range template.Official {
		dir := filepath.Join(t.TempDir(), "demo")
		if err := runStarterInit(t, templates, o.Name, dir, false); err != nil {
			t.Errorf("%s: %v", o.Name, err)
			continue
		}
		cfg, err := plat5yml.Load(filepath.Join(dir, "plat5.yml"))
		if err != nil {
			t.Errorf("%s: %v", o.Name, err)
			continue
		}
		if problems, err := cfg.Validate(); err != nil || len(problems) > 0 {
			t.Errorf("%s: %v %v", o.Name, problems, err)
		}
		var ops map[string]map[string]any
		routes, _ := os.ReadFile(filepath.Join(dir, operatorRoutesFile))
		if err := yaml.Unmarshal(routes, &ops); err != nil || ops["services"]["api"] == nil {
			t.Errorf("%s: operator.routes.yml: %v\n%s", o.Name, err, routes)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, "customer-console", "src", "appName.ts")); !strings.Contains(string(b), `"demo"`) {
			t.Errorf("%s: console app name not substituted", o.Name)
		}
	}
}

func mustInitFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := initFiles.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
