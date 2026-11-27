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
  plat5.yml               the deployment's config, from the starter: its kinds, roles and service
                          files, plus the customer console's origin under cors
  services/               the service files it lists, from the starter: your API's, identity's, audit's
  plat5.local.yml           how plat5 start runs it on your machine
  api/                    your API, from the starter
  customer-console/       your customers' console (--no-customer-console: none)
  operator-console/       your operators' console (--no-operator-console: none)
  docker-compose.yml      runs your apps: includes each app folder's compose file
  operator.yml            the operator gateway's config (control plane, services, authorizer)
  README.md               what's here, and how to run it
```

Each app is a starting point the project owns, not a dependency. The first-party starters are `go-fiber-api`, `node-fastify-api` and `bun-effect-api`, each with the consoles beside it; what each one's API does is in its own README. A starter can also be `owner/repo` on GitHub, or a `.tar.gz` URL ([starters](#starters)).

`--template none` is Plat5 alone: `plat5.yml` with one kind, `organization`, and the roles `admin` and `member`, `services/identity.yml`, `services/audit.yml`, `plat5.local.yml`, and `operator.yml` for the operator gateway. Add your own API as a service file in `services/`, list it in `plat5.yml`'s `services`, and point `app.compose` in `plat5.local.yml` at its compose file. A service file's routes sit in a kind's scope, relative to its node (`/orgs/{organization}/…`), in the `user` scope for the person's own data, such as `/user/notification-settings` (the service gets `X-Plat5-User-Id`), or in `public` ([plat5 `docs/reference/config/services.md`](https://github.com/plat5dev/plat5/blob/master/docs/reference/config/services.md)).

Self-hosting a deployment is compose, not this CLI: [plat5 `docs/guides/deploy.md`](https://github.com/plat5dev/plat5/blob/master/docs/guides/deploy.md).

## Install

```bash
brew install plat5dev/tap/plat5
# or
curl -fsSL https://plat5.com/install.sh | sh
```

macOS and Linux, amd64 and arm64. On Windows, use WSL2. The script verifies the release's checksum and installs to `/usr/local/bin` (or `~/.local/bin`); `PLAT5_CLI_VERSION=v0.17.0` pins a release, `PLAT5_INSTALL_DIR` picks the directory. Or download a tarball from [GitHub Releases](https://github.com/plat5dev/cli/releases). `plat5 --version` prints `plat5 0.17.0` (no leading `v`); `plat5 version` adds the image versions the project pins.

From source: `go install github.com/plat5dev/cli/cmd/plat5@latest`.

## What `plat5 start` runs

One compose project, `plat5-<project_id>`, with one Postgres (a database per plane), then your app's own compose file, as its own compose project, `<project_id>` ([State](#state)).

| Module | What | Port | Login |
|--------|------|------|-------|
| core (always) | Gateway | 5400 | |
| | Control plane (admin API) | 5402 | admin token `dev-admin-token` |
| `auth` | Plat5 Auth, the customer IdP, in dev mode | 5401 | `dev@example.com` / `password` |
| `observability` | Grafana (grafana/otel-lgtm: Loki, Tempo, Prometheus) | 5403 | none |
| | OTLP (Alloy, which also reads every container's logs) | 4318 HTTP, 4317 gRPC | |
| `operator` | Operator gateway ([operator](https://github.com/plat5dev/operator)) on `operator.yml`, and operator-audit, the operator audit log | 5410 | a dev token from its IdP |
| | Operator IdP: a second Plat5 Auth, in dev mode, never the customer IdP | 5411 | `operator@example.com` / `password` |
| | Operator console (`operator.console`, in your app's compose file) | 5412 | `operator@example.com` / `password` |
| `authorizer` *(opt-in)* | Topaz OpenID AuthZEN 1.0 PDP (`authorizer:`, `http://topaz:8383`) and Directory v3 REST API | 5420, 5421 | none |
| your app (`app.compose`) | whatever its compose file runs | its own | |

`auth`, `observability`, and `operator` are on unless `plat5.local.yml` turns one off; `authorizer` is off unless turned on (`plat5 init --authorizer topaz`). `plat5 start -x observability` skips one for a run; `-x app` runs everything but your app, so you can run it on your machine.

The operator module runs operator's images as operator's dev compose does (operator `compose/docker-compose.yml`): the gateway on Plat5's network with `IDENTITY_URL` set to identity, the project's `operator.yml` mounted as its `CONFIG_FILE`, operator-audit in its own database on the one Postgres, and the operator IdP at the project's auth pin. The operator console is an app the project owns: `docker-compose.yml` includes its compose file, and `start` runs it only with the module, so turning the module off, or `-x operator`, leaves the folder and runs none of it. `start` restarts the operator gateway when `operator.yml` changes.

A busy port is an error naming it, never a silent move: stop what holds it, or pin another under `ports:`. When another Plat5 project holds it, the error names the project and how to stop it. Running `plat5 start` again updates the stack in place.

`start` prints its steps, and each image it has to pull, one per line. Docker compose's own pull, build, and container output goes to `start.log` in the stack dir; a failure prints its last lines and the path. `-v` / `--verbose` shows it all instead.

Once the control plane is up, `start` applies `plat5.yml` as `plat5 apply` does. It applies without a prompt; a blocked plan (a kind removed, or its parent changed, while it has nodes) is printed and refused, and a gateway replica that refuses the new revision fails `start`. Apply checks the config's rules, as in any deployment. A role may grant any permission, and a route permission no role grants is legal: only a `"*"` role holds it.

Then it makes sure there is something to call with, through identity's routes at the gateway like any client: the dev person (`dev@example.com`, signed in with a dev token from local Auth; `GET /user`), their name, "Dev Person" (`PATCH /user`, while they have none, since local Auth's tokens carry no name; a name already set stays), a node named `Dev` of each root kind they own (found through `GET /user/nodes?kind=organization`, or made with `POST /orgs`; `/orgs/dev`), and one personal key (`POST /user/keys`), which acts as them in every tree. Every credential, token or key, is sent as `Authorization: Bearer`. It reuses them while the key works. A config without those routes gets a warning. Then it prints every link, login, and key; `plat5 status` prints them again.

```
my-app is running.

  Customer console  http://localhost:3001  dev@example.com / password
  Operator console  http://localhost:5412  operator@example.com / password

Plat5
  Gateway           http://localhost:5400
  Auth              http://localhost:5401  sign-up codes: plat5 logs auth | grep code
  Control plane     http://localhost:5402  admin token dev-admin-token
  Operator gateway  http://localhost:5410  config: operator.yml
  Operator IdP      http://localhost:5411
  Grafana           http://localhost:5403  Plat5 folder: Service Health, Auth, Debug

Dev person  dev@example.com
  Personal key      plat5-k-2-…            acts as them in every tree

Open http://localhost:3001 and sign in as dev@example.com / password.
```

The consoles lead, each with its login; a module turned off, or apps not run, drops its rows. `start` ends by pointing at the customer console; `status` lists container health instead.

The operator gateway routes by service name (`/services/{service}/{path…}`) and takes node ids, not slugs: operator [`docs/reference/config.md`](https://github.com/plat5dev/operator/blob/master/docs/reference/config.md). Every call through it is in the operator audit log, `GET /services/operator-audit/operator-audit-events?node_id=…`.

Local Auth runs in dev mode: `POST /dev/token` mints a token for any email, and login codes for new sign-ups are in `plat5 logs auth | grep code`. The sign-in page says it's the local stack and shows the dev login. The operator IdP is the same in its own colors, with its own users, at `:5411`.

## Commands

| Command | |
|---------|--|
| `plat5 init [dir] [--template <starter>] [--preset <name>] [--authorizer <pdp>]` | Create a project. Asks for the starter, preset (with `--template none`), modules, and optional AuthZEN authorizer; `-y` takes the defaults (`--template none`, `--preset minimal`, default modules, `--authorizer none`). Presets: `minimal`, `saas`, `enterprise`, `public`. `--authorizer topaz` adds the local Topaz module, `topaz/manifest.yaml`, top-level `authorizer:` (replacing built-in RBAC), and (with `--template none`) record-level example routes. `--no-auth`, `--no-observability`, `--no-operator` turn a module off. With a starter, `--no-customer-console` and `--no-operator-console` leave a console out. `--project-id <id>` sets the project id ([State](#state)); `--sources <dir>` runs checkouts ([contributors](#contributors-run-checkouts)) |
| `plat5 start [-x module,…] [-v]` | Run the stack and your app, apply `plat5.yml`, seed the dev person, print the summary |
| `plat5 status` | The summary again, and container health |
| `plat5 stop` | Stop everything. Data (volumes) is kept |
| `plat5 reset [-y]` | Stop everything and delete the project's volumes: Plat5's and the operator's databases, your apps' volumes, Grafana's data. The next `start` seeds a fresh dev person. Asks first unless `-y` |
| `plat5 logs [service…]` | Container logs. Follows on a terminal; piped, prints and exits. `--app` for your app; `-f` / `-f=false` to choose |
| `plat5 doctor` | Check Docker, `plat5.local.yml`, `plat5.yml`, `operator.yml`, and ports, the operator console's included (naming the Plat5 project that holds a busy one) |
| `plat5 validate [-f file …] [plat5.yml]` | Check a deployment's config against the schema, offline (`--auth-config <file>` validates a control-plane `AUTH_CONFIG_FILE` against `control_plane_auth.json`) |
| `plat5 plan [-f file …] [plat5.yml] [-out plan.json] [--detailed-exitcode]` | Dry run: the diff applying it would make |
| `plat5 apply [-f file …] [plat5.yml \| plan.json] [--auto-approve] [--reason <text>] [--ref <ref>]` | Plan, confirm, apply, or apply a saved plan (attaching optional `X-Plat5-Change-Reason` / `X-Plat5-Change-Ref`, with `--ref` auto-filled from `GITHUB_SHA`/`CI_COMMIT_SHA`); then wait up to 10s for the gateway's replicas to load it, and exit 1 if one refused it |
| `plat5 login [--issuer <name>] [--device-code] [--no-browser]` | Sign in to an OIDC-enabled control plane (RFC 8252 PKCE or RFC 8628 Device Flow) and cache tokens in `$XDG_STATE_HOME/plat5/credentials.json` (`0600`) |
| `plat5 logout [--all]` | Remove cached control-plane login credentials |
| `plat5 whoami` | Show the verified control-plane identity and `read` / `apply` access level (`GET /auth/whoami`) |
| `plat5 routes list [-f file …] [plat5.yml]` | Every service's expanded routes, identity's and audit's included: method, public path, scope, upstream, permissions. A dry run: the routes the control plane expanded |
| `plat5 routes generate [-f file …] [plat5.yml]` | Write identity's and audit's routes for every kind into `services/identity.yml` and `services/audit.yml`, overwriting them but keeping their `url`, `timeouts` and `max_body_bytes`. Offline. Run it after adding or removing a kind, or changing a kind's `path` or `list_permission`; review the diff, and delete again any route you had dropped |
| `plat5 config pull [--revision n] [dir]` | Write an applied revision as `plat5.yml` and `services/<service>.yml` |
| `plat5 authorizer sync [manifest.yaml]` | Upload `authorizer.manifest` (or a file argument) to Topaz's Directory v3 API without restarting the stack |
| `plat5 version` / `--version` | The CLI version, and the image versions `plat5.local.yml` pins |

`validate`, `plan`, `apply`, `routes list`, `routes generate`, `config pull`, `login`, `logout`, and `whoami` work against a control plane. `validate` and `routes generate` are offline; the others talk to the local control plane (from `plat5.local.yml` when present, else `http://localhost:5402` and `dev-admin-token`) unless `--control-plane-url` / `PLAT5_CONTROL_PLANE_URL` points elsewhere.

Control-plane credentials resolve in order:
1. `--token` / `PLAT5_TOKEN` (or `--admin-token` / `PLAT5_ADMIN_TOKEN`)
2. `--token-file` / `PLAT5_TOKEN_FILE` (e.g., Kubernetes projected ServiceAccount JWT)
3. `--token-cmd` / `PLAT5_TOKEN_CMD` (external credential helper printing a token to stdout)
4. Cached `plat5 login` OIDC session for the target URL (auto-refreshed via `refresh_token`)
5. Native CI OIDC: GitHub Actions (`ACTIONS_ID_TOKEN_REQUEST_URL` + `ACTIONS_ID_TOKEN_REQUEST_TOKEN`, audience from `--oidc-audience` / `PLAT5_OIDC_AUDIENCE` / control-plane URL) or GitLab CI (`PLAT5_OIDC_TOKEN`)
6. Local `plat5.local.yml` `admin_token` / `dev-admin-token`

Pass `-f` / `--file` multiple times to layer environment overlays over a base `plat5.yml` (cannot be combined with a positional file argument):

```bash
plat5 validate -f plat5.yml -f plat5.prod.yml
plat5 plan     -f plat5.yml -f plat5.prod.yml
plat5 apply    -f plat5.yml -f plat5.prod.yml
```

Overlays merge left-to-right with RFC 7396 merge-patch semantics: maps merge recursively, scalars and lists (such as `cors.allowed_origins`) replace, and `null` deletes a key. An overlay's `services:` may be a mapping (e.g. `services: { api: { url: http://api.prod.internal:3000 } }`) or a list of service files resolved relative to the overlay file. Validation and control-plane errors name the exact file that set the failing field.

## `plat5.local.yml`

How `plat5 start` runs the project on your machine. A deployment never reads it, and it holds nothing that's in `plat5.yml`. Found by walking up from the working directory; `plat5.yml` is the file beside it. Relative paths resolve against its directory. Unknown keys are errors, with a hint that a newer CLI may know them.

`cli:` is the oldest CLI that reads the file. `plat5 init` writes the version that introduced the newest key it uses (`internal/config.SchemaVersion`), not its own version, and every command checks it first: an older CLI says the project needs a newer one and how to upgrade, instead of failing on a key. Raise `SchemaVersion` in a release that adds a key. A dev build, one built without a release version (`go build` from a checkout), passes every floor and says so in one line: `plat5: a dev build (no release version), so plat5.local.yml's cli: >=0.17.0 is not checked`.

```yaml
project_id: my-app                 # plat5 init --project-id, else the directory name; one folder per id, see State
cli: ">=0.17.0"                    # the oldest plat5 CLI that reads this file

versions:                          # image tags; plat5 init writes the latest of each
  plat5: v0.19.0
  auth: v0.4.2                     # also the operator IdP's
  operator: v0.9.0

modules:                           # auth, observability, operator default true; authorizer defaults false
  auth: true
  observability: true
  operator: true
  authorizer: false                # local Topaz OpenID AuthZEN 1.0 PDP

authorizer:                        # with modules.authorizer: true
  provider: topaz
  manifest: ./topaz/manifest.yaml
  # image: ghcr.io/aserto-dev/topaz:latest

gateway:                           # optional local gateway overrides
  scope_claim: scope               # AUTH_SCOPE_CLAIM
  audit_mode: sync                 # sync, async, or off
  rate_limit_enabled: true
  upstream_signing: false          # generates local Ed25519 key in project state

app:                               # your app; plat5 start runs it after Plat5
  compose: ./docker-compose.yml
  web: http://localhost:3001       # its browser origin: allowed to sign in with local Auth

operator:                          # the operator module
  config: ./operator.yml           # the operator gateway's CONFIG_FILE; required with the module
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
  topaz: 5420
  topaz_directory: 5421
admin_token: dev-admin-token       # local only
secret_brand: plat5                # API keys are {brand}-k-2-…, invite tokens {brand}-i-1-…
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
  name_claims: [name, preferred_username]  # JWT claims for display name seeding (AUTH_NAME_CLAIMS); [] disables
otel:
  endpoint: http://host.docker.internal:4318   # your collector, instead of the observability module
source:                            # contributors: build from checkouts; plat5 init --sources writes it (see below)
  plat5: ../plat5
```

### Your app and its url

A service file's `url` is the address the gateway reaches, in the local stack as in any deployment, so `plat5 start` and `plat5 apply` send the same config. Write it with your app's compose service name: `url: http://api:3000`.

When `plat5 start` runs `app.compose`, each service a `url` names is put on the Plat5 network with a generated override (`app-override.yml` in the stack dir), so the gateway reaches it by name, the same way on every OS. Its host port can then be bound to `127.0.0.1`: only the gateway may reach a Plat5 service, and the host port is for your own curls. With `plat5 start -x app` there is no container: `start` maps the name to your machine on the gateway (`extra_hosts: api:host-gateway`), so the app must listen there on the url's port, beyond loopback on Linux. A service named like a Plat5 service (`gateway`, `postgres`, …) can't join; rename it.

The template contract for an app's compose file: `plat5 start` runs it with `docker compose -p <project_id> up -d --build --wait` and this env, so it can say `${PLAT5_GATEWAY_URL}`:

| Env | |
|-----|--|
| `PLAT5_GATEWAY_URL` | The gateway, as the browser reaches it |
| `PLAT5_AUTH_URL` | The customer IdP's issuer, as the browser reaches it |
| `PLAT5_AUTH_CLIENT_ID` | The OAuth client id (`plat5`) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Where to send traces and metrics (Alloy on the host, `http://host.docker.internal:4318`), or empty |
| `PLAT5_OPERATOR_URL` | The operator gateway, as the browser reaches it |
| `PLAT5_OPERATOR_AUTH_URL` | The operator IdP's issuer, as the browser reaches it |
| `PLAT5_OPERATOR_CLIENT_ID` | The operator console's OAuth client id (`operator-console`) |
| `PLAT5_AUTHORIZER_URL` | Local Topaz AuthZEN URL (`http://topaz:8383`) when `modules.authorizer` is on |
| `PLAT5_TOPAZ_DIRECTORY_URL` | Local Topaz Directory v3 REST URL (`http://topaz:9393`) when `modules.authorizer` is on |
| `PLAT5_UPSTREAM_SIGNING_PUBLIC_KEY` | Base64url Ed25519 public key when `gateway.upstream_signing: true` |

## Starters

`--template` takes a first-party name (`go-fiber-api`, `node-fastify-api`, `bun-effect-api`: the repo `plat5dev/template-<name>`), `owner/repo` on GitHub, or a `.tar.gz` URL. A starter and the consoles (`plat5dev/template-solid-customer-console`, `plat5dev/template-solid-operator-console`) are downloaded as GitHub archives at `--template-ref` (or `PLAT5_TEMPLATE_REF`; default `master`; a branch, tag, or commit), and nothing is cached: each `init` fetches again. `init` prints the commit each came from. With `--sources`, one that has a checkout there is copied from it instead ([contributors](#contributors-run-checkouts)). It refuses a directory that already has any file a starter writes, before writing anything.

From an API starter, `init`:

- copies the starter into `api/`, the customer console into `customer-console/` and the operator console into `operator-console/`, each without its own project files (`plat5.yml`, `plat5.local.yml`, `services/`, `plat5.template.yml`), `.github`, the manifest's `exclude`, or local state (`.git`, `node_modules`, `dist`, `.env`, `*.local`, `*.log`, `.DS_Store`, `data/*.db`), and replaces `{{project_id}}` in each `substitute` file with the project id;
- writes the starter's config, its `config` file as `plat5.yml` and the service files it lists at the same paths, as they are: the starter owns its kinds, roles and routes. With the customer console, it adds the console's `app.web` under `cors.allowed_origins`, before `services:` ([plat5 `docs/reference/config/cors.md`](https://github.com/plat5dev/plat5/blob/master/docs/reference/config/cors.md)); a starter's `plat5.yml` that sets `cors` itself is refused;
- writes `docker-compose.yml`, which `include:`s each app's compose file, and `plat5.local.yml` with `app.compose: ./docker-compose.yml`, `app.web` the customer console's origin, and `operator.console` the operator console's compose file and origin;
- writes `operator.yml`: the operator gateway's local config (polling Plat5's control plane to discover your API's services and routes, with `authorizer.mode: disabled` for local dev; operator [`docs/reference/config.md`](https://github.com/plat5dev/operator/blob/master/docs/reference/config.md)), and names it as `operator.config` in `plat5.local.yml`. `plat5 start`'s operator gateway mounts it as its `CONFIG_FILE`; a deployed operator gateway mounts its own copy with `authorizer.mode: authzen` and the deployment's addresses.

A template's manifest, `plat5.template.yml` at its root. Unknown keys are errors.

```yaml
name: go-fiber-api
kind: api                              # api: an API starter; customer-console, operator-console: the consoles
description: Go + Fiber reference API
config: plat5.yml                      # api: its plat5.yml; init copies it and the service files it lists
substitute: [go.mod]                   # files whose {{project_id}} becomes the project id
app:
  compose: docker-compose.yml          # how plat5 start runs it, to the contract above
  web: http://localhost:3001           # the consoles: its browser origin
next: [go mod tidy, go run .]          # how to run it yourself with plat5 start -x app
exclude: [AGENTS.md]                   # paths, from its root, not to copy
```

The API's service file's `url` names the app's compose service (`http://api:3000`), so `plat5 start` joins it to the Plat5 network ([your app and its url](#your-app-and-its-url)). Each starter's repo root is also a project you can run alone: its own `plat5.yml`, `plat5.local.yml` and `services/`, which `init` writes at the project's root instead of `api/` (less `plat5.local.yml`, which init writes itself).

## Contributors: run checkouts

`source:` builds a module's images from a local checkout instead of pulling them, on the same embedded stack (prod targets: what ships). Keys are the `versions:` keys; a sourced module needs no version.

`plat5 init --sources <dir>` runs every checkout in `<dir>`, each in its repo's folder: it writes `source:` for each of `plat5`, `auth` and `operator` that has one, and copies the starter and the consoles from theirs (`template-<name>`). One without a checkout is its release, and init says so on its line.

```bash
# beside the siblings, as plat5's scripts/clone-siblings lays them out
plat5 init scratch --sources .. --project-id scratch-cli --template go-fiber-api -y
cd scratch && plat5 start     # builds, then runs; the first Rust build takes a while
```

A CLI built from this checkout with no version flag is a dev build ([`cli:`](#plat5localyml)): it runs any project, so `go build -o plat5 ./cmd/plat5` is all `--sources` needs.

The embedded compose files (`internal/bundle/stack`) track plat5 HEAD, so a `plat5` checkout at HEAD runs as it will ship. For hot reload of one service, use that repo's own dev compose.

One copy tracks plat5 too: the config schema (`internal/schema`, plat5's `schema/`, for offline `validate`). `--template none`'s files (`internal/cmd/initfiles`) are the CLI's own: its `services/identity.yml` and `services/audit.yml` are `plat5 routes generate`'s output for its `plat5.yml`, and a test checks them against the generator. Two track operator: its local starter config (`internal/cmd/operatorfiles/operator.yml`), which `operator.yml` starts from, and the operator module's compose file (`internal/bundle/stack/operator.yml`), whose env is operator's `compose/docker-compose.yml`'s. Their tests compare them with a `plat5` and an `operator` checkout beside the CLI, and skip without one. So do the tests that init a project from each first-party template checkout beside the CLI.

## State

`$XDG_STATE_HOME/plat5/projects/<project_id>/` (default `~/.local/state/plat5/…`, on macOS too): `state.json` (the folder that owns the project id, what the last start ran, and the dev keys; mode 0600) and `stack/` (the compose files, the generated override, and the sign-in themes, the operator IdP's included).

One folder per project id: the id names both compose projects, the stack's (`plat5-<project_id>`) and your app's (`<project_id>`), their volumes and images, and this state, so a second folder with the same id would take over the first one's. A folder's default id is the normalized directory name docker compose gives a project in it, so there `docker compose` sees your app's containers as `plat5` runs them; under another id, add `-p <project_id>`. `state.json` records the owning folder (absolute, symlinks resolved), the first one to `plat5 start` the id, and `start`, `stop`, `reset`, `status`, `logs` and `init` from any other folder refuse, naming both folders; `doctor` reports it as a failed check. A state with no folder recorded is the next `start`'s. To run both, give one its own id: `plat5 init --project-id b-work`, or in an existing project's `plat5.local.yml`:

```yaml
project_id: b-work
```
