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
	if !strings.Contains(string(data), "${PLAT5_VERSION:?set PLAT5_VERSION}") {
		t.Fatalf("plat5 compose must require PLAT5_VERSION:\n%s", data)
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
	if !strings.Contains(string(data), "${AUTH_VERSION:?set AUTH_VERSION}") {
		t.Fatalf("auth compose must require AUTH_VERSION:\n%s", data)
	}
	if !strings.Contains(string(data), `AUTH_DEV_MODE: "true"`) {
		t.Fatalf("local auth compose must enable AUTH_DEV_MODE:\n%s", data)
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
	if !strings.Contains(s, "${OPERATOR_VERSION:?set OPERATOR_VERSION}") {
		t.Fatalf("operator compose must require OPERATOR_VERSION:\n%s", s)
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
