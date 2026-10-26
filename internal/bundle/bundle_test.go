package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

// Core wires the gateway to identity's internal API and the IdP's audiences and
// subject claim, and the control plane to identity for plan counts. start
// applies the config, so nothing seeds it.
func TestCoreEnv(t *testing.T) {
	data, _ := content.ReadFile("stack/core.yml")
	core := string(data)
	for _, want := range []string{
		"AUTH_ALLOWED_AUDIENCES: ${IDP_AUDIENCES:?}",
		"AUTH_SUBJECT_CLAIM: ${IDP_SUBJECT_CLAIM:?}",
		"IDENTITY_INTERNAL_URL: http://identity:3001",
		"ADMIT_CACHE_TTL: 5s",
	} {
		if !strings.Contains(core, want) {
			t.Errorf("core.yml lacks %q", want)
		}
	}
	if n := strings.Count(core, "IDENTITY_INTERNAL_URL: http://identity:3001"); n != 2 {
		t.Errorf("IDENTITY_INTERNAL_URL is set %d times, want the gateway and the control plane", n)
	}
}

// Core sets only env that plat5's own compose sets, service by service. Checked
// when plat5 is checked out beside the CLI, as in plat5dev.
func TestCoreEnvMatchesPlat5(t *testing.T) {
	theirs, err := os.ReadFile(filepath.Join("..", "..", "..", "plat5", "compose", "docker-compose.yml"))
	if err != nil {
		t.Skipf("no plat5 compose beside the CLI: %v", err)
	}
	ours, _ := content.ReadFile("stack/core.yml")
	plat5, cli := composeEnv(t, theirs), composeEnv(t, ours)
	for svc, env := range cli {
		known, ok := plat5[svc]
		if !ok {
			continue
		}
		for k := range env {
			if !known[k] {
				t.Errorf("core.yml sets %s on %s; plat5's compose doesn't", k, svc)
			}
		}
	}
}

// The operator module sets only env that operator's dev compose sets, service by
// service, beside the telemetry every local Plat5 Auth gets. Checked when
// operator is checked out beside the CLI, as in plat5dev.
func TestOperatorEnvMatchesOperator(t *testing.T) {
	theirs, err := os.ReadFile(filepath.Join("..", "..", "..", "operator", "compose", "docker-compose.yml"))
	if err != nil {
		t.Skipf("no operator compose beside the CLI: %v", err)
	}
	ours, _ := content.ReadFile("stack/operator.yml")
	op, cli := composeEnv(t, theirs), composeEnv(t, ours)
	telemetry := map[string]bool{"OTEL_EXPORTER_OTLP_ENDPOINT": true, "OTEL_SERVICE_NAME": true, "OTEL_SERVICE_NAMESPACE": true, "DEPLOYMENT_ENV": true}
	for svc, env := range cli {
		known, ok := op[svc]
		if !ok {
			t.Errorf("operator.yml runs %s; operator's compose doesn't", svc)
			continue
		}
		for k := range env {
			if !known[k] && !(svc == "operator-auth" && telemetry[k]) {
				t.Errorf("operator.yml sets %s on %s; operator's compose doesn't", k, svc)
			}
		}
		for k := range known {
			if !env[k] {
				t.Errorf("operator's compose sets %s on %s; operator.yml doesn't", k, svc)
			}
		}
	}
}

func composeEnv(t *testing.T, data []byte) map[string]map[string]bool {
	t.Helper()
	var doc struct {
		Services map[string]struct {
			Environment map[string]any `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]bool{}
	for svc, s := range doc.Services {
		out[svc] = map[string]bool{}
		for k := range s.Environment {
			out[svc][k] = true
		}
	}
	return out
}

func TestServices(t *testing.T) {
	names, err := Services()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gateway", "identity", "auth", "lgtm", "operator", "operator-audit", "operator-auth"} {
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
