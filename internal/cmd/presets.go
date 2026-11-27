package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/routegen"
)

const (
	presetMinimal    = "minimal"
	presetSaaS       = "saas"
	presetEnterprise = "enterprise"
	presetPublic     = "public"
)

type namedOption struct {
	Name        string
	Description string
}

var presetOptions = []namedOption{
	{presetMinimal, "One kind (organization), admin and member roles"},
	{presetSaaS, "organization -> project, default-deny projects, guest invites"},
	{presetEnterprise, "organization -> project -> environment, custom roles, tiered rate limits, SSO claims"},
	{presetPublic, "organization (subdomain host) -> project"},
}

func presetNames() []string {
	out := make([]string, len(presetOptions))
	for i, o := range presetOptions {
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

// writePresetFiles writes plat5.yml and generated identity/audit service files
// for the chosen preset. Existing files are kept.
func writePresetFiles(dir, preset string) error {
	if preset == "" {
		preset = presetMinimal
	}
	if preset == presetMinimal {
		return writeInitFiles(dir)
	}

	ymlBytes := []byte(presetConfig(preset))

	kinds, err := routegen.ReadKinds(ymlBytes)
	if err != nil {
		return fmt.Errorf("preset %s kinds: %w", preset, err)
	}
	files := []projectFile{
		{rel: "plat5.yml", data: ymlBytes},
	}
	identityFile, auditFile := routegen.Generate(kinds, routegen.Options{
		PermissionSeparator: config.DefaultPermissionSeparator,
	})
	for _, gen := range []routegen.File{identityFile, auditFile} {
		rel := filepath.ToSlash(filepath.Join("services", gen.Service+".yml"))
		rendered, err := routegen.Render(gen, nil)
		if err != nil {
			return fmt.Errorf("render %s: %w", rel, err)
		}
		files = append(files, projectFile{rel: rel, data: rendered})
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

func presetConfig(preset string) string {
	switch preset {
	case presetSaaS:
		return saasPresetYML
	case presetEnterprise:
		return enterprisePresetYML
	case presetPublic:
		return publicPresetYML
	default:
		data, _ := initFiles.ReadFile("initfiles/plat5.yml")
		return string(data)
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
    tiers:
      free: { requests: 60, window: 1s }
      pro: { requests: 500, window: 1s }
      enterprise: { requests: 2000, window: 1s }
defaults:
  rate_limit: [standard]
services:
  - services/identity.yml
  - services/audit.yml
`

const publicPresetYML = `# The deployment's config (https://github.com/plat5dev/plat5/blob/master/docs/concepts/config.md).
# Preset: public (subdomain host routing on organization).
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
`
