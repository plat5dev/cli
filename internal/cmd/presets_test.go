package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/plat5yml"
)

func runInitPresetFlags(t *testing.T, preset, starter, sources, dir string) (string, error) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	initTemplate, initPreset, initSourcesDir, initYes = starter, preset, sources, true
	initVersions = []string{"plat5=v0.9.0", "auth=v0.2.2", "operator=v0.5.0"}
	defer func() {
		initTemplate, initPreset, initSourcesDir, initYes = noTemplate, presetMinimal, "", false
		initVersions = nil
	}()
	var err error
	out := captureStdout(t, func() { err = runInit(initCmd, []string{dir}) })
	return out, err
}

func TestAllPresetsValidate(t *testing.T) {
	for _, preset := range presetNames() {
		t.Run(preset, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "proj")
			if _, err := runInitPresetFlags(t, preset, noTemplate, "", dir); err != nil {
				t.Fatalf("init %s: %v", preset, err)
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

			// Presets must never emit placeholder services beyond identity and audit.
			if len(cfg.Files) != 2 || cfg.Files["identity"] != "services/identity.yml" || cfg.Files["audit"] != "services/audit.yml" {
				t.Fatalf("preset %s services = %v, want only identity and audit", preset, cfg.Files)
			}

			if preset == presetEnterprise {
				if r.Gateway.ScopeClaim != "scope" {
					t.Fatalf("enterprise preset should set gateway scope_claim: %+v", r.Gateway)
				}
				claims, _ := cfg.Flat["policy_claims"].([]any)
				if len(claims) != 2 {
					t.Fatalf("enterprise preset should set policy_claims in plat5.yml: %v", cfg.Flat["policy_claims"])
				}
				if cfg.Flat["custom_roles"] != true {
					t.Fatalf("enterprise preset should enable custom_roles")
				}
				rls, _ := cfg.Flat["rate_limits"].(map[string]any)
				if len(rls) != 1 || rls["standard"] == nil {
					t.Fatalf("enterprise preset should only define used rate_limits (standard), got %v", rls)
				}
				std, _ := rls["standard"].(map[string]any)
				if _, ok := std["tiers"].(map[string]any); !ok {
					t.Fatalf("enterprise standard rate limit should define tiers: %v", std)
				}
			}
			if preset == presetPublic {
				kinds, _ := cfg.Flat["kinds"].(map[string]any)
				org, _ := kinds["organization"].(map[string]any)
				if org["host"] != "{organization}.localhost" {
					t.Fatalf("public preset should set organization.host, got %v", org["host"])
				}
			}
		})
	}
}

func TestInitPresetRejectsInvalidCombinations(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bad")
	if _, err := runInitPresetFlags(t, "bogus", noTemplate, "", dir); err == nil || !strings.Contains(err.Error(), "--preset") {
		t.Fatalf("want unknown preset error, got %v", err)
	}
	if _, err := runInitPresetFlags(t, presetEnterprise, "go-fiber-api", fixtureTemplates(t), dir); err == nil || !strings.Contains(err.Error(), "--preset applies to --template none") {
		t.Fatalf("want preset+starter conflict error, got %v", err)
	}
}
