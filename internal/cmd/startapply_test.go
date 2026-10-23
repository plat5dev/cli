package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plat5dev/cli/internal/controlplane"
)

// fakeControlPlane answers dry runs with plan, and records applies.
type fakeControlPlane struct {
	plan    map[string]any
	applies []map[string]any
}

func (f *fakeControlPlane) server(t *testing.T) *controlplane.Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apply" {
			w.WriteHeader(404)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("dry_run") == "true" {
			_ = json.NewEncoder(w).Encode(f.plan)
			return
		}
		f.applies = append(f.applies, body)
		_ = json.NewEncoder(w).Encode(map[string]any{"revision": 8, "summary": f.plan["summary"]})
	}))
	t.Cleanup(srv.Close)
	return controlplane.New(srv.URL, "t")
}

func initDir(t *testing.T) string {
	dir := t.TempDir()
	captureStdout(t, func() {
		if err := writeInitFiles(dir); err != nil {
			t.Fatal(err)
		}
	})
	return filepath.Join(dir, "plat5.yml")
}

func TestApplyForStart(t *testing.T) {
	path := initDir(t)
	safe := map[string]any{
		"revision": 7,
		"summary":  map[string]any{"add": 1},
		"changes":  []any{map[string]any{"class": "safe", "op": "add", "what": "role", "name": "viewer"}},
	}
	for name, tc := range map[string]struct {
		plan    map[string]any
		applied bool
		err     string
	}{
		"no changes": {plan: map[string]any{"revision": 7, "summary": map[string]any{}, "changes": []any{}}},
		"safe":       {plan: safe, applied: true},
		"destructive": {plan: map[string]any{
			"revision": 7,
			"summary":  map[string]any{"remove": 1},
			"changes":  []any{map[string]any{"class": "destructive", "op": "remove", "what": "kind", "name": "project", "effects": map[string]any{"nodes": 3}}},
			"confirm":  map[string]any{"project": map[string]any{"nodes": 3}},
		}, err: "Run plat5 apply"},
		"blocked": {plan: map[string]any{
			"revision": 7,
			"summary":  map[string]any{"blocked": 1},
			"changes":  []any{map[string]any{"class": "blocked", "op": "change", "what": "kind", "name": "project", "reason": "parent_narrowed", "in_the_way": map[string]any{"nodes": 2}}},
		}, err: "blocked"},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeControlPlane{plan: tc.plan}
			var out bytes.Buffer
			_, err := applyForStart(f.server(t), path, &out)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("want error containing %q, got %v", tc.err, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if got := len(f.applies) == 1; got != tc.applied {
				t.Fatalf("applied %v, want %v\n%s", got, tc.applied, out.String())
			}
			if tc.applied {
				body := f.applies[0]
				expect, _ := body["expect"].(map[string]any)
				if expect["revision"] != float64(7) || body["confirm"] != nil || body["kinds"] == nil {
					t.Fatalf("apply body: expect %v, confirm %v", expect, body["confirm"])
				}
			}
		})
	}
}

func TestApplyForStartChecksTheSchemaFirst(t *testing.T) {
	path := initDir(t)
	if err := os.WriteFile(path, []byte("kinds: {}\nroles: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeControlPlane{}
	if _, err := applyForStart(f.server(t), path, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "nothing sent") {
		t.Fatalf("want a schema refusal before any call, got %v", err)
	}
}

func TestURLHosts(t *testing.T) {
	flat := map[string]any{"services": map[string]any{
		"api":      map[string]any{"url": "http://api:3000"},
		"identity": map[string]any{"url": "http://identity:3000"},
		"worker":   map[string]any{"url": "http://api:3001"},
	}}
	if got := strings.Join(urlHosts(flat), ","); got != "api,identity" {
		t.Fatalf("%s", got)
	}
}

func TestRootKinds(t *testing.T) {
	flat := map[string]any{"kinds": map[string]any{
		"directory":    map[string]any{"sign_in": "idp"},
		"organization": map[string]any{"segment": "orgs"},
		"service_area": map[string]any{},
		"project":      map[string]any{"parent": "organization"},
	}}
	got := rootKinds(flat)
	if len(got) != 2 || got[0].Kind != "organization" || got[0].Segment != "orgs" || got[1].Segment != "service-areas" {
		t.Fatalf("%+v", got)
	}
}
