package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadClear(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	s := State{
		ProjectID:        "demo",
		Plat5Compose:     "/tmp/plat5",
		GatewayPort:      5001,
		ControlPlanePort: 5002,
		StartedAt:        time.Now().UTC().Truncate(time.Second),
	}
	if err := Save(s); err != nil {
		t.Fatal(err)
	}
	got, err := Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	if got.Plat5Compose != s.Plat5Compose || got.GatewayPort != 5001 {
		t.Fatalf("got %+v", got)
	}
	p, err := Dir("demo")
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(dir, "plat5", "projects", "demo") {
		t.Fatalf("dir %s", p)
	}
	if err := Clear("demo"); err != nil {
		t.Fatal(err)
	}
	got, err = Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	if got.Plat5Compose != "" {
		t.Fatalf("expected empty after clear: %+v", got)
	}
}

// State written before the control plane names its port registry_port.
func TestLoadLegacyRegistryPort(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	p, err := Dir("old")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "state.json"), []byte(`{"project_id":"old","registry_port":5012,"roles_hash":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load("old")
	if err != nil {
		t.Fatal(err)
	}
	if got.ControlPlanePort != 5012 || got.LegacyRegistryPort != 0 {
		t.Fatalf("got %+v", got)
	}
}
