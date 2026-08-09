package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestTemplate(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `name: ` + name + `
description: Test template
upstreams:
  api: "3000"
routes:
  - ./routes.yml
substitute:
  - package.json
next:
  - pnpm install
  - plat5 start
`
	if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "routes.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"{{project_id}}"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte("export {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// noise that must not copy
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "x", "a"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "app.db"), []byte("db"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", ".gitkeep"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plat5.yml.example"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadListCopySubstitute(t *testing.T) {
	root := t.TempDir()
	writeTestTemplate(t, root, "bun-effect-api")

	list, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "bun-effect-api" {
		t.Fatalf("list: %+v", list)
	}

	tpl, err := Load(root, "bun-effect-api")
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Manifest.Upstreams["api"] != "3000" {
		t.Fatalf("upstreams: %+v", tpl.Manifest.Upstreams)
	}

	dest := t.TempDir()
	if err := tpl.Copy(dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "routes.yml")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "src", "main.ts")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "node_modules")); !os.IsNotExist(err) {
		t.Fatal("node_modules should not copy")
	}
	if _, err := os.Stat(filepath.Join(dest, "data", "app.db")); !os.IsNotExist(err) {
		t.Fatal("app.db should not copy")
	}
	if _, err := os.Stat(filepath.Join(dest, "data", ".gitkeep")); err != nil {
		t.Fatal(".gitkeep should copy")
	}
	if _, err := os.Stat(filepath.Join(dest, "plat5.yml.example")); !os.IsNotExist(err) {
		t.Fatal("plat5.yml.example should not copy")
	}
	if _, err := os.Stat(filepath.Join(dest, ManifestName)); !os.IsNotExist(err) {
		t.Fatal("manifest should not copy")
	}

	if err := tpl.Substitute(dest, "happ"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dest, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"happ"`) || strings.Contains(string(body), "{{project_id}}") {
		t.Fatalf("substitute failed: %s", body)
	}
}

func TestCopyRefusesOverwrite(t *testing.T) {
	root := t.TempDir()
	writeTestTemplate(t, root, "t1")
	tpl, err := Load(root, "t1")
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "routes.yml"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = tpl.Copy(dest)
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("expected overwrite error, got %v", err)
	}
}
