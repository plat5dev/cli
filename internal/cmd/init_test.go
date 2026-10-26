package cmd

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/plat5yml"
)

func allModules() map[string]bool {
	return map[string]bool{config.ModuleAuth: true, config.ModuleObservability: true, config.ModuleOperator: true}
}

var pins = map[string]string{"plat5": "v0.8.1", "auth": "v0.2.2", "operator": "v0.5.0"}

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
		if r.OperatorRoutes != filepath.Clean("/proj/operator.routes.yml") || r.OperatorConsole != "" {
			t.Fatalf("%s: operator: %q %q", name, r.OperatorRoutes, r.OperatorConsole)
		}
		if !strings.Contains(yml, `cli: ">=0.9.0"`) {
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

// init writes a config that passes the schema, with organization and project
// kinds and identity's and audit's service files listed, and keeps files that
// are there.
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
		if err := writeInitFiles(dir, basePlat5YML(t)); err != nil {
			t.Fatal(err)
		}
	})
	if got, _ := os.ReadFile(filepath.Join(dir, "services", "audit.yml")); string(got) != string(mine) {
		t.Fatalf("an existing service file was replaced:\n%s", got)
	}
	_ = os.Remove(filepath.Join(dir, "services", "audit.yml"))
	captureStdout(t, func() { _ = writeInitFiles(dir, basePlat5YML(t)) })

	cfg, err := plat5yml.Load(filepath.Join(dir, "plat5.yml"))
	if err != nil {
		t.Fatal(err)
	}
	problems, err := cfg.Validate()
	if err != nil || len(problems) > 0 {
		t.Fatalf("%v %v", problems, err)
	}
	kinds, _ := cfg.Flat["kinds"].(map[string]any)
	if kinds["organization"] == nil || kinds["project"] == nil {
		t.Fatalf("kinds: %v", kinds)
	}
	if cfg.Files["identity"] != "services/identity.yml" || cfg.Files["audit"] != "services/audit.yml" {
		t.Fatalf("services: %v", cfg.Files)
	}
	if got := rootKinds(cfg.Flat); len(got) != 1 || got[0].Kind != "organization" {
		t.Fatalf("root kinds: %+v", got)
	}
}

// init's files, loaded, are the config plat5's e2e applies. Checked when plat5
// is checked out beside the CLI, as in plat5dev.
func TestInitFilesMatchPlat5(t *testing.T) {
	theirs, err := os.ReadFile(filepath.Join("..", "..", "..", "plat5", "e2e", "testdata", "config.json"))
	if err != nil {
		t.Skipf("no plat5 e2e config beside the CLI: %v", err)
	}
	dir := t.TempDir()
	captureStdout(t, func() {
		if err := writeInitFiles(dir, basePlat5YML(t)); err != nil {
			t.Fatal(err)
		}
	})
	cfg, err := plat5yml.Load(filepath.Join(dir, "plat5.yml"))
	if err != nil {
		t.Fatal(err)
	}
	ours, err := json.Marshal(cfg.Flat)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	if err := json.Unmarshal(theirs, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(ours, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("internal/cmd/initfiles differ from plat5's e2e/testdata/config.json:\nplat5: %s\ninit:  %s", theirs, ours)
	}
}

func basePlat5YML(t *testing.T) []byte {
	t.Helper()
	data, err := initFiles.ReadFile("initfiles/plat5.yml")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParsePairs(t *testing.T) {
	out := map[string]string{}
	if err := parsePairs("--source", []string{"plat5=../plat5"}, config.Sources, out); err != nil || out["plat5"] != "../plat5" {
		t.Fatalf("%v %v", out, err)
	}
	if err := parsePairs("--source", []string{"operator=../operator"}, config.Sources, out); err != nil || out["operator"] != "../operator" {
		t.Fatalf("%v %v", out, err)
	}
	if err := parsePairs("--source", []string{"billing=../billing"}, config.Sources, out); err == nil {
		t.Fatal("unknown key")
	}
	if err := parsePairs("--source", []string{"plat5"}, config.Sources, out); err == nil {
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

// A --source path must be a directory; the flag repeats, one checkout each.
func TestResolveSources(t *testing.T) {
	tmp := t.TempDir()
	for _, d := range []string{"work", "plat5", "operator"} {
		if err := os.Mkdir(filepath.Join(tmp, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(filepath.Join(tmp, "work"))

	out := map[string]string{}
	if err := parsePairs("--source", []string{"plat5=../plat5", "operator=../operator"}, config.Sources, out); err != nil {
		t.Fatal(err)
	}
	if err := resolveSources(out); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(tmp, "operator"); !samePath(out["operator"], want) {
		t.Fatalf("operator: %q, want %q", out["operator"], want)
	}

	out = map[string]string{}
	if err := parsePairs("--source", []string{"plat5=../plat5,operator=../operator"}, config.Sources, out); err != nil {
		t.Fatal(err)
	}
	err := resolveSources(out)
	if err == nil {
		t.Fatal("comma form: want an error")
	}
	for _, want := range []string{"--source plat5=../plat5,operator=../operator", "no directory at", "plat5,operator=", "one --source per checkout"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err, want)
		}
	}

	out = map[string]string{"auth": "../auth"}
	if err := resolveSources(out); err == nil || !strings.Contains(err.Error(), "--source auth=../auth: no directory at ") {
		t.Fatalf("missing checkout: %v", err)
	}
}

// samePath compares paths through symlinks (macOS's temp dir is under /private).
func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
