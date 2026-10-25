package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/plat5yml"
	"gopkg.in/yaml.v3"
)

func allModules() map[string]bool {
	return map[string]bool{config.ModuleAuth: true, config.ModuleObservability: true}
}

var pins = map[string]string{"plat5": "v0.8.0", "auth": "v0.2.2"}

// What init writes is what start reads.
func TestRenderedDevYMLResolves(t *testing.T) {
	for name, ch := range map[string]initChoices{
		"default":   {modules: allModules(), sources: map[string]string{}},
		"from-src":  {modules: allModules(), sources: map[string]string{"plat5": "/src/plat5"}},
		"fewer-mod": {modules: map[string]bool{config.ModuleAuth: true}, sources: map[string]string{}},
	} {
		yml := renderDevYML("demo", ch, pins)
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
		if ch.modules[config.ModuleObservability] != r.Modules[config.ModuleObservability] {
			t.Fatalf("%s: modules %v", name, r.Modules)
		}
	}
}

func TestRenderedDevYMLWithoutAuthSaysWhy(t *testing.T) {
	yml := renderDevYML("demo", initChoices{modules: map[string]bool{}, sources: map[string]string{}}, pins)
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

// init's files are plat5's compose seed, which plat5 checks against the
// generator. Checked when plat5 is checked out beside the CLI, as in plat5dev.
func TestInitFilesMatchPlat5(t *testing.T) {
	seed := filepath.Join("..", "..", "..", "plat5", "compose", "seed")
	if _, err := os.Stat(seed); err != nil {
		t.Skip("no plat5 checkout beside the CLI")
	}
	for _, f := range []string{"plat5.yml", "services/identity.yml", "services/audit.yml"} {
		theirs, err := os.ReadFile(filepath.Join(seed, f))
		if err != nil {
			t.Fatal(err)
		}
		ours, err := initFiles.ReadFile("initfiles/" + f)
		if err != nil {
			t.Fatal(err)
		}
		var a, b any
		if err := yaml.Unmarshal(theirs, &a); err != nil {
			t.Fatal(err)
		}
		if err := yaml.Unmarshal(ours, &b); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a, b) {
			t.Errorf("internal/cmd/initfiles/%s differs from plat5's compose/seed/%s: copy it again", f, f)
		}
	}
}

func TestInitRefusesStarters(t *testing.T) {
	initTemplate = "go-fiber-api"
	defer func() { initTemplate = noTemplate }()
	if err := runInit(initCmd, []string{t.TempDir()}); err == nil || !strings.Contains(err.Error(), "--template none") {
		t.Fatalf("want a refusal naming --template none, got %v", err)
	}
}

func TestParsePairs(t *testing.T) {
	out := map[string]string{}
	if err := parsePairs("--source", []string{"plat5=../plat5"}, config.Sources, out); err != nil || out["plat5"] != "../plat5" {
		t.Fatalf("%v %v", out, err)
	}
	if err := parsePairs("--source", []string{"operator=../operator"}, config.Sources, out); err == nil {
		t.Fatal("unknown key")
	}
	if err := parsePairs("--source", []string{"plat5"}, config.Sources, out); err == nil {
		t.Fatal("missing value")
	}
}
