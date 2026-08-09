package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadClear(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	s := State{
		ProjectID:    "demo",
		Plat5Compose: "/tmp/plat5",
		GatewayPort:  5001,
		RegistryPort: 5002,
		StartedAt:    time.Now().UTC().Truncate(time.Second),
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
