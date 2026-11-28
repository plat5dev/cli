# Registries (`registry.yml`)

A registry is an index of plugins. The first-party registry (`plat5dev/registry`) and any third-party or private company registry use the exact same format, resolution rules, and CLI commands.

## Registry index format (`registry.yml`)

A registry repo (or HTTPS URL) serves `registry.yml` at its root. Unknown keys are errors.

```yaml
schema: 1
name: plat5
description: Official Plat5 plugin registry

entries:
  - id: auth0
    type: idp
    description: Auth0 OIDC / OAuth 2.0
    tags: [oidc, saas]
    versions:
      - version: 1.0.0
        cli: ">=0.20.0"
        path: idp/auth0

  - id: go-fiber-api
    type: service
    description: Go + Fiber reference API
    tags: [go, fiber, starter]
    versions:
      - version: 1.0.0
        cli: ">=0.20.0"
        source: plat5dev/template-go-fiber-api@v1.0.0

  - id: stripe-billing
    type: service
    description: Stripe subscriptions, checkout, and webhook billing slice
    tags: [billing, stripe, ddd]
    versions:
      - version: 1.0.0
        cli: ">=0.20.0"
        path: service/stripe-billing

  - id: cerbos
    type: authorizer
    description: Cerbos ABAC/RBAC policy engine (OpenID AuthZEN 1.0)
    tags: [authzen, abac, rbac]
    versions:
      - version: 1.0.0
        cli: ">=0.20.0"
        plat5: ">=0.19.0"
        path: authorizer/cerbos
```

### Fields

| Field | Required | Rule |
|-------|----------|------|
| `schema` | yes | Must be `1`. |
| `name` | yes | Default alias for the registry (`[a-z][a-z0-9-]*`). |
| `description` | no | Human-readable description. |
| `entries` | yes | List of plugin entries. `id` must be unique within a registry. |

### Entry fields

| Field | Required | Rule |
|-------|----------|------|
| `id` | yes | `[a-z][a-z0-9-]*`, max 64 chars. |
| `type` | yes | Must match the target plugin's `plat5.plugin.yml` `type`. |
| `description` | yes | Shown in the wizard and `plat5 registry search`. |
| `tags` | no | Search/filter keywords for the wizard. |
| `deprecated` | no | Optional deprecation message; hidden from default wizard lists unless selected explicitly. |
| `versions` | yes | Non-empty list of released versions, ordered newest-first. |

### Version fields

| Field | Required | Rule |
|-------|----------|------|
| `version` | yes | Semver (`X.Y.Z`). |
| `cli` | no | CLI compatibility constraint (`">=0.20.0"`, or range `">=0.20.0 <1.0.0"`). |
| `plat5` | no | Optional Plat5 server compatibility constraint (`">=0.19.0"`). |
| `path` | one of `path` / `source` | Subdirectory inside this registry repository (e.g. `idp/auth0`). |
| `source` | one of `path` / `source` | External plugin location: `owner/repo@ref`, `owner/repo//subdir@ref`, or `https://…/archive.tar.gz`. |
| `sha256` | no | Optional tarball checksum verified before extraction. |

## Version matching

When resolving an entry without an explicit `@version`, the CLI walks `versions` in semver descending order and selects the **first version whose `cli:` constraint matches the running CLI** (dev builds match all `cli:` floors, matching [cliversion.go](../../internal/config/cliversion.go)).

- If an entry exists in the registry but *no* version matches the running CLI, the wizard shows it as unavailable with `(requires plat5 >=X.Y.Z)` and explicit flag selection returns an upgrade hint rather than failing on an unknown manifest key.
- Pinning an exact version (`--auth auth0@1.0.0`) bypasses auto-selection and checks its `cli:` constraint directly.

## Plugin reference syntax

Every slot flag (`--preset`, `--service`, `--auth`, `--authz`, `--operator-auth`, `--operator-authz`, `--customer-console`, `--operator-console`, `--observability`, `--ci`, `--deploy`, `--recipe`) and `plat5 add <ref>` accept the same reference forms:

| Form | Example | Resolution |
|------|---------|------------|
| Bare ID | `stripe-billing` | Default registry first, then registered third-party registries. Errors if ambiguous across two third-party registries. |
| Registry-qualified | `acme/okta-internal` | Looked up only in the `acme` registry. |
| Version-pinned | `auth0@1.0.0` or `acme/okta@2.1.0` | Exact version from the registry. |
| GitHub repo | `owner/repo` or `owner/repo@v1.2.0` | Downloads `https://github.com/owner/repo/archive/<ref>.tar.gz`. |
| GitHub monorepo subdir | `owner/repo//service/stripe-billing@v1.2.0` | Downloads archive and extracts only `service/stripe-billing` as the plugin root. |
| Archive URL | `https://example.com/plugin.tar.gz` | Downloads and extracts `.tar.gz`. |
| Local directory | `./my-plugin` or `/abs/path/my-plugin` | Loads directly from disk (no network). |
| Disabled | `none` | Explicitly turns a single-select slot off. |

For `type: service` plugins, append `--as <name>` (on `plat5 add`) or `<ref>:<name>` (on repeatable `--service` flags, e.g. `--service go-fiber-api:orders`) to override the default `service_name`.

### Private repositories

When fetching from GitHub (either a registry or a plugin archive), the CLI sends `Authorization: Bearer <token>` if `GITHUB_TOKEN` or `GH_TOKEN` is set in the environment. This lets organizations host private registries and internal domain slices without custom infrastructure.

## Local registry configuration & caching

Configured registries live in `$XDG_CONFIG_HOME/plat5/registries.yml` (default `~/.config/plat5/registries.yml`):

```yaml
registries:
  - name: official
    source: plat5dev/registry
    default: true
  - name: acme
    source: acme-corp/plat5-registry
  - name: local-dev
    source: /Users/kyle/kyl/plat5dev/registry
```

- **Default registry:** When `registries.yml` does not exist or omits `official`, `plat5dev/registry` is implicitly active as the default.
- **Caching:** Fetched `registry.yml` indexes are cached under `$XDG_CACHE_HOME/plat5/registries/<name>/` with HTTP `ETag` / `If-None-Match` conditional requests (TTL: 15 minutes unless `plat5 registry update` is run).
- **Offline fallback:** If the network is unreachable, the CLI falls back to the cached index, and finally to a minimal embedded fallback snapshot compiled into the binary for the default registry.
- **Local contributor workflow:** Passing `--sources <dir>` (e.g. `plat5 init my-app --sources ..`) automatically resolves `../registry` as the default registry and `../template-<name>` for any external official repos present in `<dir>`, using the exact same code path as a local third-party registry.

## CLI commands

| Command | Behavior |
|---------|----------|
| `plat5 registry list` | List configured registries and their cached status |
| `plat5 registry add <name> <source>` | Register a GitHub repo (`owner/repo[@ref]`), URL, or local directory |
| `plat5 registry remove <name>` | Remove a registered third-party registry |
| `plat5 registry update [name]` | Force-refresh cached registry index(es) |
| `plat5 registry search [query] [--type <type>]` | Search plugins across all configured registries |
| `plat5 registry validate [dir]` | Offline validator for registry authors: checks `registry.yml`, every inline `plat5.plugin.yml`, `[[ ]]` template syntax, and sample assembly against `plat5yml.Validate()` |
