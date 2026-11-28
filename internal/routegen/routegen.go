// Package routegen writes identity's and audit's routes for the kinds in
// plat5.yml: services/identity.yml and services/audit.yml, ordinary service
// files (plat5 docs/reference/config/generated-routes.md). The files it writes
// are scaffolding: once written they are the truth, edited like any other.
package routegen

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Kind is one kind of plat5.yml, as the generator reads it.
type Kind struct {
	Name           string
	Parent         string
	Path           string
	ListPermission string
}

// Root reports a kind with no parent.
func (k Kind) Root() bool { return k.Parent == "" }

// Collection is the literal in the kind's path: /projects for /projects/{project}.
func (k Kind) Collection() string {
	c, _, _ := strings.Cut(strings.TrimPrefix(k.Path, "/"), "/")
	return "/" + c
}

// Options controls how Generate names permissions and filters scaffolded routes.
type Options struct {
	// PermissionSeparator joins permission segments (e.g. ":", "_", ".", "-").
	PermissionSeparator string
	// Exclude omits matching generated routes (each entry is "<METHOD> /path",
	// for example "POST /orgs"). Matched by HTTP method and path shape.
	Exclude []string
}

// Prefix is the kind's permission prefix: its list_permission without
// `<sep>read` when it ends in that suffix, else the kind's name.
func (k Kind) Prefix(sep string) string {
	if sep != "" {
		if p, ok := strings.CutSuffix(k.ListPermission, sep+"read"); ok && p != "" {
			return p
		}
	}
	return k.Name
}

// AnyMember is the permissions value that admits any member of the tree.
const AnyMember = "any_member"

// Method is one verb of a route. Required is nil in the user scope; a kind
// scope's is a list of permissions or [AnyMember].
type Method struct {
	Verb     string
	Required []string
}

// Route is one route of a scope.
type Route struct {
	Path string
	// Upstream is set only when it isn't the scope's upstream_prefix plus Path.
	Upstream     string
	AllowDeleted bool
	Methods      []Method
}

// Scope is one scope block: a kind's, or user.
type Scope struct {
	Name           string
	UpstreamPrefix string
	Routes         []Route
}

// File is one generated service.
type File struct {
	Service string
	// URL is the default url, for a file that doesn't exist yet.
	URL    string
	Scopes []Scope
}

// The services the generator writes, and their urls on the local stack.
const (
	Identity = "identity"
	Audit    = "audit"
)

var defaultURLs = map[string]string{Identity: "http://identity:3000", Audit: "http://audit:3002"}

// kindPath is a kind's path: /<collection>/{<kind name>}.
var kindPath = regexp.MustCompile(`^/[a-z][a-z0-9-]{1,61}[a-z0-9]/\{([a-z][a-z0-9_]{0,31})\}$`)

// ReadKinds reads plat5.yml's kinds, in the file's order. It checks only what
// the generator needs: each kind's path and parent.
func ReadKinds(data []byte) ([]Kind, error) {
	return ReadKindsMany(data)
}

// ReadKindsMany reads kinds from a base plat5.yml and zero or more overlay
// files, preserving declaration order and applying RFC 7396 merge-patch
// semantics to kinds (a null kind deletes it; a mapping merges into it).
func ReadKindsMany(docs ...[]byte) ([]Kind, error) {
	if len(docs) == 0 {
		return nil, fmt.Errorf("kinds: at least one kind is required")
	}
	var order []string
	raw := map[string]map[string]any{}
	for _, data := range docs {
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
		if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
			return nil, fmt.Errorf("must be a mapping")
		}
		top := doc.Content[0]
		var kinds *yaml.Node
		found := false
		for i := 0; i+1 < len(top.Content); i += 2 {
			if top.Content[i].Value == "kinds" {
				kinds = top.Content[i+1]
				found = true
			}
		}
		if !found {
			continue
		}
		if kinds.Tag == "!!null" {
			order = nil
			raw = map[string]map[string]any{}
			continue
		}
		if kinds.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("kinds: must be a mapping")
		}
		for i := 0; i+1 < len(kinds.Content); i += 2 {
			name := kinds.Content[i].Value
			val := kinds.Content[i+1]
			if val.Tag == "!!null" {
				delete(raw, name)
				continue
			}
			var fields map[string]any
			if err := val.Decode(&fields); err != nil {
				return nil, fmt.Errorf("kinds.%s: %w", name, err)
			}
			existing, ok := raw[name]
			if !ok {
				existing = map[string]any{}
				raw[name] = existing
				order = append(order, name)
			}
			for fk, fv := range fields {
				if fv == nil {
					delete(existing, fk)
				} else {
					existing[fk] = fv
				}
			}
		}
	}
	var out []Kind
	names := map[string]bool{}
	for _, name := range order {
		fields, ok := raw[name]
		if !ok {
			continue
		}
		k := Kind{Name: name}
		k.Parent, _ = fields["parent"].(string)
		k.Path, _ = fields["path"].(string)
		k.ListPermission, _ = fields["list_permission"].(string)
		if m := kindPath.FindStringSubmatch(k.Path); m == nil || m[1] != k.Name {
			return nil, fmt.Errorf("kinds.%s.path: want /<collection>/{%s}, got %q", k.Name, k.Name, k.Path)
		}
		if k.ListPermission == "" {
			return nil, fmt.Errorf("kinds.%s.list_permission: is required", k.Name)
		}
		names[k.Name] = true
		out = append(out, k)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("kinds: at least one kind is required")
	}
	for _, k := range out {
		if k.Parent != "" && !names[k.Parent] {
			return nil, fmt.Errorf("kinds.%s.parent: %q is not a kind", k.Name, k.Parent)
		}
	}
	return out, nil
}

func perms(p ...string) []string { return p }

var anyMember = []string{AnyMember}

// Generate is identity's and audit's routes for kinds, in kinds' order: each
// kind's own routes in its scope, its collection's routes in its parent's
// scope, and on a root, its member routes and its create in the user scope.
// Then the user scope's own routes. POST on a root's /members isn't generated.
func Generate(kinds []Kind, opts Options) (identity, audit File) {
	sep := opts.PermissionSeparator
	join := func(parts ...string) string { return strings.Join(parts, sep) }
	identity = File{Service: Identity, URL: defaultURLs[Identity]}
	audit = File{Service: Audit, URL: defaultURLs[Audit]}
	var roots []Route
	for _, k := range kinds {
		p := k.Prefix(sep)
		prefix := "/nodes/{" + k.Name + ".id}"
		read, write := perms(join(p, "bindings", "read")), perms(join(p, "bindings", "write"))
		invite := write
		if !k.Root() {
			invite = perms(join("guests", "write"))
		}
		routes := []Route{
			{Path: "/", Methods: []Method{
				{"GET", perms(join(p, "read"))}, {"PATCH", perms(join(p, "write"))}, {"DELETE", perms(join(p, "delete"))},
			}},
			{Path: "/restore", AllowDeleted: true, Methods: []Method{{"POST", perms(join(p, "delete"))}}},
		}
		for _, c := range kinds {
			if c.Parent == k.Name {
				up := prefix + "/children/" + c.Name
				routes = append(routes, Route{Path: c.Collection(), Upstream: up, Methods: []Method{
					{"GET", anyMember}, {"POST", perms(join(c.Prefix(sep), "create"))},
				}})
			}
		}
		membership := Route{Path: "/membership", Upstream: prefix + "/memberships/{member.id}", Methods: []Method{{"GET", anyMember}}}
		if k.Root() {
			membership.Methods = append(membership.Methods, Method{"DELETE", anyMember})
		}
		rolesRoute := Route{Path: "/roles", Methods: []Method{{"GET", anyMember}}}
		if k.Root() {
			rolesRoute.Methods = append(rolesRoute.Methods, Method{"POST", write})
		}
		routes = append(routes, membership,
			Route{Path: "/bindings", Methods: []Method{{"GET", read}, {"POST", write}}},
			Route{Path: "/bindings/{binding_id}", Methods: []Method{{"PATCH", write}, {"DELETE", write}}},
			Route{Path: "/service-accounts", Methods: []Method{{"GET", read}, {"POST", write}}},
			Route{Path: "/service-accounts/{service_account_id}", Methods: []Method{{"GET", read}, {"PATCH", write}, {"DELETE", write}}},
			Route{Path: "/service-accounts/{service_account_id}/keys", Methods: []Method{{"GET", write}, {"POST", write}}},
			Route{Path: "/service-accounts/{service_account_id}/keys/{key_id}", Methods: []Method{{"PATCH", write}, {"DELETE", write}}},
			Route{Path: "/invites", Methods: []Method{{"GET", invite}, {"POST", invite}}},
			Route{Path: "/invites/{invite_id}", Methods: []Method{{"DELETE", invite}}},
			rolesRoute,
		)
		if k.Root() {
			routes = append(routes,
				Route{Path: "/roles/{role_id}", Methods: []Method{{"GET", anyMember}, {"PATCH", write}, {"DELETE", write}}},
				Route{Path: "/permissions", Methods: []Method{{"GET", read}}},
				Route{Path: "/security-policy", Methods: []Method{{"GET", read}, {"PATCH", write}}},
				Route{Path: "/members", Methods: []Method{{"GET", read}}},
				Route{Path: "/members/{member_id}", Methods: []Method{{"GET", anyMember}, {"DELETE", write}}},
				Route{Path: "/members/{member_id}/status", Methods: []Method{{"PATCH", write}}},
				Route{Path: "/groups", Methods: []Method{{"GET", read}, {"POST", write}}},
				Route{Path: "/groups/{group_id}", Methods: []Method{{"GET", read}, {"PATCH", write}, {"DELETE", write}}},
				Route{Path: "/groups/{group_id}/members", Methods: []Method{{"GET", read}, {"POST", write}}},
				Route{Path: "/groups/{group_id}/members/{member_id}", Methods: []Method{{"DELETE", write}}},
			)
			roots = append(roots, Route{Path: k.Collection(), Upstream: "/roots/" + k.Name, Methods: []Method{{Verb: "POST"}}})
		}
		identity.Scopes = append(identity.Scopes, Scope{Name: k.Name, UpstreamPrefix: prefix, Routes: routes})
		audit.Scopes = append(audit.Scopes, Scope{Name: k.Name, UpstreamPrefix: prefix, Routes: []Route{
			{Path: "/audit-events", Methods: []Method{{"GET", perms(join("audit", "read"))}}},
		}})
	}
	user := "/users/{user.id}"
	identity.Scopes = append(identity.Scopes, Scope{Name: "user", Routes: append(roots,
		Route{Path: "/user", Upstream: user, Methods: []Method{{Verb: "GET"}, {Verb: "PATCH"}, {Verb: "DELETE"}}},
		Route{Path: "/user/nodes", Upstream: user + "/nodes", Methods: []Method{{Verb: "GET"}}},
		Route{Path: "/user/keys", Upstream: user + "/keys", Methods: []Method{{Verb: "GET"}, {Verb: "POST"}}},
		Route{Path: "/user/keys/{key_id}", Upstream: user + "/keys/{key_id}", Methods: []Method{{Verb: "PATCH"}, {Verb: "DELETE"}}},
		Route{Path: "/user/invites/redeem", Upstream: user + "/invites/redeem", Methods: []Method{{Verb: "POST"}}},
	)})
	audit.Scopes = append(audit.Scopes, Scope{Name: "user", Routes: []Route{
		{Path: "/user/audit-events", Upstream: user + "/audit-events", Methods: []Method{{Verb: "GET"}}},
	}})
	if len(opts.Exclude) > 0 {
		skip := buildSkipSet(opts.Exclude)
		identity = filterFile(identity, kinds, skip)
		audit = filterFile(audit, kinds, skip)
	}
	return identity, audit
}

// pathShape replaces every {param} segment with {} so matching matches Plat5's
// route-shape rules regardless of parameter name.
func pathShape(p string) string {
	if p == "/" || p == "" {
		return "/"
	}
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") && len(seg) >= 2 {
			parts[i] = "{}"
		}
	}
	return strings.Join(parts, "/")
}

func routeKey(method, publicPath string) string {
	return strings.ToUpper(strings.TrimSpace(method)) + " " + pathShape(strings.TrimSpace(publicPath))
}

func buildSkipSet(entries []string) map[string]bool {
	out := map[string]bool{}
	for _, raw := range entries {
		fields := strings.Fields(raw)
		if len(fields) != 2 {
			continue
		}
		out[routeKey(fields[0], fields[1])] = true
	}
	return out
}

func kindChainPath(kinds []Kind) map[string]string {
	byName := make(map[string]Kind, len(kinds))
	for _, k := range kinds {
		byName[k.Name] = k
	}
	out := make(map[string]string, len(kinds))
	for _, k := range kinds {
		var pieces []string
		cur := k
		seen := map[string]bool{}
		for !seen[cur.Name] {
			seen[cur.Name] = true
			pieces = append(pieces, cur.Path)
			if cur.Parent == "" {
				break
			}
			p, ok := byName[cur.Parent]
			if !ok {
				break
			}
			cur = p
		}
		for i, j := 0, len(pieces)-1; i < j; i, j = i+1, j-1 {
			pieces[i], pieces[j] = pieces[j], pieces[i]
		}
		out[k.Name] = strings.Join(pieces, "")
	}
	return out
}

func expandPublicPath(chainPaths map[string]string, scope, routePath string) string {
	if scope == "user" || scope == "public" {
		return routePath
	}
	base := chainPaths[scope]
	if routePath == "/" {
		return base
	}
	return base + routePath
}

func filterFile(f File, kinds []Kind, skip map[string]bool) File {
	if len(skip) == 0 {
		return f
	}
	chainPaths := kindChainPath(kinds)
	var scopes []Scope
	for _, sc := range f.Scopes {
		var routes []Route
		for _, r := range sc.Routes {
			pub := expandPublicPath(chainPaths, sc.Name, r.Path)
			var methods []Method
			for _, m := range r.Methods {
				if skip[routeKey(m.Verb, pub)] {
					continue
				}
				methods = append(methods, m)
			}
			if len(methods) > 0 {
				r.Methods = methods
				routes = append(routes, r)
			}
		}
		if len(routes) > 0 {
			sc.Routes = routes
			scopes = append(scopes, sc)
		}
	}
	f.Scopes = scopes
	return f
}

// claimedRoutes reads every non-identity, non-audit service file listed in
// paths' services: entries and returns their claimed "<METHOD> <public_path>"
// entries so WriteFiles skips routes already wrapped by the project's services.
func claimedRoutes(kinds []Kind, docs [][]byte, paths []string) []string {
	chainPaths := kindChainPath(kinds)
	var claimed []string
	for idx, data := range docs {
		var top map[string]any
		if err := yaml.Unmarshal(data, &top); err != nil {
			continue
		}
		baseDir := filepath.Dir(paths[idx])
		rawSvc, ok := top["services"]
		if !ok {
			continue
		}
		list, ok := rawSvc.([]any)
		if !ok {
			continue
		}
		for _, item := range list {
			rel, ok := item.(string)
			if !ok || strings.TrimSpace(rel) == "" {
				continue
			}
			svcPath := filepath.Clean(filepath.Join(baseDir, rel))
			svcBytes, err := os.ReadFile(svcPath)
			if err != nil {
				continue
			}
			var svcFile map[string]any
			if err := yaml.Unmarshal(svcBytes, &svcFile); err != nil {
				continue
			}
			for name, body := range svcFile {
				if name == Identity || name == Audit {
					continue
				}
				m, ok := body.(map[string]any)
				if !ok {
					continue
				}
				scopesMap, ok := m["scopes"].(map[string]any)
				if !ok {
					continue
				}
				for scopeName, scopeVal := range scopesMap {
					sm, ok := scopeVal.(map[string]any)
					if !ok {
						continue
					}
					routesList, ok := sm["routes"].([]any)
					if !ok {
						continue
					}
					for _, rawRoute := range routesList {
						rm, ok := rawRoute.(map[string]any)
						if !ok {
							continue
						}
						rpath, _ := rm["path"].(string)
						if rpath == "" {
							continue
						}
						pub := expandPublicPath(chainPaths, scopeName, rpath)
						switch mv := rm["methods"].(type) {
						case []any:
							for _, v := range mv {
								if verb, ok := v.(string); ok && verb != "" {
									claimed = append(claimed, verb+" "+pub)
								}
							}
						case map[string]any:
							for verb := range mv {
								if verb != "" {
									claimed = append(claimed, verb+" "+pub)
								}
							}
						}
					}
				}
			}
		}
	}
	return claimed
}

// ClaimedRoutesFromServices inspects an in-memory map of service name -> service
// body (excluding identity and audit) and returns "<METHOD> /public/path" entries
// claimed by user services so Generate skips them automatically.
func ClaimedRoutesFromServices(kinds []Kind, services map[string]any) []string {
	chainPaths := kindChainPath(kinds)
	var claimed []string
	for svcName, rawSvc := range services {
		if svcName == Identity || svcName == Audit {
			continue
		}
		svcMap, ok := rawSvc.(map[string]any)
		if !ok {
			continue
		}
		scopes, ok := svcMap["scopes"].(map[string]any)
		if !ok {
			continue
		}
		for scopeName, rawScope := range scopes {
			scopeMap, ok := rawScope.(map[string]any)
			if !ok {
				continue
			}
			routesList, ok := scopeMap["routes"].([]any)
			if !ok {
				continue
			}
			for _, rawRoute := range routesList {
				rm, ok := rawRoute.(map[string]any)
				if !ok {
					continue
				}
				rpath, _ := rm["path"].(string)
				if rpath == "" {
					continue
				}
				pub := expandPublicPath(chainPaths, scopeName, rpath)
				switch mv := rm["methods"].(type) {
				case []any:
					for _, v := range mv {
						if verb, ok := v.(string); ok && verb != "" {
							claimed = append(claimed, verb+" "+pub)
						}
					}
				case map[string]any:
					for verb := range mv {
						if verb != "" {
							claimed = append(claimed, verb+" "+pub)
						}
					}
				}
			}
		}
	}
	return claimed
}

// Header is the comment at the top of each generated file.
func Header(service string) string {
	return fmt.Sprintf(`# %s's routes at the gateway, for every kind in plat5.yml. plat5 routes
# generate wrote them (https://github.com/plat5dev/plat5/blob/master/docs/reference/config/generated-routes.md); from
# here this is an ordinary service file, served as written.
#
# To wrap a route in your own service, claim that method and path in your
# service file; plat5 routes generate skips it automatically. To permanently
# disable a route without replacing it, list "<METHOD> /path" under
# routes.exclude in plat5.local.yml.
#
# Regenerate after adding or removing a kind, or changing a kind's path or
# list_permission: plat5 routes generate. It overwrites this file, keeping only
# url, timeouts and max_body_bytes.
`, strings.ToUpper(service[:1])+service[1:])
}

// Kept is what a regenerated file keeps from the one it replaces.
var Kept = []string{"url", "timeouts", "max_body_bytes"}

// Render writes f as YAML: the header, then the service. kept holds the
// fields of the existing file to keep (Kept); without a url, f's default is used.
func Render(f File, kept map[string]any) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(Header(f.Service))
	fmt.Fprintf(&b, "%s:\n", f.Service)
	url := f.URL
	if u, ok := kept["url"]; ok {
		s, ok := u.(string)
		if !ok {
			return nil, fmt.Errorf("url: must be a string")
		}
		url = s
	}
	fmt.Fprintf(&b, "  url: %s\n", scalar(url))
	for _, k := range Kept[1:] {
		v, ok := kept[k]
		if !ok {
			continue
		}
		var out bytes.Buffer
		enc := yaml.NewEncoder(&out)
		enc.SetIndent(2)
		if err := enc.Encode(map[string]any{k: v}); err != nil {
			return nil, err
		}
		if err := enc.Close(); err != nil {
			return nil, err
		}
		for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
			b.WriteString("  " + line + "\n")
		}
	}
	b.WriteString("  scopes:\n")
	for _, s := range f.Scopes {
		fmt.Fprintf(&b, "    %s:\n", s.Name)
		if s.UpstreamPrefix != "" {
			fmt.Fprintf(&b, "      upstream_prefix: %s\n", s.UpstreamPrefix)
		}
		b.WriteString("      routes:\n")
		for _, r := range s.Routes {
			fmt.Fprintf(&b, "        - path: %s\n", r.Path)
			if r.Upstream != "" {
				fmt.Fprintf(&b, "          upstream: %s\n", r.Upstream)
			}
			if r.AllowDeleted {
				b.WriteString("          allow_deleted: true\n")
			}
			if r.Methods[0].Required == nil {
				verbs := make([]string, len(r.Methods))
				for i, m := range r.Methods {
					verbs[i] = m.Verb
				}
				fmt.Fprintf(&b, "          methods: [%s]\n", strings.Join(verbs, ", "))
				continue
			}
			b.WriteString("          methods:\n")
			for _, m := range r.Methods {
				fmt.Fprintf(&b, "            %s: { permissions: %s }\n", m.Verb, required(m.Required))
			}
		}
	}
	return b.Bytes(), nil
}

func required(p []string) string {
	if len(p) == 1 {
		return p[0]
	}
	return "[" + strings.Join(p, ", ") + "]"
}

// scalar is s as a YAML scalar, quoted only when it must be.
func scalar(s string) string {
	out, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Sprintf("%q", s)
	}
	return strings.TrimRight(string(out), "\n")
}

// WriteFiles generates identity's and audit's files from the plat5.yml (plus
// any overlay files) in paths, into services/ beside paths[0], overwriting
// them; an existing file's url, timeouts and max_body_bytes are kept. It
// returns the files written, relative to paths[0]'s directory.
func WriteFiles(opts Options, paths ...string) ([]string, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("at least one config file is required")
	}
	if opts.PermissionSeparator == "" {
		return nil, fmt.Errorf("permission_separator is required")
	}
	docs := make([][]byte, 0, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		docs = append(docs, data)
	}
	kinds, err := ReadKindsMany(docs...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(paths[0]), err)
	}
	genOpts := opts
	if claimed := claimedRoutes(kinds, docs, paths); len(claimed) > 0 {
		genOpts.Exclude = append(append([]string(nil), opts.Exclude...), claimed...)
	}
	identity, audit := Generate(kinds, genOpts)
	dir := filepath.Dir(paths[0])
	// Read and render both before writing either, so a refusal writes nothing.
	files := map[string][]byte{}
	var written []string
	for _, f := range []File{identity, audit} {
		rel := filepath.Join("services", f.Service+".yml")
		kept, err := keep(filepath.Join(dir, rel), f.Service)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.ToSlash(rel), err)
		}
		if files[rel], err = Render(f, kept); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.ToSlash(rel), err)
		}
		written = append(written, rel)
	}
	if err := os.MkdirAll(filepath.Join(dir, "services"), 0o755); err != nil {
		return nil, err
	}
	for _, rel := range written {
		if err := os.WriteFile(filepath.Join(dir, rel), files[rel], 0o644); err != nil {
			return nil, err
		}
	}
	return written, nil
}

// keep reads the fields Kept of the service in an existing file. No file keeps
// nothing; a file that isn't the service's is an error.
func keep(path, service string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var file map[string]any
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if len(file) == 0 {
		return nil, nil
	}
	body, ok := file[service].(map[string]any)
	if len(file) != 1 || !ok {
		return nil, fmt.Errorf("must hold one service, %s, to be regenerated", service)
	}
	out := map[string]any{}
	for _, k := range Kept {
		if v, ok := body[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}
