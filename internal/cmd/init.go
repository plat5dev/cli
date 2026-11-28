package cmd

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/prompt"
	"github.com/plat5dev/cli/internal/release"
	"github.com/plat5dev/cli/internal/template"
	"github.com/spf13/cobra"
)

// noTemplate is --template none: Plat5 alone, with one kind, organization.
const noTemplate = "none"

// defaultStarter is the starter the init prompt highlights first.
const defaultStarter = "bun-effect-api"

// initFiles is the config --template none writes: one kind, organization, the
// roles admin and member, and identity's and audit's service files as plat5
// routes generate writes them (TestInitServiceFilesAreGenerated). A starter
// brings its own.
//
//go:embed initfiles
var initFiles embed.FS

// initFetch is how init downloads templates: GitHub (tests point it elsewhere).
var initFetch template.ResolveOptions

var (
	initTemplate          string
	initPreset            string
	initSourcesDir        string
	initProjectID         string
	initTemplateRef       string
	initYes               bool
	initForce             bool
	initNoAuth            bool
	initNoObservability   bool
	initNoOperator        bool
	initNoCustomerConsole bool
	initNoOperatorConsole bool
	initVersions          []string
)

var initCmd = &cobra.Command{
	Use:   "init [directory]",
	Short: "Create a project",
	Long: `Create a Plat5 project, then cd into it and run plat5 start:

  plat5.yml            the deployment's config: one kind, organization, its
                       roles, the service files
  services/            identity's and audit's service files
  plat5.local.yml      how plat5 start runs it on your machine
  operator.yml         the operator gateway's config (when operator is on)

With an API starter (--template go-fiber-api), instead:

  plat5.yml            the starter's config
  services/            the service files the starter's plat5.yml lists
  api/                 your API, from the starter
  customer-console/    a console for your customers (--no-customer-console: none)
  operator-console/    a console for your operators (--no-operator-console: none)
  docker-compose.yml   runs your apps
  README.md            what's here, and how to run it

Each app is a starting point the project owns, not a dependency.

  plat5 init my-app                           asks for a starter, preset, and modules
  plat5 init my-app -y                        the defaults: Plat5 alone (minimal), default modules
  plat5 init my-app --preset enterprise -y    3-level hierarchy, custom roles, tiered rate limits
  plat5 init my-app --template go-fiber-api -y
  plat5 init my-app --template go-topaz-api -y

Presets (--preset, with --template none):
  minimal     one kind (organization), admin and member roles
  saas        organization -> project, default-deny projects, guest invites
  enterprise  organization -> project -> environment, custom roles, tiered rate limits, SSO claims
  public      organization (subdomain host) -> project

Without a directory, the project is the current directory, and the project id
is the directory's name; --project-id sets another, written to plat5.local.yml's
project_id. One folder per project id: init refuses an id another folder owns.
An existing plat5.yml, service file or operator.yml is kept with
--template none; a starter refuses a project that has any of its files.

A starter is a first-party name (go-fiber-api, go-topaz-api, node-fastify-api,
bun-effect-api), owner/repo on GitHub, or a .tar.gz URL. Starters and the
consoles are downloaded at --template-ref (default master); nothing is cached.

Contributors: --sources .. runs every checkout in .. instead of its release:
the modules ../plat5, ../auth and ../operator are built from their checkouts,
and the first-party templates are copied from theirs (../template-go-fiber-api,
../template-go-topaz-api, ../template-solid-customer-console,
../template-solid-operator-console). One without a checkout is its release, and
init says so. init prints where each came from.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

func init() {
	f := initCmd.Flags()
	f.StringVar(&initTemplate, "template", noTemplate, "Starter: "+strings.Join(template.OfficialNames(), ", ")+", owner/repo, a .tar.gz URL, or none")
	f.StringVar(&initPreset, "preset", presetMinimal, "Config preset with --template none: "+strings.Join(presetNames(), ", "))
	f.BoolVarP(&initYes, "yes", "y", false, "Don't ask; take the defaults (--template none, --preset minimal, default modules)")
	f.BoolVar(&initForce, "force", false, "Overwrite an existing plat5.local.yml")
	f.BoolVar(&initNoAuth, "no-auth", false, "Turn the auth module off (then set idp: in plat5.local.yml)")
	f.BoolVar(&initNoObservability, "no-observability", false, "Turn the observability module off")
	f.BoolVar(&initNoOperator, "no-operator", false, "Turn the operator module off")
	f.BoolVar(&initNoCustomerConsole, "no-customer-console", false, "Don't add the customer console (customer-console/)")
	f.BoolVar(&initNoOperatorConsole, "no-operator-console", false, "Don't add the operator console (operator-console/)")
	f.StringVar(&initSourcesDir, "sources", "", "Run the modules and first-party templates from their checkouts in this directory (plat5, auth, operator, template-<name>)")
	f.StringVar(&initProjectID, "project-id", "", "The project id, written to plat5.local.yml (default: the directory's name)")
	f.StringVar(&initTemplateRef, "template-ref", "", "Git ref of downloaded templates (default master; or PLAT5_TEMPLATE_REF)")
	f.StringArrayVar(&initVersions, "version", nil, "Pin an image: plat5=v0.19.0 (repeatable; default the latest tag)")
	registerComposableInitFlags(initCmd)
}

type initChoices struct {
	dir string
	// starter is --template; empty for none.
	starter           string
	preset            string
	noCustomerConsole bool
	noOperatorConsole bool
	modules           map[string]bool
	// sourcesDir is --sources, absolute; empty without it. sources is each
	// module with a checkout there, by version key.
	sourcesDir string
	sources    map[string]string
	versions   map[string]string
}

func runInit(cmd *cobra.Command, args []string) error {
	if usesComposableInit(cmd) {
		return runComposableInit(cmd, args)
	}
	if !initYes && prompt.Interactive() && !cmd.Flags().Changed("template") && !cmd.Flags().Changed("preset") &&
		!cmd.Flags().Changed("no-auth") && !cmd.Flags().Changed("no-observability") && !cmd.Flags().Changed("no-operator") {
		return runInteractiveWizardInit(cmd, args)
	}
	ch, err := initChoose(cmd, args)
	if err != nil {
		return err
	}

	devPath := filepath.Join(ch.dir, config.FileName)
	if _, err := os.Stat(devPath); err == nil && !initForce {
		return fmt.Errorf("%s already exists (--force to overwrite it)", devPath)
	}
	projectID, err := initProjectIDFor(ch.dir)
	if err != nil {
		return err
	}
	if _, err := guardOwner(projectID, ch.dir, true); err != nil {
		return err
	}
	if err := os.MkdirAll(ch.dir, 0o755); err != nil {
		return err
	}

	versions, err := resolveVersions(ch)
	if err != nil {
		return err
	}

	var lay *layout
	if ch.starter != "" {
		opts := initFetch
		opts.LocalRoot = ch.sourcesDir
		opts.Ref = firstNonEmpty(initTemplateRef, strings.TrimSpace(os.Getenv("PLAT5_TEMPLATE_REF")))
		if lay, err = loadLayout(opts, ch.starter, !ch.noCustomerConsole, !ch.noOperatorConsole, ch.modules[config.ModuleOperator]); err != nil {
			return err
		}
		defer lay.close()
		if err := lay.checkFree(ch.dir); err != nil {
			return err
		}
	}

	fmt.Println("Created", relPath(mustGetwd(), ch.dir)+":")
	if lay != nil {
		if err := lay.write(ch.dir, projectID); err != nil {
			return err
		}
	} else {
		if err := writePresetFiles(ch.dir, ch.preset); err != nil {
			return err
		}
		if ch.modules[config.ModuleOperator] {
			if err := writeOperatorConfig(ch.dir); err != nil {
				return err
			}
		}
	}
	if err := os.WriteFile(devPath, []byte(renderDevYML(projectID, ch, versions, lay)), 0o644); err != nil {
		return err
	}
	fmt.Println("  wrote", config.FileName, "(project_id "+projectID+")")

	fmt.Println()
	fmt.Println("Plat5 and its modules run from:")
	for _, line := range moduleSources(ch, versions) {
		fmt.Println("  " + line)
	}

	fmt.Println()
	fmt.Println("Next:")
	if wd, _ := os.Getwd(); filepath.Clean(wd) != filepath.Clean(ch.dir) {
		fmt.Printf("  cd %s\n", relPath(wd, ch.dir))
	}
	fmt.Println("  plat5 start")
	return nil
}

// initProjectIDFor is --project-id, which must already be a project id as plat5
// makes one, or else dir's name made into one.
func initProjectIDFor(dir string) (string, error) {
	if initProjectID == "" {
		id := config.SanitizeProjectID(filepath.Base(dir))
		if id == "" {
			return "", fmt.Errorf("can't make a project id from %q: pass --project-id", filepath.Base(dir))
		}
		return id, nil
	}
	id := config.SanitizeProjectID(initProjectID)
	if id == "" {
		return "", fmt.Errorf("--project-id %q: a project id is letters, digits, - and _", initProjectID)
	}
	if id != initProjectID {
		return "", fmt.Errorf("--project-id %q: a project id is lowercase letters, digits, - and _, starting with a letter or digit, at most 64 (--project-id %s)", initProjectID, id)
	}
	return id, nil
}

// sourcesFrom is each module with a checkout in dir, in its repo's folder:
// dir/plat5, dir/auth, dir/operator.
func sourcesFrom(dir string) map[string]string {
	out := map[string]string{}
	for _, k := range config.Sources {
		p := filepath.Join(dir, k)
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			out[k] = p
		}
	}
	return out
}

// moduleSources is a line per module saying where it runs from: its checkout,
// or its release image, and with --sources, the checkout it didn't find.
func moduleSources(ch initChoices, versions map[string]string) []string {
	var out []string
	for _, k := range config.Sources {
		var line string
		switch {
		case ch.sources[k] != "":
			line = "checkout " + ch.sources[k]
		case versions[k] != "":
			line = "release image " + versions[k]
		default:
			line = "release image, no tag yet (set versions." + k + " in " + config.FileName + ")"
		}
		if ch.sourcesDir != "" && ch.sources[k] == "" {
			line += ", no checkout at " + filepath.Join(ch.sourcesDir, k)
		}
		out = append(out, fmt.Sprintf("%-9s %s", k, line))
	}
	return out
}

// writeInitFiles writes --template none's plat5.yml and identity's and audit's
// service files, keeping any that exist.
func writeInitFiles(dir string) error {
	return fs.WalkDir(initFiles, "initfiles", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel("initfiles", path)
		target := filepath.Join(dir, rel)
		if _, err := os.Stat(target); err == nil {
			fmt.Println("  kept", rel)
			return nil
		}
		data, err := initFiles.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
		fmt.Println("  wrote", rel)
		return nil
	})
}

// writeOperatorConfig writes operator.yml from the default operator config,
// keeping one that exists.
func writeOperatorConfig(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, operatorConfigFile)); err == nil {
		fmt.Println("  kept", operatorConfigFile)
		return nil
	}
	return writeFile(dir, operatorConfigFile, operatorDefaultConfig)
}

// initChoose settles the directory, starter, preset, authorizer, modules,
// sources, and pins from args, flags, and (on a terminal, without -y) prompts.
func initChoose(cmd *cobra.Command, args []string) (initChoices, error) {
	preset := strings.TrimSpace(initPreset)
	if preset == "" {
		preset = presetMinimal
	}
	if !isValidPreset(preset) {
		return initChoices{}, fmt.Errorf("--preset %q: unknown preset (want %s)", preset, strings.Join(presetNames(), ", "))
	}

	ch := initChoices{
		preset: preset,
		modules: map[string]bool{
			config.ModuleAuth:          !initNoAuth,
			config.ModuleObservability: !initNoObservability,
			config.ModuleOperator:      !initNoOperator,
		},
		noCustomerConsole: initNoCustomerConsole,
		noOperatorConsole: initNoOperatorConsole,
		sources:           map[string]string{},
		versions:          map[string]string{},
	}
	if err := parsePairs("--version", initVersions, config.Sources, ch.versions); err != nil {
		return ch, err
	}
	if initSourcesDir != "" {
		abs, err := filepath.Abs(prompt.ExpandPath(initSourcesDir))
		if err != nil {
			return ch, err
		}
		if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
			return ch, fmt.Errorf("--sources %s: no directory at %s", initSourcesDir, abs)
		}
		ch.sourcesDir = abs
		ch.sources = sourcesFrom(abs)
	}

	dir := ""
	if len(args) == 1 {
		dir = args[0]
	}
	starter := initTemplate
	if !initYes && prompt.Interactive() {
		if !cmd.Flags().Changed("template") {
			if cmd.Flags().Changed("preset") {
				starter = noTemplate
			} else {
				starter = defaultStarter
			}
		}
		var step1 []huh.Field
		if dir == "" {
			dir = "my-app"
			step1 = append(step1, huh.NewInput().Title("Project directory").Value(&dir).
				Validate(func(s string) error {
					if initProjectID == "" && config.SanitizeProjectID(filepath.Base(s)) == "" {
						return errors.New("pick a name with letters or digits")
					}
					return nil
				}))
		}
		if !cmd.Flags().Changed("template") && !cmd.Flags().Changed("preset") {
			var opts []huh.Option[string]
			for _, o := range template.Official {
				opts = append(opts, huh.NewOption(fmt.Sprintf("%-18s %s", o.Name, o.Description), o.Name))
			}
			opts = append(opts, huh.NewOption(fmt.Sprintf("%-18s %s", noTemplate, "Plat5 alone: the config, no apps"), noTemplate))
			step1 = append(step1, huh.NewSelect[string]().Title("Starter").Options(opts...).Value(&starter))
		}
		if err := runForm(step1); err != nil {
			return ch, err
		}

		var step2 []huh.Field
		if starter == noTemplate && !cmd.Flags().Changed("preset") {
			var pOpts []huh.Option[string]
			for _, o := range presetOptions {
				pOpts = append(pOpts, huh.NewOption(fmt.Sprintf("%-12s %s", o.Name, o.Description), o.Name))
			}
			step2 = append(step2, huh.NewSelect[string]().Title("Config preset").Options(pOpts...).Value(&preset))
		}
		askModules := !cmd.Flags().Changed("no-auth") && !cmd.Flags().Changed("no-observability") && !cmd.Flags().Changed("no-operator")
		picked := []string{config.ModuleAuth, config.ModuleObservability, config.ModuleOperator}
		if askModules {
			step2 = append(step2, huh.NewMultiSelect[string]().
				Title("Modules (space toggles)").
				Options(
					huh.NewOption("auth           sign-in for your users (Plat5 Auth)", config.ModuleAuth).Selected(true),
					huh.NewOption("observability  Grafana: logs, traces, metrics", config.ModuleObservability).Selected(true),
					huh.NewOption("operator       the operator gateway, its sign-in and audit log, the operator console", config.ModuleOperator).Selected(true),
				).
				Value(&picked))
		}
		if err := runForm(step2); err != nil {
			return ch, err
		}
		if askModules {
			for _, m := range []string{config.ModuleAuth, config.ModuleObservability, config.ModuleOperator} {
				ch.modules[m] = contains(picked, m)
			}
		}
		ch.preset = preset
	}

	if starter != noTemplate && (preset != presetMinimal || cmd.Flags().Changed("preset")) {
		return ch, fmt.Errorf("--preset applies to --template none (a starter owns its own plat5.yml)")
	}

	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(prompt.ExpandPath(dir))
	if err != nil {
		return ch, err
	}
	ch.dir = abs
	if starter != noTemplate {
		ch.starter = starter
	}
	return ch, nil
}

func runForm(fields []huh.Field) error {
	if len(fields) == 0 {
		return nil
	}
	err := huh.NewForm(huh.NewGroup(fields...)).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return errors.New("cancelled")
	}
	if err != nil && strings.Contains(err.Error(), "TTY") {
		return errors.New("no terminal to ask on: run plat5 init -y to take the defaults (--template none, every module), or pass --template and the --no-* flags")
	}
	return err
}

// parsePairs reads key=value flags into out, checking keys against allowed.
func parsePairs(flag string, pairs, allowed []string, out map[string]string) error {
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || strings.TrimSpace(v) == "" {
			return fmt.Errorf("%s %q: want key=value", flag, p)
		}
		if !contains(allowed, k) {
			return fmt.Errorf("%s %s: unknown (want %s)", flag, k, strings.Join(allowed, ", "))
		}
		out[k] = strings.TrimSpace(v)
	}
	return nil
}

// resolveVersions pins every image to its latest tag, unless a --version sets it
// or it is built from a checkout. A pin the chosen modules need must resolve; the rest
// are written when they can be, so turning a module on later just works.
func resolveVersions(ch initChoices) (map[string]string, error) {
	cfg := config.Resolved{Modules: ch.modules}
	needed := cfg.NeededVersions()
	out := map[string]string{}
	for _, k := range config.Sources {
		if ch.sources[k] != "" {
			continue
		}
		if v := ch.versions[k]; v != "" {
			out[k] = v
			continue
		}
		tag, err := release.Latest(config.VersionRepos[k])
		if err != nil {
			if contains(needed, k) {
				return nil, fmt.Errorf("latest %s: %w (pin one with --version %s=vX.Y.Z)", config.VersionRepos[k], err, k)
			}
			continue
		}
		out[k] = tag
	}
	return out, nil
}

// renderDevYML is plat5.local.yml: how plat5 start runs the project. lay is the
// starter's layout, nil with --template none.
func renderDevYML(projectID string, ch initChoices, versions map[string]string, lay *layout) string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	p("# How plat5 start runs this project on your machine. A deployment never reads\n")
	p("# it: the deployment's config is plat5.yml. See `plat5 start --help`.\n")
	p("project_id: %s\n", yamlString(projectID))
	p("cli: \">=%s\"  # the oldest plat5 CLI that reads this file\n\n", config.SchemaVersion)

	if len(versions) > 0 {
		p("# Image tags. plat5 init wrote the latest of each.\nversions:\n")
		for _, k := range config.Sources {
			if v := versions[k]; v != "" {
				p("  %s: %s\n", k, yamlString(v))
			}
		}
		p("\n")
	}
	if len(ch.sources) > 0 {
		p("# Built from these checkouts instead of pulled.\nsource:\n")
		for _, k := range config.Sources {
			if v := ch.sources[k]; v != "" {
				p("  %s: %s\n", k, yamlString(v))
			}
		}
		p("\n")
	}

	p("# What plat5 start runs beside Plat5. plat5 start -x <module> skips one for a run.\n")
	p("modules:\n")
	p("  %-20s # Plat5 Auth: sign-in for your users (dev mode)\n", fmt.Sprintf("auth: %t", ch.modules[config.ModuleAuth]))
	p("  %-20s # Grafana with logs, traces, metrics\n", fmt.Sprintf("observability: %t", ch.modules[config.ModuleObservability]))
	p("  %-20s # the operator gateway, its IdP and audit log\n\n", fmt.Sprintf("operator: %t", ch.modules[config.ModuleOperator]))

	if lay != nil {
		r := lay.starter.Manifest.Routes
		if r.PermissionSeparator != "" || len(r.Exclude) > 0 {
			p("# plat5 routes generate options from the starter.\nroutes:\n")
			if r.PermissionSeparator != "" {
				p("  permission_separator: %s\n", yamlString(r.PermissionSeparator))
			}
			if len(r.Exclude) > 0 {
				p("  exclude:\n")
				for _, ex := range r.Exclude {
					p("    - %s\n", yamlString(ex))
				}
			}
			p("\n")
		}
	}

	if ch.preset == presetEnterprise {
		p("# Local gateway overrides (enterprise preset: JWT scope narrowing).\n")
		p("gateway:\n")
		p("  scope_claim: scope\n\n")
	}

	if ch.modules[config.ModuleOperator] {
		if lay != nil && lay.operatorConsole != nil {
			p("# The operator module: the operator gateway on operator.yml, its IdP and\n")
			p("# audit log, and operator-console/, which docker-compose.yml includes and plat5\n")
			p("# start runs only with the module.\n")
			p("operator:\n  config: ./%s\n", operatorConfigFile)
			p("  console:\n    compose: ./%s\n", lay.operatorConsoleCompose())
			p("    web: %s  # may sign in with the operator IdP and call the operator gateway\n", yamlString(lay.operatorWeb()))
			p("  # origins: []          # more origins the operator side allows, each with <origin>/callback\n\n")
		} else {
			p("# The operator module: the operator gateway on operator.yml, its IdP and\n")
			p("# audit log.\n")
			p("operator:\n  config: ./%s\n", operatorConfigFile)
			p("  # origins: []          # browser origins the operator side allows, each with <origin>/callback\n\n")
		}
	}

	if lay != nil {
		folders := apiDir + "/"
		if lay.customerConsole != nil {
			folders += ", " + customerConsoleDir + "/"
		}
		p("# Your apps: %s. plat5 start runs docker-compose.yml\n", folders)
		p("# after Plat5 (-x app to run them yourself). The API's service file's url\n")
		p("# names its compose service.\n")
		p("app:\n  compose: ./%s\n", projectCompose)
		if web := lay.web(); web != "" {
			p("  web: %s  # customer-console/: allowed by local Auth and gateway CORS\n", yamlString(web))
		}
	} else {
		p("# Your app. Set compose: and plat5 start runs it after Plat5 (-x app to run it\n")
		p("# yourself). A service file's url names its compose service (http://api:3000).\n")
		p("# app:\n#   compose: ./docker-compose.yml\n#   web: http://localhost:3001\n")
	}

	p(`
# --- Optional ---
# ports:                 # host ports (defaults: gateway 5400, auth 5401, control_plane 5402,
#   gateway: 5400        #   grafana 5403, otlp_grpc 4317, otlp_http 4318, operator 5410,
#                        #   operator_auth 5411)
# admin_token: dev-admin-token
# secret_brand: plat5    # API keys {brand}-k-2-…, invite tokens {brand}-i-1-…
# gateway:               # optional local gateway overrides
#   scope_claim: scope
#   audit_mode: sync     # sync (default), async, or off
#   rate_limit_enabled: true
#   upstream_signing: false
#   cors_allowed_origins: [http://localhost:5173]   # defaults to app.web + auth.origins
#   cors_allowed_headers: [idempotency-key]
# routes:                # plat5 routes generate options
#   permission_separator: ":"   # ":", "_", ".", or "-"
#   exclude: ["POST /orgs"]     # permanently omit generated routes
# auth:                  # local Auth, beyond app.web
#   clients: [plat5]
#   origins: []
#   redirect_uris: []
#   theme_file: ./theme.json
# idp:                   # with modules.auth off: your IdP
#   issuer: https://idp.example.com
#   jwks_uri: https://idp.example.com/.well-known/jwks.json
#   audiences: [my-api]
#   subject_claim: sub
#   name_claims: [name, preferred_username]   # [] disables name seeding
# otel:
#   endpoint: http://host.docker.internal:4318   # your collector, instead of observability
`)
	if !ch.modules[config.ModuleAuth] {
		b.WriteString("\n# modules.auth is off: plat5 start needs idp: above.\n")
	}
	return b.String()
}

func mustGetwd() string {
	wd, _ := os.Getwd()
	return wd
}

func yamlString(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, ":#{}[]&*?|>!%@`'\" \t") || s == "true" || s == "false" {
		return fmt.Sprintf("%q", s)
	}
	return s
}
