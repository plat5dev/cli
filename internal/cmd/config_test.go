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

	"github.com/plat5dev/cli/internal/controlplane"
)

func destructivePlan() *controlplane.Plan {
	return &controlplane.Plan{
		Revision: 41,
		Summary:  controlplane.Summary{Add: 2, Change: 1, Remove: 1},
		Changes: []controlplane.Change{
			{Class: "safe", Op: "add", What: "kind", Name: "environment"},
			{Class: "destructive", Op: "remove", What: "kind", Name: "folder",
				Effects: &controlplane.Effects{Nodes: 14, Bindings: 22, ServiceAccounts: 3, Keys: 9}},
			{Class: "safe", Op: "change", What: "role", Name: "admin"},
			{Class: "safe", Op: "change", What: "service", Name: "e10s", Routes: &controlplane.RouteCounts{Add: 1}},
		},
		Confirm: map[string]controlplane.Confirm{"folder": {Nodes: 14}},
	}
}

func TestPrintPlan(t *testing.T) {
	var b bytes.Buffer
	printPlan(&b, destructivePlan())
	out := b.String()
	for _, want := range []string{
		"Plan against revision 41:",
		"  + kind environment\n",
		"  - kind folder",
		"DESTRUCTIVE: soft-deletes 14 nodes (22 bindings, 3 service accounts, 9 keys)",
		"  ~ service e10s",
		"routes +1 ~0 -0",
		"Plan: 2 to add, 1 to change, 1 to remove.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
	b.Reset()
	printPlan(&b, &controlplane.Plan{Revision: 3})
	if !strings.HasPrefix(b.String(), "No changes.") {
		t.Errorf("got %s", b.String())
	}
}

func TestAutoApproveNeedsEveryDestructiveCount(t *testing.T) {
	p := destructivePlan()
	if _, err := confirmFromFlags(p, nil); err == nil || !strings.Contains(err.Error(), "--confirm folder=14") {
		t.Fatalf("got %v", err)
	}
	got, err := confirmFromFlags(p, []string{"folder=14"})
	if err != nil || got["folder"].Nodes != 14 {
		t.Fatalf("got %v %v", got, err)
	}
	// A count that no longer matches is sent as given: the control plane refuses it.
	if got, _ := confirmFromFlags(p, []string{"folder=13"}); got["folder"].Nodes != 13 {
		t.Fatalf("got %v", got)
	}
	if _, err := confirmFromFlags(p, []string{"folder"}); err == nil {
		t.Fatal("want an error for a bad --confirm")
	}
}

func TestConfirmInteractivelyAsksForTheCount(t *testing.T) {
	p := destructivePlan()
	var out bytes.Buffer
	got, err := confirmInteractively(strings.NewReader("14\nyes\n"), &out, p)
	if err != nil || !reflect.DeepEqual(got, p.Confirm) {
		t.Fatalf("got %v %v", got, err)
	}
	if !strings.Contains(out.String(), "Type 14 to confirm") {
		t.Errorf("prompt: %s", out.String())
	}
	if _, err := confirmInteractively(strings.NewReader("yes\nyes\n"), &out, p); err == nil {
		t.Fatal("yes is not the count")
	}
	if _, err := confirmInteractively(strings.NewReader("14\ny\n"), &out, p); err == nil {
		t.Fatal("only yes applies")
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
	if err := writeSavedPlan(path, flat, destructivePlan()); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"kinds":   map[string]any{},
		"expect":  map[string]any{"revision": 41.0},
		"confirm": map[string]any{"folder": map[string]any{"nodes": 14.0}},
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
			{"path":"services.e10s.routes[0].scope","message":"\"team\" is not a kind or public"},
			{"path":"default_role","message":"\"x\" is not a role"}]}}}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	plat5 := `kinds: { directory: { sign_in: idp }, organization: {} }
roles: { admin: { labels: ["*"], bindable_at: [organization] } }
default_role: admin
creator_role: admin
services: [routes/e10s.yml]
`
	e10s := "e10s:\n  url: http://e10s:3000\n  routes:\n    - { path: /h, scope: team, methods: [GET] }\n"
	_ = os.MkdirAll(filepath.Join(dir, "routes"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "plat5.yml"), []byte(plat5), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "routes/e10s.yml"), []byte(e10s), 0o644)

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
		"routes/e10s.yml: services.e10s.routes[0].scope:",
		"plat5.yml: default_role:",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("want %q in:\n%s", want, stderr)
		}
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
