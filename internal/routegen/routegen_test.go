package routegen

import (
	"reflect"
	"strings"
	"testing"
)

func TestPrefix(t *testing.T) {
	for _, tc := range []struct{ name, list, want string }{
		{"project", "project:read", "project"},
		{"environment", "project:read", "project"},
		{"environment", "env:list", "environment"},
		{"environment", ":read", "environment"},
	} {
		if got := (Kind{Name: tc.name, ListPermission: tc.list}).Prefix(); got != tc.want {
			t.Errorf("%s %s: got %s", tc.name, tc.list, got)
		}
	}
}

func TestReadKinds(t *testing.T) {
	kinds, err := ReadKinds([]byte(`kinds:
  team: { path: "/teams/{team}" }
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
		`kinds: { team: { path: "/teams/{board}" } }`:             "kinds.team.path",
		`kinds: { team: { path: "/teams/{team}", parent: org } }`: "kinds.team.parent",
	} {
		if _, err := ReadKinds([]byte(in)); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: got %v", in, err)
		}
	}

	// Overlay merging: add a kind, modify an existing one, delete another with null.
	merged, err := ReadKindsMany(
		[]byte("kinds:\n  team: { path: \"/teams/{team}\" }\n  old: { parent: team, path: \"/olds/{old}\" }\n"),
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
	})
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
