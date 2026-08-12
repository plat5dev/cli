package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/plat5dev/cli/internal/compose"
	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/prompt"
	"github.com/plat5dev/cli/internal/template"
	"github.com/spf13/cobra"
)

var (
	initProjectID            string
	initPlat5Compose         string
	initAuthCompose          string
	initObservabilityCompose string
	initAuth                 bool
	initObservability        bool
	initForce                bool
	initYes                  bool
	initTemplate             string
	initTemplatesDir         string
	initPlat5Version         string
	initAuthVersion          string
	initTemplateRef          string
	initListTemplates        bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a Plat5 project (optionally from a template)",
	Long: `Create a Plat5 project in the current directory.

Examples:
  plat5 init --auth -y
  plat5 init --template bun-effect-api --auth -y
  plat5 init --list-templates
  plat5 init                          # interactive prompts

plat5 start pulls published images (no local Plat5 checkout).
--template fetches a starter (official name, owner/repo, or archive URL),
or from local --templates-dir, then writes plat5.yml.
Without a template: plat5.yml + sample routes.yml.

List first-party starters: plat5 init --list-templates
Community: plat5 init --template owner/my-template
Copy refuses to overwrite existing files.

Advanced (local compose trees for development): --plat5-compose / --auth-compose /
--observability-compose or PLAT5_COMPOSE / PLAT5_AUTH_COMPOSE / PLAT5_OBSERVABILITY_COMPOSE.`,
	RunE: runInit,
}

func init() {
	initCmd.Flags().StringVar(&initProjectID, "project-id", "", "Project id (default: directory name)")
	initCmd.Flags().StringVar(&initPlat5Compose, "plat5-compose", "", "Advanced: local Plat5 compose dir (or PLAT5_COMPOSE)")
	initCmd.Flags().StringVar(&initAuthCompose, "auth-compose", "", "Advanced: local Auth compose dir (or PLAT5_AUTH_COMPOSE)")
	initCmd.Flags().StringVar(&initObservabilityCompose, "observability-compose", "", "Advanced: local observability compose dir")
	initCmd.Flags().BoolVar(&initAuth, "auth", false, "Enable Plat5 Auth in plat5.yml")
	initCmd.Flags().BoolVar(&initObservability, "observability", false, "Enable observability stack in plat5.yml")
	initCmd.Flags().BoolVar(&initForce, "force", false, "Overwrite existing plat5.yml (bare init only)")
	initCmd.Flags().BoolVarP(&initYes, "yes", "y", false, "Non-interactive; do not prompt")
	initCmd.Flags().StringVar(&initTemplate, "template", "", "Starter: official name, owner/repo, or https://…/archive/….tar.gz")
	initCmd.Flags().StringVar(&initTemplatesDir, "templates-dir", "", "Local templates root (skip remote fetch)")
	initCmd.Flags().StringVar(&initPlat5Version, "plat5-version", "", "Runtime GHCR pin written to plat5.yml (default v0.1.3)")
	initCmd.Flags().StringVar(&initAuthVersion, "auth-version", "", "Auth GHCR pin written to auth.version (default v0.1.3)")
	initCmd.Flags().StringVar(&initTemplateRef, "template-ref", "", "Git ref for remote templates (default master; or PLAT5_TEMPLATE_REF)")
	initCmd.Flags().BoolVar(&initListTemplates, "list-templates", false, "List first-party templates and exit")
}

func runInit(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	if initListTemplates {
		return runListTemplates(cmd)
	}

	ymlPath := filepath.Join(cwd, "plat5.yml")
	if _, err := os.Stat(ymlPath); err == nil && !initForce {
		return fmt.Errorf("%s already exists (use --force to overwrite plat5.yml)", ymlPath)
	}

	interactive := !initYes && prompt.Interactive()
	var session *prompt.Session
	if interactive {
		session = prompt.NewSession(os.Stdin, os.Stdout)
	}

	projectID := initProjectID
	if projectID == "" {
		projectID = filepath.Base(cwd)
	}
	if session != nil && !cmd.Flags().Changed("project-id") {
		projectID, err = session.String("Project id", projectID)
		if err != nil {
			return err
		}
		if projectID == "" {
			return fmt.Errorf("project id is required")
		}
	}

	// Compose paths: flags/env only (advanced local-dev). Never prompted.
	plat5Compose := initPlat5Compose
	if plat5Compose == "" {
		plat5Compose = os.Getenv("PLAT5_COMPOSE")
	}
	if plat5Compose != "" {
		plat5Compose, err = resolvePlat5Flag(plat5Compose)
		if err != nil {
			return fmt.Errorf("plat5-compose: %w", err)
		}
	}

	// Interactive template choice when --template omitted.
	chosenTemplate := initTemplate
	if session != nil && !cmd.Flags().Changed("template") {
		chosenTemplate, err = promptTemplate(session)
		if err != nil {
			return err
		}
	}

	authEnabled := initAuth
	if session != nil && !cmd.Flags().Changed("auth") {
		authEnabled, err = session.YesNo("Enable Plat5 Auth?", false)
		if err != nil {
			return err
		}
	}

	authCompose := initAuthCompose
	if authCompose == "" {
		authCompose = os.Getenv("PLAT5_AUTH_COMPOSE")
	}
	if authCompose != "" {
		authCompose, err = resolveAuthFlag(authCompose)
		if err != nil {
			return fmt.Errorf("auth-compose: %w", err)
		}
	}

	obsEnabled := initObservability
	if session != nil && !cmd.Flags().Changed("observability") {
		obsEnabled, err = session.YesNo("Enable observability?", false)
		if err != nil {
			return err
		}
	}

	obsCompose := initObservabilityCompose
	if obsCompose == "" {
		obsCompose = os.Getenv("PLAT5_OBSERVABILITY_COMPOSE")
	}
	if obsCompose != "" {
		obsCompose, err = resolveObservabilityFlag(obsCompose)
		if err != nil {
			return fmt.Errorf("observability-compose: %w", err)
		}
	}

	var tpl *template.Template
	if chosenTemplate != "" {
		opts, err := templateResolveOpts()
		if err != nil {
			return err
		}
		tpl, err = template.Resolve(opts, chosenTemplate)
		if err != nil {
			return err
		}
		if err := tpl.Copy(cwd); err != nil {
			return err
		}
		if err := tpl.Substitute(cwd, projectID); err != nil {
			return err
		}
		fmt.Printf("Copied template %s → %s\n", tpl.Manifest.Name, cwd)
	}

	upstreams := map[string]string{"api": "3000"}
	routes := []string{"./routes.yml"}
	if tpl != nil {
		if len(tpl.Manifest.Upstreams) > 0 {
			upstreams = tpl.Manifest.Upstreams
		}
		if len(tpl.Manifest.Routes) > 0 {
			routes = tpl.Manifest.Routes
		}
	}

	body := renderPlat5YML(projectID, plat5Compose, authCompose, authEnabled, obsCompose, obsEnabled, upstreams, routes)
	if err := os.WriteFile(ymlPath, []byte(body), 0o644); err != nil {
		return err
	}
	fmt.Println("Created", ymlPath)

	if obsEnabled {
		if n, err := enableTemplateOTLP(cwd); err != nil {
			return err
		} else if n > 0 {
			fmt.Printf("Enabled OTEL_EXPORTER_OTLP_ENDPOINT in %d compose file(s)\n", n)
		}
	}

	if tpl == nil {
		routesPath := filepath.Join(cwd, "routes.yml")
		if _, err := os.Stat(routesPath); err == nil {
			fmt.Println("Keeping existing", routesPath)
		} else {
			if err := os.WriteFile(routesPath, []byte(sampleRoutes), 0o644); err != nil {
				return err
			}
			fmt.Println("Created", routesPath)
		}
	}

	fmt.Println()
	fmt.Println("Next:")
	n := 1
	fmt.Printf("  %d. plat5 start\n", n)
	n++
	if tpl != nil {
		for _, step := range tpl.Manifest.Next {
			fmt.Printf("  %d. %s\n", n, step)
			n++
		}
	} else {
		fmt.Printf("  %d. Set upstreams in plat5.yml if needed\n", n)
	}
	return nil
}

// promptTemplate offers a menu when templates are discoverable.
// Returns template name or "" for bare scaffold.
func promptTemplate(session *prompt.Session) (string, error) {
	opts, err := templateResolveOpts()
	if err != nil {
		fmt.Printf("Tip: templates unavailable (%v) — bare scaffold\n", err)
		return "", nil
	}
	list, err := template.ListAvailable(opts)
	if err != nil || len(list) == 0 {
		if err != nil {
			fmt.Println("Tip: could not list templates:", err)
		}
		return "", nil
	}

	display := make([]string, 0, len(list)+1)
	display = append(display, "none — plat5.yml + sample routes only")
	for _, s := range list {
		if s.Description != "" {
			display = append(display, fmt.Sprintf("%s — %s", s.Name, s.Description))
		} else {
			display = append(display, s.Name)
		}
	}
	// Default: first real template (index 1)
	defaultIdx := 1
	if len(display) == 1 {
		defaultIdx = 0
	}
	fmt.Println("Template:")
	idx, err := session.Choice("Template", display, defaultIdx)
	if err != nil {
		return "", err
	}
	if idx == 0 {
		return "", nil
	}
	return list[idx-1].Name, nil
}

func runListTemplates(cmd *cobra.Command) error {
	opts, err := templateResolveOpts()
	if err != nil {
		return err
	}
	list, err := template.ListAvailable(opts)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("No templates found")
		return nil
	}
	for _, s := range list {
		if s.Description != "" {
			fmt.Printf("%-20s  %s\n", s.Name, s.Description)
		} else {
			fmt.Println(s.Name)
		}
	}
	return nil
}

func initVersion() string {
	if initPlat5Version != "" {
		return initPlat5Version
	}
	if v := os.Getenv("PLAT5_VERSION"); v != "" {
		return v
	}
	return "v0.1.3"
}

func initAuthVer() string {
	if initAuthVersion != "" {
		return initAuthVersion
	}
	if v := os.Getenv("AUTH_VERSION"); v != "" {
		return v
	}
	return "v0.1.3"
}

// templateResolveOpts prefers explicit local dirs; otherwise remote GitHub archives.
func templateResolveOpts() (template.ResolveOptions, error) {
	opts := template.ResolveOptions{Ref: initTemplateRef}
	if initTemplatesDir != "" {
		abs, err := filepath.Abs(prompt.ExpandPath(initTemplatesDir))
		if err != nil {
			return opts, err
		}
		opts.LocalRoot = abs
		return opts, nil
	}
	if v := os.Getenv("PLAT5_TEMPLATES"); v != "" {
		abs, err := filepath.Abs(prompt.ExpandPath(v))
		if err != nil {
			return opts, err
		}
		opts.LocalRoot = abs
		return opts, nil
	}
	return opts, nil
}

func validatePlat5Path(p string) error {
	if strings.TrimSpace(p) == "" {
		return nil
	}
	_, err := compose.ResolveDir(p)
	return err
}

func validateAuthPath(p string) error {
	_, err := compose.ResolveDir(p)
	return err
}

func validateObservabilityPath(p string) error {
	_, err := compose.ResolveDir(p)
	return err
}

func resolvePlat5Flag(p string) (string, error) {
	return compose.ResolveDir(prompt.ExpandPath(p))
}

func resolveAuthFlag(p string) (string, error) {
	return compose.ResolveDir(prompt.ExpandPath(p))
}

func resolveObservabilityFlag(p string) (string, error) {
	return compose.ResolveDir(prompt.ExpandPath(p))
}

func renderPlat5YML(projectID, plat5Path, auth string, authEnabled bool, obs string, obsEnabled bool, upstreams map[string]string, routes []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "project_id: %s\n\n", yamlString(projectID))
	fmt.Fprintf(&b, "# Runtime GHCR tag (gateway, registry, identity).\n")
	fmt.Fprintf(&b, "plat5_version: %s\n\n", yamlString(initVersion()))
	if plat5Path != "" {
		fmt.Fprintf(&b, "plat5_compose: %s\n", yamlString(plat5Path))
	}
	if auth != "" {
		fmt.Fprintf(&b, "auth_compose: %s\n", yamlString(auth))
	}
	if obs != "" {
		fmt.Fprintf(&b, "observability_compose: %s\n", yamlString(obs))
	}
	if plat5Path != "" || auth != "" || obs != "" {
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "auth:\n  enabled: %t\n", authEnabled)
	if authEnabled {
		fmt.Fprintf(&b, "  version: %s  # ghcr.io/plat5dev/auth (independent of plat5_version)\n", yamlString(initAuthVer()))
		// Defaults match web-demo (Vite :5173) + Postman OAuth callback.
		fmt.Fprintf(&b, "  allowed_clients: [plat5]\n")
		fmt.Fprintf(&b, "  allowed_redirect_uris:\n")
		fmt.Fprintf(&b, "    - http://localhost:5173/callback\n")
		fmt.Fprintf(&b, "    - https://oauth.pstmn.io/v1/callback\n")
		fmt.Fprintf(&b, "  allowed_origins:\n")
		fmt.Fprintf(&b, "    - http://localhost:5173\n")
	}
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "observability:\n  enabled: %t\n\n", obsEnabled)
	fmt.Fprintf(&b, "# Optional host port pins. Omit a key to use defaults;\n")
	fmt.Fprintf(&b, "# unpinned busy ports are auto-allocated. Pinned + busy → start fails.\n")
	fmt.Fprintf(&b, "# ports:\n#   gateway: 5001\n#   registry: 5002\n#   auth: 5000\n")
	fmt.Fprintf(&b, "#   grafana: 3002\n#   otlp_grpc: 4317\n#   otlp_http: 4318\n#   alloy: 12345\n\n")
	fmt.Fprintf(&b, "admin_token: %s\n\n", config.DefaultAdminToken)
	if obsEnabled {
		fmt.Fprintf(&b, "# OTLP for Plat5/Auth containers (matches default ports.otlp_http).\n")
		fmt.Fprintf(&b, "otel:\n  endpoint: http://host.docker.internal:4318\n\n")
	} else {
		fmt.Fprintf(&b, "# Optional OTLP for Plat5/Auth containers (unset = no export).\n")
		fmt.Fprintf(&b, "# When observability.enabled, CLI auto-wires host.docker.internal:<otlp_http> if unset.\n")
		fmt.Fprintf(&b, "# otel:\n#   endpoint: http://host.docker.internal:4318\n\n")
	}
	fmt.Fprintf(&b, "# Where your services listen. Keys match services.* in routes.yml.\n")
	fmt.Fprintf(&b, "# Bare port → host process (CLI expands to host.docker.internal for Docker Plat5).\n")
	fmt.Fprintf(&b, "# Or host:port / full URL for localhost or remote origins.\n")
	fmt.Fprintf(&b, "upstreams:\n")
	if len(upstreams) == 0 {
		fmt.Fprintf(&b, "  api: 3000\n")
	} else {
		keys := make([]string, 0, len(upstreams))
		for k := range upstreams {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if k == "api" && i != 0 {
				keys = append([]string{"api"}, append(keys[:i], keys[i+1:]...)...)
				break
			}
		}
		for _, k := range keys {
			fmt.Fprintf(&b, "  %s: %s\n", k, yamlString(upstreams[k]))
		}
	}
	fmt.Fprintf(&b, "\nroutes:\n")
	if len(routes) == 0 {
		fmt.Fprintf(&b, "  - ./routes.yml\n")
	} else {
		for _, r := range routes {
			fmt.Fprintf(&b, "  - %s\n", yamlString(r))
		}
	}
	return b.String()
}

func yamlString(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, ":#{}[]&*?|>!%@`'\" \t") {
		return fmt.Sprintf("%q", s)
	}
	return s
}

// enableTemplateOTLP uncomments OTEL_EXPORTER_OTLP_ENDPOINT in project compose files
// (template starters ship it commented). Returns how many files were changed.
func enableTemplateOTLP(root string) (int, error) {
	var changed int
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if base == "node_modules" || base == ".git" || base == "vendor" || base == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if name != "docker-compose.yml" && name != "docker-compose.yaml" &&
			name != "compose.yml" && name != "compose.yaml" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out, n := uncommentOTLPEndpoint(string(data))
		if n == 0 {
			return nil
		}
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			return err
		}
		changed++
		return nil
	})
	return changed, err
}

// uncommentOTLPEndpoint turns commented OTEL_EXPORTER_OTLP_ENDPOINT lines into active env entries.
func uncommentOTLPEndpoint(src string) (string, int) {
	lines := strings.Split(src, "\n")
	var n int
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "#") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(trim, "#"))
		if !strings.HasPrefix(rest, "OTEL_EXPORTER_OTLP_ENDPOINT") {
			continue
		}
		// Preserve indentation of the comment marker.
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		lines[i] = indent + rest
		n++
	}
	if n == 0 {
		return src, 0
	}
	return strings.Join(lines, "\n"), n
}

const sampleRoutes = `services:
  api:
    # url comes from plat5.yml upstreams (applied by the CLI)
    public:
      routes:
        - path: /api
          methods: [GET, POST, PUT, PATCH, DELETE]
        - path: /api/{id}
          methods: [GET, POST, PUT, PATCH, DELETE]
`
