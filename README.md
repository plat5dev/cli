# Plat5 CLI

Run Plat5 locally, and manage a deployment's config. Everything runs in Docker: the CLI is one binary and needs nothing else.

```bash
plat5 init my-app       # a project: the config, plus an API starter and the consoles if you pick one
cd my-app
plat5 start             # Plat5 and its modules; applies the config; runs your apps; prints links, logins, a key
```

`plat5 init` writes a project you own. It asks for a starter; `-y` takes `--template none`, Plat5 alone. With an API starter (`--template go-fiber-api`):

```
my-app/
  plat5.yml               the deployment's config: organization, project and environment kinds,
                          roles, the customer console's origin under cors, the service files
  services/identity.yml   identity's service file: its url, and overrides of its resources
  services/audit.yml      audit's
  services/api.yml        your API's: its url and its routes, from the starter
  plat5.dev.yml           how plat5 start runs it on your machine
  api/                    your API, from the starter
  customer-console/       your customers' console (--no-customer-console: none)
  operator-console/       your operators' console (--no-operator-console: none)
  docker-compose.yml      runs your apps: includes each app folder's compose file
  operator.routes.yml     the operator gateway's route list, with your API's operator routes
  README.md               what's here, and how to extend it
```

Each app is a starting point the project owns, not a dependency. The starters are `go-fiber-api`, `node-fastify-api` and `bun-effect-api`: the same API (tasks inside an environment, and the person's profile) in three stacks, which both consoles work with. A starter can also be `owner/repo` on GitHub, or a `.tar.gz` URL ([starters](#starters)).

`--template none` is Plat5 alone: `plat5.yml`, `services/identity.yml`, `services/audit.yml`, `plat5.dev.yml`, and `operator.routes.yml` from operator's shipped route list. Add your own API as a service file in `services/`, list it in `plat5.yml`'s `services`, and point `app.compose` in `plat5.dev.yml` at its compose file. A service file's routes sit in a kind's scope, relative to its node (`/orgs/{organization}/…`), in the `user` scope for the person's own data, such as `/user/notification-settings` (the service gets `X-Plat5-User-Id`), or in `public` ([plat5 `docs/reference/config/services.md`](https://github.com/plat5dev/plat5/blob/master/docs/reference/config/services.md)).

Self-hosting a deployment is compose, not this CLI: [plat5 `docs/guides/deploy.md`](https://github.com/plat5dev/plat5/blob/master/docs/guides/deploy.md).

## Install

```bash
brew install plat5dev/tap/plat5
# or
curl -fsSL https://plat5.com/install.sh | sh
```

macOS and Linux, amd64 and arm64. On Windows, use WSL2. The script verifies the release's checksum and installs to `/usr/local/bin` (or `~/.local/bin`); `PLAT5_CLI_VERSION=v0.10.0` pins a release, `PLAT5_INSTALL_DIR` picks the directory. Or download a tarball from [GitHub Releases](https://github.com/plat5dev/cli/releases). `plat5 --version` prints `plat5 0.9.0` (no leading `v`); `plat5 version` adds the image versions the project pins.

From source: `go install github.com/plat5dev/cli/cmd/plat5@latest`.

## What `plat5 start` runs

One compose project, `plat5-<project_id>`, with one Postgres (a database per plane), then your app's own compose file.

| Module | What | Port | Login |
|--------|------|------|-------|
| core (always) | Gateway | 5400 | |
| | Control plane (admin API) | 5402 | admin token `dev-admin-token` |
| `auth` | Plat5 Auth, the customer IdP, in dev mode | 5401 | `dev@example.com` / `password` |
| `observability` | Grafana (grafana/otel-lgtm: Loki, Tempo, Prometheus) | 5403 | none |
| | OTLP (Alloy, which also reads every container's logs) | 4318 HTTP, 4317 gRPC | |
| `operator` | Operator gateway ([operator](https://github.com/plat5dev/operator)) on `operator.routes.yml`, and operator-audit, the operator audit log | 5410 | a dev token from its IdP |
| | Operator IdP: a second Plat5 Auth, in dev mode, never the customer IdP | 5411 | `operator@example.com` / `password` |
| | Operator console (`operator.console`, in your app's compose file) | 5412 | `operator@example.com` / `password` |
| your app (`app.compose`) | whatever its compose file runs | its own | |

All modules are on unless `plat5.dev.yml` turns one off. `plat5 start -x observability` skips one for a run; `-x app` runs everything but your app, so you can run it on your machine.

The operator module runs operator's images as operator's dev compose does (operator `compose/docker-compose.yml`): the gateway on Plat5's network with `IDENTITY_URL` set to identity, the project's `operator.routes.yml` mounted as its `ROUTES_FILE`, operator-audit in its own database on the one Postgres, and the operator IdP at the project's auth pin. The operator console is an app the project owns: `docker-compose.yml` includes its compose file, and `start` runs it only with the module, so turning the module off, or `-x operator`, leaves the folder and runs none of it. `start` restarts the operator gateway when `operator.routes.yml` changes; it reads its routes at boot.

A busy port is an error naming it, never a silent move: stop what holds it, or pin another under `ports:`. When another Plat5 project holds it, the error names the project and how to stop it. Running `plat5 start` again updates the stack in place.

`start` prints its steps, and each image it has to pull, one per line. Docker compose's own pull, build, and container output goes to `start.log` in the stack dir; a failure prints its last lines and the path. `-v` / `--verbose` shows it all instead.

Once the control plane is up, `start` applies `plat5.yml` as `plat5 apply` does ([plat5 `docs/reference/cli.md`](https://github.com/plat5dev/plat5/blob/master/docs/reference/cli.md#the-local-stack)). It applies without a prompt; a blocked plan (a kind removed, or its parent changed, while it has nodes) is printed and refused, and a gateway replica that refuses the new revision fails `start`. Apply checks the config's rules, as in any deployment: a permission a role grants that no route requires, and that is no kind's `list_permission`, is refused, so a typo in a role stops `start` here. A route permission no role grants is legal: only a `"*"` role holds it.

Then it makes sure there is something to call with, through identity's routes at the gateway like any client: the dev person (`dev@example.com`, named "Dev User" by the `name` claim of the dev token it asks local Auth for; `GET /user`), a node named `Dev` of each root kind they own (found through `GET /user/nodes?kind=organization`, or made with `POST /orgs`; `/orgs/dev`), and one personal key (`POST /user/keys`), which acts as them in every tree. Every credential, token or key, is sent as `Authorization: Bearer`. It reuses them while the key works. A config without those routes gets a warning. Then it prints every link, login, and key; `plat5 status` prints them again.

```
Plat5
  Gateway        http://localhost:5400
  Auth           http://localhost:5401  dev@example.com / password
  …
Operator
  Gateway        http://localhost:5410  routes: operator.routes.yml
  IdP            http://localhost:5411  operator@example.com / password
  Console        http://localhost:5412  operator@example.com / password
  …
Dev (dev@example.com)
  organization   /orgs/dev              "Dev", which they own
  Personal key   plat5-k-2-…            acts as them in every tree

Try it:
  curl http://localhost:5400/orgs/dev -H "Authorization: Bearer plat5-k-2-…"

As an operator, with a dev token from the operator IdP:
  TOKEN=$(curl -s http://localhost:5411/dev/token -H 'Content-Type: application/json' \
    -d '{"email":"operator@example.com","client_id":"operator-console"}' | jq -r .access_token)
  curl http://localhost:5410/nodes/<the Dev org's id> -H "Authorization: Bearer $TOKEN"
```

The operator gateway takes node ids, not slugs: operator [`docs/reference/routes.md`](https://github.com/plat5dev/operator/blob/master/docs/reference/routes.md). Every call through it is in the operator audit log, `GET /operator-audit-events?node_id=…`.

Local Auth runs in dev mode: `POST /dev/token` mints a token for any email, and login codes for new sign-ups are in `plat5 logs auth | grep code`. The sign-in page says it's the local stack and shows the dev login. The operator IdP is the same in its own colors, with its own users, at `:5411`.

## Commands

| Command | |
|---------|--|
| `plat5 init [dir] [--template <starter>]` | Create a project. Asks for the starter and the modules; `-y` takes the defaults (`--template none`, Plat5 alone, and every module). `--no-auth`, `--no-observability`, `--no-operator` turn a module off. With a starter, `--no-customer-console` and `--no-operator-console` leave a console out |
| `plat5 start [-x module,…] [-v]` | Run the stack and your app, apply `plat5.yml`, seed the dev person, print the summary |
| `plat5 status` | The summary again, and container health |
| `plat5 stop` | Stop everything. Data (volumes) is kept |
| `plat5 logs [service…]` | Container logs. Follows on a terminal; piped, prints and exits. `--app` for your app; `-f` / `-f=false` to choose |
| `plat5 doctor` | Check Docker, `plat5.dev.yml`, `plat5.yml`, `operator.routes.yml`, and ports, the operator console's included (naming the Plat5 project that holds a busy one) |
| `plat5 validate [plat5.yml]` | Check a deployment's config against the schema, offline |
| `plat5 plan [plat5.yml] [-out plan.json] [--detailed-exitcode]` | Dry run: the diff applying it would make |
| `plat5 apply [plat5.yml \| plan.json] [--auto-approve]` | Plan, confirm, apply, or apply a saved plan; then wait up to 10s for the gateway's replicas to load it, and exit 1 if one refused it |
| `plat5 routes list [plat5.yml]` | Every service's expanded routes, identity's and audit's included: method, public path, scope, upstream, permissions. A dry run: the routes the control plane expanded |
| `plat5 config pull [--revision n] [dir]` | Write an applied revision as `plat5.yml` and `services/<service>.yml` |
| `plat5 version` / `--version` | The CLI version, and the image versions `plat5.dev.yml` pins |

`validate`, `plan`, `apply`, `routes list` and `config pull` work on a deployment's config: `plat5.yml` and the service files it lists ([plat5 `docs/reference/cli.md`](https://github.com/plat5dev/plat5/blob/master/docs/reference/cli.md)). `validate` is offline; the others talk to the local control plane (`http://localhost:5402`, `dev-admin-token`) unless `--control-plane-url` / `--admin-token` (or `PLAT5_CONTROL_PLANE_URL` / `PLAT5_ADMIN_TOKEN`) point elsewhere.

## `plat5.dev.yml`

How `plat5 start` runs the project on your machine. A deployment never reads it, and it holds nothing that's in `plat5.yml`. Found by walking up from the working directory; `plat5.yml` is the file beside it. Relative paths resolve against its directory. Unknown keys are errors, with a hint that a newer CLI may know them.

`cli:` is the oldest CLI that reads the file. `plat5 init` writes the version that introduced the newest key it uses (`internal/config.SchemaVersion`), not its own version, and every command checks it first: an older CLI says the project needs a newer one and how to upgrade, instead of failing on a key. Raise `SchemaVersion` in a release that adds a key.

```yaml
project_id: my-app                 # default: the directory name
cli: ">=0.9.0"                     # the oldest plat5 CLI that reads this file

versions:                          # image tags; plat5 init writes the latest of each
  plat5: v0.9.0
  auth: v0.2.2                     # also the operator IdP's
  operator: v0.5.0

modules:                           # all true unless set false
  auth: true
  observability: true
  operator: true

app:                               # your app; plat5 start runs it after Plat5
  compose: ./docker-compose.yml
  web: http://localhost:3001       # its browser origin: allowed to sign in with local Auth

operator:                          # the operator module
  routes: ./operator.routes.yml    # the operator gateway's ROUTES_FILE; required with the module
  console:                         # the operator console: app.compose includes it, start runs it with the module
    compose: ./operator-console/docker-compose.yml
    web: http://localhost:5412     # its origin: allowed to sign in with the operator IdP and call the operator gateway
  origins: []                      # more origins the operator IdP and gateway allow; each also allows <origin>/callback

# Optional
ports:                             # host ports; these are the defaults
  gateway: 5400
  auth: 5401
  control_plane: 5402
  grafana: 5403
  otlp_grpc: 4317
  otlp_http: 4318
  operator: 5410
  operator_auth: 5411
admin_token: dev-admin-token       # local only
apikey_brand: plat5                # keys are {brand}-k-2-…
auth:                              # local Auth, beyond app.web
  clients: [plat5]                 # also the gateway's audiences (Plat5 Auth's aud is the client id)
  origins: []                      # each also allows <origin>/callback
  redirect_uris: []
  theme_file: ./theme.json         # OpenAuth Theme JSON for the sign-in page
idp:                               # with modules.auth false: the gateway's IdP
  issuer: https://idp.example.com
  jwks_uri: https://idp.example.com/.well-known/jwks.json
  audiences: [my-api]
  subject_claim: sub               # the person's IdP subject (AUTH_SUBJECT_CLAIM)
otel:
  endpoint: http://host.docker.internal:4318   # your collector, instead of the observability module
source:                            # contributors: build from checkouts (see below)
  plat5: ../plat5
```

### Your app and its url

A service file's `url` is the address the gateway reaches, in the local stack as in any deployment, so `plat5 start` and `plat5 apply` send the same config. Write it with your app's compose service name: `url: http://api:3000`.

When `plat5 start` runs `app.compose`, each service a `url` names is put on the Plat5 network with a generated override (`app-override.yml` in the stack dir), so the gateway reaches it by name, the same way on every OS. Its host port can then be bound to `127.0.0.1`: only the gateway may reach a Plat5 service, and the host port is for your own curls. With `plat5 start -x app` there is no container: `start` maps the name to your machine on the gateway (`extra_hosts: api:host-gateway`), so the app must listen there on the url's port, beyond loopback on Linux. A service named like a Plat5 service (`gateway`, `postgres`, …) can't join; rename it.

The template contract for an app's compose file: `plat5 start` runs it with `docker compose up -d --build --wait` and this env, so it can say `${PLAT5_GATEWAY_URL}`:

| Env | |
|-----|--|
| `PLAT5_GATEWAY_URL` | The gateway, as the browser reaches it |
| `PLAT5_AUTH_URL` | The customer IdP's issuer, as the browser reaches it |
| `PLAT5_AUTH_CLIENT_ID` | The OAuth client id (`plat5`) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Where to send traces and metrics (Alloy on the host, `http://host.docker.internal:4318`), or empty |
| `PLAT5_OPERATOR_URL` | The operator gateway, as the browser reaches it |
| `PLAT5_OPERATOR_AUTH_URL` | The operator IdP's issuer, as the browser reaches it |
| `PLAT5_OPERATOR_CLIENT_ID` | The operator console's OAuth client id (`operator-console`) |

## Starters

`--template` takes a first-party name (`go-fiber-api`, `node-fastify-api`, `bun-effect-api`: the repo `plat5dev/template-<name>`), `owner/repo` on GitHub, or a `.tar.gz` URL. A starter and the consoles (`plat5dev/template-customer-console`, `plat5dev/template-operator-console`) are downloaded as GitHub archives at `--template-ref` (or `PLAT5_TEMPLATE_REF`; default `master`; a branch, tag, or commit), and nothing is cached: each `init` fetches again. `init` prints the commit each came from. It refuses a directory that already has any file a starter writes, before writing anything.

From an API starter, `init`:

- copies the starter into `api/`, the customer console into `customer-console/` and the operator console into `operator-console/`, each without its own project files (`plat5.yml`, `plat5.dev.yml`, `services/`, `plat5.template.yml`), its operator routes, `.github`, the manifest's `exclude`, or local state (`.git`, `node_modules`, `dist`, `.env`, `*.local`, `*.log`, `.DS_Store`, `data/*.db`), and replaces `{{project_id}}` in each `substitute` file with the project id;
- writes the starter's service file as `services/<service>.yml` and lists it in `plat5.yml`, beside identity's and audit's, with the console's `app.web` under `cors.allowed_origins` ([plat5 `docs/reference/config/cors.md`](https://github.com/plat5dev/plat5/blob/master/docs/reference/config/cors.md));
- writes `docker-compose.yml`, which `include:`s each app's compose file, and `plat5.dev.yml` with `app.compose: ./docker-compose.yml`, `app.web` the customer console's origin, and `operator.console` the operator console's compose file and origin;
- writes `operator.routes.yml`: operator's shipped route list (operator `routes.yml`) with the starter's operator routes added under its `services:`, in operator's route format (operator [`docs/reference/routes.md`](https://github.com/plat5dev/operator/blob/master/docs/reference/routes.md)), and names it as `operator.routes` in `plat5.dev.yml`. `plat5 start`'s operator gateway mounts it as its `ROUTES_FILE`; a deployed operator gateway mounts a copy with the deployment's addresses.

A template's manifest, `plat5.template.yml` at its root. Unknown keys are errors.

```yaml
name: go-fiber-api
kind: api                              # api: an API starter; customer-console, operator-console: the consoles
description: Go + Fiber reference API
service: services/api.yml              # api: its service file, one service, keyed by its name
operator_routes: ./routes.operator.yml # api, optional: a services: block in operator's route format
substitute: [go.mod]                   # files whose {{project_id}} becomes the project id
app:
  compose: docker-compose.yml          # how plat5 start runs it, to the contract above
  web: http://localhost:3001           # the consoles: its browser origin
next: [go mod tidy, go run .]          # how to run it yourself with plat5 start -x app
exclude: [AGENTS.md]                   # paths, from its root, not to copy
```

The service file's `url` names the app's compose service (`http://api:3000`), so `plat5 start` joins it to the Plat5 network ([your app and its url](#your-app-and-its-url)). Each starter's repo root is also a project you can run alone: its own `plat5.yml`, `plat5.dev.yml` and `services/`, which `init` leaves out.

## Contributors: build from checkouts

`source:` builds a module's images from a local checkout instead of pulling them, on the same embedded stack (prod targets: what ships). Keys are the `versions:` keys; a sourced module needs no version. `init`'s `--source` takes one `key=path` per checkout and repeats; it fails on a path that isn't a directory.

```bash
plat5 init my-app --source plat5=../plat5 --source auth=../auth --source operator=../operator
cd my-app && plat5 start      # builds, then runs; the first Rust build takes a while
```

A CLI built from this checkout reports a pseudo-version older than the `cli:` floor `init` writes, so every command refuses the project. Build it with the version flag, at the floor (`internal/config.SchemaVersion`) or newer:

```bash
go build -ldflags "-X github.com/plat5dev/cli/internal/cmd.Version=0.9.0" -o plat5 ./cmd/plat5
```

`--templates-dir` (or `PLAT5_TEMPLATES_DIR`) reads first-party templates from checkouts instead of GitHub, each in its repo's folder: with `--templates-dir ..`, `go-fiber-api` is `../template-go-fiber-api` and the consoles are `../template-customer-console` and `../template-operator-console`.

```bash
plat5 init my-app --templates-dir .. --template go-fiber-api -y
```

The embedded compose files (`internal/bundle/stack`) track plat5 HEAD, so a `plat5` checkout at HEAD runs as it will ship. For hot reload of one service, use that repo's own dev compose.

Two copies track plat5 too: the config schema (`internal/schema`, plat5's `schema/`, for offline `validate`) and the files `init` writes (`internal/cmd/initfiles`, which load into the config plat5's e2e applies, `e2e/testdata/config.json`). Two track operator: its shipped route list (`internal/cmd/operatorfiles/routes.yml`, operator's `routes.yml`), which `operator.routes.yml` starts from, and the operator module's compose file (`internal/bundle/stack/operator.yml`), whose env is operator's `compose/docker-compose.yml`'s. Their tests compare them with a `plat5` and an `operator` checkout beside the CLI, and skip without one. So do the tests that init a project from each first-party template checkout beside the CLI.

## State

`$XDG_STATE_HOME/plat5/projects/<project_id>/` (default `~/.local/state/plat5/…`, on macOS too): `state.json` (what the last start ran, and the dev keys; mode 0600) and `stack/` (the compose files, the generated override, and the sign-in themes, the operator IdP's included).
