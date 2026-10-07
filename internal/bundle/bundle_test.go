package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializePlat5(t *testing.T) {
	dir := t.TempDir()
	if err := MaterializePlat5(dir); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"docker-compose.yml",
	} {
		p := filepath.Join(dir, rel)
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			t.Fatalf("%s: %v", rel, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 100 {
		t.Fatalf("compose too small: %d", len(data))
	}
	if !strings.Contains(string(data), "${PLAT5_VERSION:-v0.5.0}") {
		t.Fatalf("plat5 compose default pin missing:\n%s", data)
	}
	if strings.Contains(string(data), "${PLAT5_VERSION:-v0.2.0}") {
		t.Fatal("stale PLAT5_VERSION default v0.2.0")
	}
}

func TestMaterializeAuth(t *testing.T) {
	dir := t.TempDir()
	if err := MaterializeAuth(dir); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "docker-compose.yml")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "${AUTH_VERSION:-"+DefaultAuthVersion+"}") {
		t.Fatalf("auth compose default pin missing:\n%s", data)
	}
	if strings.Contains(string(data), "${AUTH_VERSION:-v0.1.8}") {
		t.Fatal("stale AUTH_VERSION default v0.1.8")
	}
	if !strings.Contains(string(data), `AUTH_DEV_MODE: "true"`) {
		t.Fatalf("local auth compose must enable AUTH_DEV_MODE:\n%s", data)
	}
}

func TestDefaultAuthVersion(t *testing.T) {
	if DefaultAuthVersion != "v0.1.10" {
		t.Fatalf("DefaultAuthVersion %q", DefaultAuthVersion)
	}
	if DefaultVersion != "v0.5.0" {
		t.Fatalf("DefaultVersion should be v0.5.0, got %q", DefaultVersion)
	}
	if DefaultOperatorVersion != "v0.3.0" {
		t.Fatalf("DefaultOperatorVersion %q", DefaultOperatorVersion)
	}
}

func TestMaterializeOperator(t *testing.T) {
	dir := t.TempDir()
	if err := MaterializeOperator(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "${OPERATOR_VERSION:-"+DefaultOperatorVersion+"}") {
		t.Fatalf("operator compose default pin missing:\n%s", s)
	}
	for _, want := range []string{"ghcr.io/dexidp/dex:", "ROUTES_FILE: /routes.yml", "localhost:8004/health/ready"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q:\n%s", want, s)
		}
	}
	for _, old := range []string{"CONSOLE_ASSETS", "BOOTSTRAP", "DB_PATH", "operator_data"} {
		if strings.Contains(s, old) {
			t.Fatalf("old operator model %q still in compose:\n%s", old, s)
		}
	}
	if !strings.Contains(s, "ghcr.io/plat5dev/operator:") {
		t.Fatalf("operator image missing:\n%s", s)
	}
}

func TestMaterializeObservability(t *testing.T) {
	dir := t.TempDir()
	if err := MaterializeObservability(dir); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"docker-compose.yml",
		"monitoring/alloy-config.alloy",
		"dashboards/service-health.json",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
	}
}
