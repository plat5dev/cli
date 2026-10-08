# Plat5 CLI

Local development CLI for **consumer projects** using Plat5.

Requires a project `plat5.yml` (`plat5 init`). Starts Plat5 (and optionally Auth / observability / Operator) via Docker Compose and applies gateway routes through the route-registry admin API.

`plat5 start` pulls runtime images via `plat5_version` and Auth via `auth.version` (independent pins) using compose embedded in the CLI.  
Advanced (local development): set `plat5_compose` / `auth_compose` / `observability_compose` / `operator_compose` to local compose trees.

Self-host (server) uses published images + compose — see [plat5dev/plat5 self-hosting](https://github.com/plat5dev/plat5/blob/master/docs/self-hosting.md).

## Install

**Binary:** [GitHub Releases](https://github.com/plat5dev/cli/releases) — download `plat5_<version>_<os>_<arch>.tar.gz`, extract, put `plat5` on your `PATH`. `plat5 version` prints the release version without the leading `v` (tag `v0.4.1` → `0.4.1`).

**Go:**

```bash
go install github.com/plat5dev/cli/cmd/plat5@latest
# clone: go install ./cmd/plat5
plat5 version
```

## Quick start (consumer project)

```bash
mkdir my-app && cd my-app

plat5 init --template bun-effect-api --auth -y
plat5 start

plat5 status

# Get a dev token (local auth only; use the auth URL from `plat5 status`, default :5000)
curl -s -X POST http://localhost:5000/dev/token \
  -H 'Content-Type: application/json' -d '{"email":"dev@example.com"}' | jq -r .access_token

plat5 stop
```

The local stack always runs Auth with `AUTH_DEV_MODE=true`: `POST /dev/token` is open and login codes are written to the Auth logs (`plat5 logs --auth`) instead of being emailed. This can't be turned off locally. To test production behavior, run Auth's prod compose instead (point `auth_compose` at it).

`plat5_version` (default `v0.4.1`) pins runtime GHCR tags. With Auth enabled, `auth.version` / `AUTH_VERSION` (default `v0.1.11`) pins `ghcr.io/plat5dev/auth` independently. With Operator enabled, `operator.version` / `OPERATOR_VERSION` (default `v0.4.0`) pins `ghcr.io/plat5dev/operator` independently.

Templates: first-party short names (`plat5 init --list-templates`) fetch public GitHub repos under `plat5dev/template-*` (branch `master`, override with `--template-ref` / `PLAT5_TEMPLATE_REF`). Also accepts `owner/repo` or an archive URL. Cached under `~/.cache/plat5/templates/`. Local: `--templates-dir` / `PLAT5_TEMPLATES` (directory of template folders).

## Commands

| Command | Description |
|---------|-------------|
| `plat5 init` | Create project; `--template` copies a reference app + writes `plat5.yml` (`--auth` / `--operator`) |
| `plat5 start [-d] [--auth] [--observability] [--operator] [--build]` | Start stacks, wait for registry, apply routes |
| `plat5 stop [--auth] [--observability] [--operator]` | Stop Plat5; modules if started / enabled |
| `plat5 status` | URLs, health, registered routes |
| `plat5 doctor` | Docker, project config, ports |
| `plat5 logs [-f=false] [service…]` | Plat5 compose logs (`--auth` / `--observability` / `--operator`). Follows by default; `-f=false` prints and exits |
| `plat5 routes apply [file…]` | `POST /apply` (defaults to `routes:` list) |
| `plat5 routes list` | List services |
| `plat5 routes get <name>` | Show service config |
| `plat5 routes rm <name>` | Delete service |
| `plat5 version` | CLI version |

## Project config (`plat5.yml`)

Walks up from cwd. **Required** for all project commands.

```yaml
project_id: my-app          # default: directory name; local compose isolation slug

plat5_version: v0.4.1                  # runtime GHCR tag

auth:
  enabled: false
  # version: v0.1.11                   # Auth image pin when enabled (AUTH_VERSION)
  # Project OAuth surface → issuer env on start (plat5 init --auth defaults = web-demo :5173).
  # allowed_clients: [plat5]
  # allowed_redirect_uris:
  #   - http://localhost:5173/callback
  #   - https://oauth.pstmn.io/v1/callback
  # allowed_origins:
  #   - http://localhost:5173
  # public_issuer_url:                 # default: derived auth URL (localhost:<ports.auth>)
  # theme_file: ./theme.json           # optional OpenAuth Theme JSON

observability:
  enabled: false

operator:
  enabled: false
  # version: v0.4.0                 # Operator image pin when enabled (OPERATOR_VERSION)
  # allowed_origins:                # browser consoles: gateway CORS + IdP redirect <origin>/callback
  #   - http://localhost:5173

# Optional host port pins. Omitted keys use defaults;
# if a default is busy, start auto-allocates. Pinned + busy → error.
ports:
  gateway: 5001
  registry: 5002
  auth: 5000
  operator: 5004
  operator_idp: 5556
  grafana: 3002
  otlp_grpc: 4317
  otlp_http: 4318
  alloy: 12345

admin_token: dev-admin-token   # local only; do not put production tokens here

# API key brand → identity + gateway APIKEY_BRAND. Unset → plat5.
# [a-z][a-z0-9]*, max 32. Keys are {brand}-sk-1- / {brand}-mk-1-. Sessions are {brand}-ms-1-.
# apikey_brand: plat5

# Optional OTLP for Plat5/Auth containers (unset = no export).
# When observability.enabled, CLI auto-wires host.docker.internal:<otlp_http>
# if otel.endpoint is unset. Explicit endpoint always wins.
# Host-published Alloy: CLI injects env and adds host-gateway extra_hosts
# only when the endpoint host is host.docker.internal.
# otel:
#   endpoint: http://host.docker.internal:4318
# Or set OTEL_EXPORTER_OTLP_ENDPOINT in the environment (overrides yml).

# Topology: where each service process listens (keys = services.* in routes files).
# Injected as url at apply time (overwrites url in the file for that service).
upstreams:
  api: http://host.docker.internal:3000  # app on the host (bare `3000` is shorthand for this)
  # api: http://localhost:3000           # gateway shares host network view
  # api: http://api.internal:8080        # named host on a shared network
  # Always http://host:port: no path, no query. https:// is rejected (TLS upstreams aren't supported yet).

# Route contract files (paths, scopes). Prefer upstreams for urls.
routes:
  - ./routes.identity.yml            # identity public surface (edit or omit)
  - ./routes.yml
  # - ./routes.dev.yml                   # optional extras (e.g. debug routes)

# Roles: each grants labels; routes require them. Required.
roles: ./roles.yml
```

Relative paths resolve against the directory containing `plat5.yml`.

Flags / env still override: `--plat5-compose`, `PLAT5_COMPOSE`, `PLAT5_ADMIN_TOKEN`, `PLAT5_GATEWAY_URL`, etc.

### Upstreams

| Value | Becomes | When to use |
|-------|---------|-------------|
| `3000` (bare port) | `http://host.docker.internal:3000` | App on the host; Plat5 gateway in Docker (default local) |
| `localhost:3000` / `127.0.0.1:3000` | `http://localhost:3000` | Gateway can use loopback (non-Docker gateway, etc.) |
| `host:port` | `http://host:port` (a port is required) | Named host on a shared network |
| `http://host:port` | unchanged | Same, written out |

The url written into the route config is always exactly `http://host:port`. An `https://` value, any other scheme, a path, or a query is a config error (`TLS (https) upstreams aren't supported yet; use http://host:port`).

Unknown keys in `plat5.yml` are also a config error naming the key. A leftover `bootstrap:` key was removed in v0.3.3; delete it.

Keys must match service names in the routes file(s). `plat5 routes apply` and `plat5 start` bind upstreams before `POST /apply`. A key that matches no service in any applied file prints `warning: upstreams.<name> matches no service …` (the apply still succeeds). A service with no `url` and no `upstreams` entry fails before upload: `service "<name>" has no url: add it under upstreams: in plat5.yml, or set url in the routes file`.

You can still set `url` directly in `routes.yml`; an `upstreams` entry for that service wins.

## Routes

```bash
plat5 routes apply              # all files in plat5.yml routes: + upstream bind
plat5 routes apply ./other.yml
```

`routes.yml` holds the HTTP surface (paths, scopes). Keep deployment topology in `upstreams` so the same contract can point at different origins later.

`plat5 start` applies the configured route files after the registry is ready.

## Roles

`roles.yml` is your deployment's roles: each role grants labels, and routes require labels with `required_labels`. Plat5 names no roles; `plat5 init` writes a starter (`owner` / `admin` / `member`) whose `org:*` labels match `routes.identity.yml`. A template may ship its own. Contract: [plat5 `docs/roles.md`](https://github.com/plat5dev/plat5/blob/master/docs/roles.md).

```yaml
roles:
  owner: ["*"]
  admin: [org:write, org:members:write, org:service-accounts:write]
  member: []
creator_role: owner
default_role: member
```

`plat5 start` mounts the file into identity (`ROLES_FILE`). Identity reads it at boot, so when the file changes, `plat5 start` restarts identity. The gateway caches credentials for up to `APIKEY_CACHE_TTL_SECS` (300s), so a role change reaches existing sessions and keys within that window. `roles:` is required: identity does not boot without a roles file, and every member holds one of its roles.

## Ports and multi-project

Each project gets compose project names `plat5-<project_id>`, `plat5-<project_id>-auth`, `plat5-<project_id>-observability`, `plat5-<project_id>-operator`.

Host port mappings are written to override files under XDG state so two projects do not share containers. Defaults: gateway 5001, registry 5002, auth 5000, operator 5004, operator_idp 5556, grafana 3002, OTLP 4317/4318, alloy 12345. Unpinned busy ports are reallocated; **pinned** ports never auto-move. `plat5 start` is safe to re-run: a stack of this project that is already running keeps the ports it has (the CLI prints `Plat5 is already running for this project; keeping its ports.`), and `docker compose up` leaves unchanged containers alone. Run `plat5 stop` first to pick ports again.

Start order: observability → auth → plat5 → operator. Stop runs operator first, then plat5, auth, observability. Operator joins the Plat5 compose network (`plat5-<project_id>_plat5`) after Plat5 is up so the image route list can dial `http://identity:3000`. Identity is not published. Routes are not rewritten. Operator requires detached start (the default).

## Operator

[Operator](https://github.com/plat5dev/operator) is a headless gateway for staff: staff JWT in, identity path out, attribution logged. It has no accounts. Staff sign in at a local Dex the CLI configures (`staff@example.com` / `password`, issuer `http://localhost:<operator_idp>/dex`).

| Dex client | For |
|------------|-----|
| `operator-cli` (secret `operator-cli-secret`) | Scripts. Password grant |
| `operator-console` (public, PKCE) | Browser consoles on `operator.allowed_origins`, redirect `<origin>/callback` |

Both client ids are in the gateway's `AUTH_AUDIENCES`. `allowed_origins` is also its `ALLOWED_ORIGINS`.

```bash
TOKEN=$(curl -s http://localhost:5556/dex/token \
  -u operator-cli:operator-cli-secret \
  -d grant_type=password -d scope="openid email" \
  -d username=staff@example.com -d password=password | jq -r .access_token)
curl -s http://localhost:5004/organizations -H "Authorization: Bearer $TOKEN"
```

The CLI doesn't publish the operator's health port (8004). To check operator health, run `plat5 status` (probes the Operator URL) or `docker compose ps` for the operator project (`plat5-<project_id>-operator`), whose healthcheck hits `/health/ready` inside the container.

For a browser console, run [operator-console](https://github.com/plat5dev/operator-console) with `VITE_GATEWAY_URL=http://localhost:5004`, `VITE_AUTH_ISSUER=http://localhost:5556/dex`, `VITE_AUTH_CLIENT_ID=operator-console`.

The Dex config and the gateway's IdP settings live in the generated override under XDG state, so path mode (`operator_compose` → `operator/compose`) gets the same wiring.

## State (XDG)

Same path on macOS and Linux:

| Path | Role |
|------|------|
| `$XDG_STATE_HOME/plat5/projects/<id>/` | `state.json`, compose overrides (default `~/.local/state/plat5/…`) |

## Path mode (contributors)

Point `plat5_compose` / `auth_compose` / `observability_compose` / `operator_compose` (flags/env/yml) at a directory that **contains** `docker-compose.yml` (or `compose.yml`). No layout probing — exact path only. Image mode (embedded compose + GHCR) is the default for consumers.
