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
	if !strings.Contains(string(data), "${AUTH_VERSION:-v0.1.6}") {
		t.Fatalf("auth compose default pin missing:\n%s", data)
	}
	if strings.Contains(string(data), "${AUTH_VERSION:-v0.1.5}") {
		t.Fatal("stale AUTH_VERSION default v0.1.5")
	}
}

func TestDefaultAuthVersion(t *testing.T) {
	if DefaultAuthVersion != "v0.1.6" {
		t.Fatalf("DefaultAuthVersion %q", DefaultAuthVersion)
	}
	if DefaultVersion != "v0.1.8" {
		t.Fatalf("DefaultVersion should stay v0.1.8, got %q", DefaultVersion)
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
