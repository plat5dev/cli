# Plugin manifest (`plat5.plugin.yml`)

Every plugin—whether an application service / DDD slice, console, IdP recipe, AuthZEN authorizer, observability backend, preset, CI workflow pack, or deployment bundle—is a directory with a `plat5.plugin.yml` manifest at its root.

Unknown keys are errors (strict YAML decoding).

## Full shape

```yaml
schema: 1
name: stripe-billing
type: service
description: Stripe subscriptions, checkout, and webhook billing slice
cli: ">=0.20.0"

service_name: billing

requires:
  kinds: [organization]
  authz: any
  operator: false

conflicts: []

capabilities:
  permission_separator: ":"

prompts:
  - key: stripe_secret_key
    type: secret
    env: STRIPE_SECRET_KEY
    title: Stripe Secret Key (test mode)
    help: Find at https://dashboard.stripe.com/test/apikeys
    required: false

verify:
  http_get: "https://[[.Prompt.domain]]/.well-known/jwks.json"
  expect_status: 200
  label: Checking JWKS endpoint

target: "[[.ServiceName]]"
render:
  - services/service.yml
  - docker-compose.yml
  - src/config.ts
exclude:
  - AGENTS.md

fragments:
  plat5:
    permissions:
      "[[.Perm \"billing\" \"read\"]]":
        description: View billing plan and invoices
        bindable_at: [organization]
      "[[.Perm \"billing\" \"write\"]]":
        description: Manage subscriptions and payment methods
        bindable_at: [organization]
  operator:
    services:
      "[[.ServiceName]]":
        routes:
          - path: /orgs/{organization_id}/comp-plan
            node: organization_id
            methods: [POST]
  services:
    - src: services/service.yml
      dest: "services/[[.ServiceName]].yml"
  compose: docker-compose.yml
  env:
    - name: STRIPE_SECRET_KEY
      value: "[[.Prompt.stripe_secret_key]]"
      secret: true
    - name: STRIPE_WEBHOOK_SECRET
      value: ""
      secret: true
  gitignore:
    - ".env.local"

app:
  compose: docker-compose.yml

next:
  - "Forward webhooks locally: stripe listen --forward-to http://localhost:5400/webhooks/stripe"
```

## Top-level fields

| Field | Required | Rule |
|-------|----------|------|
| `schema` | yes | Must be `1`. |
| `name` | yes | `[a-z][a-z0-9-]*`, max 64 chars. |
| `type` | yes | One of `preset`, `idp`, `authorizer`, `service`, `customer-console`, `operator-console`, `observability`, `ci`, `deploy`, `recipe`. |
| `description` | yes | Short one-line summary shown in the wizard and `plat5 registry search`. |
| `cli` | no | Minimum CLI semver floor (`">=X.Y.Z"`). Checked before loading; same format as `plat5.local.yml`'s `cli:`. |
| `service_name` | no | For `type: service`: default Plat5 service name (`[a-z][a-z0-9-]*`, default: `name`). Overridable by the user via `--as <name>`. Exposed as `[[.ServiceName]]`. |
| `requires` | no | Compatibility prerequisites evaluated before prompting. |
| `conflicts` | no | Explicit incompatibilities beyond single-slot conflicts. |
| `capabilities` | no | Traits exposed to downstream layers during assembly. |
| `prompts` | no | Ordered list of interactive inputs collected from flags, `--spec`, or the wizard. |
| `verify` | no | Optional read-only HTTP preflight check run after prompts complete. |
| `target` | no | Destination subdirectory in the project for copied files (supports `[[ ]]` templates). Defaults by `type` (see table below). `.` copies to project root. |
| `render` | no | Relative paths inside the plugin processed as Go `text/template` with `[[ ]]` delimiters. |
| `exclude` | no | Relative paths or globs excluded when copying files into `target`. |
| `fragments` | no | Structured patches merged into project root files. |
| `app` | no | `compose` (relative to `target`) and `web` (browser origin, required for console types). |
| `next` | no | Human-readable follow-up hints printed in `README.md` and command output. |
| `recipe` | only for `type: recipe` | Map of slot name → plugin reference (or list of references for `0..N` slots like `service`, `ci`, `deploy`). |

## Plugin types and defaults

| `type` | Slot(s) | Cardinality | Default `target` | Notes |
|--------|---------|-------------|------------------|-------|
| `preset` | `preset` | 0..1 | *(no files copied)* | Contributes base `plat5.yml` (`kinds`, `roles`, `rate_limits`) and `plat5.local.yml` overrides |
| `idp` | `auth`, `operator-auth` | 0..1 per slot | `idp` | Customer or operator authentication |
| `authorizer` | `authz`, `operator-authz` | 0..1 per slot | `authorizer` | Built-in RBAC or external AuthZEN 1.0 PDP |
| `service` | `service` | **0..N** | `[[.ServiceName]]` | Application API or plug-and-play DDD slice (`services/<name>.yml` + Compose service) |
| `customer-console` | `customer-console` | 0..1 | `customer-console` | Requires `app.compose` and `app.web` |
| `operator-console` | `operator-console` | 0..1 | `operator-console` | Requires `app.compose` and `app.web` |
| `observability` | `observability` | 0..1 | `observability` | Configures `modules.observability`, `observability.mode`, or `otel:` |
| `ci` | `ci` | 0..N | `.` | Additive CI workflows (e.g. `.github/workflows/*.yml`) |
| `deploy` | `deploy` | 0..N | `.` | Additive deployment assets (`plat5.prod.yml`, `deploy/k8s/`, `deploy/argo/`) |
| `recipe` | shorthand | — | *(no files copied)* | Expands into a set of slot selections; explicit flags override recipe entries |

## `requires`, `conflicts`, and `capabilities`

### `requires`

| Field | Type | Rule |
|-------|------|------|
| `kinds` | `[]string` | Every listed Plat5 kind name (e.g. `[organization, project]`) must exist in `plat5.yml`. |
| `authz` | string | `rbac`, `authzen`, or `any` (default `any`). |
| `authz_style` | string | Optional: `rbac`, `abac`, `rebac`, or `any`. |
| `operator` | bool | When `true`, requires the operator module to be enabled. |

### `conflicts`

List of `{ id: "<name>" }` or `{ type: "<type>", authz_style: "<style>" }` rules that fail fast during Stage 2 (`Check`) with a clear explanation.

### `capabilities`

| Field | Type | Meaning |
|-------|------|---------|
| `permission_separator` | string | Preferred permission separator (`":"`, `"_"`, `"."`, or `"-"`). If set by an authorizer (e.g. `"_"` for Topaz), it overrides `":"` across `routegen` and rendered service files. |
| `authz_style` | string | `rbac`, `abac`, or `rebac`. |
| `dev_seed` | bool | For `idp` plugins: whether `plat5 start` can auto-seed a dev user (`true` only for `plat5-auth`). |

## `prompts`

Prompts are declarative so the CLI can render them in the wizard, accept them via `--set <plugin>.<key>=<val>` or `--spec`, and enforce secret hygiene automatically.

| Field | Required | Rule |
|-------|----------|------|
| `key` | yes | `[a-z][a-z0-9_]*`. Exposed in templates as `[[.Prompt.<key>]]`. |
| `type` | yes | `input`, `secret`, `select`, or `confirm`. |
| `title` | yes | Label shown in the wizard. |
| `help` | no | Secondary help text (e.g. link to provider dashboard). |
| `placeholder` | no | Example value shown in empty input fields. |
| `default` | no | Default value used with `-y` or when left blank. Supports `[[ ]]` expressions referencing earlier prompts. |
| `options` | for `select` | List of `{ label, value }` pairs. |
| `validate` | no | Built-in validator: `nonempty`, `url`, `http_url`, `hostname`, `origin`, `slug`, or `email`. |
| `required` | no | Default `true` unless `default` is set or `type: confirm`. |
| `env` | required for `secret` | Target environment variable name in `.env.local` (e.g. `STRIPE_SECRET_KEY`). |
| `when` | no | Simple condition (`"<key> == <value>"` or `"<key> != <value>"`) to show/hide follow-up prompts dynamically. |

> [!IMPORTANT]
> Prompts with `type: secret` are masked on input, never echoed in logs or the printed reproducible CLI command, and never written into YAML files—only into `.env.local` (mode `0600`).

## `verify`

Optional read-only HTTP preflight check executed after prompts are answered (skipped with `-y`, `--no-verify`, or offline mode). Failure warns and lets the user re-edit prompts or continue anyway.

| Field | Required | Rule |
|-------|----------|------|
| `http_get` | one of `http_get` / `http_head` | Rendered URL (`http://` or `https://`). |
| `http_head` | one of `http_get` / `http_head` | Rendered URL. |
| `headers` | no | Map of header name → rendered value (may reference `[[.Prompt.*]]`). |
| `expect_status` | no | Expected HTTP status code (default `200`). |
| `timeout` | no | Go duration, max `15s` (default `5s`). |
| `label` | no | Spinner message shown while checking. |

## Template rendering (`[[ ]]`)

Files listed under `render:`—plus string values inside `target:`, `fragments:`, and `verify:`—are rendered with Go `text/template` using **`[[` and `]]`** delimiters.

Using `[[ ]]` instead of `{{ }}` avoids delimiter collisions with GitHub Actions (`${{ secrets.FOO }}`), Helm (`{{ .Values.* }}`), and JSX/Solid templates.

### Template context

| Variable / Helper | Type | Description |
|-------------------|------|-------------|
| `.ProjectID` | `string` | Normalized project ID (`my-app`) |
| `.ServiceName` | `string` | For `type: service`: resolved service name (`service_name`, `name`, or `--as <name>`) |
| `.SecretBrand` | `string` | Configured `secret_brand` (default `plat5`) |
| `.PermissionSeparator` | `string` | Active separator (`:` or `_` / `.` / `-`) |
| `.Perm "billing" "read"` | `func(...string) string` | Joins segments with `.PermissionSeparator` (`billing:read` or `billing_read`) |
| `.Prompt.<key>` | `any` | Answered prompt value for the current plugin |
| `.Slot` | `string` | Target slot (`auth`, `operator-auth`, `authz`, `operator-authz`, `service`, …) |
| `.Selected.<slot>` | `string` | Selected plugin name in a single-select slot |
| `.Services` | `[]string` | Service names present in the assembled project |
| `.HasOperator` | `bool` | Whether the operator module is enabled |
| `.HasCustomerConsole` | `bool` | Whether a customer console is included |
| `.Kinds` | `[]string` | Plat5 hierarchy kind names present in `plat5.yml` |
| `lower`, `upper`, `trimSuffix`, `trimPrefix`, `join` | funcs | Standard string helpers |

## `fragments`

Fragments describe structured edits applied to project root files instead of copying raw root files.

| Key | Target file | Merge semantics |
|-----|-------------|-----------------|
| `plat5` | `plat5.yml` | RFC 7396 merge-patch (maps merge recursively, lists/scalars replace, `null` deletes a key). Same semantics as `plat5yml.LoadMany`. |
| `operator` | `operator.yml` | RFC 7396 merge-patch. Applied only when the operator module is enabled. |
| `local` | `plat5.local.yml` | RFC 7396 merge-patch. |
| `services` | `services/<name>.yml` + `plat5.yml` `services:` | List of string paths (e.g. `services/api.yml`) or `{ src, dest }` mappings (e.g. `{ src: services/service.yml, dest: "services/[[.ServiceName]].yml" }`). Copied/rendered into project root `services/` and appended to `plat5.yml`'s `services:` list (deduplicated; duplicate service name is an error). |
| `compose` | `docker-compose.yml` or `docker-compose.infra.yml` | Appends `<target>/<compose>` to root `docker-compose.yml` (for `service`, `customer-console`, `operator-console`) or `docker-compose.infra.yml` (for `authorizer`, `idp`, `observability`) `include:` list (deduplicated). |
| `infra_compose` | `docker-compose.infra.yml` | Explicit override to include `<target>/<infra_compose>` in `docker-compose.infra.yml` regardless of plugin `type`. |
| `env` | `.env.example` + `.env.local` | List of `{ name, value, secret, comment }`. Always written (empty/placeholder) to `.env.example`; non-empty values written to `.env.local`. |
| `gitignore` | `.gitignore` | Ensures each line exists in `.gitignore` (deduplicated). |

### Service namespacing rules (`type: service`)

Because multiple `type: service` plugins (and multiple instances via `--as <name>`) run inside the same `<project_id>` Compose project:

1. Compose service names and named volumes **must** be namespaced with `[[.ServiceName]]` (e.g. `[[.ServiceName]]:` and `[[.ServiceName]]_data:`).
2. Avoid fixed host `ports:` bindings by default (all traffic enters through the gateway on `:5400`, and internal Docker DNS gives `http://[[.ServiceName]]:3000` zero conflict). If exposing a debug host port, guard it behind an env var or omit it.

### Slot mapping for `idp` and `authorizer`

A single `type: idp` plugin (e.g. `okta`) can fill `auth` (`--auth okta`), `operator-auth` (`--operator-auth okta`), or both:

- In slot `auth`, `fragments.local.idp` writes to `plat5.local.yml`'s top-level `idp:` block.
- In slot `operator-auth`, the engine remaps `fragments.local.idp` → `operator.idp` (unless the plugin explicitly sets `fragments.local.operator.idp`).

Similarly, a `type: authorizer` plugin can fill `authz` (`plat5.yml`), `operator-authz` (`operator.yml`), or both.

## Copied files and automatic exclusions

When copying a plugin's directory into `target`:

- Noise is always skipped at any depth: `.git`, `node_modules`, `dist`, `.env`, `.env.local`, `*.local`, `.DS_Store`, `*.log`.
- Plugin-root metadata is always skipped: `plat5.plugin.yml`, `plat5.yml`, `plat5.local.yml`, `services/` (copied only via `fragments.services`), `.github` (except for `type: ci` / `type: deploy` targeting `.`), `data/*.db*`.
- Any path escaping the plugin root or `target` (`..`, symlinks outside root, absolute paths) is rejected.

---

## Complete examples

### 1. Customer / Operator IdP (`idp/auth0/plat5.plugin.yml`)

```yaml
schema: 1
name: auth0
type: idp
description: Auth0 OIDC / OAuth 2.0
cli: ">=0.20.0"

prompts:
  - key: domain
    type: input
    title: Auth0 tenant domain
    placeholder: acme.us.auth0.com
    validate: hostname
  - key: audience
    type: input
    title: API audience (identifier)
    placeholder: https://api.acme.com
    validate: nonempty
  - key: client_id
    type: input
    title: Application Client ID (for console sign-in)
    validate: nonempty

verify:
  http_get: "https://[[.Prompt.domain]]/.well-known/jwks.json"
  expect_status: 200
  label: Verifying Auth0 JWKS endpoint

fragments:
  local:
    modules:
      auth: false
    idp:
      issuer: "https://[[.Prompt.domain]]/"
      jwks_uri: "https://[[.Prompt.domain]]/.well-known/jwks.json"
      audiences: ["[[.Prompt.audience]]"]
      client_id: "[[.Prompt.client_id]]"
      subject_claim: sub
      name_claims: [name, nickname]
```

### 2. Generic API Starter (`template-go-fiber-api/plat5.plugin.yml`)

Can be installed once as `api` (default `service_name: api`) or instantiated multiple times with `plat5 add go-fiber-api --as orders`:

```yaml
schema: 1
name: go-fiber-api
type: service
service_name: api
description: Go + Fiber reference API (project tasks and user profile)
cli: ">=0.20.0"

requires:
  kinds: [organization, project]

target: "[[.ServiceName]]"
render:
  - go.mod
  - docker-compose.yml
  - services/service.yml
exclude:
  - AGENTS.md

fragments:
  services:
    - src: services/service.yml
      dest: "services/[[.ServiceName]].yml"
  compose: docker-compose.yml

app:
  compose: docker-compose.yml
next:
  - "cd [[.ServiceName]] && go mod tidy && go run ."
```

### 3. Observability (`observability/grafana-cloud/plat5.plugin.yml`)

```yaml
schema: 1
name: grafana-cloud
type: observability
description: Grafana Cloud via local Alloy collector (logs, traces, metrics)
cli: ">=0.20.0"

prompts:
  - key: otlp_endpoint
    type: input
    title: Grafana Cloud OTLP Gateway URL
    placeholder: https://otlp-gateway-prod-us-east-0.grafana.net/otlp
    validate: http_url
  - key: instance_id
    type: input
    title: Grafana Cloud Instance ID
    validate: nonempty
  - key: api_token
    type: secret
    env: GRAFANA_CLOUD_TOKEN
    title: Grafana Cloud API Token (metrics/logs/traces write)
    help: Create in Grafana Cloud Portal → OpenTelemetry → Configure

fragments:
  local:
    modules:
      observability: true
    observability:
      mode: alloy-only
    otel:
      endpoint: "[[.Prompt.otlp_endpoint]]"
      headers:
        Authorization: "Basic ${GRAFANA_CLOUD_OTLP_AUTH}"
  env:
    - name: GRAFANA_CLOUD_INSTANCE_ID
      value: "[[.Prompt.instance_id]]"
      secret: false
    - name: GRAFANA_CLOUD_TOKEN
      value: "[[.Prompt.api_token]]"
      secret: true
  gitignore:
    - ".env.local"
```

### 4. CI Pack (`ci/github-actions/plat5.plugin.yml`)

```yaml
schema: 1
name: github-actions
type: ci
description: GitHub Actions (validate/plan on PR, OIDC apply on merge, Docker build)
cli: ">=0.20.0"

target: .
prompts:
  - key: control_plane_url
    type: input
    title: Production control plane URL
    placeholder: https://cp.example.com
    required: false

render:
  - .github/workflows/plat5-config.yml
  - .github/workflows/docker.yml
```
