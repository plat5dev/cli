package routes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var fixtures = filepath.Join("..", "..", "..", "plat5", "services", "control-plane", "fixtures")

// The control plane's fixtures: for each config with a gateway feed beside it,
// the expansion is the feed's services, routes and fields alike. Checked when
// plat5 is checked out beside the CLI, as in plat5dev.
func TestMatchesControlPlaneFeeds(t *testing.T) {
	if _, err := os.Stat(fixtures); err != nil {
		t.Skip("no plat5 checkout beside the CLI")
	}
	cases := map[string]string{"catalog": filepath.Join(fixtures, "catalog", "plat5.yml")}
	plans, _ := filepath.Glob(filepath.Join(fixtures, "plans", "*", "gateway-feed.json"))
	for _, feed := range plans {
		dir := filepath.Dir(feed)
		cases[filepath.Base(dir)] = filepath.Join(dir, "after.yml")
	}
	if len(plans) == 0 {
		t.Fatal("no plan fixtures with a gateway feed")
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Expand(readYAML(t, path))
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(filepath.Dir(path), "gateway-feed.json"))
			if err != nil {
				t.Fatal(err)
			}
			var feed struct {
				Services map[string]any `json:"services"`
			}
			if err := json.Unmarshal(data, &feed); err != nil {
				t.Fatal(err)
			}
			var ours map[string]any
			data, _ = json.Marshal(got)
			_ = json.Unmarshal(data, &ours)
			for svc := range feed.Services {
				if _, ok := ours[svc]; !ok {
					t.Errorf("no service %s", svc)
				}
			}
			for svc, want := range feed.Services {
				if !reflect.DeepEqual(ours[svc], want) {
					a, _ := json.MarshalIndent(ours[svc], "", " ")
					b, _ := json.MarshalIndent(want, "", " ")
					t.Errorf("%s differs:\ngot  %s\nwant %s", svc, a, b)
				}
			}
			if len(ours) != len(feed.Services) {
				t.Errorf("%d services, the feed %d", len(ours), len(feed.Services))
			}
		})
	}
}

func readYAML(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func expand(t *testing.T, doc string) map[string]Service {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal([]byte(doc), &m); err != nil {
		t.Fatal(err)
	}
	got, err := Expand(m)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func find(t *testing.T, s Service, method, path string) *Route {
	t.Helper()
	for i, r := range s.Routes {
		if r.Method == method && r.Path == path {
			return &s.Routes[i]
		}
	}
	return nil
}

const kinds = `
kinds:
  organization:
    path: /orgs/{organization}
    list_permission: organization:read
  project:
    parent: organization
    path: /projects/{project}
    list_permission: project:read
`

// Overrides: a verb merges field by field, route-level fields reach every verb,
// a kind's override comes after the shared one, false removes, and false then
// a kind's override brings a resource back.
func TestResourceOverrides(t *testing.T) {
	got := expand(t, kinds+`
rate_limits: { default: { requests: 1, window_seconds: 1 }, slow: { requests: 1, window_seconds: 9 } }
services:
  identity:
    url: http://identity:3000
    resources:
      bindings:
        rate_limit: [slow]
        methods:
          POST: { audit: false }
      membership:
        methods:
          DELETE: { required_permissions: ["{kind}:leave"] }
      roles: false
    kinds:
      project:
        resources:
          bindings:
            methods:
              GET: false
          roles: { path: /role-list }
          children: { path: /apps }
`)["identity"]

	post := find(t, got, "POST", "/orgs/{organization}/bindings")
	if post == nil || post.Audit || !reflect.DeepEqual(post.RateLimit, []string{"slow"}) || post.RequiredPermissions.String() != "organization:bindings:write" {
		t.Fatalf("org bindings POST: %+v", post)
	}
	if find(t, got, "GET", "/orgs/{organization}/projects/{project}/bindings") != nil {
		t.Fatal("project bindings GET kept")
	}
	if r := find(t, got, "GET", "/orgs/{organization}/bindings"); r == nil || !reflect.DeepEqual(r.RateLimit, []string{"slow"}) {
		t.Fatalf("org bindings GET: %+v", r)
	}
	if r := find(t, got, "DELETE", "/orgs/{organization}/membership"); r == nil || r.RequiredPermissions.String() != "organization:leave" {
		t.Fatalf("org membership DELETE: %+v", r)
	}
	if find(t, got, "DELETE", "/orgs/{organization}/projects/{project}/membership") != nil {
		t.Fatal("membership DELETE below a root")
	}
	if find(t, got, "GET", "/orgs/{organization}/roles") != nil {
		t.Fatal("org roles kept")
	}
	if r := find(t, got, "GET", "/orgs/{organization}/projects/{project}/role-list"); r == nil || r.Upstream != "/nodes/{project.id}/roles" {
		t.Fatalf("project roles: %+v", r)
	}
	if r := find(t, got, "POST", "/orgs/{organization}/apps"); r == nil || r.Upstream != "/nodes/{organization.id}/children/project" || r.RequiredPermissions.String() != "project:create" {
		t.Fatalf("children: %+v", r)
	}
}

func TestOverrideErrors(t *testing.T) {
	var m map[string]any
	_ = yaml.Unmarshal([]byte(kinds+`
services:
  identity:
    url: http://identity:3000
    kinds:
      folder: { resources: { node: false } }
      project:
        resources:
          members: false
          membership: { methods: { DELETE: false } }
`), &m)
	_, err := Expand(m)
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{
		`services.identity.kinds.folder: "folder" is not a kind`,
		"services.identity.kinds.project.resources.members: project has no members resource: it isn't a root kind",
		"services.identity.kinds.project.resources.membership.methods.DELETE: membership has no DELETE here",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
}

func TestServiceRoutes(t *testing.T) {
	got := expand(t, kinds+`
services:
  e10s:
    url: http://e10s:3000
    scopes:
      project:
        upstream_prefix: /projects/{project.id}
        routes:
          - path: /
            required_permissions: [project:read]
            methods: [GET]
          - path: /plans/{plan_id}
            required_permissions: any_member
            rate_limit: false
            methods:
              GET:
              PUT: { required_permissions: [catalog:write, project:write], audit: false }
      public:
        routes:
          - path: /health
            methods: [GET]
`)["e10s"]
	want := []Route{
		{Path: "/health", Method: "GET", Scope: "public", Upstream: "/health", RateLimit: []string{"default"}},
		{Path: "/orgs/{organization}/projects/{project}", Method: "GET", Scope: "project", Upstream: "/projects/{project.id}",
			RequiredPermissions: &Required{Permissions: []string{"project:read"}}, RateLimit: []string{"default"}},
		{Path: "/orgs/{organization}/projects/{project}/plans/{plan_id}", Method: "GET", Scope: "project", Upstream: "/projects/{project.id}/plans/{plan_id}",
			RequiredPermissions: &Required{AnyMember: true}, RateLimit: []string{}},
		{Path: "/orgs/{organization}/projects/{project}/plans/{plan_id}", Method: "PUT", Scope: "project", Upstream: "/projects/{project.id}/plans/{plan_id}",
			RequiredPermissions: &Required{Permissions: []string{"catalog:write", "project:write"}}, RateLimit: []string{}},
	}
	if !reflect.DeepEqual(got.Routes, want) {
		a, _ := json.MarshalIndent(got.Routes, "", " ")
		t.Fatalf("got %s", a)
	}
	if got.Timeouts != (Timeouts{"10s", "30s"}) || got.MaxBodyBytes != defaultMaxBodyBytes {
		t.Fatalf("defaults: %+v", got)
	}
}
