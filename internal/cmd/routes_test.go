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

func TestPrintRoutes(t *testing.T) {
	var buf bytes.Buffer
	printRoutes(&buf, map[string]controlplane.Service{
		"identity": {URL: "http://identity:3000", Routes: []controlplane.Route{
			{Method: "POST", Path: "/orgs", Scope: "user", Upstream: "/roots/organization"},
			{Method: "GET", Path: "/orgs/{organization}/roles", Scope: "organization", Upstream: "/nodes/{organization.id}/roles",
				RequiredPermissions: &controlplane.Required{AnyMember: true}},
		}},
		"e10s": {URL: "http://e10s:3000", Routes: []controlplane.Route{
			{Method: "DELETE", Path: "/orgs/{organization}/plans", Scope: "organization", Upstream: "/plans",
				RequiredPermissions: &controlplane.Required{Permissions: []string{"catalog:write", "organization:write"}}},
		}},
	})
	want := "e10s  http://e10s:3000\n" +
		"  DELETE  /orgs/{organization}/plans  organization  /plans                          catalog:write, organization:write\n" +
		"\n" +
		"identity  http://identity:3000\n" +
		"  POST    /orgs                       user          /roots/organization\n" +
		"  GET     /orgs/{organization}/roles  organization  /nodes/{organization.id}/roles  any_member\n"
	if buf.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

// routesConfig writes a valid plat5.yml with one service and returns its path.
func routesConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	plat5 := `kinds: { organization: { path: "/orgs/{organization}", list_permission: organization:read } }
roles: { admin: { permissions: ["*"], bindable_at: [organization] } }
creator_role: admin
rate_limits: { default: { requests: 60, window_seconds: 60 } }
services: [services/e10s.yml]
`
	e10s := "e10s:\n  url: http://e10s:3000\n  scopes:\n    public:\n      routes:\n        - { path: /health, methods: [GET] }\n"
	_ = os.MkdirAll(filepath.Join(dir, "services"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "plat5.yml"), []byte(plat5), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "services/e10s.yml"), []byte(e10s), 0o644)
	return filepath.Join(dir, "plat5.yml")
}

// dryRunServer answers POST /apply?dry_run=true with status and body, and
// keeps the config it was sent. --control-plane-url points at it.
func dryRunServer(t *testing.T, status int, body string) *map[string]any {
	t.Helper()
	sent := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/apply" || r.URL.Query().Get("dry_run") != "true" {
			w.WriteHeader(404)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	flagControlPlaneURL = srv.URL
	t.Cleanup(func() { flagControlPlaneURL = "" })
	return &sent
}

// routes list sends the config as a dry run and prints the services it answers.
func TestRoutesListPrintsTheDryRunsRoutes(t *testing.T) {
	sent := dryRunServer(t, http.StatusOK, `{"revision": 3, "summary": {"add": 0, "change": 0, "remove": 0, "blocked": 0}, "changes": [],
		"services": {
			"e10s": {"url": "http://e10s:3000", "timeouts": {"connect": "10s", "read": "30s"}, "max_body_bytes": 10485760, "routes": [
				{"path": "/health", "method": "GET", "scope": "public", "upstream": "/health", "required_permissions": null, "rate_limit": ["default"], "audit": false, "allow_deleted": false}
			]},
			"identity": {"url": "http://identity:3000", "timeouts": {"connect": "10s", "read": "30s"}, "max_body_bytes": 10485760, "routes": [
				{"path": "/orgs/{organization}/members", "method": "GET", "scope": "organization", "upstream": "/nodes/{organization.id}/members", "required_permissions": "any_member", "rate_limit": ["default"], "audit": false, "allow_deleted": false}
			]}
		}}`)
	path := routesConfig(t)
	out := captureStdout(t, func() {
		if err := runRoutesList(routesListCmd, []string{path}); err != nil {
			t.Fatal(err)
		}
	})
	want := "e10s  http://e10s:3000\n" +
		"  GET  /health                       public        /health\n" +
		"\n" +
		"identity  http://identity:3000\n" +
		"  GET  /orgs/{organization}/members  organization  /nodes/{organization.id}/members  any_member\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	if _, ok := (*sent)["services"].(map[string]any)["e10s"]; !ok {
		t.Fatalf("sent %v", *sent)
	}
}

// A config the control plane refuses prints its errors as plan does.
func TestRoutesListPrintsARefusal(t *testing.T) {
	dryRunServer(t, http.StatusUnprocessableEntity, `{"error":{"code":"VALIDATION_ERROR","message":"x","details":{"fields":[
		{"path":"services.e10s.scopes.public.routes[0].path","message":"two routes of one shape"}]}}}`)
	path := routesConfig(t)
	var err error
	stderr := captureStderr(t, func() { err = runRoutesList(routesListCmd, []string{path}) })
	if err == nil || !strings.Contains(err.Error(), "VALIDATION_ERROR") {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(stderr, "services/e10s.yml: services.e10s.scopes.public.routes[0].path:") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

// The control plane's fixture feeds decode as the dry run's services, no field
// unknown: they're the same shape.
func TestFixtureFeedServicesDecode(t *testing.T) {
	feeds, _ := filepath.Glob(filepath.Join(planFixtures, "*", "gateway-feed.json"))
	if len(feeds) == 0 {
		t.Skip("no plat5 checkout beside the CLI")
	}
	for _, f := range feeds {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var feed struct {
			Services json.RawMessage `json:"services"`
		}
		if err := json.Unmarshal(data, &feed); err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(bytes.NewReader(feed.Services))
		dec.DisallowUnknownFields()
		var services map[string]controlplane.Service
		if err := dec.Decode(&services); err != nil {
			t.Errorf("%s: %v", f, err)
		}
		if len(services) == 0 {
			t.Errorf("%s: no services", f)
		}
	}
}
