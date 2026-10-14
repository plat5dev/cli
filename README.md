# Plat5 CLI

Run Plat5 locally, and manage its routes and roles. Everything runs in Docker: the CLI is one binary and needs nothing else.

```bash
plat5 init my-app       # pick a starter
cd my-app
plat5 start             # Plat5, its modules, and your app; prints links, logins, keys
```

Self-hosting a deployment is compose, not this CLI: [plat5 `docs/self-hosting.md`](https://github.com/plat5dev/plat5/blob/master/docs/self-hosting.md).

## Install

```bash
brew install plat5dev/tap/plat5
# or
curl -fsSL https://plat5.com/install.sh | sh
```

macOS and Linux, amd64 and arm64. On Windows, use WSL2. The script verifies the release's checksum and installs to `/usr/local/bin` (or `~/.local/bin`); `PLAT5_CLI_VERSION=v0.6.0` pins a release, `PLAT5_INSTALL_DIR` picks the directory. Or download a tarball from [GitHub Releases](https://github.com/plat5dev/cli/releases). `plat5 version` prints the version without the leading `v`.

From source: `go install github.com/plat5dev/cli/cmd/plat5@latest`.

## What `plat5 start` runs

One compose project, `plat5-<project_id>`, with one Postgres (a database per plane), then your app's own compose file.

| Module | What | Port | Login |
|--------|------|------|-------|
| core (always) | Gateway | 5400 | |
| | Control plane (admin API) | 5402 | admin token `dev-admin-token` |
| `auth` | Plat5 Auth, the customer IdP, in dev mode | 5401 | `dev@example.com` / `password` |
| `operator` | Operator gateway (staff) | 5410 | |
| | Staff sign-in (a second Plat5 Auth: its own issuer and database) | 5411 | |
| | Operator console | 5412 | `staff@example.com` / `password` |
| `observability` | Grafana (grafana/otel-lgtm: Loki, Tempo, Prometheus) | 5403 | none |
| | OTLP (Alloy, which also reads every container's logs) | 4318 HTTP, 4317 gRPC | |
| your app (`app.compose`) | A starter's API and web app | 3000, 3001 | `dev@example.com` / `password` |

All modules are on unless `plat5.yml` turns one off. `plat5 start -x operator,observability` skips some for one run; `-x app` runs everything but your app, so you can run it on your machine.

A busy port is an error naming it, never a silent move: stop what holds it, or pin another under `ports:`. Running `plat5 start` again updates the stack in place.

After the stack is healthy, start applies `roles.yml` and the route files, then makes sure there is something to call with: the dev person (`dev@example.com`), an org they own named `Dev`, a user key, and a member key. It reuses them while the keys work. Then it prints every link, login, and key; `plat5 status` prints them again.

```
Plat5
  Gateway        http://localhost:5400
  Auth           http://localhost:5401  dev@example.com / password
  …
Dev keys (dev@example.com, owner of "Dev")
  User key       plat5-k-2-…            user routes
  Member key     plat5-k-2-…            org and member routes

Try it:
  curl http://localhost:5400/org/projects -H "X-API-Key: plat5-k-2-…"
```

Local Auth runs in dev mode: `POST /dev/token` mints a token for any email, and login codes for new sign-ups are in `plat5 logs auth`. The sign-in pages say which plane they are (customer or staff) and show the dev login.

## Commands

| Command | |
|---------|--|
| `plat5 init [dir]` | Create a project from a starter. Asks for the starter and modules; `-y` takes the defaults. `--template none` is Plat5 alone |
| `plat5 start [-x module,…]` | Run the stack and your app, apply roles and routes, seed dev keys, print the summary |
| `plat5 status` | The summary again, and container health |
| `plat5 stop` | Stop everything. Data (volumes) is kept |
| `plat5 logs [service…]` | Container logs, following. `--app` for your app; `-f=false` to print and exit |
| `plat5 doctor` | Check Docker, `plat5.yml`, files, and ports |
| `plat5 routes apply [file…]` | `POST /apply` (default: the `routes:` files, with `upstreams:` bound) |
| `plat5 routes list` / `get <name>` / `rm <name>` | The control plane's services |
| `plat5 roles apply [file]` / `roles show` | `PUT /roles` (default: `roles:`), and the current document |
| `plat5 version` | |

`routes` and `roles` talk to the local control plane unless `--control-plane-url` / `--admin-token` (or `PLAT5_CONTROL_PLANE_URL` / `PLAT5_ADMIN_TOKEN`) point elsewhere.

## `plat5.yml`

Found by walking up from the working directory. Relative paths resolve against its directory. Unknown keys are errors.

```yaml
project_id: my-app                 # default: the directory name

versions:                          # image tags; plat5 init writes the latest of each
  plat5: v0.7.0
  auth: v0.2.0                     # both Auth instances
  operator: v0.4.0
  operator_console: v0.1.0

modules:                           # all true unless set false
  auth: true
  operator: true
  observability: true

app:                               # your app; plat5 start runs it after Plat5
  compose: ./docker-compose.yml
  web: http://localhost:3001       # its browser origin: allowed to sign in with local Auth

upstreams:                         # where services listen, by routes.yml service name
  api: 3000

routes:
  - ./routes.identity.yml
  - ./routes.audit.yml
  - ./routes.yml

roles: ./roles.yml                 # omit to turn roles off

# Optional
ports:                             # host ports; these are the defaults
  gateway: 5400
  auth: 5401
  control_plane: 5402
  grafana: 5403
  operator: 5410
  staff_auth: 5411
  operator_console: 5412
  otlp_grpc: 4317
  otlp_http: 4318
admin_token: dev-admin-token       # local only
apikey_brand: plat5                # keys are {brand}-k-2-…, sessions {brand}-s-2-…
auth:                              # local Auth, beyond app.web
  clients: [plat5]                 # also the gateway's audiences (Plat5 Auth's aud is the client id)
  origins: []                      # each also allows <origin>/callback
  redirect_uris: []
  theme_file: ./theme.json         # OpenAuth Theme JSON for the sign-in page
operator:
  origins: []                      # more staff consoles (CORS and staff redirects)
idp:                               # with modules.auth false: the gateway's IdP
  issuer: https://idp.example.com
  jwks_uri: https://idp.example.com/.well-known/jwks.json
  audiences: [my-api]
  user_id_claim: sub
otel:
  endpoint: http://host.docker.internal:4318   # your collector, instead of the observability module
source:                            # contributors: build from checkouts (see below)
  plat5: ../plat5
```

### Upstreams

| Value | Becomes | When to use |
|-------|---------|-------------|
| `3000` (bare port) | `http://host.docker.internal:3000` | Your app's port on the host. A starter's compose file publishes it there too |
| `localhost:3000` / `127.0.0.1:3000` | `http://localhost:3000` | Gateway can use loopback (non-Docker gateway, etc.) |
| `host:port` | `http://host:port` (a port is required) | Named host on a shared network |
| `http://host:port` | unchanged | Same, written out |

The url written into the route config is always exactly `http://host:port`. An `https://` value, any other scheme, a path, or a query is a config error (`TLS (https) upstreams aren't supported yet; use http://host:port`).

Unknown keys in `plat5.yml` are also a config error naming the key.

Keys must match service names in the routes file(s). `plat5 routes apply` and `plat5 start` bind upstreams before `POST /apply`. A key that matches no service in any applied file prints `warning: upstreams.<name> matches no service …` (the apply still succeeds). A service with no `url` and no `upstreams` entry fails before upload: `service "<name>" has no url: add it under upstreams: in plat5.yml, or set url in the routes file`.

You can still set `url` directly in `routes.yml`; an `upstreams` entry for that service wins.

## Routes

```bash
plat5 routes apply              # all files in plat5.yml routes: + upstream bind
plat5 routes apply ./other.yml
```

`routes.yml` holds the HTTP surface (paths, scopes). Keep deployment topology in `upstreams` so the same contract can point at different origins later.

`plat5 start` applies the roles file and the configured route files after the control plane is ready.

### Audit

The gateway records each audited request through the `audit` service: `organization` and `member` requests in the org's log, `user` requests in the person's own log. Writes are audited by default, and reads a route marks `audit: true` ([plat5 `docs/audit.md`](https://github.com/plat5dev/plat5/blob/master/docs/audit.md)). It is on by default. While audit is down, audited requests are `503`. `routes.audit.yml` publishes `GET /org/audit-events` (label `org:audit:read`) and `GET /user/audit-events`. Locally, audit is always on.

## Roles

`roles.yml` is your deployment's roles: each role grants labels, and routes require labels with `required_labels`. Plat5 names no roles; `plat5 init` writes a starter (`owner` / `admin` / `member`) whose `org:*` labels match `routes.identity.yml` and `routes.audit.yml`. A template may ship its own. Contract: [plat5 `docs/roles.md`](https://github.com/plat5dev/plat5/blob/master/docs/roles.md).

```yaml
roles:
  owner: ["*"]
  admin: [org:write, org:members:write, org:service-accounts:write, org:audit:read]
  member: []
creator_role: owner
default_role: member
```

Roles live in the control plane. `plat5 start` writes the file there (it also seeds the first roles document, so identity can start), and `plat5 roles apply` writes it without a restart. Identity picks up a change within a second or two. The gateway caches credentials for up to `APIKEY_CACHE_TTL_SECS` (300s), so a role change reaches existing sessions and keys within that window. Remove `roles:` from `plat5.yml` to turn roles off (`ROLES_ENABLED=false` on identity): no member has a role, every member holds every label, and authorization is left to something else. Turning roles on or off recreates identity (`ROLES_ENABLED` is a boot switch).

## Starters

A starter is a repo `plat5 init` copies: `plat5dev/template-*` by short name (`plat5 init --list-templates`), any `owner/repo`, or an archive URL. Remote starters are cached under `~/.cache/plat5/templates/`; `--templates-dir` reads them from a local directory, `--template-ref` picks a git ref.

### The starter contract

What a starter ships, so `plat5 init` and `plat5 start` can run it with no other tools than Docker:

| | |
|--|--|
| `plat5.template.yml` | `name`, `description`, `upstreams`, `routes`, `substitute` (files where `{{project_id}}` is replaced), `app`, and `next` |
| `app.compose` | A compose file that runs the API with hot reload. No `name:`: the project is the directory's |
| `app.web` | The web app's origin, if the compose file runs one. Local Auth allows it to sign in |
| `next` | How to run the API on your machine instead (`plat5 start -x app`) |
| `routes.yml`, `routes.identity.yml`, `routes.audit.yml`, `roles.yml` | The route files and roles `plat5.yml` points at |

`plat5 start` runs `docker compose up -d --build --wait` on `app.compose` with this env, so the file can say `${PLAT5_GATEWAY_URL}`:

| Env | |
|-----|--|
| `PLAT5_GATEWAY_URL` | The gateway, as the browser reaches it |
| `PLAT5_AUTH_URL` | The customer IdP's issuer, as the browser reaches it |
| `PLAT5_AUTH_CLIENT_ID` | The OAuth client id (`plat5`) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Where to send traces and metrics (Alloy on the host, `http://host.docker.internal:4318`), or empty |

Ports: the API on `3000`, the web app on `3001`. First-party starters share one API (profiles, projects, tasks), so they share one web app: [`template-web`](https://github.com/plat5dev/template-web), run from `ghcr.io/plat5dev/template-web`. A starter with its own frontend (SSR, Next.js) runs that instead.

## Contributors: build from checkouts

`source:` builds a module's images from a local checkout instead of pulling them, on the same embedded stack (prod targets: what ships). Keys are the `versions:` keys; a sourced module needs no version.

```bash
plat5 init my-app --source plat5=../plat5 --source auth=../auth
cd my-app && plat5 start      # builds, then runs; the first Rust build takes a while
```

The embedded compose files (`internal/bundle/stack`) track plat5 HEAD, so a `plat5` checkout at HEAD runs as it will ship. For hot reload of one service, use that repo's own dev compose.

## State

`$XDG_STATE_HOME/plat5/projects/<project_id>/` (default `~/.local/state/plat5/…`, on macOS too): `state.json` (what the last start ran, and the dev keys; mode 0600) and `stack/` (the compose files, the generated override, and the sign-in themes).
