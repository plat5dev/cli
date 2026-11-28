# Local config additions (`plat5.local.yml` & `.env.local`)

This doc specifies the Phase 1 extensions to `plat5.local.yml` and `plat5 start` required so external IdPs, Operator SSO, Grafana Cloud / OTLP vendors, and project-owned containers work cleanly without hardcoding providers in the CLI.

Adding these keys raises `config.SchemaVersion` in [`internal/config/cliversion.go`](../../internal/config/cliversion.go).

## `.env.local` secret loading

Secrets collected by `plat5 init` / `plat5 add` (or added manually by a developer) live in `.env.local` beside `plat5.local.yml`, never plaintext inside `plat5.local.yml`.

### Rules

1. When `plat5 start` (or `plat5 doctor`) loads `plat5.local.yml`, it reads `.env.local` in the same directory if present (or the path in `env_file:` if overridden).
2. String values in `plat5.local.yml` support `${VAR}` and `${VAR:-default}` expansion sourced from process env first, then `.env.local`. Unresolved `${VAR}` without a default is a startup error naming the key and variable.
3. `.env.local` variables are also passed into the stack and app compose interpolation environments.
4. `plat5 init` / `plat5 add` always create `.env.local` with permissions `0600` and ensure `.env.local` is listed in `.gitignore`.
5. `plat5 doctor` warns if `.env.local` exists and is not ignored by git (when inside a git repo).

## New `plat5.local.yml` fields

```yaml
env_file: .env.local              # optional; default .env.local if present

infra:                            # NEW: project-owned infra (authorizers, local IdPs)
  compose: ./docker-compose.infra.yml   # stays running on `plat5 start -x app`

idp:                              # customer IdP (when modules.auth: false)
  issuer: https://acme.us.auth0.com/
  jwks_uri: https://acme.us.auth0.com/.well-known/jwks.json
  audiences: [https://api.acme.com]
  client_id: abc123spa            # NEW: forwarded to customer-console as PLAT5_AUTH_CLIENT_ID
  subject_claim: sub
  name_claims: [name, nickname]

operator:
  config: ./operator.yml
  console:
    compose: ./operator-console/docker-compose.yml
    web: http://localhost:5412
  origins: []
  idp:                            # NEW: external operator IdP (turns off local operator-auth)
    issuer: https://acme.okta.com/oauth2/default
    jwks_uri: https://acme.okta.com/oauth2/default/v1/keys
    audiences: [plat5-operator]
    client_id: op123spa           # forwarded to operator-console as PLAT5_OPERATOR_CLIENT_ID
    operator_id_claim: sub        # AUTH_OPERATOR_ID_CLAIM (default: sub)
    operator_email_claim: email   # AUTH_OPERATOR_EMAIL_CLAIM (default: email)

observability:                    # NEW: controls local observability containers
  mode: local                     # local (lgtm + alloy, default) | alloy-only | off

otel:
  endpoint: https://otlp-gateway-prod-us-east-0.grafana.net/otlp
  headers:                        # NEW: passed as OTEL_EXPORTER_OTLP_HEADERS (comma-separated k=v)
    Authorization: "Basic ${GRAFANA_CLOUD_OTLP_AUTH}"
```

### Field rules

#### `infra.compose`
- Optional path to a project-owned Compose file (typically `./docker-compose.infra.yml`) for local infrastructure containers such as AuthZEN PDPs (`cerbos`, `topaz`, `openfga`) or local IdPs (`keycloak`, `dex`).
- Runs under compose project `<project_id>-infra`, starts **before** `app.compose` (after core Plat5), and auto-joins any service referenced by `urlHosts` onto the `plat5` network.
- Unlike `app.compose`, `infra.compose` **stays running** when you pass `plat5 start -x app` so host-run APIs can still reach local Cerbos/Topaz/Keycloak. Pass `-x infra` to skip it explicitly.

#### `idp.client_id`
- Optional string. When `modules.auth: false`, `stack.AppEnv()` forwards:
  - `PLAT5_AUTH_URL` = `idp.issuer`
  - `PLAT5_AUTH_CLIENT_ID` = `idp.client_id` (empty if unset)
  - `PLAT5_AUTH_AUDIENCE` = `idp.audiences[0]` (when `idp.audiences` is non-empty and not `"*"`)
  - `PLAT5_AUTH_SUBJECT_CLAIM` = `idp.subject_claim` (default `"sub"`)
- Fixes the current gap where `AppEnv()` hardcoded `PLAT5_AUTH_CLIENT_ID=plat5` and omitted audience and subject claim for external IdPs.

#### `operator.idp`
- Optional block. When any field is set:
  - `issuer`, `jwks_uri`, and `audiences` (non-empty, no `"*"` wildcard—matching the [operator IdP contract](https://github.com/plat5dev/operator/blob/master/docs/reference/idp.md)) are required.
  - Local `operator-auth` container is **not started** (and `versions.auth` is no longer required if `modules.auth` is also `false`).
  - The operator gateway receives `AUTH_ISSUER`, `AUTH_JWKS_URI`, `AUTH_ALLOWED_AUDIENCES`, `AUTH_OPERATOR_ID_CLAIM` (default `sub`), and `AUTH_OPERATOR_EMAIL_CLAIM` (default `email`).
  - `stack.AppEnv()` forwards `PLAT5_OPERATOR_AUTH_URL` = `operator.idp.issuer` and `PLAT5_OPERATOR_CLIENT_ID` = `operator.idp.client_id`.

#### `observability.mode` and `otel.headers`
- `observability.mode`:
  - `local` (default when `modules.observability: true`): runs `lgtm` + `alloy` (current behavior).
  - `alloy-only`: runs `alloy` without `lgtm`, forwarding OTLP telemetry (traces, metrics, and scraped Docker logs) to a remote OTLP endpoint configured via `otel.endpoint` + `otel.headers` (e.g., Grafana Cloud's single OTLP Gateway) or explicit `LOKI_*` / `TEMPO_*` / `MIMIR_*` env vars.
  - `off`: equivalent to `modules.observability: false`.
- `otel.headers`: optional map of header name → value, serialized to `OTEL_EXPORTER_OTLP_HEADERS` (`key=value,key2=value2`, RFC 4317 OTLP standard) on every Plat5 container and in `AppEnv()`.

## Extended auto-networking (`urlHosts`)

Today `plat5 start` joins app compose services whose names appear as hostnames in `plat5.yml` (`services.*.url` and `authorizer.url`) onto the `plat5` Docker network.

Extend hostname discovery to also collect hostnames from:

1. `operator.yml` → `authorizer.url` and `services.*.url`
2. `plat5.local.yml` → `idp.jwks_uri` and `operator.idp.jwks_uri`
3. `plat5.local.yml` → `otel.endpoint`

Any matching service in `app.compose` (such as a project-owned `keycloak`, `cerbos`, `topaz`, `openfga`, or custom collector container) is automatically joined to the `plat5` network on `plat5 start`, and mapped to `host-gateway` on `plat5 start -x app`.

## Updated `AppEnv` template contract

Added to the existing `AppEnv` variables documented in [`../README.md`](../README.md#your-app-and-its-url):

| Env | Value |
|-----|-------|
| `PLAT5_AUTH_AUDIENCE` | First non-wildcard audience from `idp.audiences` (empty with local Auth) |
| `PLAT5_AUTH_SUBJECT_CLAIM` | `properties.user_id` with local Auth; `idp.subject_claim` (default `sub`) with external IdP |
| `OTEL_EXPORTER_OTLP_HEADERS` | Comma-separated `k=v` headers from `otel.headers` (empty if unset) |
