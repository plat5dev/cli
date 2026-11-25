package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/plat5yml"
	"gopkg.in/yaml.v3"
)

func runInitPresetFlags(t *testing.T, preset, authorizer, starter, sources, dir string) (string, error) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	initTemplate, initPreset, initAuthorizer, initSourcesDir, initYes = starter, preset, authorizer, sources, true
	initVersions = []string{"plat5=v0.9.0", "auth=v0.2.2", "operator=v0.5.0"}
	defer func() {
		initTemplate, initPreset, initAuthorizer, initSourcesDir, initYes = noTemplate, presetMinimal, authorizerNone, "", false
		initVersions = nil
	}()
	var err error
	out := captureStdout(t, func() { err = runInit(initCmd, []string{dir}) })
	return out, err
}

func TestAllPresetsAndTopazValidate(t *testing.T) {
	for _, preset := range presetNames() {
		for _, authz := range authorizerNames() {
			name := preset + "_" + authz
			t.Run(name, func(t *testing.T) {
				dir := filepath.Join(t.TempDir(), "proj")
				if _, err := runInitPresetFlags(t, preset, authz, noTemplate, "", dir); err != nil {
					t.Fatalf("init %s: %v", name, err)
				}

				// plat5.yml + all service files pass schema validation.
				cfg, err := plat5yml.Load(filepath.Join(dir, "plat5.yml"))
				if err != nil {
					t.Fatalf("load plat5.yml: %v", err)
				}
				problems, err := cfg.Validate()
				if err != nil || len(problems) > 0 {
					t.Fatalf("validate plat5.yml: %v %v", problems, err)
				}

				// plat5.local.yml parses and resolves cleanly.
				rawLocal, err := os.ReadFile(filepath.Join(dir, config.FileName))
				if err != nil {
					t.Fatalf("read %s: %v", config.FileName, err)
				}
				f, err := config.Parse(rawLocal)
				if err != nil {
					t.Fatalf("parse %s: %v\n%s", config.FileName, err, rawLocal)
				}
				r, err := config.Resolve(f, filepath.Join(dir, config.FileName), config.Overrides{})
				if err != nil {
					t.Fatalf("resolve %s: %v\n%s", config.FileName, err, rawLocal)
				}

				if authz == authorizerTopaz {
					if !r.Modules[config.ModuleAuthorizer] {
						t.Fatalf("authorizer module should be enabled")
					}
					if _, ok := cfg.Flat["authorizer"].(map[string]any)["url"]; !ok {
						t.Fatalf("plat5.yml missing authorizer.url: %v", cfg.Flat["authorizer"])
					}
					for _, forbidden := range []string{"roles", "creator_role", "custom_roles", "permissions"} {
						if _, ok := cfg.Flat[forbidden]; ok {
							t.Fatalf("plat5.yml with top-level authorizer must not set %s", forbidden)
						}
					}
					if cfg.Files["docs"] != "services/docs.yml" {
						t.Fatalf("expected services/docs.yml, got %v", cfg.Files)
					}
					manifestBytes, err := os.ReadFile(filepath.Join(dir, topazManifestFile))
					if err != nil {
						t.Fatalf("missing %s: %v", topazManifestFile, err)
					}
					var doc map[string]any
					if err := yaml.Unmarshal(manifestBytes, &doc); err != nil {
						t.Fatalf("invalid topaz manifest YAML: %v", err)
					}
				} else if r.Modules[config.ModuleAuthorizer] {
					t.Fatalf("authorizer module should default off")
				}

				if preset == presetEnterprise {
					if r.Gateway.ScopeClaim != "scope" {
						t.Fatalf("enterprise preset should set gateway scope_claim: %+v", r.Gateway)
					}
					claims, _ := cfg.Flat["policy_claims"].([]any)
					if len(claims) != 2 {
						t.Fatalf("enterprise preset should set policy_claims in plat5.yml: %v", cfg.Flat["policy_claims"])
					}
					if authz == authorizerNone && cfg.Flat["custom_roles"] != true {
						t.Fatalf("enterprise preset should enable custom_roles")
					}
				}
				if preset == presetPublic {
					if cfg.Files["portal"] != "services/portal.yml" {
						t.Fatalf("public preset should include services/portal.yml, got %v", cfg.Files)
					}
				}
			})
		}
	}
}

func TestInitStarterWithTopaz(t *testing.T) {
	templates := fixtureTemplates(t)
	dir := filepath.Join(t.TempDir(), "demo")
	if _, err := runInitPresetFlags(t, presetMinimal, authorizerTopaz, "go-fiber-api", templates, dir); err != nil {
		t.Fatalf("starter + topaz: %v", err)
	}
	cfg, err := plat5yml.Load(filepath.Join(dir, "plat5.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if problems, err := cfg.Validate(); err != nil || len(problems) > 0 {
		t.Fatalf("validate starter+topaz plat5.yml: %v %v", problems, err)
	}
	if _, ok := cfg.Flat["authorizer"].(map[string]any)["url"]; !ok {
		t.Fatalf("starter+topaz missing authorizer.url")
	}
	for _, forbidden := range []string{"roles", "creator_role", "custom_roles", "permissions"} {
		if _, ok := cfg.Flat[forbidden]; ok {
			t.Fatalf("starter+topaz plat5.yml must not set %s", forbidden)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, topazManifestFile)); err != nil {
		t.Fatalf("starter+topaz missing manifest: %v", err)
	}
}

func TestInitPresetRejectsInvalidCombinations(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bad")
	if _, err := runInitPresetFlags(t, "bogus", authorizerNone, noTemplate, "", dir); err == nil || !strings.Contains(err.Error(), "--preset") {
		t.Fatalf("want unknown preset error, got %v", err)
	}
	if _, err := runInitPresetFlags(t, presetMinimal, "opa", noTemplate, "", dir); err == nil || !strings.Contains(err.Error(), "--authorizer") {
		t.Fatalf("want unknown authorizer error, got %v", err)
	}
	if _, err := runInitPresetFlags(t, presetEnterprise, authorizerNone, "go-fiber-api", fixtureTemplates(t), dir); err == nil || !strings.Contains(err.Error(), "--preset applies to --template none") {
		t.Fatalf("want preset+starter conflict error, got %v", err)
	}
}
