package schema

import (
	"fmt"
	"sort"
)

// checkStaticRules validates offline semantic rules that JSON Schema alone
// cannot express cleanly, keeping `plat5 validate` aligned with control-plane
// static validation (e.g. scope-specific route restrictions and cross-key
// references to declared kinds, roles, and rate limits).
func checkStaticRules(instance any) []FieldError {
	cfg, ok := instance.(map[string]any)
	if !ok {
		return nil
	}

	kinds, _ := cfg["kinds"].(map[string]any)
	roles, _ := cfg["roles"].(map[string]any)
	rateLimits, _ := cfg["rate_limits"].(map[string]any)

	var errs []FieldError
	add := func(path, msg string) {
		errs = append(errs, FieldError{Path: path, Message: msg})
	}

	// Roles: bindable_at must reference declared kinds; includes must reference declared roles.
	for roleName, roleRaw := range roles {
		role, ok := roleRaw.(map[string]any)
		if !ok {
			continue
		}
		if ba, ok := role["bindable_at"].([]any); ok {
			for i, kRaw := range ba {
				kName, _ := kRaw.(string)
				if _, exists := kinds[kName]; !exists {
					add(fmt.Sprintf("roles.%s.bindable_at[%d]", roleName, i), fmt.Sprintf("%q is not a kind", kName))
				}
			}
		}
		if inc, ok := role["includes"].([]any); ok {
			for i, rRaw := range inc {
				rName, _ := rRaw.(string)
				if _, exists := roles[rName]; !exists {
					add(fmt.Sprintf("roles.%s.includes[%d]", roleName, i), fmt.Sprintf("%q is not a role", rName))
				}
			}
		}
	}

	// creator_role must reference a declared role when set.
	if cr, ok := cfg["creator_role"].(string); ok && cr != "" {
		if _, exists := roles[cr]; !exists {
			add("creator_role", fmt.Sprintf("%q is not a role", cr))
		}
	}

	// rate_limits: count_at must reference a declared kind.
	for rlName, rlRaw := range rateLimits {
		rl, ok := rlRaw.(map[string]any)
		if !ok {
			continue
		}
		if ca, ok := rl["count_at"].(string); ok && ca != "" {
			if _, exists := kinds[ca]; !exists {
				add(fmt.Sprintf("rate_limits.%s.count_at", rlName), fmt.Sprintf("%q is not a kind", ca))
			}
		}
	}

	// defaults.rate_limit: each entry must reference a declared policy without count_at.
	if defs, ok := cfg["defaults"].(map[string]any); ok {
		if drl, ok := defs["rate_limit"].([]any); ok {
			for i, nameRaw := range drl {
				name, _ := nameRaw.(string)
				rlRaw, exists := rateLimits[name]
				if !exists {
					add(fmt.Sprintf("defaults.rate_limit[%d]", i), fmt.Sprintf("%q is not a rate_limit policy", name))
					continue
				}
				if rl, ok := rlRaw.(map[string]any); ok {
					if ca, ok := rl["count_at"].(string); ok && ca != "" {
						add(fmt.Sprintf("defaults.rate_limit[%d]", i), fmt.Sprintf("%q counts at a kind: a default applies to user and public routes too, which have no node to count at", name))
					}
				}
			}
		}
	}

	// Services & scopes: static route/scope restrictions.
	services, _ := cfg["services"].(map[string]any)
	for svcName, svcRaw := range services {
		svc, ok := svcRaw.(map[string]any)
		if !ok {
			continue
		}
		scopes, _ := svc["scopes"].(map[string]any)
		for scopeName, scopeRaw := range scopes {
			scopeAt := fmt.Sprintf("services.%s.scopes.%s", svcName, scopeName)
			_, isKind := kinds[scopeName]
			isPublic := scopeName == "public"
			isUser := scopeName == "user"
			if !isKind && !isPublic && !isUser {
				add(scopeAt, fmt.Sprintf("%q is not a kind, user or public", scopeName))
				continue
			}
			scope, ok := scopeRaw.(map[string]any)
			if !ok {
				continue
			}
			routes, _ := scope["routes"].([]any)
			for i, routeRaw := range routes {
				route, ok := routeRaw.(map[string]any)
				if !ok {
					continue
				}
				routeAt := fmt.Sprintf("%s.routes[%d]", scopeAt, i)
				_, routeHasPerms := route["permissions"]
				_, routeHasAudit := route["audit"]
				_, routeHasAllowDeleted := route["allow_deleted"]
				_, routeHasResource := route["resource"]

				if isPublic {
					if routeHasAudit {
						add(routeAt+".audit", "a public route has no audit log")
					}
					if routeHasPerms {
						add(routeAt+".permissions", "a public route takes no permissions")
					}
					if routeHasAllowDeleted {
						add(routeAt+".allow_deleted", "only in a kind scope; this is public")
					}
					if routeHasResource {
						add(routeAt+".resource", "only in a kind scope; this is public")
					}
				}
				if isUser {
					if routeHasPerms {
						add(routeAt+".permissions", "a user route takes no permissions")
					}
					if routeHasAllowDeleted {
						add(routeAt+".allow_deleted", "only in a kind scope; this is user")
					}
					if routeHasResource {
						add(routeAt+".resource", "only in a kind scope; this is user")
					}
				}

				switch methods := route["methods"].(type) {
				case []any:
					if isKind && !routeHasPerms {
						add(routeAt+".permissions", fmt.Sprintf("required in a %s scope: a permission, list of permissions, or any_member", scopeName))
					}
				case map[string]any:
					for verb, mRaw := range methods {
						m, _ := mRaw.(map[string]any)
						_, mHasPerms := m["permissions"]
						_, mHasAudit := m["audit"]
						_, mHasResource := m["resource"]
						verbAt := fmt.Sprintf("%s.methods.%s", routeAt, verb)

						if isPublic {
							if mHasAudit {
								add(verbAt+".audit", "a public route has no audit log")
							}
							if mHasPerms {
								add(verbAt+".permissions", "a public route takes no permissions")
							}
							if mHasResource {
								add(verbAt+".resource", "only in a kind scope; this is public")
							}
						}
						if isUser {
							if mHasPerms {
								add(verbAt+".permissions", "a user route takes no permissions")
							}
							if mHasResource {
								add(verbAt+".resource", "only in a kind scope; this is user")
							}
						}
						if isKind && !routeHasPerms && !mHasPerms {
							add(verbAt+".permissions", fmt.Sprintf("required for %s in a %s scope: a permission, list of permissions, or any_member", verb, scopeName))
						}
					}
				}
			}
		}
	}

	sort.SliceStable(errs, func(i, j int) bool { return errs[i].Path < errs[j].Path })
	return errs
}
