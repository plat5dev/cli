package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

const apiManifest = `name: demo-api
kind: api
service: services/api.yml
operator_routes: ./routes.operator.yml
substitute: [package.json]
app:
  compose: docker-compose.yml
next: [pnpm install]
exclude: [notes.md]
`

func writeStarter(t *testing.T, dir string) {
	t.Helper()
	writeFiles(t, dir, map[string]string{
		ManifestName:            apiManifest,
		"plat5.yml":             "services: [services/api.yml]\n",
		"plat5.dev.yml":         "project_id: demo-api\n",
		"services/api.yml":      "api: {url: \"http://api:3000\"}\n",
		"routes.operator.yml":   "services: {}\n",
		".github/workflows/ci":  "on: push\n",
		"package.json":          `{"name":"{{project_id}}"}`,
		"docker-compose.yml":    "services:\n  api: {}\n",
		"src/main.ts":           "export {}\n",
		"src/services/tasks.ts": "export {}\n",
		"node_modules/x/a":      "nope",
		"dist/main.js":          "nope",
		"data/app.db":           "db",
		"data/.gitkeep":         "",
		".env":                  "SECRET=1\n",
		".env.example":          "PORT=3000\n",
		".env.local":            "SECRET=1\n",
		"notes.md":              "mine\n",
	})
}

// Copy leaves out the template's own project, its operator routes, its exclude
// list and noise, and keeps the app; substitute writes the project id.
func TestCopy(t *testing.T) {
	src := t.TempDir()
	writeStarter(t, src)
	tpl, err := LoadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := tpl.Copy(dest, "happ"); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		"package.json":          true,
		"docker-compose.yml":    true,
		"src/main.ts":           true,
		"src/services/tasks.ts": true,
		"data/.gitkeep":         true,
		".env.example":          true,
		ManifestName:            false,
		"plat5.yml":             false,
		"plat5.dev.yml":         false,
		"services":              false,
		"routes.operator.yml":   false,
		".github":               false,
		"node_modules":          false,
		"dist":                  false,
		"data/app.db":           false,
		".env":                  false,
		".env.local":            false,
		"notes.md":              false,
	} {
		_, err := os.Stat(filepath.Join(dest, path))
		if got := err == nil; got != want {
			t.Errorf("%s copied: %v, want %v", path, got, want)
		}
	}
	body, _ := os.ReadFile(filepath.Join(dest, "package.json"))
	if string(body) != `{"name":"happ"}` {
		t.Errorf("substitute: %s", body)
	}

	if err := tpl.Copy(dest, "happ"); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("a second copy over the first: %v", err)
	}
}

func TestCheck(t *testing.T) {
	for name, tc := range map[string]struct {
		manifest, want, err string
	}{
		"api":                  {apiManifest, KindAPI, ""},
		"console":              {"kind: customer-console\napp: {compose: docker-compose.yml, web: \"http://localhost:3001\"}\n", KindCustomerConsole, ""},
		"wrong kind":           {"kind: customer-console\napp: {compose: c.yml, web: \"http://x\"}\n", KindAPI, `kind is "customer-console", want api`},
		"no kind":              {"service: s.yml\napp: {compose: c.yml}\n", KindAPI, `kind is "", want api`},
		"api, no service":      {"kind: api\napp: {compose: c.yml}\n", KindAPI, "needs service"},
		"console, no web":      {"kind: customer-console\napp: {compose: c.yml}\n", KindCustomerConsole, "needs app.web"},
		"operator":             {"kind: operator-console\napp: {compose: docker-compose.yml, web: \"http://localhost:5412\"}\n", KindOperatorConsole, ""},
		"operator, no web":     {"kind: operator-console\napp: {compose: c.yml}\n", KindOperatorConsole, "needs app.web"},
		"operator as customer": {"kind: operator-console\napp: {compose: c.yml, web: \"http://x\"}\n", KindCustomerConsole, `kind is "operator-console", want customer-console`},
		"outside":              {"kind: api\nservice: ../s.yml\napp: {compose: c.yml}\n", KindAPI, "not a path inside"},
		"unknown key":          {"kind: api\nroutes: [./routes.yml]\n", KindAPI, "field routes not found"},
	} {
		dir := t.TempDir()
		writeFiles(t, dir, map[string]string{ManifestName: tc.manifest})
		tpl, err := LoadDir(dir)
		if err == nil {
			err = tpl.Check(tc.want)
		}
		if tc.err == "" && err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("%s: got %v, want %q", name, err, tc.err)
		}
	}
}
