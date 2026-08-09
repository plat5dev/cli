package upstreams

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestExpand(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"3000", "host.docker.internal:3000"},
		{" 8080 ", "host.docker.internal:8080"},
		{"localhost:3000", "localhost:3000"},
		{"127.0.0.1:3000", "127.0.0.1:3000"},
		{"api.example.com:8443", "api.example.com:8443"},
		{"https://api.example.com/v1", "api.example.com:443"},
		{"http://host.docker.internal:3000", "host.docker.internal:3000"},
		{"http://api.example.com", "api.example.com:80"},
	}
	for _, tc := range cases {
		got, err := Expand(tc.in)
		if err != nil {
			t.Fatalf("Expand(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("Expand(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestExpandErrors(t *testing.T) {
	for _, in := range []string{"", "0", "70000", "not a url path/only", "api.example.com"} {
		if _, err := Expand(in); err == nil {
			t.Fatalf("Expand(%q) expected error", in)
		}
	}
}

func TestParseMap(t *testing.T) {
	raw := map[string]any{
		"api":     3000,
		"other":   "localhost:4000",
		"remote":  "https://api.example.com",
		"asfloat": float64(5000),
	}
	m, err := ParseMap(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m["api"] != "3000" || m["other"] != "localhost:4000" {
		t.Fatalf("%v", m)
	}
	if m["remote"] != "https://api.example.com" || m["asfloat"] != "5000" {
		t.Fatalf("%v", m)
	}
}

func TestBindInjectsURL(t *testing.T) {
	in := []byte(`
services:
  api:
    public:
      routes:
        - path: /api
          methods: [GET]
  other:
    url: keep.example:1
    user:
      routes:
        - path: /x
          methods: [GET]
`)
	out, err := Bind(in, map[string]string{"api": "3000"})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Services["api"]["url"] != "host.docker.internal:3000" {
		t.Fatalf("api url %v", doc.Services["api"]["url"])
	}
	if doc.Services["other"]["url"] != "keep.example:1" {
		t.Fatalf("other url changed: %v", doc.Services["other"]["url"])
	}
	// routes preserved
	pub := doc.Services["api"]["public"].(map[string]any)
	if pub == nil {
		t.Fatal("public missing")
	}
}

func TestBindEmptyNoop(t *testing.T) {
	in := []byte("services:\n  a:\n    url: http://x\n")
	out, err := Bind(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Fatal("expected same bytes")
	}
}

func TestBindNoMatchReturnsOriginal(t *testing.T) {
	in := []byte("services:\n  a:\n    url: http://x\n")
	out, err := Bind(in, map[string]string{"missing": "3000"})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Fatalf("got %s", out)
	}
}

func TestBindOverwritesFileURL(t *testing.T) {
	in := []byte("services:\n  api:\n    url: http://old:1\n    public:\n      routes: []\n")
	out, err := Bind(in, map[string]string{"api": "https://new.example"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "new.example:443") {
		t.Fatalf("%s", out)
	}
	if strings.Contains(string(out), "http://old:1") {
		t.Fatalf("old url remains: %s", out)
	}
}
