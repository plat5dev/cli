# Project composition

This doc defines the rules for `plat5 init`, `plat5 add`, plugins, and registries. Reference shapes live in [`../reference/plugin.md`](../reference/plugin.md) and [`../reference/registry.md`](../reference/registry.md).

## Terminology

- **`kinds`** — reserved exclusively for Plat5's resource hierarchy (`organization`, `project`, `environment`) in `plat5.yml`.
- **`type`** — what a plugin *is* in `plat5.plugin.yml` and `registry.yml` (`preset`, `idp`, `authorizer`, `service`, `customer-console`, `operator-console`, `observability`, `ci`, `deploy`, `recipe`).
- **`slot`** — where a plugin attaches in a project (e.g. a `type: idp` plugin can fill either the `auth` slot or the `operator-auth` slot; a `type: authorizer` plugin can fill `authz` or `operator-authz`).

## Principles

1. **First-party uses the community system.** Official plugins (`go-fiber-api`, `auth0`, `cerbos`, `grafana-cloud`, `stripe-billing`, `github-actions`, …) are authored in the exact same `plat5.plugin.yml` format and distributed through the exact same `registry.yml` mechanism as third-party plugins. The only special thing about the official registry (`plat5dev/registry`) is that it is preconfigured as the default.
2. **Protocols are the boundaries, not source-code branches.** Layers compose at open standards—RFC 9068 JWT/JWKS for authentication, OpenID AuthZEN 1.0 for authorization, OTLP for telemetry, and Docker Compose `include:` for local runtime—not by templating `if auth0 && cerbos` conditionals across service source files.
3. **Multi-service by default (plug-and-play DDD slices).** Plat5's gateway routes to any number of independent services (`services/*.yml`). `type: service` plugins are `0..N`: a project can start with one API starter (`plat5 init --service go-fiber-api`) and add polyglot domain slices anytime (`plat5 add stripe-billing`, `plat5 add notifications`, or `plat5 add go-fiber-api --as orders`). Every slice shares the project's tenancy tree, authn, authz, audit log, rate limits, and OTEL tracing.
4. **Declarative and safe.** Running `plat5 init` or `plat5 add` executes no untrusted plugin scripts. Plugins declare manifests, static files, `[[ ]]` templates, prompts, optional read-only HTTP verification probes, and structured YAML merge-patches.
5. **Projects own what init/add writes.** Generated services, policies, Compose files, and CI workflows belong to the project, not to a runtime dependency.
6. **Valid before touching disk.** Every assembled `plat5.yml` and its `services/*.yml`—including cross-service route collision checks—are validated in memory against Plat5's embedded JSON Schemas before a single file is written.
7. **Interactive and scriptable stay equivalent.** Every interactive wizard run produces a typed `Spec` and prints the exact non-interactive `plat5 init` flags that reproduce it.

## Managed core vs. project-owned components

To avoid growing hardcoded modules in the CLI for every third-party tool, local runtime ownership splits cleanly in two:

| Owned by | What lives there | How `plat5 start` runs it |
|----------|------------------|---------------------------|
| **CLI (`bundle/stack/`)** | Plat5 core (`gateway`, `control-plane`, `identity`, `audit`, `postgres`, `valkey`) and optional first-party sidecars (`plat5-auth`, `operator`, `lgtm`, `alloy`) | Embedded Compose files in the `plat5-<project_id>` compose project |
| **Project infra (`docker-compose.infra.yml`)** | Project-owned infrastructure: external AuthZEN PDPs (`cerbos`, `topaz`, `openfga`), local IdPs (`keycloak`, `dex`), dev mail (`mailpit`) | `include:`d in `docker-compose.infra.yml` (`infra.compose`), auto-joined to the `plat5` network; **stays running** with `plat5 start -x app` |
| **Project apps (`docker-compose.yml`)** | Application services & DDD slices (`api/`, `billing/`, `orders/`), customer & operator consoles | `include:`d in `docker-compose.yml` (`app.compose`), auto-joined to the `plat5` network; skipped by `plat5 start -x app` so you can run apps on your host |

`plat5 start` discovers which project services must join the `plat5` network by reading hostnames referenced in:

- `plat5.yml` (`services.*.url` and `authorizer.url`)
- `operator.yml` (`authorizer.url` and `services.*.url`)
- `plat5.local.yml` (`idp.jwks_uri`, `operator.idp.jwks_uri`, and `otel.endpoint`)

Any project-owned container referenced by one of those URLs is automatically reachable from Plat5's gateways without modifying the CLI. Inside the `plat5` network, services are addressed by container name (`http://billing:3000`, `http://api:3000`), so multiple slices can listen on the same internal port without colliding.

## Slots and cardinality

A project `Spec` attaches plugins to orthogonal slots:

| Slot | Accepts plugin `type` | Cardinality | Role |
|------|-----------------------|-------------|------|
| `preset` | `preset` | 0..1 | Base `plat5.yml`: hierarchy (`kinds`), default RBAC `roles`, `rate_limits`, `deletion_grace`, `policy_claims` |
| `auth` | `idp` | 0..1 (default `plat5-auth`) | Customer authentication (`modules.auth` or `idp:` block, optional local IdP container) |
| `authz` | `authorizer` | 0..1 (default `plat5-rbac`) | Customer authorization (built-in RBAC or external AuthZEN 1.0 PDP + starter policies) |
| `service` | `service` | **0..N** | Application APIs and plug-and-play domain slices (`services/<name>.yml`, `<name>/`, compose include, optional roles/permissions/operator routes) |
| `customer-console` | `customer-console` | 0..1 | Customer UI (`customer-console/`, compose file, `app.web`) |
| `operator-console` | `operator-console` | 0..1 | Operator UI (`operator-console/`, compose file, `operator.console`) |
| `operator-auth` | `idp` | 0..1 | Operator authentication (`operator.idp:`) |
| `operator-authz` | `authorizer` | 0..1 | Operator authorization (`operator.yml` `authorizer:`) |
| `observability` | `observability` | 0..1 (default `local-lgtm`) | Telemetry pipeline (`local-lgtm`, `grafana-cloud`, OTLP vendor, or `none`) |
| `ci` | `ci` | 0..N | Additive CI workflows (GitHub Actions, GitLab CI, Docker build/push) |
| `deploy` | `deploy` | 0..N | Additive deployment assets (environment overlays, Helm, Kustomize, ArgoCD) |

`type: recipe` is a shorthand bundle that pre-populates a set of slots (e.g. `enterprise-b2b`). Individual flags override recipe defaults.

Single-select slots (`0..1`) implicitly replace/conflict with another plugin in the same slot. Multi-select slots (`0..N`) compose additively; two plugins writing the same output path, registering the same service name, or declaring overlapping HTTP routes fail during planning/validation before touching disk.

## The assembly pipeline

Both `plat5 init` and `plat5 add` execute the same six stages. Stages 1–5 perform zero writes to the project directory.

```text
1. Resolve    Fetch manifests and files from registries, owner/repo//subdir@ref,
              .tar.gz URLs, or local paths; match cli: semver floors.
2. Check      Verify requires / conflicts across all selected plugins (and existing
              project state for plat5 add).
3. Prompt     Collect missing prompt values (flags/spec → interactive wizard → defaults);
              optionally run read-only verify HTTP probes.
4. Assemble   Apply fragments in deterministic order to build an in-memory Plan:
                a. preset             → base plat5.yml (kinds, roles, rate_limits)
                b. authz              → patch plat5.yml (keep RBAC or set authorizer: + null RBAC)
                c. service (0..N)     → copy each service dir, render services/<name>.yml with
                                        resolved permission_separator, merge optional permissions/
                                        roles/operator routes, add compose include
                d. routegen.Generate  → synthesize services/identity.yml + services/audit.yml
                                        for the assembled kinds, separator, and claimed routes
                e. auth / operator    → patch plat5.local.yml and operator.yml
                f. consoles           → copy console dirs, wire origins & compose includes
                g. observability      → patch plat5.local.yml (modules / observability / otel)
                h. ci / deploy        → render additive workflows, overlays, deploy files
                i. hygiene            → assemble .env.example, .env.local, .gitignore, README.md
5. Validate   Run plat5yml.Config.Validate() on the assembled plat5.yml + services/*.yml
              (verifying schema rules, referenced kinds, and cross-service route overlaps),
              plus validate plat5.local.yml and operator.yml in memory.
6. Emit       Apply the Plan to disk (refusing unexpected overwrites unless --force),
              writing .env.local with mode 0600, and print the reproducible CLI command.
```

### Why `routegen.Generate` runs in Stage 4d

Running `routegen.Generate` after `preset`, `authz`, and all `service` plugins guarantees two things automatically:

- **Permission separator adaptation:** If an authorizer declares `capabilities.permission_separator: "_"` (e.g. Topaz, which forbids `:`), `services/identity.yml`, `services/audit.yml`, and every `[[.Perm "billing" "read"]]` in `services/*.yml` and `plat5.yml` use `_` consistently, and `routes.permission_separator: "_"` is written to `plat5.local.yml`.
- **Claimed-route deduplication:** If a service plugin wraps a lifecycle route such as `POST /orgs`, `routegen.Generate` omits it from `services/identity.yml` automatically.

## Secret hygiene

Plugins may prompt for credentials (Stripe API keys, Grafana Cloud tokens, OTLP headers, AuthZEN PDP bearer tokens, IdP client secrets). The engine enforces three hard rules:

1. Any prompt with `type: secret` or `env` entry with `secret: true` is **never** written into `plat5.local.yml`, `plat5.yml`, `operator.yml`, or `docker-compose.yml`.
2. Secret values are written **only** to `.env.local` (file mode `0600`). Config files reference them by `${ENV_VAR}` interpolation or `token_env: ENV_VAR`.
3. Every project with a `.env.local` also gets `.env.example` (key names + comments, no secret values) and `.env.local` added to `.gitignore`.

## Day-2 additions (`plat5 add`)

Because `blueprint.Build` emits an explicit `Plan` (`CreateFile`, `MergeYAML`, `AppendLines`, `AddComposeInclude`), `plat5 add <ref>` reuses the same pipeline against an existing project directory:

```bash
plat5 add stripe-billing                  # type: service inferred from manifest → billing/ + services/billing.yml
plat5 add go-fiber-api --as orders        # instantiate a generic starter under a custom service name
plat5 add github-actions                  # type: ci inferred → .github/workflows/*
plat5 add okta --slot operator-auth       # disambiguate slot when a type can attach to either plane
plat5 add stripe-billing --dry-run        # preview file diff and config changes without writing
```

- Reads existing `plat5.yml`, `services/*.yml`, `plat5.local.yml`, `operator.yml`, `docker-compose.infra.yml`, and `docker-compose.yml` as the starting state instead of a fresh preset.
- Patches existing YAML files at the `yaml.Node` AST level so hand-written comments and key ordering are preserved.
- Checks `requires.kinds` against the project's actual `plat5.yml` kinds (e.g. a billing slice requiring `organization` works on any project that has an `organization` kind).
- Runs `plat5yml.Config.Validate()` across existing + newly added services before writing, catching route collisions or missing kinds immediately.
- Refuses to overwrite existing non-managed files unless `--force` is passed.

## Package boundaries in `cli/`

To keep composition fast to test and shared by both `init` and `add`, the implementation separates into four packages:

| Package | Responsibility | Side effects |
|---------|---------------|--------------|
| `internal/registry` | Registry config, index fetching, ETag cache, `cli:` version matching, archive/subdir extraction | Network + cache dir only |
| `internal/plugin` | `plat5.plugin.yml` parsing, schema validation, compatibility checking | Read-only FS |
| `internal/blueprint` | Template rendering, YAML fragment merging, `routegen`, in-memory validation, `Plan` generation | Pure (zero TTY, zero stdout, zero disk writes) |
| `internal/wizard` | Interactive Bubble Tea + `huh` shell (Quickstart vs. Custom, Back navigation, live stack summary, async `verify` spinners) | Terminal UI only |

`internal/cmd/init.go` and `internal/cmd/add.go` are thin adapters binding flags onto command-local structs (no package-global flag state), invoking `wizard` when interactive, calling `blueprint.Build`, and applying the resulting `Plan`.
