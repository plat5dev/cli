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
	// Audit is on in the gateway image, which refuses to boot without AUDIT_URL and AUDIT_TOKEN.
	for _, want := range []string{
		"ghcr.io/plat5dev/operator-audit:${OPERATOR_VERSION:?set OPERATOR_VERSION}",
		"AUDIT_URL: http://operator-audit:8005",
		"AUDIT_TOKEN: dev-audit-token",
		"INTERNAL_TOKEN: dev-audit-token",
		`command: ["migrate"]`,
		"WRITER_ROLE: audit_writer",
		"./postgres-init.sql:/docker-entrypoint-initdb.d/",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q:\n%s", want, s)
		}
	}
	sql, err := os.ReadFile(filepath.Join(dir, "postgres-init.sql"))
	if err != nil {
		t.Fatal(err)
	}
	// The roles the init creates are the ones the DATABASE_URLs log in as.
	for role, url := range map[string]string{
		"audit_owner":  "postgres://audit_owner:audit_owner@operator-postgres",
		"audit_writer": "postgres://audit_writer:audit_writer@operator-postgres",
	} {
		if !strings.Contains(string(sql), "CREATE ROLE "+role+" LOGIN PASSWORD '"+role+"'") {
			t.Fatalf("init must create %s:\n%s", role, sql)
		}
		if !strings.Contains(s, url) {
			t.Fatalf("missing %q:\n%s", url, s)
		}
	}
	if !strings.Contains(string(sql), "CREATE SCHEMA audit AUTHORIZATION audit_owner") {
		t.Fatalf("init must create schema audit owned by audit_owner:\n%s", sql)
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
