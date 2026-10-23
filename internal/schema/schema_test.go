package schema

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func sample() map[string]any {
	return map[string]any{
		"kinds": map[string]any{
			"directory":    map[string]any{"sign_in": "idp"},
			"organization": map[string]any{"segment": "orgs"},
		},
		"roles": map[string]any{
			"admin": map[string]any{"labels": []any{"*"}, "bindable_at": []any{"organization"}},
		},
		"default_role": "admin",
		"creator_role": "admin",
		"services": map[string]any{
			"e10s": map[string]any{
				"url": "http://e10s:3000",
				"routes": []any{
					map[string]any{"path": "/h", "scope": "public", "methods": []any{"GET"}},
					map[string]any{"path": "/x", "methods": []any{"GET"}},
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
	if len(errs) != 1 || errs[0].Path != "services.e10s.routes[1]" {
		t.Fatalf("got %v", errs)
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
