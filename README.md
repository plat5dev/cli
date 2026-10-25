# Plat5 CLI

Run Plat5 locally, and manage a deployment's config. Everything runs in Docker: the CLI is one binary and needs nothing else.

```bash
plat5 init my-app       # a project: the config, its service files, and how to run it
cd my-app
plat5 start             # Plat5 and its modules; applies the config; prints links, logins, a key
```

`plat5 init` writes a project you own:

```
my-app/
  plat5.yml               the deployment's config: organization and project kinds, roles
  services/identity.yml   identity's service file: its url, and overrides of its resources
  services/audit.yml      audit's
  plat5.dev.yml           how plat5 start runs it on your machine
```

Starters (an API and consoles to copy into the project) aren't available yet: `--template none`, the default, is the only one. Add your own API as a service file in `services/`, list it in `plat5.yml`'s `services`, and point `app.compose` in `plat5.dev.yml` at its compose file.

Self-hosting a deployment is compose, not this CLI: [plat5 `docs/guides/deploy.md`](https://github.com/plat5dev/plat5/blob/master/docs/guides/deploy.md).

## Install

```bash
brew install plat5dev/tap/plat5
# or
curl -fsSL https://plat5.com/install.sh | sh
```

macOS and Linux, amd64 and arm64. On Windows, use WSL2. The script verifies the release's checksum and installs to `/usr/local/bin` (or `~/.local/bin`); `PLAT5_CLI_VERSION=v0.8.0` pins a release, `PLAT5_INSTALL_DIR` picks the directory. Or download a tarball from [GitHub Releases](https://github.com/plat5dev/cli/releases). `plat5 --version` prints `plat5 0.8.0` (no leading `v`); `plat5 version` adds the image versions the project pins.

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
| your app (`app.compose`) | whatever its compose file runs | its own | |

All modules are on unless `plat5.dev.yml` turns one off. `plat5 start -x observability` skips one for a run; `-x app` runs everything but your app, so you can run it on your machine.

A busy port is an error naming it, never a silent move: stop what holds it, or pin another under `ports:`. When another Plat5 project holds it, the error names the project and how to stop it. Running `plat5 start` again updates the stack in place.

`start` prints its steps, and each image it has to pull, one per line. Docker compose's own pull, build, and container output goes to `start.log` in the stack dir; a failure prints its last lines and the path. `-v` / `--verbose` shows it all instead.

Once the control plane is up, `start` applies `plat5.yml` as `plat5 apply` does ([plat5 `docs/reference/cli.md`](https://github.com/plat5dev/plat5/blob/master/docs/reference/cli.md#the-local-stack)). It applies without a prompt; a blocked plan (a kind removed, or its parent changed, while it has nodes) is printed and refused, and a gateway replica that refuses the new revision fails `start`. Then it compares the labels routes require with the labels roles grant. A label on one side only, a letter or two from one on the other, is warned as a likely typo, whichever side it is on: `stats:read (GET /orgs/{organization}/stats) and stats:raed (role member)`. A required label no role grants by name is how an owner-only route is written, so it is a quiet note. With no role holding `"*"`, that note is a warning: nobody can call the route.

Then it makes sure there is something to call with, through the generated routes like any client: the dev person (`dev@example.com`, named "Dev User" by the `name` claim of the dev token it asks local Auth for), a node labelled `Dev` of each root kind they own (`/orgs/dev`), and one personal key, which acts as them in every tree. It reuses them while the key works. A config without those routes gets a warning. Then it prints every link, login, and key; `plat5 status` prints them again.

```
Plat5
  Gateway        http://localhost:5400
  Auth           http://localhost:5401  dev@example.com / password
  …
Dev (dev@example.com)
  organization   /orgs/dev              "Dev", which they own
  Personal key   plat5-k-2-…            acts as them in every tree

Try it:
  curl http://localhost:5400/orgs/dev -H "X-API-Key: plat5-k-2-…"
```

Local Auth runs in dev mode: `POST /dev/token` mints a token for any email, and login codes for new sign-ups are in `plat5 logs auth | grep code`. The sign-in page says it's the local stack and shows the dev login.

## Commands

| Command | |
|---------|--|
| `plat5 init [dir]` | Create a project. Asks for the modules; `-y` takes the defaults. `--template none` (the default) is the only starter |
| `plat5 start [-x module,…] [-v]` | Run the stack and your app, apply `plat5.yml`, seed the dev person, print the summary |
| `plat5 status` | The summary again, and container health |
| `plat5 stop` | Stop everything. Data (volumes) is kept |
| `plat5 logs [service…]` | Container logs. Follows on a terminal; piped, prints and exits. `--app` for your app; `-f` / `-f=false` to choose |
| `plat5 doctor` | Check Docker, `plat5.dev.yml`, `plat5.yml`, and ports (naming the Plat5 project that holds a busy one) |
| `plat5 validate [plat5.yml]` | Check a deployment's config against the schema, offline |
| `plat5 plan [plat5.yml] [-out plan.json] [--detailed-exitcode]` | Dry run: the diff applying it would make |
| `plat5 apply [plat5.yml \| plan.json] [--auto-approve]` | Plan, confirm, apply, or apply a saved plan; then wait up to 10s for the gateway's replicas to load it, and exit 1 if one refused it |
| `plat5 routes generate [plat5.yml]` | Write `routes/identity.yml` and `routes/audit.yml` from the config's kinds |
| `plat5 config pull [--revision n] [dir]` | Write an applied revision as `plat5.yml` and `services/<service>.yml` |
| `plat5 version` / `--version` | The CLI version, and the image versions `plat5.dev.yml` pins |

`validate`, `plan`, `apply`, `routes generate` and `config pull` work on a deployment's config: `plat5.yml` and the service files it lists ([plat5 `docs/reference/cli.md`](https://github.com/plat5dev/plat5/blob/master/docs/reference/cli.md)). They talk to the local control plane (`http://localhost:5402`, `dev-admin-token`) unless `--control-plane-url` / `--admin-token` (or `PLAT5_CONTROL_PLANE_URL` / `PLAT5_ADMIN_TOKEN`) point elsewhere.

## `plat5.dev.yml`

How `plat5 start` runs the project on your machine. A deployment never reads it, and it holds nothing that's in `plat5.yml`. Found by walking up from the working directory; `plat5.yml` is the file beside it. Relative paths resolve against its directory. Unknown keys are errors, with a hint that a newer CLI may know them.

`cli:` is the oldest CLI that reads the file. `plat5 init` writes the version that introduced the newest key it uses (`internal/config.SchemaVersion`), not its own version, and every command checks it first: an older CLI says the project needs a newer one and how to upgrade, instead of failing on a key. Raise `SchemaVersion` in a release that adds a key.

```yaml
project_id: my-app                 # default: the directory name
cli: ">=0.8.0"                     # the oldest plat5 CLI that reads this file

versions:                          # image tags; plat5 init writes the latest of each
  plat5: v0.8.0
  auth: v0.2.2

modules:                           # all true unless set false
  auth: true
  observability: true

app:                               # your app; plat5 start runs it after Plat5
  compose: ./docker-compose.yml
  web: http://localhost:3001       # its browser origin: allowed to sign in with local Auth

# Optional
ports:                             # host ports; these are the defaults
  gateway: 5400
  auth: 5401
  control_plane: 5402
  grafana: 5403
  otlp_grpc: 4317
  otlp_http: 4318
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

## Contributors: build from checkouts

`source:` builds a module's images from a local checkout instead of pulling them, on the same embedded stack (prod targets: what ships). Keys are the `versions:` keys; a sourced module needs no version.

```bash
plat5 init my-app --source plat5=../plat5 --source auth=../auth
cd my-app && plat5 start      # builds, then runs; the first Rust build takes a while
```

The embedded compose files (`internal/bundle/stack`) track plat5 HEAD, so a `plat5` checkout at HEAD runs as it will ship. For hot reload of one service, use that repo's own dev compose.

Two copies track plat5 too: the config schema (`internal/schema`, for offline `validate`) and the files `init` writes (`internal/cmd/initfiles`, plat5's `compose/seed`). Their tests compare them with a `plat5` checkout beside the CLI, and skip without one.

## State

`$XDG_STATE_HOME/plat5/projects/<project_id>/` (default `~/.local/state/plat5/…`, on macOS too): `state.json` (what the last start ran, and the dev keys; mode 0600) and `stack/` (the compose files, the generated override, and the sign-in themes).
