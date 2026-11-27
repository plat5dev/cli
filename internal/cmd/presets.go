package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/routegen"
	"gopkg.in/yaml.v3"
)

const (
	presetMinimal    = "minimal"
	presetSaaS       = "saas"
	presetEnterprise = "enterprise"
	presetPublic     = "public"

	authorizerNone  = "none"
	authorizerTopaz = "topaz"

	topazManifestFile = "topaz/manifest.yaml"
)

type namedOption struct {
	Name        string
	Description string
}

var presetOptions = []namedOption{
	{presetMinimal, "One kind (organization), admin and member roles"},
	{presetSaaS, "organization -> project, default-deny projects, guest invites"},
	{presetEnterprise, "organization -> project -> environment, custom roles, tiered rate limits, SSO claims"},
	{presetPublic, "organization (subdomain host) -> project, public and tenant portal routes"},
}

var authorizerOptions = []namedOption{
	{authorizerNone, "Built-in Plat5 RBAC only"},
	{authorizerTopaz, "Local Topaz AuthZEN 1.0 PDP (replaces built-in RBAC) with starter ReBAC manifest"},
}

func presetNames() []string {
	out := make([]string, len(presetOptions))
	for i, o := range presetOptions {
		out[i] = o.Name
	}
	return out
}

func authorizerNames() []string {
	out := make([]string, len(authorizerOptions))
	for i, o := range authorizerOptions {
		out[i] = o.Name
	}
	return out
}

func isValidPreset(p string) bool {
	for _, o := range presetOptions {
		if o.Name == p {
			return true
		}
	}
	return false
}

func isValidAuthorizer(a string) bool {
	for _, o := range authorizerOptions {
		if o.Name == a {
			return true
		}
	}
	return false
}

// writePresetFiles writes plat5.yml, generated identity/audit service files,
// any preset example service files, and (with --authorizer topaz) Topaz's
// manifest and record-level example service file. Existing files are kept.
func writePresetFiles(dir, preset, authorizer string) error {
	if preset == "" {
		preset = presetMinimal
	}
	if authorizer == "" {
		authorizer = authorizerNone
	}
	if preset == presetMinimal && authorizer == authorizerNone {
		return writeInitFiles(dir)
	}

	baseYML, extraServices, hasProject := presetConfig(preset)
	ymlBytes := []byte(baseYML)
	permSep := config.DefaultPermissionSeparator

	if authorizer == authorizerTopaz {
		permSep = "_"
		var err error
		ymlBytes, err = injectTopazAuthorizer(ymlBytes)
		if err != nil {
			return err
		}
		ymlBytes = rewriteListPermissionSeparator(ymlBytes, config.DefaultPermissionSeparator, permSep)
		ymlBytes = appendServiceEntry(ymlBytes, "services/docs.yml")
		for i := range extraServices {
			if extraServices[i].rel == "services/portal.yml" {
				extraServices[i].data = []byte(publicPortalServiceYML(permSep))
			}
		}
		scopeKind := "organization"
		if hasProject {
			scopeKind = "project"
		}
		extraServices = append(extraServices, projectFile{
			rel:  "services/docs.yml",
			data: []byte(topazDocsServiceYML(scopeKind)),
		})
	}

	kinds, err := routegen.ReadKinds(ymlBytes)
	if err != nil {
		return fmt.Errorf("preset %s kinds: %w", preset, err)
	}
	files := []projectFile{
		{rel: "plat5.yml", data: ymlBytes},
	}
	identityFile, auditFile := routegen.Generate(kinds, routegen.Options{PermissionSeparator: permSep})
	for _, gen := range []routegen.File{identityFile, auditFile} {
		rel := filepath.ToSlash(filepath.Join("services", gen.Service+".yml"))
		rendered, err := routegen.Render(gen, nil)
		if err != nil {
			return fmt.Errorf("render %s: %w", rel, err)
		}
		files = append(files, projectFile{rel: rel, data: rendered})
	}
	files = append(files, extraServices...)
	if authorizer == authorizerTopaz {
		files = append(files, projectFile{
			rel:  topazManifestFile,
			data: []byte(topazDocsManifest(hasProject)),
		})
	}

	for _, f := range files {
		if err := writeFileIfMissing(dir, f.rel, f.data); err != nil {
			return err
		}
	}
	return nil
}

func writeFileIfMissing(dir, rel string, data []byte) error {
	target := filepath.Join(dir, rel)
	if _, err := os.Stat(target); err == nil {
		fmt.Println("  kept", rel)
		return nil
	}
	return writeFile(dir, rel, data)
}

// injectTopazAuthorizer replaces built-in RBAC keys (roles, creator_role,
// custom_roles, permissions) with a top-level authorizer: block before
// services: in plat5.yml. Refuses a config that already declares authorizer:.
func injectTopazAuthorizer(base []byte) ([]byte, error) {
	var top map[string]any
	if err := yaml.Unmarshal(base, &top); err != nil {
		return nil, fmt.Errorf("plat5.yml: %w", err)
	}
	if _, ok := top["authorizer"]; ok {
		return nil, fmt.Errorf("plat5.yml already sets authorizer: remove it or omit --authorizer")
	}
	cleaned := stripRBACKeys(base)
	head, list, ok := bytes.Cut(append([]byte("\n"), cleaned...), []byte("\nservices:"))
	if !ok {
		return nil, fmt.Errorf("plat5.yml has no services: list")
	}
	var b bytes.Buffer
	b.Write(head[1:])
	b.WriteString("\n# External OpenID AuthZEN 1.0 authorizer (https://github.com/plat5dev/plat5/blob/master/docs/reference/config/authorizer.md).\n")
	b.WriteString("# Replaces built-in RBAC; runs locally via the authorizer module (topaz/manifest.yaml).\n")
	b.WriteString("authorizer:\n")
	b.WriteString("  url: http://topaz:9393\n")
	b.WriteString("  timeout: 2s\n")
	b.WriteString("  cache_ttl: 5s\n")
	b.WriteString("  negative_cache_ttl: 2s\n")
	b.WriteString("services:")
	b.Write(list)
	return b.Bytes(), nil
}

// rewriteListPermissionSeparator replaces oldSep with newSep in list_permission values
// so Topaz presets emit Topaz-valid permission names without colons.
func rewriteListPermissionSeparator(yml []byte, oldSep, newSep string) []byte {
	if oldSep == newSep {
		return yml
	}
	lines := strings.Split(string(yml), "\n")
	for i, line := range lines {
		if idx := strings.Index(line, "list_permission:"); idx >= 0 {
			prefix := line[:idx+len("list_permission:")]
			rest := line[idx+len("list_permission:"):]
			lines[i] = prefix + strings.ReplaceAll(rest, oldSep, newSep)
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

// rewriteServicePermissionsSeparator replaces oldSep with newSep in `permissions:`
// values inside a service YAML file without touching URLs or comments.
func rewriteServicePermissionsSeparator(yml []byte, oldSep, newSep string) []byte {
	if oldSep == newSep {
		return yml
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(yml, &doc); err != nil {
		return yml
	}
	var walk func(n *yaml.Node, inPerms bool)
	walk = func(n *yaml.Node, inPerms bool) {
		switch n.Kind {
		case yaml.DocumentNode:
			for _, c := range n.Content {
				walk(c, false)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				walk(v, k.Value == "permissions")
			}
		case yaml.SequenceNode:
			for _, c := range n.Content {
				walk(c, inPerms)
			}
		case yaml.ScalarNode:
			if inPerms && n.Value != routegen.AnyMember {
				n.Value = strings.ReplaceAll(n.Value, oldSep, newSep)
			}
		}
	}
	walk(&doc, false)
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return yml
	}
	_ = enc.Close()
	return out.Bytes()
}

// stripRBACKeys removes top-level roles, creator_role, custom_roles, and
// permissions blocks (mutually exclusive with top-level authorizer) and any
// immediately preceding comment block describing roles.
func stripRBACKeys(yml []byte) []byte {
	lines := strings.Split(string(yml), "\n")
	var out []string
	skipping := false
	for _, line := range lines {
		isTopKey := len(line) > 0 && line[0] != ' ' && line[0] != '\t' && line[0] != '#'
		if isTopKey {
			key, _, _ := strings.Cut(line, ":")
			switch key {
			case "roles", "creator_role", "custom_roles", "permissions":
				if key == "roles" {
					for len(out) > 0 && strings.HasPrefix(out[len(out)-1], "#") {
						out = out[:len(out)-1]
					}
				}
				skipping = true
				continue
			default:
				skipping = false
			}
		}
		if skipping {
			// Stop skipping on a top-level comment that introduces the next section.
			if strings.HasPrefix(line, "#") {
				skipping = false
				out = append(out, line)
			}
			continue
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
}

func appendServiceEntry(yml []byte, rel string) []byte {
	s := strings.TrimRight(string(yml), "\n")
	return []byte(s + "\n  - " + rel + "\n")
}

func presetConfig(preset string) (plat5YML string, extras []projectFile, hasProject bool) {
	switch preset {
	case presetSaaS:
		return saasPresetYML, nil, true
	case presetEnterprise:
		return enterprisePresetYML, nil, true
	case presetPublic:
		return publicPresetYML, []projectFile{
			{rel: "services/portal.yml", data: []byte(publicPortalServiceYML(config.DefaultPermissionSeparator))},
		}, true
	default:
		data, _ := initFiles.ReadFile("initfiles/plat5.yml")
		return string(data), nil, false
	}
}

const saasPresetYML = `# The deployment's config (https://github.com/plat5dev/plat5/blob/master/docs/concepts/config.md).
# Preset: saas (organization -> project, default-deny projects, guest invites).
deletion_grace: 30d
reserved_slugs: [new, settings, api, admin]
kinds:
  organization:
    path: /orgs/{organization}
    list_permission: organization:read
  project:
    parent: organization
    path: /projects/{project}
    list_permission: project:read
    bind_creator: false     # creators reach projects through roles bound above
roles:
  admin:
    permissions: ["*"]
    bindable_at: [organization, project]
  member:
    permissions: [organization:read]
    bindable_at: [organization]
  developer:
    includes: [member]
    permissions: [project:read, project:write, project:bindings:read]
    bindable_at: [organization, project]
  viewer:
    includes: [member]
    permissions: [project:read]
    bindable_at: [organization, project]
creator_role: admin
rate_limits:
  standard: { requests: 100, window: 1s }
defaults:
  rate_limit: [standard]
services:
  - services/identity.yml
  - services/audit.yml
`

const enterprisePresetYML = `# The deployment's config (https://github.com/plat5dev/plat5/blob/master/docs/concepts/config.md).
# Preset: enterprise (organization -> project -> environment, custom roles, tiered rate limits, SSO policy claims).
deletion_grace: 30d
reserved_slugs: [new, settings, api, admin]
policy_claims: [amr, idp_connection]
kinds:
  organization:
    path: /orgs/{organization}
    list_permission: organization:read
  project:
    parent: organization
    path: /projects/{project}
    list_permission: project:read
    bind_creator: false
  environment:
    parent: project
    path: /envs/{environment}
    list_permission: project:read
    bind_creator: false
roles:
  admin:
    permissions: ["*"]
    bindable_at: [organization, project, environment]
  member:
    permissions: [organization:read]
    bindable_at: [organization]
  developer:
    includes: [member]
    permissions: [project:read, project:write, project:bindings:read]
    bindable_at: [organization, project, environment]
  viewer:
    includes: [member]
    permissions: [project:read]
    bindable_at: [organization, project, environment]
creator_role: admin
custom_roles: true
permissions:
  organization:read:
    description: Read organization metadata
    bindable_at: [organization]
  project:read:
    description: Read projects and environments
    bindable_at: [organization, project, environment]
  project:write:
    description: Modify projects and environments
    bindable_at: [organization, project, environment]
  audit:read:
    description: Read organization and project audit logs
    bindable_at: [organization, project]
  organization:bindings:write:
    description: Manage organization access bindings, groups, and security policy
    custom_role_assignable: false
  project:bindings:write:
    description: Manage project access bindings and service accounts
    custom_role_assignable: false
rate_limits:
  standard:
    requests: 100
    window: 1s
  org_api:
    requests: 200
    window: 1s
    count_at: organization
    tiers:
      free: { requests: 60, window: 1s }
      pro: { requests: 500, window: 1s }
      enterprise: { requests: 2000, window: 1s }
  project_api:
    requests: 100
    window: 1s
    count_at: project
    tiers:
      free: { requests: 30, window: 1s }
      pro: { requests: 250, window: 1s }
      enterprise: { requests: 1000, window: 1s }
defaults:
  rate_limit: [standard]
services:
  - services/identity.yml
  - services/audit.yml
`

const publicPresetYML = `# The deployment's config (https://github.com/plat5dev/plat5/blob/master/docs/concepts/config.md).
# Preset: public (subdomain host routing on organization, public and tenant portal routes).
deletion_grace: 30d
reserved_slugs: [new, settings, api, admin]
kinds:
  organization:
    path: /orgs/{organization}
    list_permission: organization:read
    host: "{organization}.localhost"
  project:
    parent: organization
    path: /projects/{project}
    list_permission: project:read
    bind_creator: false
roles:
  admin:
    permissions: ["*"]
    bindable_at: [organization, project]
  member:
    permissions: [organization:read]
    bindable_at: [organization]
  developer:
    includes: [member]
    permissions: [project:read, project:write]
    bindable_at: [organization, project]
  viewer:
    includes: [member]
    permissions: [project:read]
    bindable_at: [organization, project]
creator_role: admin
rate_limits:
  standard: { requests: 100, window: 1s }
defaults:
  rate_limit: [standard]
services:
  - services/identity.yml
  - services/audit.yml
  - services/portal.yml
`

func publicPortalServiceYML(sep string) string {
	return fmt.Sprintf(`# Example portal service for the public preset: unauthenticated routes under
# scopes.public and authenticated tenant routes under scopes.project.
portal:
  url: http://portal:3000
  scopes:
    public:
      routes:
        - path: /public/announcements/{slug}
          methods: [GET]
    project:
      upstream_prefix: /projects/{project.id}
      routes:
        - path: /announcements
          methods:
            GET:  { permissions: project%sread }
            POST: { permissions: project%swrite }
`, sep, sep)
}

func topazDocsServiceYML(scopeKind string) string {
	prefix := "/orgs/{organization.id}"
	resourceID := "{organization.id}:{doc_id}"
	if scopeKind == "project" {
		prefix = "/projects/{project.id}"
		resourceID = "{project.id}:{doc_id}"
	}
	return fmt.Sprintf(`# Example record-level authorization routes backed by Topaz (AuthZEN 1.0).
# Test decisions right away with POST /access/routes after plat5 start,
# or point url: at your own service container.
docs:
  url: http://docs:3000
  scopes:
    %s:
      upstream_prefix: %s
      routes:
        - path: /docs/{doc_id}
          resource: { type: document, id: %q }
          methods:
            GET:    { permissions: docs_read }
            PATCH:  { permissions: docs_write }
            DELETE: { permissions: docs_delete }
`, scopeKind, prefix, resourceID)
}

func topazDocsManifest(hasProject bool) string {
	var b strings.Builder
	b.WriteString("# yaml-language-server: $schema=https://www.topaz.sh/schema/manifest.json\n")
	b.WriteString("# Starter ReBAC manifest for Plat5 + Topaz (OpenID AuthZEN 1.0).\n")
	b.WriteString("# Topaz permission identifiers use underscores (colons are forbidden).\n")
	b.WriteString("# Edit this file and run `plat5 authorizer sync` to update Topaz.\n")
	b.WriteString(`model:
  version: 3

types:
  member: {}

  organization:
    relations:
      admin: member
      viewer: member
    permissions:
      organization_read: viewer | admin
      organization_write: admin
      organization_delete: admin
      organization_bindings_read: admin
      organization_bindings_write: admin
      project_create: admin
      audit_read: viewer | admin
`)
	if hasProject {
		b.WriteString(`
  project:
    relations:
      organization: organization
      admin: member
      editor: member
      viewer: member
    permissions:
      project_read: viewer | editor | admin | organization->admin
      project_write: editor | admin | organization->admin
      project_delete: admin | organization->admin
      project_bindings_read: admin | organization->admin
      project_bindings_write: admin | organization->admin
      guests_write: admin | organization->admin
      audit_read: viewer | editor | admin | organization->admin
`)
	}
	b.WriteString(`
  document:
    relations:
      owner: member
      editor: member
      viewer: member
    permissions:
      docs_read: viewer | editor | owner
      docs_write: editor | owner
      docs_delete: owner
`)
	return b.String()
}

const topazStarterManifest = `# yaml-language-server: $schema=https://www.topaz.sh/schema/manifest.json
# Starter ReBAC manifest for Plat5 + Topaz (OpenID AuthZEN 1.0).
# Topaz permission identifiers use underscores (colons are forbidden).
# Add ` + "`resource: { type: task, id: \"{project.id}:{task_id}\" }`" + ` on routes in
# services/api.yml, then run ` + "`plat5 authorizer sync`" + ` after editing this file.
model:
  version: 3

types:
  member: {}

  organization:
    relations:
      admin: member
      viewer: member
    permissions:
      organization_read: viewer | admin
      organization_write: admin
      organization_delete: admin
      organization_bindings_read: admin
      organization_bindings_write: admin
      project_create: admin
      audit_read: viewer | admin

  project:
    relations:
      organization: organization
      admin: member
      editor: member
      viewer: member
    permissions:
      project_read: viewer | editor | admin | organization->admin
      project_write: editor | admin | organization->admin
      project_delete: admin | organization->admin
      project_bindings_read: admin | organization->admin
      project_bindings_write: admin | organization->admin
      guests_write: admin | organization->admin
      audit_read: viewer | editor | admin | organization->admin

  task:
    relations:
      owner: member
      editor: member
      viewer: member
    permissions:
      project_read: viewer | editor | owner
      project_write: editor | owner
      task_delete: owner
`
