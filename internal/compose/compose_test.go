package compose

import (
	"strings"
	"testing"
)

func TestOverlayEnvRunnerWins(t *testing.T) {
	base := []string{"PLAT5_VERSION=from-shell", "KEEP=1", "PATH=/bin"}
	got := overlayEnv(base, []string{"PLAT5_VERSION=from-runner", "AUTH_ALLOWED_AUDIENCES=e10s"})
	if v := envValue(got, "PLAT5_VERSION"); v != "from-runner" {
		t.Fatalf("PLAT5_VERSION %q", v)
	}
	if v := envValue(got, "AUTH_ALLOWED_AUDIENCES"); v != "e10s" {
		t.Fatalf("audience %q", v)
	}
	if v := envValue(got, "KEEP"); v != "1" {
		t.Fatalf("KEEP %q", v)
	}
	if strings.Count(strings.Join(got, "\n"), "PLAT5_VERSION=") != 1 {
		t.Fatalf("duplicate pin:\n%s", strings.Join(got, "\n"))
	}
}

func TestCmdAppliesEnv(t *testing.T) {
	t.Setenv("PLAT5_VERSION", "from-shell")
	r := Runner{Dir: t.TempDir(), Env: []string{"PLAT5_VERSION=v0.5.0"}}
	c := r.cmd("restart", "identity")
	if v := envValue(c.Env, "PLAT5_VERSION"); v != "v0.5.0" {
		t.Fatalf("restart env PLAT5_VERSION %q", v)
	}
	if !strings.Contains(strings.Join(c.Args, " "), "restart identity") {
		t.Fatalf("args %v", c.Args)
	}
}

func TestCmdWithoutEnvInherits(t *testing.T) {
	r := Runner{Dir: t.TempDir()}
	c := r.cmd("ps")
	if c.Env != nil {
		t.Fatal("empty Env must leave the process environment alone")
	}
}

func envValue(env []string, key string) string {
	var found string
	var n int
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			found = strings.TrimPrefix(e, prefix)
			n++
		}
	}
	if n != 1 {
		return ""
	}
	return found
}
