# Composable `init`, `add` & Registry Roadmap

Phased rollout across `cli/` and the sibling default registry repo (`../registry` → `plat5dev/registry`). Each phase ships standalone user value.

## Phase 1 — Runtime seams (`cli/`)

Unblock external IdPs, Operator SSO, Grafana Cloud / OTLP vendors, and project-owned containers in `plat5 start` (see [`reference/local-config.md`](reference/local-config.md)).

- [ ] Add `.env.local` loading + `${VAR}` interpolation in `internal/config` and `internal/stack` (plus gitignore check in `plat5 doctor`).
- [ ] Add `idp.client_id` and forward `PLAT5_AUTH_CLIENT_ID`, `PLAT5_AUTH_AUDIENCE`, `PLAT5_AUTH_SUBJECT_CLAIM` in `stack.AppEnv()`.
- [ ] Add `operator.idp` support in `internal/config`, `internal/stack`, and `internal/bundle/stack/operator.yml` (skip `operator-auth` when external IdP is set).
- [ ] Split `lgtm` and `alloy` compose files in `internal/bundle/stack/` to support `observability.mode: alloy-only` and `otel.headers`.
- [ ] Extend `urlHosts()` auto-networking to `operator.yml`, `idp.jwks_uri`, `operator.idp.jwks_uri`, and `otel.endpoint`.
- [ ] Bump `config.SchemaVersion` and add unit tests.

## Phase 2 — `registry` + `plugin` + `blueprint` engine (`cli/`)

Replace `scaffold.go` / `template/` with the pure `Plan`-based composition engine (see [`concepts/composition.md`](concepts/composition.md), [`reference/plugin.md`](reference/plugin.md), [`reference/registry.md`](reference/registry.md)).

- [ ] `internal/registry`: `registry.yml` parser + schema validator, `$XDG_CONFIG_HOME/plat5/registries.yml`, ETag cache, `cli:` semver matching, `owner/repo//subdir@ref`, `GITHUB_TOKEN`/`GH_TOKEN`, `--sources` local registry support, and `plat5 registry` subcommands (`list`, `add`, `remove`, `update`, `search`, `validate`).
- [ ] `internal/plugin`: `plat5.plugin.yml` parser + strict validation, `requires`/`conflicts` checker.
- [ ] `internal/blueprint`: pure `Build(Spec, Plugins) (Plan, error)` — `[[ ]]` template renderer, RFC 7396 fragment merging for `plat5.yml` / `plat5.local.yml` / `operator.yml`, multi-service `type: service` (`0..N` + `--as <name>`), `routegen.Generate` with resolved `permission_separator`, in-memory `plat5yml.Config.Validate()` (including cross-service route overlap detection), `.env.local` (`0600`) + `.env.example` + `.gitignore` emission, reproducible CLI command formatter.
- [ ] Refactor `internal/cmd/init.go` to struct-bound flags (no package globals) + repeatable `--service` + `--spec <file|->` + `--dry-run`.
- [ ] Add `plat5 add <ref> [--slot <slot>] [--as <name>] [--dry-run]` sharing `internal/blueprint` against an existing project directory.
- [ ] Matrix tests verifying `preset × service(s) × authorizer × idp` combinations in memory against `plat5yml.Validate()`.

## Phase 3 — First-party default registry (`../registry`) & Bubble Tea wizard (`cli/`)

Dogfood the registry for all first-party layers and launch the interactive setup wizard.

- [ ] Initialize sibling repo `../registry` (`plat5dev/registry`) with `registry.yml` and CI running `plat5 registry validate`.
- [ ] Publish initial first-party plugins in `../registry`:
  - `preset/*`: `minimal`, `saas`, `enterprise`, `public`
  - `idp/*`: `plat5-auth`, `auth0`, `clerk`, `okta`, `entra`, `cognito`, `keycloak` (local Docker), `custom-oidc`
  - `observability/*`: `local-lgtm`, `grafana-cloud`, `honeycomb`, `axiom`, `custom-otlp`
  - `recipe/*`: `quickstart`, `b2b-saas`, `enterprise-sso`
- [ ] Migrate `template-go-fiber-api`, `template-bun-effect-api`, `template-node-fastify-api`, `template-solid-customer-console`, and `template-solid-operator-console` to `plat5.plugin.yml` (`services/service.yml` with `[[.Perm ...]]` + `[[.ServiceName]]`) and register them in `../registry`.
- [ ] `internal/wizard`: Bubble Tea shell wrapping `huh` groups — Quickstart vs. Custom mode, step rail, Back navigation, live stack summary, async `verify` spinners (JWKS / OTLP checks).
- [ ] Embed a minimal fallback copy of the default `registry.yml` in `cli/` for offline first runs.

## Phase 4 — Pluggable Authorization (`../registry`)

- [ ] Add `authorizer/*` plugins usable in `authz`, `operator-authz`, or both:
  - `plat5-rbac`
  - `cerbos` (Docker + starter ABAC/RBAC policies + AuthZEN 1.0)
  - `topaz` (Docker + starter ReBAC manifest + `permission_separator: "_"`)
  - `openfga` (Docker + starter DSL model + AuthZEN bridge)
  - `custom-authzen` (URL + optional `token_env` → `.env.local`)
- [ ] Optional follow-on: reusable lifecycle-sync sidecar feeding Identity's lifecycle log into Topaz / OpenFGA so service plugins stay authz-agnostic for node hierarchy sync.

## Phase 5 — DDD Service Slices, CI & Delivery Packs (`../registry`)

- [ ] Example plug-and-play `service/*` domain slices (e.g. `stripe-billing`, `notifications`, `webhooks`) demonstrating `plat5 add <slice>` with permissions, operator routes, and `.env.local` secrets.
- [ ] `ci/github-actions` (PR `validate` + `plan`, merge OIDC `apply`, Docker build/push to GHCR).
- [ ] `deploy/env-overlays` (`plat5.staging.yml`, `plat5.prod.yml`).
- [ ] `deploy/k8s-argo` / `deploy/helm` / `deploy/compose-prod`.
- [ ] Security & compliance toggles (`secret_brand`, `gateway.upstream_signing`, `audit_mode`, control-plane `AUTH_CONFIG_FILE`, generated `AGENTS.md`).

## Phase 6 — Community Docs & Publishing (`web/`)

- [ ] Publish plugin & registry authoring guides and searchable registry catalog on `web/src/content/docs/`.
