package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/plat5dev/cli/internal/controlplane"
)

// The control plane's plan fixtures: real dry-run answers.
func fixturePlan(t *testing.T, name string) *controlplane.Plan {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(planFixtures, name, "plan.json"))
	if os.IsNotExist(err) {
		t.Skip("no plat5 checkout beside the CLI")
	}
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p controlplane.Plan
	if err := dec.Decode(&p); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return &p
}

var planFixtures = filepath.Join("..", "..", "..", "plat5", "services", "control-plane", "fixtures", "plans")

// Every fixture plan decodes with no unknown field, and its summary counts its
// lines: a blocked line only as blocked.
func TestFixturePlansDecode(t *testing.T) {
	entries, err := os.ReadDir(planFixtures)
	if os.IsNotExist(err) {
		t.Skip("no plat5 checkout beside the CLI")
	}
	n := 0
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(planFixtures, e.Name(), "plan.json")); err != nil {
			continue
		}
		n++
		p := fixturePlan(t, e.Name())
		var got controlplane.Summary
		for _, c := range p.Changes {
			switch {
			case c.Blocked != nil:
				got.Blocked++
			case c.Op == "add":
				got.Add++
			case c.Op == "change":
				got.Change++
			case c.Op == "remove":
				got.Remove++
			}
		}
		if got != p.Summary {
			t.Errorf("%s: summary %+v, lines count %+v", e.Name(), p.Summary, got)
		}
	}
	if n == 0 {
		t.Fatal("no plan fixtures")
	}
}

func TestPrintPlan(t *testing.T) {
	for name, wants := range map[string][]string{
		"remove-kind-blocked": {
			"Plan against revision 7:\n\n  - kind folder\n",
			"      BLOCKED (kind_not_empty): 5 nodes in the way, e.g. 01jb8z3m5q0000000000000001, 01jb8z3m5q0000000000000002, 01jb8z3m5q0000000000000003, 01jb8z3m5q0000000000000004, 01jb8z3m5q0000000000000005\n",
			"      - parent: \"organization\"\n      - path: \"/folders/{folder}\"\n",
			"  ~ role admin\n      ~ bindable_at: [\"organization\", \"project\", \"folder\"] → [\"organization\", \"project\"]\n",
			"  ~ service e10s\n      - GET /orgs/{organization}/folders/{folder}/plans\n          - scope: \"folder\"\n",
			"          - required_permissions: [\"folder:read\"]\n          - rate_limit: [\"default\"]\n          - audit: false\n",
			"\nPlan: 0 to add, 2 to change, 0 to remove. 1 blocked: nothing can apply until it's cleared.\n",
		},
		"parent-change-blocked": {
			"  ~ kind environment\n      ~ parent: \"organization\" → \"project\"\n      BLOCKED (kind_not_empty): 1 node in the way",
			"      + GET /orgs/{organization}/projects/{project}/envs/{environment}/flags\n          + scope: \"environment\"\n",
		},
		"settings-and-cors": {
			"  ~ setting deletion_grace: \"30d\" → \"7d\"\n",
			"  ~ setting reserved_slugs: [] → [\"new\", \"settings\"]\n",
			"  ~ setting defaults\n      ~ timeouts: {connect: \"10s\", read: \"30s\"} → {connect: \"5s\", read: \"20s\"}\n      ~ max_body_bytes: 10485760 → 2097152\n",
			"  + rate_limit evals-org\n      + requests: 300\n      + window_seconds: 1\n      + count_at: \"organization\"\n",
			"  ~ rate_limit default\n      ~ requests: 60 → 120\n",
			"      ~ GET /orgs/{organization}/plans\n          ~ rate_limit: [\"default\"] → [\"evals-org\"]\n",
		},
		"services": {
			"  - service billing\n      - url: \"http://billing:3000\"\n",
			"  ~ service e10s\n      ~ url: \"http://e10s:3000\" → \"http://e10s:4000\"\n",
			"          + required_permissions: null\n",
			"      ~ GET /orgs/{organization}/projects/{project}/plans\n          ~ required_permissions: [\"project:read\"] → [\"project:read\", \"catalog:read\"]\n",
			"Plan: 1 to add, 1 to change, 1 to remove.\n",
		},
		"no-change": {"No changes. The deployment at revision"},
	} {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			printPlan(&b, fixturePlan(t, name))
			out := b.String()
			for _, want := range wants {
				if !strings.Contains(out, want) {
					t.Errorf("want %q in:\n%s", want, out)
				}
			}
			if name != "remove-kind-blocked" && name != "parent-change-blocked" && strings.Contains(out, "blocked") {
				t.Errorf("no blocked line, yet:\n%s", out)
			}
		})
	}
}

func TestConfirmedTakesOnlyYes(t *testing.T) {
	for in, want := range map[string]bool{"yes\n": true, " yes \n": true, "y\n": false, "Yes\n": false, "": false} {
		if got := confirmed(strings.NewReader(in), &bytes.Buffer{}); got != want {
			t.Errorf("%q: got %v", in, got)
		}
	}
}

// statusServer answers GET /status with each of reports in turn, then the
// last one.
func statusServer(t *testing.T, reports ...[]controlplane.GatewayReplica) *controlplane.Client {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			w.WriteHeader(404)
			return
		}
		i := min(calls, len(reports)-1)
		calls++
		_ = json.NewEncoder(w).Encode(controlplane.Status{Revision: 42, Gateway: reports[i]})
	}))
	t.Cleanup(srv.Close)
	return controlplane.New(srv.URL, "t")
}

func TestReportGateway(t *testing.T) {
	wait, poll := gatewayWait, gatewayPoll
	gatewayWait, gatewayPoll = 200*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { gatewayWait, gatewayPoll = wait, poll })

	on := func(id string, rev int64) controlplane.GatewayReplica {
		return controlplane.GatewayReplica{Replica: id, Loaded: rev}
	}
	refused := controlplane.GatewayReplica{Replica: "gw-2", Loaded: 41,
		Refused: &controlplane.Refusal{Revision: 42, Reason: "route /orgs/{organization}/files: bad"}}
	for name, tc := range map[string]struct {
		reports [][]controlplane.GatewayReplica
		out     []string
		err     string
	}{
		"waits for every replica": {
			reports: [][]controlplane.GatewayReplica{
				{on("gw-1", 41), on("gw-2", 41), on("gw-3", 41)},
				{on("gw-1", 42), on("gw-2", 41), on("gw-3", 42)},
				{on("gw-1", 42), on("gw-2", 42), on("gw-3", 42)},
			},
			out: []string{"gateway: 3/3 replicas on revision 42\n"},
		},
		"a refusal": {
			reports: [][]controlplane.GatewayReplica{{on("gw-1", 42), refused}},
			out:     []string{"gateway: 1/2 replicas on revision 42\n", "  gw-2 refused revision 42: route /orgs/{organization}/files: bad\n"},
			err:     "1 of 2 gateway replicas refused it",
		},
		"a replica still behind": {
			reports: [][]controlplane.GatewayReplica{{on("gw-1", 42), on("gw-2", 41)}},
			out:     []string{"gateway: 1/2 replicas on revision 42\n", "  gw-2 is still on revision 41\n"},
		},
		"no replicas": {
			reports: [][]controlplane.GatewayReplica{{}},
			out:     []string{"gateway: no replica has reported in the last minute\n"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			err := reportGateway(statusServer(t, tc.reports...), 42, &b, "")
			if tc.err == "" && err != nil || tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)) {
				t.Fatalf("want error %q, got %v", tc.err, err)
			}
			for _, want := range tc.out {
				if !strings.Contains(b.String(), want) {
					t.Errorf("want %q in:\n%s", want, b.String())
				}
			}
		})
	}
}

func TestNormalizeArgsTakesTerraformsOut(t *testing.T) {
	got := normalizeArgs([]string{"plan", "-out", "p.json", "-out=q.json", "--detailed-exitcode"})
	want := []string{"plan", "--out", "p.json", "--out=q.json", "--detailed-exitcode"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

// A saved plan is exactly the apply body.
func TestSavedPlanIsTheApplyBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	flat := map[string]any{"kinds": map[string]any{}}
	if err := writeSavedPlan(path, flat, &controlplane.Plan{Revision: 41}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"kinds":  map[string]any{},
		"expect": map[string]any{"revision": 41.0},
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("got %v", body)
	}
}

// Field errors from the control plane name their file.
func TestExplainNamesTheFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":{"code":"VALIDATION_ERROR","message":"x","details":{"fields":[
			{"path":"services.e10s.scopes.team","message":"\"team\" is not a kind, user or public"},
			{"path":"creator_role","message":"\"x\" is not a role"}]}}}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	plat5 := `kinds: { organization: { path: "/orgs/{organization}", list_permission: organization:read } }
roles: { admin: { permissions: ["*"], bindable_at: [organization] } }
creator_role: admin
rate_limits: { default: { requests: 60, window_seconds: 60 } }
services: [services/e10s.yml]
`
	e10s := "e10s:\n  url: http://e10s:3000\n  scopes:\n    team:\n      routes:\n        - { path: /h, methods: [GET] }\n"
	_ = os.MkdirAll(filepath.Join(dir, "services"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "plat5.yml"), []byte(plat5), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "services/e10s.yml"), []byte(e10s), 0o644)

	cfg, err := loadValid(filepath.Join(dir, "plat5.yml"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = controlplane.New(srv.URL, "t").DryRun(cfg.Flat)
	stderr := captureStderr(t, func() { err = explain(cfg, err) })
	if err == nil || !strings.Contains(err.Error(), "VALIDATION_ERROR") {
		t.Fatalf("got %v", err)
	}
	for _, want := range []string{
		"services/e10s.yml: services.e10s.scopes.team:",
		"plat5.yml: creator_role:",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("want %q in:\n%s", want, stderr)
		}
	}
}

// config pull writes plat5.yml and one service file per service, and won't
// write over a config.
func TestConfigPullWritesServiceFiles(t *testing.T) {
	flat := map[string]any{
		"kinds":        map[string]any{"organization": map[string]any{"path": "/orgs/{organization}", "list_permission": "organization:read"}},
		"roles":        map[string]any{"admin": map[string]any{"permissions": []any{"*"}, "bindable_at": []any{"organization"}}},
		"creator_role": "admin",
		"rate_limits":  map[string]any{"default": map[string]any{"requests": 60, "window_seconds": 60}},
		"services": map[string]any{
			"identity": map[string]any{"url": "http://identity:3000"},
			"audit":    map[string]any{"url": "http://audit:3002"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"revision": 3, "created_at": "now", "config": flat})
	}))
	defer srv.Close()
	flagControlPlaneURL = srv.URL
	defer func() { flagControlPlaneURL = "" }()

	dir := t.TempDir()
	captureStdout(t, func() {
		if err := runConfigPull(configPullCmd, []string{dir}); err != nil {
			t.Fatal(err)
		}
	})
	cfg, err := loadValid(filepath.Join(dir, "plat5.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Files["identity"] != "services/identity.yml" || cfg.Files["audit"] != "services/audit.yml" {
		t.Fatalf("files: %v", cfg.Files)
	}
	if !reflect.DeepEqual(cfg.Flat, flat) {
		t.Fatalf("pulled %v", cfg.Flat)
	}

	again := t.TempDir()
	_ = os.MkdirAll(filepath.Join(again, "services"), 0o755)
	_ = os.WriteFile(filepath.Join(again, "services", "e10s.yml"), []byte("e10s: {}\n"), 0o644)
	if err := runConfigPull(configPullCmd, []string{again}); err == nil || !strings.Contains(err.Error(), "services/e10s.yml") {
		t.Fatalf("want a refusal naming services/e10s.yml, got %v", err)
	}
}

func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	f()
	os.Stderr = old
	w.Close()
	var b bytes.Buffer
	_, _ = b.ReadFrom(r)
	return b.String()
}
