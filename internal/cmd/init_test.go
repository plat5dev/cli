package cmd

import (
	"strings"
	"testing"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/template"
)

func allModules() map[string]bool {
	return map[string]bool{config.ModuleAuth: true, config.ModuleOperator: true, config.ModuleObservability: true}
}

var pins = map[string]string{"plat5": "v0.7.0", "auth": "v0.2.0", "operator": "v0.4.0", "operator_console": "v0.1.0"}

// What init writes is what start reads.
func TestRenderedPlat5YMLResolves(t *testing.T) {
	tpl := &template.Template{Manifest: template.Manifest{
		Upstreams: map[string]string{"api": "3000"},
		Routes:    []string{"./routes.identity.yml", "./routes.yml"},
		App:       template.App{Compose: "docker-compose.yml", Web: "http://localhost:3001"},
	}}
	for name, tc := range map[string]struct {
		ch  initChoices
		tpl *template.Template
	}{
		"starter":   {initChoices{modules: allModules(), sources: map[string]string{}}, tpl},
		"bare":      {initChoices{modules: allModules(), sources: map[string]string{}}, nil},
		"from-src":  {initChoices{modules: allModules(), sources: map[string]string{"plat5": "/src/plat5"}}, tpl},
		"fewer-mod": {initChoices{modules: map[string]bool{config.ModuleAuth: true}, sources: map[string]string{}}, tpl},
	} {
		yml := renderPlat5YML("demo", tc.ch, pins, tc.tpl)
		f, err := config.Parse([]byte(yml))
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, yml)
		}
		r, err := config.Resolve(f, "/proj/plat5.yml", config.Overrides{})
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, yml)
		}
		if r.ProjectID != "demo" || r.RolesFile == "" {
			t.Fatalf("%s: %+v", name, r)
		}
		if tc.tpl != nil && (r.AppCompose != "/proj/docker-compose.yml" || r.AppWeb != "http://localhost:3001") {
			t.Fatalf("%s: app %q %q", name, r.AppCompose, r.AppWeb)
		}
		if tc.ch.modules[config.ModuleOperator] != r.Modules[config.ModuleOperator] {
			t.Fatalf("%s: modules %v", name, r.Modules)
		}
	}
}

func TestRenderedPlat5YMLWithoutAuthSaysWhy(t *testing.T) {
	yml := renderPlat5YML("demo", initChoices{modules: map[string]bool{}, sources: map[string]string{}}, pins, nil)
	if !strings.Contains(yml, "plat5 start needs idp:") {
		t.Fatalf("no hint:\n%s", yml)
	}
}

func TestParsePairs(t *testing.T) {
	out := map[string]string{}
	if err := parsePairs("--source", []string{"plat5=../plat5"}, config.Sources, out); err != nil || out["plat5"] != "../plat5" {
		t.Fatalf("%v %v", out, err)
	}
	if err := parsePairs("--source", []string{"web=../web"}, config.Sources, out); err == nil {
		t.Fatal("unknown key")
	}
	if err := parsePairs("--source", []string{"plat5"}, config.Sources, out); err == nil {
		t.Fatal("missing value")
	}
}
