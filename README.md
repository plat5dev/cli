# Plat5 CLI

Local development CLI for **consumer projects** using Plat5.

Requires a project `plat5.yml` (`plat5 init`). Starts Plat5 (and optionally Auth / observability) via Docker Compose and applies gateway routes through the route-registry admin API.

`plat5 start` pulls runtime images via `plat5_version` and Auth via `auth.version` (independent pins) using compose embedded in the CLI.  
Advanced (local development): set `plat5_compose` / `auth_compose` / `observability_compose` to local compose trees.

Self-host (server) uses published images + compose — see [plat5dev/plat5 self-hosting](https://github.com/plat5dev/plat5/blob/master/docs/self-hosting.md).

## Install

**Binary:** [GitHub Releases](https://github.com/plat5dev/cli/releases) — download `plat5_<version>_<os>_<arch>.tar.gz`, extract, put `plat5` on your `PATH`. `plat5 version` matches the release tag.

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
plat5 stop
```

`plat5_version` (default `v0.2.2`) pins runtime GHCR tags. With Auth enabled, `auth.version` / `AUTH_VERSION` (default `v0.1.8`) pins `ghcr.io/plat5dev/auth` independently.

Templates: first-party short names (`plat5 init --list-templates`) fetch public GitHub repos under `plat5dev/template-*` (branch `master`, override with `--template-ref` / `PLAT5_TEMPLATE_REF`). Also accepts `owner/repo` or an archive URL. Cached under `~/.cache/plat5/templates/`. Local: `--templates-dir` / `PLAT5_TEMPLATES` (directory of template folders).

## Commands

| Command | Description |
|---------|-------------|
| `plat5 init` | Create project; `--template` copies a reference app + writes `plat5.yml` |
| `plat5 start [-d] [--auth] [--observability] [--build]` | Start stacks, wait for registry, apply routes |
| `plat5 stop [--auth] [--observability]` | Stop Plat5; modules if started / enabled |
| `plat5 status` | URLs, health, registered routes |
| `plat5 doctor` | Docker, project config, ports |
| `plat5 logs [-f] [service…]` | Plat5 compose logs (`--auth` / `--observability`) |
| `plat5 routes apply [file…]` | `POST /apply` (defaults to `routes:` list) |
| `plat5 routes list` | List services |
| `plat5 routes get <name>` | Show service config |
| `plat5 routes rm <name>` | Delete service |
| `plat5 version` | CLI version |

## Project config (`plat5.yml`)

Walks up from cwd. **Required** for all project commands.

```yaml
project_id: my-app          # default: directory name; local compose isolation slug

plat5_version: v0.2.2                  # runtime GHCR tag

auth:
  enabled: false
  # version: v0.1.8                    # Auth image pin when enabled (AUTH_VERSION)
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

# Optional host port pins. Omitted keys use defaults;
# if a default is busy, start auto-allocates. Pinned + busy → error.
ports:
  gateway: 5001
  registry: 5002
  auth: 5000
  grafana: 3002
  otlp_grpc: 4317
  otlp_http: 4318
  alloy: 12345

admin_token: dev-admin-token   # local only; do not put production tokens here

# API key brand → identity + gateway APIKEY_BRAND. Unset → plat5.
# [a-z][a-z0-9]*, max 32. Keys are {brand}-sk-1- / {brand}-mk-1-.
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
  api: 3000                              # host port → http://host.docker.internal:3000
  # api: localhost:3000                  # gateway shares host network view
  # api: https://api.staging.example.com # remote origin

# Route contract files (paths, scopes). Prefer upstreams for urls.
routes:
  - ./routes.identity.yml            # identity public surface (edit or omit)
  - ./routes.yml
  # - ./routes.dev.yml                   # optional extras (e.g. debug routes)
```

Relative paths resolve against the directory containing `plat5.yml`.

Flags / env still override: `--plat5-compose`, `PLAT5_COMPOSE`, `PLAT5_ADMIN_TOKEN`, `PLAT5_GATEWAY_URL`, etc.

### Upstreams

| Value | Becomes | When to use |
|-------|---------|-------------|
| `3000` (bare port) | `http://host.docker.internal:3000` | App on the host; Plat5 gateway in Docker (default local) |
| `localhost:3000` / `127.0.0.1:3000` | `http://localhost:3000` | Gateway can use loopback (non-Docker gateway, etc.) |
| `host:port` or hostname | `http://…` | Named host on a shared network |
| `https://…` / `http://…` | unchanged | Public or remote origin |

Keys must match service names in the routes file(s). `plat5 routes apply` and `plat5 start` bind upstreams before `POST /apply`.

You can still set `url` directly in `routes.yml`; an `upstreams` entry for that service wins.

## Routes

```bash
plat5 routes apply              # all files in plat5.yml routes: + upstream bind
plat5 routes apply ./other.yml
```

`routes.yml` holds the HTTP surface (paths, scopes). Keep deployment topology in `upstreams` so the same contract can point at different origins later.

`plat5 start` applies the configured route files after the registry is ready.

## Growth path: targets (not built yet)

Today config is flat and means **local**. A natural extension is named targets without changing the route contract:

```yaml
# future sketch — not implemented
targets:
  local:
    plat5_compose: …
    upstreams: { api: 3000 }
    routes: [./routes.yml, ./routes.dev.yml]
  staging:
    # control-plane URL + creds (Cloud), no compose
    upstreams: { api: https://api.staging.example.com }
    routes: [./routes.yml]
```

Same verbs (`routes apply`, etc.); `--target` / env selects topology. Until that exists, one project = one local topology via top-level `upstreams` / `routes`.

## Ports and multi-project

Each project gets compose project names `plat5-<project_id>`, `plat5-<project_id>-auth`, `plat5-<project_id>-observability`.

Host port mappings are written to override files under XDG state so two projects do not share containers. Defaults: gateway 5001, registry 5002, auth 5000, grafana 3002, OTLP 4317/4318, alloy 12345. Unpinned busy ports are reallocated; **pinned** ports never auto-move.

Start order: observability → auth → plat5. Stop is the reverse.

## State (XDG)

Same paths on macOS and Linux:

| Path | Role |
|------|------|
| `$XDG_STATE_HOME/plat5/projects/<id>/` | `state.json`, compose overrides (default `~/.local/state/plat5/…`) |
| `$XDG_CONFIG_HOME/plat5/` | Reserved for machine config / future Cloud creds (default `~/.config/plat5`) |

## Path mode (contributors)

Point `plat5_compose` / `auth_compose` / `observability_compose` (flags/env/yml) at a directory that **contains** `docker-compose.yml` (or `compose.yml`). No layout probing — exact path only. Image mode (embedded compose + GHCR) is the default for consumers.
