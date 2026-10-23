package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s, err := Load("demo")
	if err != nil || s.ProjectID != "demo" || s.Dev != nil {
		t.Fatalf("empty load: %+v %v", s, err)
	}
	s.Modules = []string{"auth"}
	s.Dev = &Dev{Email: "dev@example.com", Key: "plat5-k-2-x"}
	if err := Save(s); err != nil {
		t.Fatal(err)
	}
	got, err := Load("demo")
	if err != nil || got.Dev == nil || got.Dev.Key != "plat5-k-2-x" || got.Modules[0] != "auth" {
		t.Fatalf("%+v %v", got, err)
	}
	dir, _ := Dir("demo")
	st, err := os.Stat(filepath.Join(dir, "state.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("state.json must be 0600: %v %v", st.Mode(), err)
	}
}

func TestUnreadableStateStartsOver(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir, _ := Dir("demo")
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(filepath.Join(dir, "state.json"), []byte("{not json"), 0o600)
	s, err := Load("demo")
	if err != nil || s.ProjectID != "demo" {
		t.Fatalf("%+v %v", s, err)
	}
}
