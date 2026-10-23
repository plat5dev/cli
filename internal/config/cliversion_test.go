package config

import (
	"strings"
	"testing"
)

func withCLI(t *testing.T, v string) {
	t.Helper()
	old := CLIVersion
	CLIVersion = v
	t.Cleanup(func() { CLIVersion = old })
}

func TestCLIRequirement(t *testing.T) {
	const yml = "cli: \">=0.7.0\"\nproject_id: demo\n"
	for _, c := range []struct {
		version string
		err     string // empty: parses
	}{
		{"0.6.2", "this project needs plat5 0.7.0 or newer (plat5.dev.yml cli: >=0.7.0), and this is plat5 0.6.2. Upgrade: brew upgrade plat5"},
		{"v0.6.9", "needs plat5 0.7.0 or newer"},
		{"0.7.0", ""},
		{"1.0.0", ""},
		{"dev", ""},
	} {
		withCLI(t, c.version)
		_, err := Parse([]byte(yml))
		switch {
		case c.err == "" && err != nil:
			t.Errorf("%s: %v", c.version, err)
		case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
			t.Errorf("%s: got %v, want %q", c.version, err, c.err)
		}
	}
}

// Too old a CLI says so before it trips on a key it doesn't know.
func TestCLIRequirementBeforeUnknownKeys(t *testing.T) {
	withCLI(t, "0.6.2")
	_, err := Parse([]byte("cli: \">=0.9.0\"\nproject_id: demo\nsomething_new: true\n"))
	if err == nil || !strings.Contains(err.Error(), "needs plat5 0.9.0 or newer") {
		t.Fatalf("got %v", err)
	}
}

func TestUnknownKeyHint(t *testing.T) {
	withCLI(t, "0.7.0")
	_, err := Parse([]byte("project_id: demo\nsomething_new: true\n"))
	if err == nil || !strings.Contains(err.Error(), `unknown key "something_new" (this is plat5 0.7.0; a newer one may know it: brew upgrade plat5`) {
		t.Fatalf("got %v", err)
	}
}

func TestCLIRequirementMalformed(t *testing.T) {
	withCLI(t, "0.7.0")
	if _, err := Parse([]byte("cli: \"0.7\"\nproject_id: demo\n")); err == nil || !strings.Contains(err.Error(), `want ">=X.Y.Z"`) {
		t.Fatalf("got %v", err)
	}
}
