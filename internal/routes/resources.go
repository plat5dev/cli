package routes

import "sort"

// Identity's and audit's resources at their defaults (plat5
// docs/reference/config/resources.md). Plat5 fixes each one's operations and
// upstreams; the deployment's overrides move or gate them. Permissions hold
// {kind}, filled in with the kind each route is for.

// resourceDefault is one resource for one kind, or for the user scope.
type resourceDefault struct {
	name string
	// kind is the kind it's for: kinds.<kind>.resources overrides it. Empty for
	// the user-scope resources, overridden only under resources.
	kind string
	// shared: resources (for every kind) overrides it. roots is set per kind.
	shared       bool
	scope        string
	path         string
	verbs        []defaultVerb
	allowDeleted bool
}

type defaultVerb struct {
	method   string
	required *Required
	upstream string
}

func perm(p string) *Required { return &Required{Permissions: []string{p}} }

var anyMember = &Required{AnyMember: true}

// defaults is every default resource of identity or audit, for every kind in
// the config, then the user scope's.
func defaults(c *config, svc string) []resourceDefault {
	var out []resourceDefault
	add := func(kind, name, scope, path string, verbs ...defaultVerb) *resourceDefault {
		out = append(out, resourceDefault{name: name, kind: kind, shared: true, scope: scope, path: path, verbs: verbs})
		return &out[len(out)-1]
	}
	v := func(method string, required *Required, upstream string) defaultVerb {
		return defaultVerb{method, required, upstream}
	}

	names := make([]string, 0, len(c.Kinds))
	for k := range c.Kinds {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		kd := c.Kinds[k]
		node := "/nodes/{" + k + ".id}"
		if svc == audit {
			add(k, "events", k, "/audit-events", v("GET", perm("audit:read"), node+"/audit-events"))
			continue
		}
		read, write := perm("{kind}:bindings:read"), perm("{kind}:bindings:write")
		add(k, "node", k, "/",
			v("GET", perm("{kind}:read"), node),
			v("PATCH", perm("{kind}:write"), node),
			v("DELETE", perm("{kind}:delete"), node))
		add(k, "restore", k, "/restore", v("POST", perm("{kind}:delete"), node+"/restore")).allowDeleted = true
		if kd.Parent != nil {
			up := "/nodes/{" + *kd.Parent + ".id}/children/" + k
			add(k, "children", *kd.Parent, kd.collection(),
				v("GET", anyMember, up),
				v("POST", perm("{kind}:create"), up))
		}
		membership := []defaultVerb{v("GET", anyMember, node+"/memberships/{member.id}")}
		if kd.isRoot() {
			membership = append(membership, v("DELETE", anyMember, node+"/members/{member.id}"))
		}
		add(k, "membership", k, "/membership", membership...)
		up := node + "/bindings"
		add(k, "bindings", k, "/bindings", v("GET", read, up), v("POST", write, up))
		up = node + "/bindings/{binding_id}"
		add(k, "binding", k, "/bindings/{binding_id}", v("PATCH", write, up), v("DELETE", write, up))
		up = node + "/service-accounts"
		add(k, "service_accounts", k, "/service-accounts", v("GET", read, up), v("POST", write, up))
		up = node + "/service-accounts/{service_account_id}"
		add(k, "service_account", k, "/service-accounts/{service_account_id}",
			v("GET", read, up), v("PATCH", write, up), v("DELETE", write, up))
		up = node + "/service-accounts/{service_account_id}/keys"
		add(k, "keys", k, "/service-accounts/{service_account_id}/keys", v("GET", write, up), v("POST", write, up))
		add(k, "key", k, "/service-accounts/{service_account_id}/keys/{key_id}",
			v("DELETE", write, node+"/service-accounts/{service_account_id}/keys/{key_id}"))
		invite := perm("guests:write")
		if kd.isRoot() {
			invite = write
		}
		up = node + "/invites"
		add(k, "invites", k, "/invites", v("GET", invite, up), v("POST", invite, up))
		add(k, "invite", k, "/invites/{invite_id}", v("DELETE", invite, node+"/invites/{invite_id}"))
		add(k, "roles", k, "/roles", v("GET", anyMember, node+"/roles"))
		if kd.isRoot() {
			up = node + "/members"
			add(k, "members", k, "/members", v("GET", read, up), v("POST", write, up))
			up = node + "/members/{member_id}"
			add(k, "member", k, "/members/{member_id}", v("GET", anyMember, up), v("DELETE", write, up))
			add(k, "roots", userScope, kd.collection(), v("POST", nil, "/roots/"+k)).shared = false
		}
	}

	user := "/users/{user.id}"
	if svc == audit {
		add("", "my_events", userScope, "/user/audit-events", v("GET", nil, user+"/audit-events"))
		return out
	}
	add("", "me", userScope, "/user", v("GET", nil, user), v("DELETE", nil, user))
	add("", "my_nodes", userScope, "/user/nodes", v("GET", nil, user+"/nodes"))
	add("", "my_keys", userScope, "/user/keys", v("GET", nil, user+"/keys"), v("POST", nil, user+"/keys"))
	add("", "my_key", userScope, "/user/keys/{key_id}", v("DELETE", nil, user+"/keys/{key_id}"))
	add("", "redeem", userScope, "/user/invites/redeem", v("POST", nil, user+"/invites/redeem"))
	return out
}
