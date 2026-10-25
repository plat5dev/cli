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
	"github.com/spf13/cobra"
)

// noTemplate is --template none: Plat5 alone, with organization and project kinds.
// It is the only starter until the starter repos ship plat5.yml and services/.
const noTemplate = "none"

// initFiles is the config init writes: organization and project kinds, the
// example roles, and identity's and audit's service files at their defaults.
//
//go:embed initfiles
var initFiles embed.FS

var (
	initTemplate        string
	initYes             bool
	initForce           bool
	initNoAuth          bool
	initNoObservability bool
	initSources         []string
	initVersions        []string
)

var initCmd = &cobra.Command{
	Use:   "init [directory]",
	Short: "Create a project",
	Long: `Create a Plat5 project, then cd into it and run plat5 start:

  plat5.yml          the deployment's config: organization and project kinds, roles
  services/          identity's and audit's service files
  plat5.dev.yml      how plat5 start runs it on your machine

  plat5 init my-app
  plat5 init my-app -y            take the defaults (every module)

Without a directory, the project is the current directory. An existing
plat5.yml or service file is kept.

Starters (an API and consoles to copy) aren't available yet: --template none,
the default, is the only one.

Contributors: --source plat5=../plat5 builds that module from a checkout instead
of pulling its image (plat5, auth).`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

func init() {
	f := initCmd.Flags()
	f.StringVar(&initTemplate, "template", noTemplate, "Starter: none (the only one for now)")
	f.BoolVarP(&initYes, "yes", "y", false, "Don't ask; take the defaults (every module)")
	f.BoolVar(&initForce, "force", false, "Overwrite an existing plat5.dev.yml")
	f.BoolVar(&initNoAuth, "no-auth", false, "Turn the auth module off (then set idp: in plat5.dev.yml)")
	f.BoolVar(&initNoObservability, "no-observability", false, "Turn the observability module off")
	f.StringArrayVar(&initSources, "source", nil, "Build a module from a checkout: plat5=../plat5 (repeatable)")
	f.StringArrayVar(&initVersions, "version", nil, "Pin an image: plat5=v0.8.0 (repeatable; default the latest tag)")
}

type initChoices struct {
	dir      string
	modules  map[string]bool
	sources  map[string]string
	versions map[string]string
}

func runInit(cmd *cobra.Command, args []string) error {
	if initTemplate != noTemplate {
		return fmt.Errorf("--template %s: starters aren't available yet; use --template none", initTemplate)
	}
	ch, err := initChoose(cmd, args)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(ch.dir, 0o755); err != nil {
		return err
	}
	devPath := filepath.Join(ch.dir, config.FileName)
	if _, err := os.Stat(devPath); err == nil && !initForce {
		return fmt.Errorf("%s already exists (--force to overwrite it)", devPath)
	}
	projectID := config.SanitizeProjectID(filepath.Base(ch.dir))
	if projectID == "" {
		return fmt.Errorf("can't make a project id from %q", filepath.Base(ch.dir))
	}

	versions, err := resolveVersions(ch)
	if err != nil {
		return err
	}

	fmt.Println("Created", relPath(mustGetwd(), ch.dir)+":")
	if err := writeInitFiles(ch.dir); err != nil {
		return err
	}
	if err := os.WriteFile(devPath, []byte(renderDevYML(projectID, ch, versions)), 0o644); err != nil {
		return err
	}
	fmt.Println("  wrote", config.FileName)

	fmt.Println()
	fmt.Println("Next:")
	if wd, _ := os.Getwd(); filepath.Clean(wd) != filepath.Clean(ch.dir) {
		fmt.Printf("  cd %s\n", relPath(wd, ch.dir))
	}
	fmt.Println("  plat5 start")
	return nil
}

// writeInitFiles writes plat5.yml and services/, keeping any that exist.
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

// initChoose settles the directory, modules, sources, and pins from args, flags,
// and (on a terminal, without -y) prompts.
func initChoose(cmd *cobra.Command, args []string) (initChoices, error) {
	ch := initChoices{
		modules: map[string]bool{
			config.ModuleAuth:          !initNoAuth,
			config.ModuleObservability: !initNoObservability,
		},
		sources:  map[string]string{},
		versions: map[string]string{},
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

	dir := ""
	if len(args) == 1 {
		dir = args[0]
	}
	if !initYes && prompt.Interactive() {
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
		askModules := !cmd.Flags().Changed("no-auth") && !cmd.Flags().Changed("no-observability")
		picked := []string{config.ModuleAuth, config.ModuleObservability}
		if askModules {
			fields = append(fields, huh.NewMultiSelect[string]().
				Title("Modules (space toggles)").
				Options(
					huh.NewOption("auth           sign-in for your users (Plat5 Auth)", config.ModuleAuth).Selected(true),
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
	}

	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(prompt.ExpandPath(dir))
	if err != nil {
		return ch, err
	}
	ch.dir = abs
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
		return errors.New("no terminal to ask on: run plat5 init -y to take the defaults (every module), or pass the --no-* flags")
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

// renderDevYML is plat5.dev.yml: how plat5 start runs the project.
func renderDevYML(projectID string, ch initChoices, versions map[string]string) string {
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
	p("  %-20s # Grafana with logs, traces, metrics\n\n", fmt.Sprintf("observability: %t", ch.modules[config.ModuleObservability]))

	p("# Your app. Set compose: and plat5 start runs it after Plat5 (-x app to run it\n")
	p("# yourself). A service file's url names its compose service (http://api:3000).\n")
	p("# app:\n#   compose: ./docker-compose.yml\n#   web: http://localhost:3001\n")

	p(`
# --- Optional ---
# ports:                 # host ports (defaults: gateway 5400, auth 5401, control_plane 5402,
#   gateway: 5400        #   grafana 5403, otlp_grpc 4317, otlp_http 4318)
# admin_token: dev-admin-token
# apikey_brand: plat5    # keys are {brand}-k-2-…
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
