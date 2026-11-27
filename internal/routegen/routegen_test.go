package routegen

import (
	"reflect"
	"strings"
	"testing"
)

func TestPrefix(t *testing.T) {
	for _, tc := range []struct{ name, list, sep, want string }{
		{"project", "project:read", ":", "project"},
		{"environment", "project:read", ":", "project"},
		{"environment", "env:list", ":", "environment"},
		{"environment", ":read", ":", "environment"},
		{"project", "project_read", "_", "project"},
		{"environment", "project_read", "_", "project"},
		{"environment", "project.read", ".", "project"},
	} {
		if got := (Kind{Name: tc.name, ListPermission: tc.list}).Prefix(tc.sep); got != tc.want {
			t.Errorf("%s %s (%s): got %s", tc.name, tc.list, tc.sep, got)
		}
	}
}

func TestReadKinds(t *testing.T) {
	kinds, err := ReadKinds([]byte(`kinds:
  team: { path: "/teams/{team}", list_permission: team:read }
  board: { parent: team, path: "/boards/{board}", list_permission: team:read }
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Kind{
		{Name: "team", Path: "/teams/{team}", ListPermission: "team:read"},
		{Name: "board", Parent: "team", Path: "/boards/{board}", ListPermission: "team:read"},
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("got %+v", kinds)
	}
	for in, msg := range map[string]string{
		"roles: {}": "kinds:",
		`kinds: { team: { path: "/teams/{team}" } }`:                                          "kinds.team.list_permission",
		`kinds: { team: { path: "/teams/{board}", list_permission: team:read } }`:             "kinds.team.path",
		`kinds: { team: { path: "/teams/{team}", list_permission: team:read, parent: org } }`: "kinds.team.parent",
	} {
		if _, err := ReadKinds([]byte(in)); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: got %v", in, err)
		}
	}

	// Overlay merging: add a kind, modify an existing one, delete another with null.
	merged, err := ReadKindsMany(
		[]byte("kinds:\n  team: { path: \"/teams/{team}\", list_permission: team:read }\n  old: { parent: team, path: \"/olds/{old}\", list_permission: old:read }\n"),
		[]byte("kinds:\n  old: null\n  board: { parent: team, path: \"/boards/{board}\", list_permission: team:read }\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("merged kinds: got %+v, want %+v", merged, want)
	}
}

func find(t *testing.T, f File, scope, path string) Route {
	t.Helper()
	for _, s := range f.Scopes {
		if s.Name != scope {
			continue
		}
		for _, r := range s.Routes {
			if r.Path == path {
				return r
			}
		}
	}
	t.Fatalf("%s: no %s %s", f.Service, scope, path)
	return Route{}
}

func TestGenerate(t *testing.T) {
	identity, audit := Generate([]Kind{
		{Name: "team", Path: "/teams/{team}", ListPermission: "team:read"},
		{Name: "board", Parent: "team", Path: "/boards/{board}", ListPermission: "team:read"},
	}, Options{PermissionSeparator: ":"})
	var scopes []string
	for _, s := range identity.Scopes {
		scopes = append(scopes, s.Name)
	}
	if strings.Join(scopes, " ") != "team board user" {
		t.Fatalf("scopes %v", scopes)
	}
	// The child's collection is in the parent's scope, with the child's prefix.
	if r := find(t, identity, "team", "/boards"); r.Upstream != "/nodes/{team.id}/children/board" ||
		!reflect.DeepEqual(r.Methods, []Method{{"GET", []string{AnyMember}}, {"POST", []string{"team:create"}}}) {
		t.Errorf("/boards: %+v", r)
	}
	// Membership: one upstream; DELETE on a root only.
	if r := find(t, identity, "team", "/membership"); r.Upstream != "/nodes/{team.id}/memberships/{member.id}" || len(r.Methods) != 2 {
		t.Errorf("team /membership: %+v", r)
	}
	if r := find(t, identity, "board", "/membership"); len(r.Methods) != 1 {
		t.Errorf("board /membership: %+v", r)
	}
	// No POST on a root's /members.
	if r := find(t, identity, "team", "/members"); !reflect.DeepEqual(r.Methods, []Method{{"GET", []string{"team:bindings:read"}}}) {
		t.Errorf("/members: %+v", r)
	}
	if r := find(t, identity, "team", "/members/{member_id}/status"); !reflect.DeepEqual(r.Methods, []Method{{"PATCH", []string{"team:bindings:write"}}}) {
		t.Errorf("/members/{member_id}/status: %+v", r)
	}
	if r := find(t, identity, "team", "/service-accounts/{service_account_id}/keys/{key_id}"); !reflect.DeepEqual(r.Methods, []Method{
		{"PATCH", []string{"team:bindings:write"}}, {"DELETE", []string{"team:bindings:write"}},
	}) {
		t.Errorf("/service-accounts/{service_account_id}/keys/{key_id}: %+v", r)
	}
	if r := find(t, identity, "user", "/user/keys/{key_id}"); !reflect.DeepEqual(r.Methods, []Method{{Verb: "PATCH"}, {Verb: "DELETE"}}) {
		t.Errorf("/user/keys/{key_id}: %+v", r)
	}
	// Root custom roles, permissions catalog, and security policy routes.
	if r := find(t, identity, "team", "/roles"); !reflect.DeepEqual(r.Methods, []Method{
		{"GET", []string{AnyMember}}, {"POST", []string{"team:bindings:write"}},
	}) {
		t.Errorf("team /roles: %+v", r)
	}
	if r := find(t, identity, "board", "/roles"); !reflect.DeepEqual(r.Methods, []Method{
		{"GET", []string{AnyMember}},
	}) {
		t.Errorf("board /roles: %+v", r)
	}
	if r := find(t, identity, "team", "/roles/{role_id}"); !reflect.DeepEqual(r.Methods, []Method{
		{"GET", []string{AnyMember}}, {"PATCH", []string{"team:bindings:write"}}, {"DELETE", []string{"team:bindings:write"}},
	}) {
		t.Errorf("team /roles/{role_id}: %+v", r)
	}
	if r := find(t, identity, "team", "/permissions"); !reflect.DeepEqual(r.Methods, []Method{
		{"GET", []string{"team:bindings:read"}},
	}) {
		t.Errorf("team /permissions: %+v", r)
	}
	if r := find(t, identity, "team", "/security-policy"); !reflect.DeepEqual(r.Methods, []Method{
		{"GET", []string{"team:bindings:read"}}, {"PATCH", []string{"team:bindings:write"}},
	}) {
		t.Errorf("team /security-policy: %+v", r)
	}
	// Root groups routes.
	if r := find(t, identity, "team", "/groups"); !reflect.DeepEqual(r.Methods, []Method{
		{"GET", []string{"team:bindings:read"}}, {"POST", []string{"team:bindings:write"}},
	}) {
		t.Errorf("/groups: %+v", r)
	}
	if r := find(t, identity, "team", "/groups/{group_id}/members/{member_id}"); !reflect.DeepEqual(r.Methods, []Method{
		{"DELETE", []string{"team:bindings:write"}},
	}) {
		t.Errorf("/groups/{group_id}/members/{member_id}: %+v", r)
	}
	if r := find(t, identity, "board", "/restore"); !r.AllowDeleted || r.Methods[0].Required[0] != "team:delete" {
		t.Errorf("board /restore: %+v", r)
	}
	if r := find(t, identity, "board", "/invites"); r.Methods[0].Required[0] != "guests:write" {
		t.Errorf("board /invites: %+v", r)
	}
	if r := find(t, identity, "user", "/teams"); r.Upstream != "/roots/team" || r.Methods[0].Required != nil {
		t.Errorf("user /teams: %+v", r)
	}
	if r := find(t, audit, "user", "/user/audit-events"); r.Upstream != "/users/{user.id}/audit-events" {
		t.Errorf("audit user: %+v", r)
	}
	if r := find(t, audit, "board", "/audit-events"); r.Methods[0].Required[0] != "audit:read" {
		t.Errorf("audit board: %+v", r)
	}
}

func TestGenerateWithUnderscoreSeparator(t *testing.T) {
	identity, audit := Generate([]Kind{
		{Name: "organization", Path: "/orgs/{organization}", ListPermission: "organization_read"},
		{Name: "project", Parent: "organization", Path: "/projects/{project}", ListPermission: "project_read"},
	}, Options{PermissionSeparator: "_"})
	if r := find(t, identity, "organization", "/"); r.Methods[0].Required[0] != "organization_read" {
		t.Errorf("org /: %+v", r)
	}
	if r := find(t, identity, "organization", "/bindings"); r.Methods[1].Required[0] != "organization_bindings_write" {
		t.Errorf("org /bindings: %+v", r)
	}
	if r := find(t, identity, "organization", "/projects"); r.Methods[1].Required[0] != "project_create" {
		t.Errorf("org /projects: %+v", r)
	}
	if r := find(t, identity, "project", "/invites"); r.Methods[0].Required[0] != "guests_write" {
		t.Errorf("project /invites: %+v", r)
	}
	if r := find(t, audit, "project", "/audit-events"); r.Methods[0].Required[0] != "audit_read" {
		t.Errorf("audit project: %+v", r)
	}
}
