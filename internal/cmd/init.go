package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/prompt"
	"github.com/plat5dev/cli/internal/release"
	"github.com/plat5dev/cli/internal/template"
	"github.com/plat5dev/cli/internal/xdg"
	"github.com/spf13/cobra"
)

// noTemplate is --template none: Plat5 only, with sample routes.
const noTemplate = "none"

var (
	initTemplate        string
	initTemplatesDir    string
	initTemplateRef     string
	initListTemplates   bool
	initYes             bool
	initForce           bool
	initNoAuth          bool
	initNoOperator      bool
	initNoObservability bool
	initNoWeb           bool
	initNoConsole       bool
	initSources         []string
	initVersions        []string
)

var initCmd = &cobra.Command{
	Use:   "init [directory]",
	Short: "Create a project from a starter",
	Long: `Create a Plat5 project, then cd into it and run plat5 start:

  api/         your API, from a starter
  web/         a web app for your customers (--no-web: none)
  console/     a console for your staff (--no-console, or --no-operator: none)
  plat5.yml, routes, roles, and a README that says how to extend each

Each app is a starting point the project owns, not a dependency.

  plat5 init my-app                         asks for a starter and modules
  plat5 init my-app --template go-fiber-api -y
  plat5 init --list-templates

Without a directory, the project is the current directory.

A starter is a first-party name (--list-templates), owner/repo on GitHub, or an
archive URL. --template none is Plat5 alone, with sample routes.

Contributors: --source plat5=../plat5 builds that module from a checkout instead
of pulling its image (plat5, auth, operator, operator_console).`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

func init() {
	f := initCmd.Flags()
	f.StringVar(&initTemplate, "template", "", "Starter: a first-party name, owner/repo, an archive URL, or none")
	f.BoolVar(&initListTemplates, "list-templates", false, "List first-party starters")
	f.BoolVarP(&initYes, "yes", "y", false, "Don't ask; take the defaults (first starter, every module)")
	f.BoolVar(&initForce, "force", false, "Overwrite an existing plat5.yml")
	f.BoolVar(&initNoAuth, "no-auth", false, "Turn the auth module off (then set idp: in plat5.yml)")
	f.BoolVar(&initNoOperator, "no-operator", false, "Turn the operator module off")
	f.BoolVar(&initNoObservability, "no-observability", false, "Turn the observability module off")
	f.BoolVar(&initNoWeb, "no-web", false, "Don't add the web app (web/)")
	f.BoolVar(&initNoConsole, "no-console", false, "Don't add the staff console (console/); the operator module runs its stock console")
	f.StringArrayVar(&initSources, "source", nil, "Build a module from a checkout: plat5=../plat5 (repeatable)")
	f.StringArrayVar(&initVersions, "version", nil, "Pin an image: plat5=v0.7.1 (repeatable; default the latest tag)")
	f.StringVar(&initTemplatesDir, "templates-dir", "", "Read starters from a local directory (or PLAT5_TEMPLATES)")
	f.StringVar(&initTemplateRef, "template-ref", "", "Git ref for remote starters (default master; or PLAT5_TEMPLATE_REF)")
}

type initChoices struct {
	dir       string
	starter   string
	modules   map[string]bool
	sources   map[string]string
	versions  map[string]string
	noWeb     bool
	noConsole bool
}

func runInit(cmd *cobra.Command, args []string) error {
	if initListTemplates {
		return listTemplates()
	}
	ch, err := initChoose(cmd, args)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(ch.dir, 0o755); err != nil {
		return err
	}
	ymlPath := filepath.Join(ch.dir, "plat5.yml")
	if _, err := os.Stat(ymlPath); err == nil && !initForce {
		return fmt.Errorf("%s already exists (--force to overwrite it)", ymlPath)
	}
	projectID := config.SanitizeProjectID(filepath.Base(ch.dir))
	if projectID == "" {
		return fmt.Errorf("can't make a project id from %q", filepath.Base(ch.dir))
	}

	removeTemplateCache()

	versions, err := resolveVersions(ch)
	if err != nil {
		return err
	}

	lay := layout{}
	if ch.starter != "" {
		opts, err := templateOpts()
		if err != nil {
			return err
		}
		tpl, err := template.Resolve(opts, ch.starter)
		if err != nil {
			return err
		}
		lay.starter = tpl
		defer func() { lay.close() }()
		if tpl.Manifest.Kind == template.KindAPI {
			fmt.Println("Created", relPath(mustGetwd(), ch.dir)+":")
			if lay, err = scaffold(ch.dir, projectID, ch, versions, opts, tpl); err != nil {
				return err
			}
			if lay.console != nil {
				// The project's console replaces the image: no pin to keep.
				delete(versions, config.VersionOperatorConsole)
			}
		} else {
			// A starter from before api/ web/ console/: the project is the starter.
			if err := tpl.Copy(ch.dir); err != nil {
				return err
			}
			if err := tpl.Substitute(ch.dir, projectID); err != nil {
				return err
			}
			fmt.Printf("Copied starter %s from %s\n", tpl.Manifest.Name, tpl.Origin)
		}
	}

	if err := os.WriteFile(ymlPath, []byte(renderPlat5YML(projectID, ch, versions, lay)), 0o644); err != nil {
		return err
	}
	if lay.api != "" {
		files := "  plat5.yml, routes.yml, roles.yml"
		if lay.operatorRoutes {
			files += ", " + operatorRoutesFile
		}
		fmt.Println(files)
		fmt.Println("  README.md  what's here, and how to extend it")
	} else {
		fmt.Println("Wrote plat5.yml")
	}

	tpl := lay.starter
	if tpl == nil {
		for name, body := range map[string]string{
			"routes.identity.yml": identityRoutesCatalog,
			"routes.audit.yml":    auditRoutesCatalog,
			"routes.yml":          sampleRoutes,
		} {
			if err := writeIfAbsent(ch.dir, name, body); err != nil {
				return err
			}
		}
	}
	// A starter may ship roles with its own labels; keep them.
	if err := writeIfAbsent(ch.dir, "roles.yml", starterRoles); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Next:")
	if wd, _ := os.Getwd(); filepath.Clean(wd) != filepath.Clean(ch.dir) {
		fmt.Printf("  cd %s\n", relPath(wd, ch.dir))
	}
	fmt.Println("  plat5 start")
	return nil
}

// initChoose settles the directory, starter, modules, sources, and pins from
// args, flags, and (on a terminal, without -y) prompts.
func initChoose(cmd *cobra.Command, args []string) (initChoices, error) {
	ch := initChoices{
		modules: map[string]bool{
			config.ModuleAuth:          !initNoAuth,
			config.ModuleOperator:      !initNoOperator,
			config.ModuleObservability: !initNoObservability,
		},
		sources:   map[string]string{},
		versions:  map[string]string{},
		noWeb:     initNoWeb,
		noConsole: initNoConsole,
	}
	if err := parsePairs("--source", initSources, config.Sources, ch.sources); err != nil {
		return ch, err
	}
	if err := parsePairs("--version", initVersions, config.Sources, ch.versions); err != nil {
		return ch, err
	}
	for k, v := range ch.sources {
		abs, err := filepath.Abs(prompt.ExpandPath(v))
		if err != nil {
			return ch, err
		}
		ch.sources[k] = abs
	}

	interactive := !initYes && prompt.Interactive()
	dir := ""
	if len(args) == 1 {
		dir = args[0]
	}
	starter := initTemplate
	if starter == noTemplate {
		starter = ""
	}

	if interactive {
		var fields []huh.Field
		if dir == "" {
			dir = "my-app"
			fields = append(fields, huh.NewInput().Title("Project directory").Value(&dir).
				Validate(func(s string) error {
					if config.SanitizeProjectID(filepath.Base(s)) == "" {
						return errors.New("pick a name with letters or digits")
					}
					return nil
				}))
		}
		if !cmd.Flags().Changed("template") {
			list, err := starters()
			if err != nil {
				return ch, err
			}
			opts := []huh.Option[string]{}
			for _, o := range list {
				opts = append(opts, huh.NewOption(fmt.Sprintf("%-26s %s", o.Name, o.Description), o.Name))
			}
			opts = append(opts, huh.NewOption(fmt.Sprintf("%-26s %s", noTemplate, "Plat5 only, with sample routes"), ""))
			starter = list[0].Name
			fields = append(fields, huh.NewSelect[string]().Title("Starter").Options(opts...).Value(&starter))
		}
		askModules := !cmd.Flags().Changed("no-auth") && !cmd.Flags().Changed("no-operator") && !cmd.Flags().Changed("no-observability")
		picked := []string{config.ModuleAuth, config.ModuleOperator, config.ModuleObservability}
		if askModules {
			fields = append(fields, huh.NewMultiSelect[string]().
				Title("Modules (space toggles)").
				Options(
					huh.NewOption("auth           sign-in for your users (Plat5 Auth)", config.ModuleAuth).Selected(true),
					huh.NewOption("operator       staff console and gateway", config.ModuleOperator).Selected(true),
					huh.NewOption("observability  Grafana: logs, traces, metrics", config.ModuleObservability).Selected(true),
				).
				Value(&picked))
		}
		if err := runForm(fields); err != nil {
			return ch, err
		}
		if askModules {
			for _, m := range config.Modules {
				ch.modules[m] = contains(picked, m)
			}
		}
	} else if !cmd.Flags().Changed("template") {
		list, err := starters()
		if err != nil {
			return ch, err
		}
		starter = list[0].Name
	}

	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(prompt.ExpandPath(dir))
	if err != nil {
		return ch, err
	}
	ch.dir = abs
	ch.starter = starter
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
		return errors.New("no terminal to ask on: run plat5 init -y to take the defaults (first starter, every module), or pass --template and the --no-* flags")
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
// or a --source builds it. A pin the chosen modules need must resolve; the rest
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

func renderPlat5YML(projectID string, ch initChoices, versions map[string]string, lay layout) string {
	tpl := lay.starter
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	p("# Plat5 project. plat5 start runs it; see `plat5 start --help`.\n")
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
	p("  %-20s # staff console, operator gateway, staff sign-in\n", fmt.Sprintf("operator: %t", ch.modules[config.ModuleOperator]))
	p("  %-20s # Grafana with logs, traces, metrics\n\n", fmt.Sprintf("observability: %t", ch.modules[config.ModuleObservability]))

	upstreams := map[string]string{"api": "3000"}
	routes := []string{"./routes.identity.yml", "./routes.audit.yml", "./routes.yml"}
	if tpl != nil {
		if len(tpl.Manifest.Upstreams) > 0 {
			upstreams = tpl.Manifest.Upstreams
		}
		if len(tpl.Manifest.Routes) > 0 {
			routes = tpl.Manifest.Routes
		}
	}
	switch {
	case lay.api != "":
		p("# Your apps: %s. plat5 start runs docker-compose.yml after Plat5\n", strings.Join(lay.folders(), ", "))
		p("# (-x app to run them yourself).\n")
		p("app:\n  compose: ./docker-compose.yml\n")
		if lay.web != nil {
			p("  web: %s  # web/: may sign in with local Auth\n", yamlString(appOrigin(lay.web, "http://localhost:3001")))
		}
		if lay.console != nil {
			p("  console: %s  # console/: may sign in as staff; no stock console runs\n", yamlString(appOrigin(lay.console, "http://localhost:5412")))
		}
		p("\n")
	case tpl != nil && tpl.Manifest.App.Compose != "":
		p("# Your app. plat5 start runs its compose file after Plat5 (-x app to run it yourself).\n")
		p("app:\n  compose: %s\n", yamlString("./"+strings.TrimPrefix(tpl.Manifest.App.Compose, "./")))
		if tpl.Manifest.App.Web != "" {
			p("  web: %s  # its browser origin: allowed to sign in with local Auth\n", yamlString(tpl.Manifest.App.Web))
		}
		p("\n")
	default:
		p("# Your app. Set compose: and plat5 start runs it after Plat5.\n")
		p("# app:\n#   compose: ./docker-compose.yml\n#   web: http://localhost:3001\n\n")
	}

	p("# Where your services listen, by routes.yml service name. A bare port that\n")
	p("# app.compose publishes is that container, on the Plat5 network (api:3000).\n")
	p("# Otherwise, or with -x app, it is a process on your machine\n")
	p("# (http://host.docker.internal:<port>). See plat5 start --help.\n")
	p("upstreams:\n")
	keys := make([]string, 0, len(upstreams))
	for k := range upstreams {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p("  %s: %s\n", k, yamlString(upstreams[k]))
	}
	p("\n# Route files, applied in order on start (or: plat5 routes apply).\nroutes:\n")
	for _, r := range routes {
		p("  - %s\n", yamlString(r))
	}
	p("\n# Roles grant labels; routes require them. Applied on start (or: plat5 roles apply).\n")
	p("# Remove the line to turn roles off: every member holds every label.\n")
	p("roles: ./roles.yml\n")
	if lay.operatorRoutes {
		p("\n# The staff plane. routes: what staff may call through the operator gateway.\n")
		p("operator:\n  routes: ./%s\n  # origins: []         # more staff consoles\n", operatorRoutesFile)
	}

	optionalOperator := "# operator:\n#   origins: []          # more staff consoles\n"
	if lay.operatorRoutes {
		optionalOperator = ""
	}
	p(`
# --- Optional ---
# ports:                 # host ports (defaults: gateway 5400, auth 5401, control_plane 5402,
#   gateway: 5400        #   grafana 5403, operator 5410, staff_auth 5411, operator_console 5412,
#                        #   otlp_grpc 4317, otlp_http 4318)
# admin_token: dev-admin-token
# apikey_brand: plat5    # keys are {brand}-k-2-…
# auth:                  # local Auth, beyond app.web
#   clients: [plat5]
#   origins: []
#   redirect_uris: []
#   theme_file: ./theme.json
%s# idp:                   # with modules.auth off: your IdP
#   issuer: https://idp.example.com
#   jwks_uri: https://idp.example.com/.well-known/jwks.json
#   audiences: [my-api]
#   user_id_claim: sub
# otel:
#   endpoint: http://host.docker.internal:4318   # your collector, instead of observability
`, optionalOperator)
	if !ch.modules[config.ModuleAuth] {
		b.WriteString("\n# modules.auth is off: plat5 start needs idp: above.\n")
	}
	return b.String()
}

// folders is the project's app folders, in order.
func (l layout) folders() []string {
	var out []string
	for _, f := range []struct {
		name string
		on   bool
	}{{apiDir + "/", l.starter != nil}, {webDir + "/", l.web != nil}, {consoleDir + "/", l.console != nil}} {
		if f.on {
			out = append(out, f.name)
		}
	}
	return out
}

// appOrigin is an app's browser origin from its manifest, or the default.
func appOrigin(t *template.Template, fallback string) string {
	if t.Manifest.App.Web != "" {
		return t.Manifest.App.Web
	}
	return fallback
}

func mustGetwd() string {
	wd, _ := os.Getwd()
	return wd
}

// starters is what --template can name: the first-party list, or a local
// --templates-dir's starters (named by folder).
func starters() ([]template.Summary, error) {
	opts, err := templateOpts()
	if err != nil {
		return nil, err
	}
	list, err := template.ListAvailable(opts)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, errors.New("no starters found")
	}
	return list, nil
}

func listTemplates() error {
	list, err := starters()
	if err != nil {
		return err
	}
	for _, s := range list {
		fmt.Printf("%-20s  %s\n", s.Name, s.Description)
	}
	return nil
}

// templateOpts prefers a local templates directory; otherwise GitHub archives.
func templateOpts() (template.ResolveOptions, error) {
	opts := template.ResolveOptions{Ref: initTemplateRef}
	dir := initTemplatesDir
	if dir == "" {
		dir = os.Getenv("PLAT5_TEMPLATES")
	}
	if dir != "" {
		abs, err := filepath.Abs(prompt.ExpandPath(dir))
		if err != nil {
			return opts, err
		}
		opts.LocalRoot = abs
	}
	return opts, nil
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

// removeTemplateCache deletes the starter cache older CLIs kept (~/.cache/plat5/templates).
// Starters are no longer cached: a branch moves, and a cached copy went stale.
func removeTemplateCache() {
	if dir, err := xdg.Plat5CacheDir(); err == nil {
		_ = os.RemoveAll(filepath.Join(dir, "templates"))
	}
}

func writeIfAbsent(dir, name, body string) error {
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}
	fmt.Println("Wrote", name)
	return nil
}
