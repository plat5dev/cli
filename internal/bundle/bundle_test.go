package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterialize(t *testing.T) {
	dir := t.TempDir()
	if err := Materialize(dir); err != nil {
		t.Fatal(err)
	}
	for _, f := range Files {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "services:") {
			t.Fatalf("%s has no services", f)
		}
	}
	for _, rel := range []string{"db-init.sh", "observability/config.alloy", "observability/dashboards.yaml", "observability/dashboards/service-health.json"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
	}
	st, err := os.Stat(filepath.Join(dir, "db-init.sh"))
	if err != nil || st.Mode()&0o100 == 0 {
		t.Fatalf("db-init.sh must be executable: %v", st.Mode())
	}
}

// Dashboards use otel-lgtm's datasource uids.
func TestDashboardsUseLGTMDatasources(t *testing.T) {
	entries, err := content.ReadDir("stack/observability/dashboards")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, _ := content.ReadFile("stack/observability/dashboards/" + e.Name())
		if strings.Contains(string(data), `"mimir"`) {
			t.Fatalf("%s references the mimir datasource", e.Name())
		}
	}
}

// Local Auth runs in dev mode, pinned by AUTH_VERSION.
func TestAuthDevMode(t *testing.T) {
	for _, f := range []string{"stack/auth.yml"} {
		data, _ := content.ReadFile(f)
		if !strings.Contains(string(data), `AUTH_DEV_MODE: "true"`) {
			t.Fatalf("%s: local Auth must enable AUTH_DEV_MODE", f)
		}
		if !strings.Contains(string(data), "ghcr.io/plat5dev/auth:${AUTH_VERSION:?}") {
			t.Fatalf("%s: Auth must be pinned by AUTH_VERSION", f)
		}
	}
}

// Core wires the gateway to admit and identity's subject claim, and the control
// plane to identity for plan counts. start applies the config, so nothing seeds it.
func TestCoreEnv(t *testing.T) {
	data, _ := content.ReadFile("stack/core.yml")
	core := string(data)
	for _, want := range []string{
		"ADMIT_URL: http://identity:3001/internal/admit",
		"AUTH_SUBJECT_CLAIM: ${IDP_SUBJECT_CLAIM:?}",
		"IDENTITY_INTERNAL_URL: http://identity:3001",
	} {
		if !strings.Contains(core, want) {
			t.Errorf("core.yml lacks %q", want)
		}
	}
	if strings.Contains(core, "SEED_") {
		t.Error("core.yml seeds the control plane; plat5 start applies plat5.yml instead")
	}
}

func TestServices(t *testing.T) {
	names, err := Services()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gateway", "identity", "auth", "lgtm"} {
		found := false
		for _, n := range names {
			found = found || n == want
		}
		if !found {
			t.Fatalf("%s missing from %v", want, names)
		}
	}
}

func TestMaterializeRemovesStaleFiles(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "observability", "dashboards", "issuer.json")
	generated := filepath.Join(dir, "override.yml")
	_ = os.MkdirAll(filepath.Dir(stale), 0o755)
	_ = os.WriteFile(stale, []byte("{}"), 0o644)
	_ = os.WriteFile(generated, []byte("services: {}"), 0o644)
	if err := Materialize(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale dashboard kept: %v", err)
	}
	if _, err := os.Stat(generated); err != nil {
		t.Fatalf("generated file removed: %v", err)
	}
}
