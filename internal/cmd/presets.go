package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	{presetPublic, "organization (subdomain host) -> project (allow_public), public portal route"},
}

var authorizerOptions = []namedOption{
	{authorizerNone, "Built-in Plat5 RBAC only"},
	{authorizerTopaz, "Local Topaz AuthZEN 1.0 PDP with starter ReBAC manifest and record-level routes"},
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

	if authorizer == authorizerTopaz {
		var err error
		ymlBytes, err = injectTopazAuthorizer(ymlBytes)
		if err != nil {
			return err
		}
		ymlBytes = appendServiceEntry(ymlBytes, "services/docs.yml")
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
	identityFile, auditFile := routegen.Generate(kinds)
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

// injectTopazAuthorizer inserts an authorizers.topaz block before services: in
// plat5.yml. Refuses a config that already declares authorizers:.
func injectTopazAuthorizer(base []byte) ([]byte, error) {
	var top map[string]any
	if err := yaml.Unmarshal(base, &top); err != nil {
		return nil, fmt.Errorf("plat5.yml: %w", err)
	}
	if _, ok := top["authorizers"]; ok {
		return nil, fmt.Errorf("plat5.yml already sets authorizers: remove it or omit --authorizer")
	}
	head, list, ok := bytes.Cut(append([]byte("\n"), base...), []byte("\nservices:"))
	if !ok {
		return nil, fmt.Errorf("plat5.yml has no services: list")
	}
	var b bytes.Buffer
	b.Write(head[1:])
	b.WriteString("\n# External OpenID AuthZEN 1.0 authorizer (https://github.com/plat5dev/plat5/blob/master/docs/reference/config/authorizers.md).\n")
	b.WriteString("# Runs locally via the authorizer module (topaz/manifest.yaml).\n")
	b.WriteString("authorizers:\n")
	b.WriteString("  topaz:\n")
	b.WriteString("    url: http://topaz:8383\n")
	b.WriteString("    timeout: 2s\n")
	b.WriteString("    cache_ttl: 5s\n")
	b.WriteString("    negative_cache_ttl: 2s\n")
	b.WriteString("services:")
	b.Write(list)
	return b.Bytes(), nil
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
			{rel: "services/portal.yml", data: []byte(publicPortalServiceYML)},
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
  project:
    parent: organization
    path: /projects/{project}
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
# Preset: enterprise (organization -> project -> environment, custom roles, tiered rate limits).
deletion_grace: 30d
reserved_slugs: [new, settings, api, admin]
kinds:
  organization:
    path: /orgs/{organization}
  project:
    parent: organization
    path: /projects/{project}
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
# Preset: public (subdomain host routing on organization, public principal access on project).
deletion_grace: 30d
reserved_slugs: [new, settings, api, admin]
kinds:
  organization:
    path: /orgs/{organization}
    host: "{organization}.localhost"
  project:
    parent: organization
    path: /projects/{project}
    allow_public: true
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

const publicPortalServiceYML = `# Example public-capable service for the public preset.
# Bind public_principal: all_users (or all_authenticated_users) with role: viewer
# on a project node to expose GET /announcements without membership.
portal:
  url: http://portal:3000
  scopes:
    project:
      upstream_prefix: /projects/{project.id}
      routes:
        - path: /announcements
          methods:
            GET:
              required_permissions: [project:read]
              allow_public: true
            POST:
              required_permissions: [project:write]
`

func topazDocsServiceYML(scopeKind string) string {
	prefix := "/orgs/{organization.id}"
	if scopeKind == "project" {
		prefix = "/projects/{project.id}"
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
          methods:
            GET:
              required_permissions: [docs:read]
              authorizer:
                name: topaz
                mode: or      # org/project RBAC allows admins; others check Topaz document grant
                resource: { type: document, id: "{doc_id}" }
            PATCH:
              required_permissions: [docs:write]
              authorizer:
                name: topaz
                mode: and     # requires both Plat5 RBAC and Topaz editor/owner on this document
                resource: { type: document, id: "{doc_id}" }
            DELETE:
              required_permissions: [docs:delete]
              authorizer:
                name: topaz
                mode: replace # Topaz document owner decides alone
                resource: { type: document, id: "{doc_id}" }
`, scopeKind, prefix)
}

func topazDocsManifest(hasProject bool) string {
	var b strings.Builder
	b.WriteString("# yaml-language-server: $schema=https://www.topaz.sh/schema/manifest.json\n")
	b.WriteString("# Starter ReBAC manifest for Plat5 + Topaz (OpenID AuthZEN 1.0).\n")
	b.WriteString("# Edit this file and run `plat5 authorizer sync` to update Topaz.\n")
	b.WriteString("model:\n  version: 3\n\ntypes:\n  member: {}\n")
	if hasProject {
		b.WriteString(`
  project:
    relations:
      viewer: member
      editor: member
    permissions:
      project:read: viewer | editor
      project:write: editor
`)
	}
	b.WriteString(`
  document:
    relations:
      owner: member
      editor: member
      viewer: member
    permissions:
      docs:read: viewer | editor | owner
      docs:write: editor | owner
      docs:delete: owner
`)
	return b.String()
}

const topazStarterManifest = `# yaml-language-server: $schema=https://www.topaz.sh/schema/manifest.json
# Starter ReBAC manifest for Plat5 + Topaz (OpenID AuthZEN 1.0).
# Attach ` + "`authorizer: { name: topaz, mode: or, resource: { type: task, id: \"{task_id}\" } }`" + `
# to routes in services/api.yml, then run ` + "`plat5 authorizer sync`" + ` after editing this file.
model:
  version: 3

types:
  member: {}

  project:
    relations:
      viewer: member
      editor: member
    permissions:
      project:read: viewer | editor
      project:write: editor

  task:
    relations:
      owner: member
      editor: member
      viewer: member
    permissions:
      project:read: viewer | editor | owner
      project:write: editor | owner
      task:delete: owner
`
