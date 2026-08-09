package xdg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateHomeOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	got, err := Plat5StateDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "plat5")
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	p, err := ProjectDir("demo")
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(want, "projects", "demo") {
		t.Fatalf("project dir %s", p)
	}
}

func TestConfigHomeDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	// ensure unset
	_ = os.Unsetenv("XDG_CONFIG_HOME")
	got, err := Plat5ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".config", "plat5")
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestPlat5CacheDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	got, err := Plat5CacheDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "plat5")
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}
