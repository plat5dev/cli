// Package routes expands a deployment's config into every service's routes as
// the gateway matches them: one per method, with the full public path, the
// upstream, and every default filled in, as the control plane serves them
// (plat5 docs/reference/api/internal.md#control-plane-feeds). Identity's and
// audit's come from their resources' defaults and the deployment's overrides
// (plat5 docs/reference/config/resources.md). Offline: the same expansion as the
// control plane's.
package routes

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Route is one route as the gateway matches it.
type Route struct {
	Path     string `json:"path"`
	Method   string `json:"method"`
	Scope    string `json:"scope"`
	Upstream string `json:"upstream"`
	// RequiredPermissions is nil outside a kind scope.
	RequiredPermissions *Required `json:"required_permissions"`
	RateLimit           []string  `json:"rate_limit"`
	Audit               bool      `json:"audit"`
	AllowDeleted        bool      `json:"allow_deleted"`
}

// Service is a service as the gateway is served it: its own fields with
// defaults filled in, and its routes sorted by path, then method.
type Service struct {
	URL          string   `json:"url"`
	Timeouts     Timeouts `json:"timeouts"`
	MaxBodyBytes uint64   `json:"max_body_bytes"`
	Routes       []Route  `json:"routes"`
}

type Timeouts struct {
	Connect string `json:"connect"`
	Read    string `json:"read"`
}

// spec is one route of a service file, or one resource of identity or audit,
// before expansion.
type spec struct {
	scope string
	// path as written: relative to the node in a kind scope.
	path         string
	upstream     *string
	prefix       string
	verbs        []verbSpec
	allowDeleted bool
}

type verbSpec struct {
	method string
	methodConfig
	// upstream is a resource's upstream for this verb, fixed by Plat5.
	upstream *string
}

// Expand expands the flat config (plat5.yml's keys, services a map of name to
// service), which passed the schema. An override of identity's or audit's
// resources that names a kind, resource or verb that isn't there is an error.
func Expand(flat map[string]any) (map[string]Service, error) {
	data, err := json.Marshal(flat)
	if err != nil {
		return nil, err
	}
	var c config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}

	connect, read, maxBody := defaultConnectTimeout, defaultReadTimeout, uint64(defaultMaxBodyBytes)
	if c.Defaults.Timeouts.Connect != nil {
		connect = *c.Defaults.Timeouts.Connect
	}
	if c.Defaults.Timeouts.Read != nil {
		read = *c.Defaults.Timeouts.Read
	}
	if c.Defaults.MaxBodyBytes != nil {
		maxBody = *c.Defaults.MaxBodyBytes
	}

	var errs []string
	out := map[string]Service{}
	for name, svc := range c.Services {
		var specs []spec
		if name == identity || name == audit {
			specs = resourceSpecs(&c, name, svc, &errs)
		} else {
			specs = routeSpecs(svc)
		}
		routes := []Route{}
		for _, s := range specs {
			routes = append(routes, expandSpec(&c, s)...)
		}
		sort.SliceStable(routes, func(i, j int) bool {
			if routes[i].Path != routes[j].Path {
				return routes[i].Path < routes[j].Path
			}
			return verbOrder(routes[i].Method) < verbOrder(routes[j].Method)
		})
		s := Service{URL: svc.URL, Timeouts: Timeouts{connect, read}, MaxBodyBytes: maxBody, Routes: routes}
		if svc.Timeouts.Connect != nil {
			s.Timeouts.Connect = *svc.Timeouts.Connect
		}
		if svc.Timeouts.Read != nil {
			s.Timeouts.Read = *svc.Timeouts.Read
		}
		if svc.MaxBodyBytes != nil {
			s.MaxBodyBytes = *svc.MaxBodyBytes
		}
		out[name] = s
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return nil, fmt.Errorf("%s", strings.Join(errs, "\n"))
	}
	return out, nil
}

func routeSpecs(svc service) []spec {
	var out []spec
	for scopeName, sc := range svc.Scopes {
		for _, r := range sc.Routes {
			var vs []verbSpec
			for m, own := range r.Methods {
				v := verbSpec{method: m, methodConfig: r.methodConfig}
				if own != nil {
					setFields(&v, *own)
				}
				vs = append(vs, v)
			}
			out = append(out, spec{
				scope:        scopeName,
				path:         r.Path,
				upstream:     r.Upstream,
				prefix:       sc.UpstreamPrefix,
				verbs:        vs,
				allowDeleted: r.AllowDeleted,
			})
		}
	}
	return out
}

// layer is one override laid over a resource default: under resources
// (shared), or under kinds.<kind>.resources.
type layer struct {
	at     string
	r      resource
	shared bool
}

// resourceSpecs is identity's or audit's resources: each default, with
// resources and then kinds.<kind>.resources laid over it.
func resourceSpecs(c *config, name string, svc service, errs *[]string) []spec {
	base := "services." + name
	defs := defaults(c, name)
	for k, o := range svc.Kinds {
		kd, ok := c.Kinds[k]
		if !ok {
			*errs = append(*errs, fmt.Sprintf("%s.kinds.%s: %q is not a kind", base, k, k))
			continue
		}
		for r := range o.Resources {
			known := false
			for _, d := range defs {
				known = known || (d.name == r && d.kind == k)
			}
			if !known {
				why := "it isn't a root kind"
				if kd.isRoot() {
					why = "it's a root kind"
				}
				*errs = append(*errs, fmt.Sprintf("%s.kinds.%s.resources.%s: %s has no %s resource: %s", base, k, r, k, r, why))
			}
		}
	}

	var out []spec
	for _, d := range defs {
		var layers []layer
		if r, ok := svc.Resources[d.name]; ok && d.shared {
			layers = append(layers, layer{base + ".resources." + d.name, r, true})
		}
		if r, ok := svc.Kinds[d.kind].Resources[d.name]; ok && d.kind != "" {
			layers = append(layers, layer{base + ".kinds." + d.kind + ".resources." + d.name, r, false})
		}
		if s, ok := resourceSpec(d, defs, layers, errs); ok {
			out = append(out, s)
		}
	}
	return out
}

func resourceSpec(d resourceDefault, defs []resourceDefault, layers []layer, errs *[]string) (spec, bool) {
	path := d.path
	removed := false
	vs := make([]verbSpec, len(d.verbs))
	present := make([]bool, len(d.verbs))
	for i, v := range d.verbs {
		up := v.upstream
		vs[i] = verbSpec{method: v.method, methodConfig: methodConfig{RequiredPermissions: v.required}, upstream: &up}
		present[i] = true
	}

	for _, l := range layers {
		if l.r.Off {
			removed = true
			continue
		}
		removed = false
		if l.r.Path != nil {
			path = *l.r.Path
		}
		for i := range vs {
			setFields(&vs[i], l.r.methodConfig)
		}
		for method, over := range l.r.Methods {
			i := -1
			for j, v := range vs {
				if v.method == method {
					i = j
				}
			}
			if i < 0 {
				// A shared override names a verb the resource has at another
				// kind (membership's DELETE, on roots only).
				if !(l.shared && hasVerbElsewhere(defs, d.name, method)) {
					*errs = append(*errs, fmt.Sprintf("%s.methods.%s: %s has no %s here", l.at, method, d.name, method))
				}
				continue
			}
			switch {
			case over == nil:
				present[i] = true
			case over.Off:
				present[i] = false
			default:
				present[i] = true
				setFields(&vs[i], over.methodConfig)
			}
		}
	}
	if removed {
		return spec{}, false
	}

	var kept []verbSpec
	for i, v := range vs {
		if !present[i] {
			continue
		}
		if d.kind != "" && v.RequiredPermissions != nil && !v.RequiredPermissions.AnyMember {
			perms := make([]string, len(v.RequiredPermissions.Permissions))
			for j, p := range v.RequiredPermissions.Permissions {
				perms[j] = strings.ReplaceAll(p, "{kind}", d.kind)
			}
			v.RequiredPermissions = &Required{Permissions: perms}
		}
		kept = append(kept, v)
	}
	if len(kept) == 0 {
		return spec{}, false
	}
	return spec{scope: d.scope, path: path, verbs: kept, allowDeleted: d.allowDeleted}, true
}

func hasVerbElsewhere(defs []resourceDefault, name, method string) bool {
	for _, d := range defs {
		if d.name != name {
			continue
		}
		for _, v := range d.verbs {
			if v.method == method {
				return true
			}
		}
	}
	return false
}

// setFields lays one override's fields over a verb.
func setFields(v *verbSpec, cfg methodConfig) {
	if cfg.RequiredPermissions != nil {
		v.RequiredPermissions = cfg.RequiredPermissions
	}
	if cfg.RateLimit != nil {
		v.RateLimit = cfg.RateLimit
	}
	if cfg.Audit != nil {
		v.Audit = cfg.Audit
	}
}

// expandSpec is the spec's routes, one per verb.
func expandSpec(c *config, s spec) []Route {
	_, kindScope := c.Kinds[s.scope]
	path := s.path
	if kindScope {
		path = c.nodePath(s.scope)
		if s.path != "/" {
			path += s.path
		}
	}
	defaultUpstream := s.prefix + s.path
	if s.prefix != "" && s.path == "/" {
		defaultUpstream = s.prefix
	}

	out := make([]Route, 0, len(s.verbs))
	for _, v := range s.verbs {
		r := Route{
			Path:         path,
			Method:       v.method,
			Scope:        s.scope,
			Upstream:     defaultUpstream,
			RateLimit:    []string{defaultPolicy},
			Audit:        s.scope != publicScope && auditedByDefault(v.method),
			AllowDeleted: s.allowDeleted,
		}
		switch {
		case v.upstream != nil:
			r.Upstream = *v.upstream
		case s.upstream != nil:
			r.Upstream = *s.upstream
		}
		if kindScope {
			r.RequiredPermissions = v.RequiredPermissions
		}
		if v.RateLimit != nil {
			r.RateLimit = v.RateLimit.Policies
		}
		if v.Audit != nil && s.scope != publicScope {
			r.Audit = *v.Audit
		}
		out = append(out, r)
	}
	return out
}
