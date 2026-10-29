package schema

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sample() map[string]any {
	return map[string]any{
		"kinds": map[string]any{
			"organization": map[string]any{"path": "/orgs/{organization}", "list_permission": "organization:read"},
		},
		"roles": map[string]any{
			"admin": map[string]any{"permissions": []any{"*"}, "bindable_at": []any{"organization"}},
		},
		"creator_role": "admin",
		"rate_limits": map[string]any{
			"default": map[string]any{"requests": 60, "window_seconds": 60},
		},
		"services": map[string]any{
			"e10s": map[string]any{
				"url": "http://e10s:3000",
				"scopes": map[string]any{
					"public": map[string]any{"routes": []any{
						map[string]any{"path": "/h", "methods": []any{"GET"}},
						map[string]any{"path": "/x"},
					}},
				},
			},
		},
	}
}

func TestErrorsNameFieldPaths(t *testing.T) {
	errs, err := ValidateConfig(sample())
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 1 || errs[0].Path != "services.e10s.scopes.public.routes[1]" {
		t.Fatalf("got %v", errs)
	}
}

// The default policy is required.
func TestDefaultPolicy(t *testing.T) {
	c := sample()
	c["services"] = map[string]any{}
	c["rate_limits"] = map[string]any{}
	errs, err := ValidateConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 1 || errs[0].Path != "rate_limits" {
		t.Errorf("got %v", errs)
	}
}

func TestDurationsAndUnknownKeys(t *testing.T) {
	c := sample()
	c["services"] = map[string]any{}
	for _, d := range []string{"1s", "90m", "12h", "30d"} {
		c["deletion_grace"] = d
		if errs, _ := ValidateConfig(c); len(errs) != 0 {
			t.Fatalf("%s: %v", d, errs)
		}
	}
	c["deletion_grace"] = "1w"
	c["extra"] = true
	errs, _ := ValidateConfig(c)
	paths := map[string]bool{}
	for _, e := range errs {
		paths[e.Path] = true
	}
	if !paths["deletion_grace"] || !paths[""] {
		t.Fatalf("got %v", errs)
	}
}

// The copy tracks plat5's schema/. Checked when plat5 is checked out beside
// the CLI, as in plat5dev.
func TestMatchesPlat5(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "plat5", "schema")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no plat5 checkout beside the CLI")
	}
	ours, err := Files()
	if err != nil {
		t.Fatal(err)
	}
	theirs, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(theirs) != len(ours) {
		t.Fatalf("plat5 has %d schema files, the CLI %d: copy plat5/schema/*.json into internal/schema/files/", len(theirs), len(ours))
	}
	for _, path := range theirs {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, ours[filepath.Base(path)]) {
			t.Errorf("%s differs from plat5's: copy plat5/schema/*.json into internal/schema/files/", filepath.Base(path))
		}
	}
}

// users.name: each field on its own, and nothing else; name_claim is unknown.
func TestUsersName(t *testing.T) {
	c := sample()
	c["services"] = map[string]any{}
	for _, users := range []any{
		map[string]any{},
		map[string]any{"name": map[string]any{}},
		map[string]any{"name": map[string]any{"claims": []any{"profile.display_name"}}},
		map[string]any{"name": map[string]any{"claims": []any{"name", "preferred_username"}, "sync": "each_sign_in"}},
	} {
		c["users"] = users
		if errs, _ := ValidateConfig(c); len(errs) != 0 {
			t.Fatalf("%v: %v", users, errs)
		}
	}
	for _, users := range []any{
		map[string]any{"name_claim": "name"},
		map[string]any{"name": map[string]any{"sync": "never"}},
		map[string]any{"name": map[string]any{"claims": []any{}}},
		map[string]any{"name": map[string]any{"claims": []any{"name", "name"}}},
		map[string]any{"name": map[string]any{"claims": []any{"a..b"}}},
		map[string]any{"name": map[string]any{"claims": []any{"a", "b", "c", "d", "e", "f", "g", "h", "i"}}},
	} {
		c["users"] = users
		if errs, _ := ValidateConfig(c); len(errs) == 0 {
			t.Errorf("%v: accepted", users)
		}
	}
}

func TestRouteChecks(t *testing.T) {
	c := sample()
	c["services"] = map[string]any{}
	for _, rc := range []any{
		map[string]any{},
		map[string]any{"path": "/access/routes"},
		map[string]any{"path": "/v1/can_i.check"},
	} {
		c["route_checks"] = rc
		if errs, _ := ValidateConfig(c); len(errs) != 0 {
			t.Fatalf("%v: %v", rc, errs)
		}
	}
	for _, rc := range []any{
		"/access/routes",
		map[string]any{"path": "access/routes"},
		map[string]any{"path": "/Access"},
		map[string]any{"path": "/access/{id}"},
		map[string]any{"path": "/.well-known"},
		map[string]any{"path": "/access/"},
		map[string]any{"path": "/" + strings.Repeat("a", 256)},
		map[string]any{"path": "/access", "method": "POST"},
	} {
		c["route_checks"] = rc
		if errs, _ := ValidateConfig(c); len(errs) == 0 {
			t.Errorf("%v: accepted", rc)
		}
	}
}
