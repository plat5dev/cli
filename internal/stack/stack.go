// Package stack is a project's local stack as one compose project: Plat5 (core)
// and the modules plat5.yml turns on, from the compose files embedded in the CLI.
// It owns the compose env, the generated override, and the app's own compose.
package stack

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/plat5dev/cli/internal/bundle"
	"github.com/plat5dev/cli/internal/compose"
	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/ports"
	"github.com/plat5dev/cli/internal/state"
	"github.com/plat5dev/cli/internal/upstreams"
)

// Container paths for files the override mounts.
const (
	rolesContainerPath          = "/seed-roles/roles.yml"
	themeContainerPath          = "/config/theme.json"
	operatorRoutesContainerPath = "/config/routes.yml"
)

// Stack is one project's local stack.
type Stack struct {
	Config config.Resolved
	// Modules is what this stack runs beside core: plat5.yml's, minus start -x.
	Modules []string
	// Dir is where the compose files are written (the project's state dir).
	Dir string
}

// New is the stack for cfg, without the excluded modules.
func New(cfg config.Resolved, exclude []string) (*Stack, error) {
	for _, x := range exclude {
		if !isModule(x) {
			return nil, fmt.Errorf("-x %s: unknown module (want %s)", x, strings.Join(config.Modules, ", "))
		}
	}
	var mods []string
	for _, m := range config.Modules {
		if cfg.Modules[m] && !contains(exclude, m) {
			mods = append(mods, m)
		}
	}
	if !contains(mods, config.ModuleAuth) && cfg.IdP.Issuer == "" {
		return nil, fmt.Errorf("-x auth: the gateway needs an IdP; set idp: in plat5.yml and turn modules.auth off")
	}
	dir, err := state.Dir(cfg.ProjectID)
	if err != nil {
		return nil, err
	}
	return &Stack{Config: cfg, Modules: mods, Dir: filepath.Join(dir, "stack")}, nil
}

// Has reports whether the stack runs a module.
func (s *Stack) Has(module string) bool { return contains(s.Modules, module) }

// Prepare writes the compose files and the override for this run.
func (s *Stack) Prepare() error {
	if err := bundle.Materialize(s.Dir); err != nil {
		return fmt.Errorf("write compose files: %w", err)
	}
	if err := config.CheckFile("roles", s.Config.RolesFile); err != nil {
		return err
	}
	if err := config.CheckFile("auth.theme_file", s.Config.Auth.ThemeFile); err != nil {
		return err
	}
	if s.Has(config.ModuleAuth) && s.Config.Auth.ThemeFile == "" {
		if err := writeTheme(filepath.Join(s.Dir, "auth-theme.json"), s.authTitle(), customerBanner, "#000000"); err != nil {
			return err
		}
	}
	if s.Has(config.ModuleOperator) {
		if err := writeTheme(filepath.Join(s.Dir, "staff-theme.json"), s.staffTitle(), staffBanner, "#b45309"); err != nil {
			return err
		}
		if err := s.writeOperatorRoutes(); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(s.Dir, "override.yml"), []byte(s.override()), 0o644)
}

// writeOperatorRoutes writes the project's operator route list with plat5.yml's
// upstreams bound into it, as the customer routes are at apply.
func (s *Stack) writeOperatorRoutes() error {
	path := s.Config.OperatorRoutesFile
	if err := config.CheckFile("operator.routes", path); err != nil || path == "" {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	bound, _, err := upstreams.BindOperator(data, s.Config.Upstreams)
	if err != nil {
		return fmt.Errorf("operator.routes: %w", err)
	}
	return os.WriteFile(filepath.Join(s.Dir, "operator-routes.yml"), bound, 0o644)
}

// fileSHA256 is the hex digest of a file, or empty when it can't be read.
func fileSHA256(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Runner is docker compose for the stack.
func (s *Stack) Runner() compose.Runner {
	files := []string{bundle.Files["core"]}
	for _, m := range s.Modules {
		files = append(files, bundle.Files[m])
	}
	files = append(files, "override.yml")
	return compose.Runner{Dir: s.Dir, ProjectName: s.Config.ComposeProject, Files: files, Env: s.Env()}
}

// AppRunner is docker compose for the app's own file, or nil when there is none.
// The app is its own compose project (its file's name), so `docker compose` in the
// app's directory sees the same containers.
func (s *Stack) AppRunner() *compose.Runner {
	if s.Config.AppCompose == "" {
		return nil
	}
	return &compose.Runner{
		Dir:   filepath.Dir(s.Config.AppCompose),
		Files: []string{s.Config.AppCompose},
		Env:   s.AppEnv(),
	}
}

// Ports is the host ports the stack publishes.
func (s *Stack) Ports() map[ports.Name]int {
	want := map[ports.Name]int{
		ports.Gateway:      s.Config.Ports[ports.Gateway],
		ports.ControlPlane: s.Config.Ports[ports.ControlPlane],
	}
	add := func(names ...ports.Name) {
		for _, n := range names {
			want[n] = s.Config.Ports[n]
		}
	}
	if s.Has(config.ModuleAuth) {
		add(ports.Auth)
	}
	if s.Has(config.ModuleOperator) {
		add(ports.Operator, ports.StaffAuth, ports.OperatorConsole)
	}
	if s.Has(config.ModuleObservability) {
		add(ports.Grafana, ports.OTLPGRPC, ports.OTLPHTTP)
	}
	return want
}

// OtelEndpoint is where Plat5's containers send telemetry: the project's own
// collector, else the observability module's Alloy, else nowhere.
func (s *Stack) OtelEndpoint() string {
	if s.Config.OtelEndpoint != "" {
		return s.Config.OtelEndpoint
	}
	if s.Has(config.ModuleObservability) {
		return "http://alloy:4318"
	}
	return ""
}

// Env is the compose interpolation env for the stack's files.
func (s *Stack) Env() []string {
	c := s.Config
	env := map[string]string{
		"ADMIN_TOKEN":                 c.AdminToken,
		"APIKEY_BRAND":                c.APIKeyBrand,
		"ROLES_ENABLED":               fmt.Sprint(c.RolesFile != ""),
		"OTEL_EXPORTER_OTLP_ENDPOINT": s.OtelEndpoint(),

		"AUTH_DISPLAY_NAME": s.authTitle(),
		"AUTH_CLIENTS":      strings.Join(c.AuthClients(), ","),
		"AUTH_ORIGINS":      strings.Join(c.AuthOrigins(), ","),
		"AUTH_DEV_USERS":    config.DevEmail + ":" + config.DevPassword,

		"STAFF_AUTH_DISPLAY_NAME": s.staffTitle(),
		"OPERATOR_ORIGINS":        strings.Join(c.OperatorConsoleOrigins(), ","),
		"STAFF_DEV_USERS":         config.StaffEmail + ":" + config.DevPassword,
	}
	env["AUTH_REDIRECT_URIS"] = strings.Join(c.AuthRedirectURIs(), ",")
	var staffRedirects []string
	for _, o := range c.OperatorConsoleOrigins() {
		staffRedirects = append(staffRedirects, o+"/callback")
	}
	env["OPERATOR_REDIRECT_URIS"] = strings.Join(staffRedirects, ",")

	// The gateway's IdP: local Auth, or the project's own.
	if s.Has(config.ModuleAuth) {
		env["IDP_ISSUER"] = c.URL(ports.Auth)
		env["IDP_JWKS_URI"] = "http://auth:5000/.well-known/jwks.json"
		env["IDP_AUDIENCES"] = strings.Join(c.AuthClients(), ",")
		env["IDP_USER_ID_CLAIM"] = "properties.user_id"
	} else {
		env["IDP_ISSUER"] = c.IdP.Issuer
		env["IDP_JWKS_URI"] = c.IdP.JWKSURI
		env["IDP_AUDIENCES"] = strings.Join(c.IdP.Audiences, ",")
		env["IDP_USER_ID_CLAIM"] = firstNonEmpty(c.IdP.UserIDClaim, "sub")
	}

	for key, name := range map[string]string{
		config.VersionPlat5:           "PLAT5_VERSION",
		config.VersionAuth:            "AUTH_VERSION",
		config.VersionOperator:        "OPERATOR_VERSION",
		config.VersionOperatorConsole: "OPERATOR_CONSOLE_VERSION",
	} {
		v := c.Versions[key]
		if c.Source[key] != "" {
			// Built from a checkout: the override replaces the image. The pin only
			// has to be non-empty for the files to interpolate.
			v = "source"
		}
		env[name] = v
	}
	for _, n := range ports.Names() {
		env[strings.ToUpper(string(n))+"_PORT"] = fmt.Sprint(c.Ports[n])
	}
	return sortedEnv(env)
}

// AppEnv is what the app's compose file can use: where Plat5 is, for the browser
// and for containers. The template contract (cli README) names these.
func (s *Stack) AppEnv() []string {
	c := s.Config
	otel := c.OtelEndpoint
	if otel == "" && s.Has(config.ModuleObservability) {
		otel = fmt.Sprintf("http://host.docker.internal:%d", c.Ports[ports.OTLPHTTP])
	}
	env := map[string]string{
		"PLAT5_GATEWAY_URL":           c.GatewayURL(),
		"PLAT5_AUTH_URL":              "",
		"PLAT5_AUTH_CLIENT_ID":        config.DefaultAuthClient,
		"OTEL_EXPORTER_OTLP_ENDPOINT": otel,
	}
	if s.Has(config.ModuleAuth) {
		env["PLAT5_AUTH_URL"] = c.URL(ports.Auth)
	} else {
		env["PLAT5_AUTH_URL"] = c.IdP.Issuer
	}
	return sortedEnv(env)
}

// Sign-in page titles: the project's, and its staff's.
func (s *Stack) authTitle() string  { return s.Config.ProjectID }
func (s *Stack) staffTitle() string { return s.Config.ProjectID + " staff" }

const (
	customerBanner = "Customer sign-in (local) · " + config.DevEmail + " / " + config.DevPassword
	staffBanner    = "Staff sign-in · operator plane (local) · " + config.StaffEmail + " / " + config.DevPassword
)

// writeTheme writes an OpenAuth Theme for a local Auth: Plat5's look, with a banner
// that says which sign-in this is and the seeded login.
func writeTheme(path, title, banner, primary string) error {
	theme := map[string]any{
		"title":      title,
		"favicon":    "/static/p5.jpg",
		"logo":       map[string]string{"light": "/static/logo.jpg", "dark": "/static/logo.jpg"},
		"background": map[string]string{"light": "#ffffff", "dark": "#ffffff"},
		"primary":    map[string]string{"light": primary, "dark": primary},
		"radius":     "md",
		"css": fmt.Sprintf(`:root { color-scheme: light; }
body { color: #000; background: #fff; }
body::before { content: %q; position: fixed; top: 0; left: 0; right: 0; padding: 8px 16px;
  background: %s; color: #fff; font: 600 13px/1.4 system-ui, sans-serif; text-align: center; }`, banner, primary),
	}
	data, err := json.MarshalIndent(theme, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// svcOverride is what the override sets on one service.
type svcOverride struct {
	env        map[string]string
	volumes    []string
	extraHosts bool
	build      *sourceBuild
}

// override is the compose override for this run: what the embedded files can't
// say with env alone.
func (s *Stack) override() string {
	c := s.Config
	svcs := map[string]*svcOverride{}
	get := func(name string) *svcOverride {
		if svcs[name] == nil {
			svcs[name] = &svcOverride{env: map[string]string{}}
		}
		return svcs[name]
	}
	mount := func(name, host, container string) {
		o := get(name)
		o.volumes = append(o.volumes, host+":"+container+":ro")
	}

	// Roles on: the project's file seeds the first roles document, so identity can
	// start. start then applies it with PUT /roles, so later edits land too.
	if c.RolesFile != "" {
		get("control-plane").env["SEED_ROLES_FILE"] = rolesContainerPath
		mount("control-plane", c.RolesFile, rolesContainerPath)
	}
	if s.Has(config.ModuleAuth) {
		theme := c.Auth.ThemeFile
		if theme == "" {
			theme = filepath.Join(s.Dir, "auth-theme.json")
		}
		get("auth").env["AUTH_THEME_FILE"] = themeContainerPath
		mount("auth", theme, themeContainerPath)
	}
	if s.Has(config.ModuleOperator) {
		get("staff-auth").env["AUTH_THEME_FILE"] = themeContainerPath
		mount("staff-auth", filepath.Join(s.Dir, "staff-theme.json"), themeContainerPath)
		// The project's route list. Its upstreams may be on the host, like the gateway's.
		if c.OperatorRoutesFile != "" {
			get("operator").env["ROUTES_FILE"] = operatorRoutesContainerPath
			// The gateway reads the file at boot. A changed hash recreates it.
			get("operator").env["PLAT5_ROUTES_SHA256"] = fileSHA256(filepath.Join(s.Dir, "operator-routes.yml"))
			mount("operator", filepath.Join(s.Dir, "operator-routes.yml"), operatorRoutesContainerPath)
			get("operator").extraHosts = true
		}
	}
	// A collector on the host: containers need host.docker.internal.
	if config.NeedsHostGateway(c.OtelEndpoint) {
		for _, name := range s.services() {
			get(name).extraHosts = true
		}
	}
	for _, sb := range s.sourceBuilds() {
		sb := sb
		get(sb.service).build = &sb
	}

	names := make([]string, 0, len(svcs))
	for n := range svcs {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("# Generated by plat5 start. Do not edit.\nservices:")
	if len(names) == 0 {
		b.WriteString(" {}\n")
		return b.String()
	}
	b.WriteString("\n")
	for _, n := range names {
		o := svcs[n]
		fmt.Fprintf(&b, "  %s:\n", n)
		if len(o.env) > 0 {
			b.WriteString("    environment:\n")
			for _, kv := range sortedEnv(o.env) {
				k, v, _ := strings.Cut(kv, "=")
				fmt.Fprintf(&b, "      %s: %q\n", k, v)
			}
		}
		if len(o.volumes) > 0 {
			b.WriteString("    volumes:\n")
			for _, v := range o.volumes {
				fmt.Fprintf(&b, "      - %q\n", v)
			}
		}
		if o.extraHosts {
			b.WriteString("    extra_hosts:\n      - \"host.docker.internal:host-gateway\"\n")
		}
		if sb := o.build; sb != nil {
			fmt.Fprintf(&b, "    image: plat5-source/%s:%s\n", sb.service, c.ProjectID)
			fmt.Fprintf(&b, "    build:\n      context: %q\n", sb.context)
			if sb.target != "" {
				fmt.Fprintf(&b, "      target: %s\n", sb.target)
			}
			b.WriteString("    pull_policy: build\n")
		}
	}
	return b.String()
}

// services is every Plat5-built service the stack runs.
func (s *Stack) services() []string {
	out := []string{"control-plane", "gateway", "identity", "audit"}
	if s.Has(config.ModuleAuth) {
		out = append(out, "auth")
	}
	if s.Has(config.ModuleOperator) {
		out = append(out, "operator", "operator-audit", "staff-auth")
	}
	return out
}

// sourceBuild is one service built from a checkout. Each service gets its own
// image tag: two services building one tag at once is a build error.
type sourceBuild struct {
	service, context, target string
}

// sourceBuilds is the services built from source: checkouts. Prod targets: what ships.
func (s *Stack) sourceBuilds() []sourceBuild {
	src := s.Config.Source
	var out []sourceBuild
	if dir := src[config.VersionPlat5]; dir != "" {
		for _, svc := range []string{"control-plane", "gateway", "identity", "audit"} {
			out = append(out, sourceBuild{svc, filepath.Join(dir, "services", svc), "prod"})
		}
	}
	if dir := src[config.VersionAuth]; dir != "" {
		if s.Has(config.ModuleAuth) {
			out = append(out, sourceBuild{"auth", filepath.Join(dir, "issuer"), "prod"})
		}
		if s.Has(config.ModuleOperator) {
			out = append(out, sourceBuild{"staff-auth", filepath.Join(dir, "issuer"), "prod"})
		}
	}
	if dir := src[config.VersionOperator]; dir != "" && s.Has(config.ModuleOperator) {
		out = append(out,
			sourceBuild{"operator", dir, "prod"},
			sourceBuild{"operator-audit", dir, "operator-audit"},
			sourceBuild{"operator-audit-migrate", dir, "operator-audit"},
		)
	}
	if dir := src[config.VersionOperatorConsole]; dir != "" && s.Has(config.ModuleOperator) {
		out = append(out, sourceBuild{"operator-console", dir, ""})
	}
	return out
}

// Builds reports whether start must build images (some module is from source).
func (s *Stack) Builds() bool { return len(s.sourceBuilds()) > 0 }

func sortedEnv(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

func isModule(m string) bool { return contains(config.Modules, m) }

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
