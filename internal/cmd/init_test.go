package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/plat5yml"
)

func allModules() map[string]bool {
	return map[string]bool{config.ModuleAuth: true, config.ModuleObservability: true, config.ModuleOperator: true}
}

var pins = map[string]string{"plat5": "v0.9.0", "auth": "v0.2.2", "operator": "v0.5.0"}

// What init writes is what start reads.
func TestRenderedDevYMLResolves(t *testing.T) {
	for name, ch := range map[string]initChoices{
		"default":   {modules: allModules(), sources: map[string]string{}},
		"from-src":  {modules: allModules(), sources: map[string]string{"plat5": "/src/plat5"}},
		"fewer-mod": {modules: map[string]bool{config.ModuleAuth: true}, sources: map[string]string{}},
		"op-src":    {modules: allModules(), sources: map[string]string{"operator": "/src/operator"}},
	} {
		yml := renderDevYML("demo", ch, pins, nil)
		f, err := config.Parse([]byte(yml))
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, yml)
		}
		r, err := config.Resolve(f, "/proj/"+config.FileName, config.Overrides{})
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, yml)
		}
		if r.ProjectID != "demo" || r.ConfigPath != filepath.Clean("/proj/plat5.yml") {
			t.Fatalf("%s: %+v", name, r)
		}
		if ch.modules[config.ModuleObservability] != r.Modules[config.ModuleObservability] || ch.modules[config.ModuleOperator] != r.Modules[config.ModuleOperator] {
			t.Fatalf("%s: modules %v", name, r.Modules)
		}
		wantConfig := ""
		if ch.modules[config.ModuleOperator] {
			wantConfig = filepath.Clean("/proj/operator.yml")
		}
		if r.OperatorConfig != wantConfig || r.OperatorConsole != "" {
			t.Fatalf("%s: operator: %q %q (want %q)", name, r.OperatorConfig, r.OperatorConsole, wantConfig)
		}
		if !strings.Contains(yml, `cli: ">=`+config.SchemaVersion+`"`) {
			t.Fatalf("%s: cli floor:\n%s", name, yml)
		}
	}
}

func TestRenderedDevYMLWithoutAuthSaysWhy(t *testing.T) {
	yml := renderDevYML("demo", initChoices{modules: map[string]bool{}, sources: map[string]string{}}, pins, nil)
	if !strings.Contains(yml, "plat5 start needs idp:") {
		t.Fatalf("no hint:\n%s", yml)
	}
}

// --template none writes a config that passes the schema, with one kind,
// organization, and identity's and audit's service files listed, and keeps
// files that are there.
func TestInitFilesValidate(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "services"), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := []byte("# mine\naudit: {url: \"http://audit:3002\"}\n")
	if err := os.WriteFile(filepath.Join(dir, "services", "audit.yml"), mine, 0o644); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() {
		if err := writeInitFiles(dir); err != nil {
			t.Fatal(err)
		}
	})
	if got, _ := os.ReadFile(filepath.Join(dir, "services", "audit.yml")); string(got) != string(mine) {
		t.Fatalf("an existing service file was replaced:\n%s", got)
	}
	_ = os.Remove(filepath.Join(dir, "services", "audit.yml"))
	captureStdout(t, func() { _ = writeInitFiles(dir) })

	cfg, err := plat5yml.Load(filepath.Join(dir, "plat5.yml"))
	if err != nil {
		t.Fatal(err)
	}
	problems, err := cfg.Validate()
	if err != nil || len(problems) > 0 {
		t.Fatalf("%v %v", problems, err)
	}
	kinds, _ := cfg.Flat["kinds"].(map[string]any)
	if len(kinds) != 1 || kinds["organization"] == nil {
		t.Fatalf("kinds: %v", kinds)
	}
	if cfg.Files["identity"] != "services/identity.yml" || cfg.Files["audit"] != "services/audit.yml" {
		t.Fatalf("services: %v", cfg.Files)
	}
	if got := rootKinds(cfg.Flat); len(got) != 1 || got[0].Kind != "organization" {
		t.Fatalf("root kinds: %+v", got)
	}
}

func TestParsePairs(t *testing.T) {
	out := map[string]string{}
	if err := parsePairs("--version", []string{"plat5=v0.9.0"}, config.Sources, out); err != nil || out["plat5"] != "v0.9.0" {
		t.Fatalf("%v %v", out, err)
	}
	if err := parsePairs("--version", []string{"operator=v0.5.0"}, config.Sources, out); err != nil || out["operator"] != "v0.5.0" {
		t.Fatalf("%v %v", out, err)
	}
	if err := parsePairs("--version", []string{"billing=v1"}, config.Sources, out); err == nil {
		t.Fatal("unknown key")
	}
	if err := parsePairs("--version", []string{"plat5"}, config.Sources, out); err == nil {
		t.Fatal("missing value")
	}
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = old
	data, _ := io.ReadAll(r)
	return string(data)
}

// samePath compares paths through symlinks (macOS's temp dir is under /private).
func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
