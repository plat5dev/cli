package cmd

import (
	"os"
	"strings"
	"testing"
)

// Without a terminal to ask, reset deletes nothing unless -y says so, and says
// before it touches Docker.
func TestResetNeedsYesWithoutATerminal(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	yml := "project_id: demo\nversions: {plat5: v0.9.0, auth: v0.2.2, operator: v0.5.0}\nmodules: {operator: false}\n"
	if err := os.WriteFile(dir+"/plat5.dev.yml", []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	resetYes = false
	err := runReset(resetCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "pass -y") {
		t.Fatalf("err = %v", err)
	}
}
