package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/state"
)

// ownerProjects makes two folders named work, a/work and b/work, each a project
// whose id is its folder's name, under a temp state home.
func ownerProjects(t *testing.T) (a, b string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	tmp := projectFolder(t.TempDir())
	yml := "versions: {plat5: v0.9.0, auth: v0.2.2, operator: v0.5.0}\nmodules: {operator: false}\n"
	for _, d := range []string{"a/work", "b/work"} {
		dir := filepath.Join(tmp, d)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plat5.dev.yml"), []byte(yml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(tmp, "a/work"), filepath.Join(tmp, "b/work")
}

// Every command on the stack or its state refuses from a folder other than the
// one that owns the project id, before it touches Docker, and says how to set
// project_id.
func TestOwnerRefusesAnotherFolder(t *testing.T) {
	a, b := ownerProjects(t)
	if err := state.Save(state.State{ProjectID: "work", Owner: a}); err != nil {
		t.Fatal(err)
	}
	want := `project id "work" belongs to the project in ` + a + `, not this one in ` + b +
		`: the stack and the app (compose projects plat5-work and work, their volumes) and the state are that project's.
Give this project its own id in ` + filepath.Join(b, "plat5.dev.yml") + `:

  project_id: b-work`

	t.Chdir(b)
	resetYes = true
	defer func() { resetYes = false }()
	for name, run := range map[string]func() error{
		"start":  func() error { return runStart(startCmd, nil) },
		"stop":   func() error { return runStop(stopCmd, nil) },
		"reset":  func() error { return runReset(resetCmd, nil) },
		"logs":   func() error { return runLogs(logsCmd, nil) },
		"status": func() error { return runStatus(statusCmd, nil) },
	} {
		if err := run(); err == nil || err.Error() != want {
			t.Errorf("%s from %s:\n got %v\nwant %s", name, b, err, want)
		}
	}

	// The owner's own folder passes, through a symlink too.
	t.Chdir(a)
	if _, err := loadOwned(mustLoadConfig(t)); err != nil {
		t.Errorf("the owner's folder: %v", err)
	}
	link := filepath.Join(filepath.Dir(filepath.Dir(a)), "link")
	if err := os.Symlink(filepath.Dir(a), link); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(link, "work"))
	if _, err := loadOwned(mustLoadConfig(t)); err != nil {
		t.Errorf("the owner's folder through a symlink: %v", err)
	}
}

// init refuses a folder whose name is a project id another folder owns, before
// it writes anything.
func TestOwnerRefusesInit(t *testing.T) {
	a, b := ownerProjects(t)
	if err := state.Save(state.State{ProjectID: "work", Owner: a}); err != nil {
		t.Fatal(err)
	}
	c := filepath.Join(filepath.Dir(filepath.Dir(b)), "c", "work")
	t.Chdir(b)
	initYes = true
	defer func() { initYes = false }()
	err := runInit(initCmd, []string{c})
	want := `project id "work" belongs to the project in ` + a + `, not this one in ` + c +
		`: the stack and the app (compose projects plat5-work and work, their volumes) and the state are that project's.
Give this project its own id:

  plat5 init --project-id c-work`
	if err == nil || err.Error() != want {
		t.Fatalf("init:\n got %v\nwant %s", err, want)
	}
	if _, err := os.Stat(c); !os.IsNotExist(err) {
		t.Errorf("init made %s: %v", c, err)
	}
}

// A state with no owner, as an older CLI wrote it, is any folder's until a start
// records one; stop and init pass it without recording.
func TestOwnerAdoption(t *testing.T) {
	a, b := ownerProjects(t)
	dir, err := state.Dir("work")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := `{"project_id": "work", "path": "/elsewhere/work/plat5.dev.yml", "started_at": "2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, forInit := range []bool{false, true} {
		if _, err := guardOwner("work", a, forInit); err != nil {
			t.Fatalf("no owner, init %v: %v", forInit, err)
		}
	}
	if st, _ := state.Load("work"); st.Owner != "" {
		t.Fatalf("the guard recorded %q", st.Owner)
	}

	// start's claim, from b.
	st, err := guardOwner("work", b, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := claimOwner(&st, b); err != nil {
		t.Fatal(err)
	}
	if st, _ := state.Load("work"); st.Owner != b {
		t.Fatalf("owner %q, want %q", st.Owner, b)
	}
	if _, err := guardOwner("work", b, false); err != nil {
		t.Errorf("the adopter: %v", err)
	}
	if _, err := guardOwner("work", a, false); err == nil || !strings.Contains(err.Error(), "belongs to the project in "+b) {
		t.Errorf("after adoption, a: %v", err)
	}
	// A recorded owner stays.
	if err := claimOwner(&st, a); err != nil || st.Owner != b {
		t.Errorf("claim over an owner: %v, owner %q", err, st.Owner)
	}
}

// An owner is recorded absolute, clean, and with symlinks resolved, also below a
// folder that doesn't exist yet.
func TestProjectFolder(t *testing.T) {
	tmp := t.TempDir()
	real, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(real, "link")); err != nil {
		t.Fatal(err)
	}
	if got := projectFolder(filepath.Join(tmp, "link", "x", "..", "new", "work")); got != filepath.Join(real, "new", "work") {
		t.Errorf("got %s", got)
	}
}

func mustLoadConfig(t *testing.T) config.Resolved {
	t.Helper()
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
