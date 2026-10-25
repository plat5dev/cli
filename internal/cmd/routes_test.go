package cmd

import (
	"bytes"
	"testing"

	"github.com/plat5dev/cli/internal/routes"
)

func TestPrintRoutes(t *testing.T) {
	var buf bytes.Buffer
	printRoutes(&buf, map[string]routes.Service{
		"identity": {URL: "http://identity:3000", Routes: []routes.Route{
			{Method: "POST", Path: "/orgs", Scope: "user", Upstream: "/roots/organization"},
			{Method: "GET", Path: "/orgs/{organization}/roles", Scope: "organization", Upstream: "/nodes/{organization.id}/roles",
				RequiredPermissions: &routes.Required{AnyMember: true}},
		}},
		"e10s": {URL: "http://e10s:3000", Routes: []routes.Route{
			{Method: "DELETE", Path: "/orgs/{organization}/plans", Scope: "organization", Upstream: "/plans",
				RequiredPermissions: &routes.Required{Permissions: []string{"catalog:write", "organization:write"}}},
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
