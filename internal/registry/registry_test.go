package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plat5dev/cli/internal/plugin"
)

func TestVersionSelection(t *testing.T) {
	versions := []Version{
		{Version: "2.0.0", CLI: ">=0.25.0", Path: "v2"},
		{Version: "1.2.0", CLI: ">=0.18.0", Path: "v12"},
		{Version: "1.0.0", CLI: ">=0.17.0", Path: "v1"},
	}
	if v := selectCompatibleVersion(versions, "0.20.0"); v == nil || v.Version != "1.2.0" {
		t.Fatalf("0.20.0: got %+v, want 1.2.0", v)
	}
	if v := selectCompatibleVersion(versions, "0.25.0"); v == nil || v.Version != "2.0.0" {
		t.Fatalf("0.25.0: got %+v, want 2.0.0", v)
	}
	if v := selectCompatibleVersion(versions, "dev"); v == nil || v.Version != "2.0.0" {
		t.Fatalf("dev: got %+v, want 2.0.0", v)
	}
	if v := selectCompatibleVersion(versions, "0.16.0"); v != nil {
		t.Fatalf("0.16.0: got %+v, want nil", v)
	}
}

func TestValidateAndResolveSiblingRegistry(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	regDir := filepath.Join(root, "registry")
	if _, err := os.Stat(filepath.Join(regDir, IndexFileName)); err != nil {
		t.Skip("no sibling registry checkout")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	problems, err := ValidateDir(regDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) > 0 {
		t.Fatalf("registry validation problems:\n%s", strings.Join(problems, "\n"))
	}

	res := &Resolver{SourcesDir: root, CLIVersion: "0.20.0"}
	for _, tc := range []struct {
		ref      string
		wantType string
	}{
		{"auth0", plugin.TypeIdP},
		{"cerbos", plugin.TypeAuthorizer},
		{"go-fiber-api", plugin.TypeService},
		{"solid-customer-console", plugin.TypeCustomerConsole},
		{"grafana-cloud", plugin.TypeObservability},
		{"github-actions", plugin.TypeCI},
		{"b2b-saas", plugin.TypeRecipe},
	} {
		p, err := res.Resolve(tc.ref)
		if err != nil {
			t.Errorf("Resolve(%q): %v", tc.ref, err)
			continue
		}
		if p.Manifest.Type != tc.wantType {
			t.Errorf("Resolve(%q): type=%q, want %q", tc.ref, p.Manifest.Type, tc.wantType)
		}
		p.Close()
	}

	results, err := res.Search("cerbos", plugin.TypeAuthorizer)
	if err != nil || len(results) != 1 || results[0].Entry.ID != "cerbos" {
		t.Fatalf("Search: %+v %v", results, err)
	}
}

func TestFallbackIndexMatchesSiblingRegistry(t *testing.T) {
	if _, err := ParseIndex(fallbackRegistryYML); err != nil {
		t.Fatalf("embedded fallback_registry.yml invalid: %v", err)
	}
	sibling, err := os.ReadFile(filepath.Join("..", "..", "..", "registry", IndexFileName))
	if err != nil {
		t.Skip("no sibling registry checkout")
	}
	if string(sibling) != string(fallbackRegistryYML) {
		t.Fatalf("fallback_registry.yml is out of sync with ../registry/registry.yml (cp ../registry/registry.yml internal/registry/fallback_registry.yml)")
	}
}
