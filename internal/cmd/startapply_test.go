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
	"github.com/plat5dev/cli/internal/devseed"
)

// fakeControlPlane answers dry runs with plan, records applies, and has one
// gateway replica on the applied revision.
type fakeControlPlane struct {
	plan    map[string]any
	applies []map[string]any
}

func (f *fakeControlPlane) server(t *testing.T) *controlplane.Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"revision": 8, "gateway": []any{map[string]any{"replica": "gw-1", "loaded": 8}}})
			return
		}
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
		if err := writeInitFiles(dir, basePlat5YML(t)); err != nil {
			t.Fatal(err)
		}
	})
	return filepath.Join(dir, "plat5.yml")
}

func TestApplyForStart(t *testing.T) {
	path := initDir(t)
	for name, tc := range map[string]struct {
		plan    map[string]any
		applied bool
		out     string
		notOut  string
		err     string
	}{
		"no changes": {plan: map[string]any{"revision": 7, "summary": map[string]any{}, "changes": []any{}}},
		// Removing a kind with no nodes left applies without a prompt.
		"remove": {plan: map[string]any{
			"revision": 7,
			"summary":  map[string]any{"remove": 1},
			"changes":  []any{map[string]any{"op": "remove", "what": "kind", "name": "folder", "before": map[string]any{"parent": "organization"}}},
		}, applied: true, out: "  Applied revision 8.\n  gateway: 1/1 replicas on revision 8\n"},
		// The first apply adds the whole config: only its summary prints.
		"first apply": {plan: map[string]any{
			"revision": 0,
			"summary":  map[string]any{"add": 1},
			"changes":  []any{map[string]any{"op": "add", "what": "kind", "name": "folder", "after": map[string]any{"parent": "organization"}}},
		}, applied: true, out: "  First apply: 1 to add.\n  Applied revision 8.\n", notOut: "+ kind folder"},
		"blocked": {plan: map[string]any{
			"revision": 7,
			"summary":  map[string]any{"blocked": 1},
			"changes": []any{map[string]any{"op": "change", "what": "kind", "name": "project",
				"before": map[string]any{"parent": "organization"}, "after": map[string]any{"parent": "folder"},
				"blocked": map[string]any{"reason": "kind_not_empty", "in_the_way": map[string]any{"nodes": 2, "sample": []any{}}}}},
		}, out: "BLOCKED (kind_not_empty): 2 nodes in the way", err: "blocked"},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeControlPlane{plan: tc.plan}
			var out bytes.Buffer
			cfg, err := applyForStart(f.server(t), path, &out)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("want error containing %q, got %v", tc.err, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.out) {
				t.Errorf("want %q in:\n%s", tc.out, out.String())
			}
			if tc.notOut != "" && strings.Contains(out.String(), tc.notOut) {
				t.Errorf("want no %q in:\n%s", tc.notOut, out.String())
			}
			if got := len(f.applies) == 1; got != tc.applied {
				t.Fatalf("applied %v, want %v\n%s", got, tc.applied, out.String())
			}
			if tc.applied {
				body := f.applies[0]
				expect, _ := body["expect"].(map[string]any)
				// The config plus expect, nothing else.
				if expect["revision"] != float64(tc.plan["revision"].(int)) || len(body) != len(cfg.Flat)+1 || body["kinds"] == nil {
					t.Fatalf("apply body: %v", body)
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
		"organization": map[string]any{"path": "/orgs/{organization}"},
		"service_area": map[string]any{"path": "/areas/{service_area}"},
		"project":      map[string]any{"parent": "organization", "path": "/projects/{project}"},
	}}
	got := rootKinds(flat)
	if len(got) != 2 || got[0] != (devseed.RootKind{Kind: "organization", Collection: "orgs"}) ||
		got[1] != (devseed.RootKind{Kind: "service_area", Collection: "areas"}) {
		t.Fatalf("%+v", got)
	}
}
