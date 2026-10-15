package stack

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteThemeStaffPlane(t *testing.T) {
	path := filepath.Join(t.TempDir(), "staff-theme.json")
	if err := writeTheme(path, "acme staff", staffPlane); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var theme struct {
		Title      string            `json:"title"`
		Logo       map[string]string `json:"logo"`
		Background map[string]string `json:"background"`
		Primary    map[string]string `json:"primary"`
		CSS        string            `json:"css"`
	}
	if err := json.Unmarshal(raw, &theme); err != nil {
		t.Fatal(err)
	}
	if theme.Title != "acme staff" {
		t.Errorf("title = %q", theme.Title)
	}
	if theme.Background["light"] != paper || theme.Background["dark"] != ink {
		t.Errorf("background = %v", theme.Background)
	}
	if theme.Primary["light"] != staffPlane.light || theme.Primary["dark"] != staffPlane.dark {
		t.Errorf("primary = %v", theme.Primary)
	}
	// The dark logo is paper letters with the staff bar.
	svg, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(theme.Logo["dark"], "data:image/svg+xml;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(svg), `fill="`+paper+`"`) || !strings.Contains(string(svg), `fill="`+staffPlane.dark+`"`) {
		t.Errorf("dark logo = %s", svg)
	}
	for _, want := range []string{`content: "operator"`, "staff@example.com", "background: var(--color-primary)"} {
		if !strings.Contains(theme.CSS, want) {
			t.Errorf("css lacks %q", want)
		}
	}
}
