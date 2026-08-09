package bundle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializePlat5(t *testing.T) {
	dir := t.TempDir()
	if err := MaterializePlat5(dir); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"docker-compose.yml",
		"seed/api-keys.yml",
		"seed/organizations.yml",
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
}

func TestMaterializeAuth(t *testing.T) {
	dir := t.TempDir()
	if err := MaterializeAuth(dir); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "docker-compose.yml")
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
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
