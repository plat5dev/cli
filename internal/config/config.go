package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/plat5dev/cli/internal/ports"
	"github.com/plat5dev/cli/internal/upstreams"
	"gopkg.in/yaml.v3"
)

const DefaultAdminToken = "dev-admin-token"

var projectIDSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// Flags are CLI overrides (highest precedence).
type Flags struct {
	Plat5Compose         string
	AuthCompose          string
	ObservabilityCompose string
	Plat5Version         string
	AuthVersion          string
	RegistryURL          string
	GatewayURL           string
	AuthURL              string
	AdminToken           string
}

// File is the on-disk plat5.yml shape.
type File struct {
	ProjectID            string             `yaml:"project_id"`
	Plat5Version         string             `yaml:"plat5_version"`
	Plat5Compose         string             `yaml:"plat5_compose"`
	AuthCompose          string             `yaml:"auth_compose"`
	ObservabilityCompose string             `yaml:"observability_compose"`
	Auth                 AuthBlock          `yaml:"auth"`
	Observability        ObservabilityBlock `yaml:"observability"`
	Otel                 OtelBlock          `yaml:"otel"`
	Ports                PortsBlock         `yaml:"ports"`
	RegistryURL          string             `yaml:"registry_url"`
	GatewayURL           string             `yaml:"gateway_url"`
	AuthURL              string             `yaml:"auth_url"`
	AdminToken           string             `yaml:"admin_token"`
	Routes               []string           `yaml:"routes"`
	Upstreams            map[string]any     `yaml:"upstreams"`
}

// AuthBlock is optional Plat5 Auth settings.
type AuthBlock struct {
	Enabled bool   `yaml:"enabled"`
	Version string `yaml:"version"` // ghcr.io/plat5dev/auth tag (AUTH_VERSION)
}

// ObservabilityBlock is optional local LGTM stack settings.
type ObservabilityBlock struct {
	Enabled bool `yaml:"enabled"`
}

// OtelBlock is optional OpenTelemetry export for local stacks started by the CLI.
type OtelBlock struct {
	// Endpoint is OTEL_EXPORTER_OTLP_ENDPOINT (e.g. http://host.docker.internal:4318).
	// Unset = no OTLP export from Plat5/Auth containers (unless observability auto-wires it).
	Endpoint string `yaml:"endpoint"`
}

// PortsBlock holds optional host port pins from yml.
type PortsBlock struct {
	Gateway  *int `yaml:"gateway"`
	Registry *int `yaml:"registry"`
	Auth     *int `yaml:"auth"`
	Grafana  *int `yaml:"grafana"`
	OTLPGRPC *int `yaml:"otlp_grpc"`
	OTLPHTTP *int `yaml:"otlp_http"`
	Alloy    *int `yaml:"alloy"`
}

// Resolved is effective project config after merge.
type Resolved struct {
	ProjectID  string
	ConfigPath string
	ConfigDir  string
	// Plat5Version pins runtime GHCR tags (gateway, registry, api-keys, organizations).
	Plat5Version string
	// AuthVersion pins ghcr.io/plat5dev/auth (independent of Plat5Version).
	AuthVersion string
	// Plat5Compose / AuthCompose set = path mode (contributor). Empty = image mode (embedded).
	Plat5Compose             string
	AuthCompose              string
	ObservabilityCompose     string
	AuthEnabled              bool
	ObservabilityEnabled     bool
	OtelEndpoint             string
	otelExplicit             bool // set from yml/env/flag — not auto-wired
	Ports                    ports.Set
	PortsExplicit            ports.Explicit
	GatewayURL               string
	RegistryURL              string
	AuthURL                  string
	GrafanaURL               string
	AlloyURL                 string
	urlExplicit              urlExplicit
	AdminToken               string
	RouteFiles               []string
	Upstreams                map[string]string // service name → raw value (port or URL); expanded at apply
	ComposeProject           string
	AuthComposeName          string
	ObservabilityComposeName string
}

type urlExplicit struct {
	Gateway  bool
	Registry bool
	Auth     bool
}

// ErrNoProject means no plat5.yml was found.
type ErrNoProject struct{}

func (e ErrNoProject) Error() string {
	return "no plat5.yml found; run plat5 init"
}

// Load finds plat5.yml (walk up), merges flags/env, resolves paths.
// Does not allocate ports — call ResolvePorts before start.
func Load(flags Flags) (Resolved, error) {
	file, configPath, err := findYAML()
	if err != nil {
		return Resolved{}, err
	}
	if file == nil {
		return Resolved{}, ErrNoProject{}
	}

	configDir := filepath.Dir(configPath)
	projectID := file.ProjectID
	if projectID == "" {
		projectID = filepath.Base(configDir)
	}
	projectID = sanitizeProjectID(projectID)
	if projectID == "" {
		return Resolved{}, fmt.Errorf("invalid project_id")
	}

	r := Resolved{
		ProjectID:                projectID,
		ConfigPath:               configPath,
		ConfigDir:                configDir,
		AuthEnabled:              file.Auth.Enabled,
		ObservabilityEnabled:     file.Observability.Enabled,
		OtelEndpoint:             strings.TrimSpace(file.Otel.Endpoint),
		AdminToken:               DefaultAdminToken,
		ComposeProject:           "plat5-" + projectID,
		AuthComposeName:          "plat5-" + projectID + "-auth",
		ObservabilityComposeName: "plat5-" + projectID + "-observability",
	}
	if r.OtelEndpoint != "" {
		r.otelExplicit = true
	}

	if file.AdminToken != "" {
		r.AdminToken = file.AdminToken
	}
	if v := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")); v != "" {
		r.OtelEndpoint = v
		r.otelExplicit = true
	}
	r.RouteFiles = resolveRouteFiles(configDir, file.Routes)

	ups, err := upstreams.ParseMap(file.Upstreams)
	if err != nil {
		return Resolved{}, err
	}
	r.Upstreams = ups

	r.Plat5Version = firstNonEmpty(flags.Plat5Version, os.Getenv("PLAT5_VERSION"), file.Plat5Version)
	if r.Plat5Version == "" {
		r.Plat5Version = "v0.1.2"
	}
	r.AuthVersion = firstNonEmpty(flags.AuthVersion, os.Getenv("AUTH_VERSION"), file.Auth.Version)
	if r.AuthVersion == "" {
		r.AuthVersion = "v0.1.2"
	}
	r.Plat5Compose = firstNonEmpty(flags.Plat5Compose, os.Getenv("PLAT5_COMPOSE"), file.Plat5Compose)
	r.AuthCompose = firstNonEmpty(flags.AuthCompose, os.Getenv("PLAT5_AUTH_COMPOSE"), file.AuthCompose)
	r.ObservabilityCompose = firstNonEmpty(flags.ObservabilityCompose, os.Getenv("PLAT5_OBSERVABILITY_COMPOSE"), file.ObservabilityCompose)
	r.Plat5Compose = resolveAgainst(configDir, r.Plat5Compose)
	r.AuthCompose = resolveAgainst(configDir, r.AuthCompose)
	r.ObservabilityCompose = resolveAgainst(configDir, r.ObservabilityCompose)

	if file.Ports.Gateway != nil {
		r.Ports.Gateway = *file.Ports.Gateway
		r.PortsExplicit.Gateway = true
	}
	if file.Ports.Registry != nil {
		r.Ports.Registry = *file.Ports.Registry
		r.PortsExplicit.Registry = true
	}
	if file.Ports.Auth != nil {
		r.Ports.Auth = *file.Ports.Auth
		r.PortsExplicit.Auth = true
	}
	if file.Ports.Grafana != nil {
		r.Ports.Grafana = *file.Ports.Grafana
		r.PortsExplicit.Grafana = true
	}
	if file.Ports.OTLPGRPC != nil {
		r.Ports.OTLPGRPC = *file.Ports.OTLPGRPC
		r.PortsExplicit.OTLPGRPC = true
	}
	if file.Ports.OTLPHTTP != nil {
		r.Ports.OTLPHTTP = *file.Ports.OTLPHTTP
		r.PortsExplicit.OTLPHTTP = true
	}
	if file.Ports.Alloy != nil {
		r.Ports.Alloy = *file.Ports.Alloy
		r.PortsExplicit.Alloy = true
	}

	if file.GatewayURL != "" {
		r.GatewayURL = file.GatewayURL
		r.urlExplicit.Gateway = true
	}
	if file.RegistryURL != "" {
		r.RegistryURL = file.RegistryURL
		r.urlExplicit.Registry = true
	}
	if file.AuthURL != "" {
		r.AuthURL = file.AuthURL
		r.urlExplicit.Auth = true
	}

	if v := os.Getenv("PLAT5_GATEWAY_URL"); v != "" {
		r.GatewayURL = v
		r.urlExplicit.Gateway = true
	}
	if v := os.Getenv("PLAT5_REGISTRY_URL"); v != "" {
		r.RegistryURL = v
		r.urlExplicit.Registry = true
	}
	if v := os.Getenv("PLAT5_AUTH_URL"); v != "" {
		r.AuthURL = v
		r.urlExplicit.Auth = true
	}
	if v := os.Getenv("PLAT5_ADMIN_TOKEN"); v != "" {
		r.AdminToken = v
	} else if v := os.Getenv("ADMIN_TOKEN"); v != "" {
		r.AdminToken = v
	}

	if flags.GatewayURL != "" {
		r.GatewayURL = flags.GatewayURL
		r.urlExplicit.Gateway = true
	}
	if flags.RegistryURL != "" {
		r.RegistryURL = flags.RegistryURL
		r.urlExplicit.Registry = true
	}
	if flags.AuthURL != "" {
		r.AuthURL = flags.AuthURL
		r.urlExplicit.Auth = true
	}
	if flags.AdminToken != "" {
		r.AdminToken = flags.AdminToken
	}

	if r.Ports.Gateway == 0 {
		r.Ports.Gateway = ports.DefaultGateway
	}
	if r.Ports.Registry == 0 {
		r.Ports.Registry = ports.DefaultRegistry
	}
	if r.Ports.Auth == 0 {
		r.Ports.Auth = ports.DefaultAuth
	}
	if r.Ports.Grafana == 0 {
		r.Ports.Grafana = ports.DefaultGrafana
	}
	if r.Ports.OTLPGRPC == 0 {
		r.Ports.OTLPGRPC = ports.DefaultOTLPGRPC
	}
	if r.Ports.OTLPHTTP == 0 {
		r.Ports.OTLPHTTP = ports.DefaultOTLPHTTP
	}
	if r.Ports.Alloy == 0 {
		r.Ports.Alloy = ports.DefaultAlloy
	}
	deriveURLs(&r)
	return r, nil
}

// ResolvePorts allocates/validates host ports and refreshes non-explicit URLs.
// When observability is enabled and otel.endpoint was not set explicitly, wires
// OTEL to http://host.docker.internal:<otlp_http>.
func ResolvePorts(r *Resolved) error {
	resolved, err := ports.Resolve(r.Ports, r.PortsExplicit)
	if err != nil {
		return err
	}
	r.Ports = resolved
	deriveURLs(r)
	wireOtelFromObservability(r)
	return nil
}

// ApplySavedPorts sets ports from a previous start and refreshes non-explicit URLs.
func ApplySavedPorts(r *Resolved, st ports.Set) {
	if st.Gateway > 0 {
		r.Ports.Gateway = st.Gateway
	}
	if st.Registry > 0 {
		r.Ports.Registry = st.Registry
	}
	if st.Auth > 0 {
		r.Ports.Auth = st.Auth
	}
	if st.Grafana > 0 {
		r.Ports.Grafana = st.Grafana
	}
	if st.OTLPGRPC > 0 {
		r.Ports.OTLPGRPC = st.OTLPGRPC
	}
	if st.OTLPHTTP > 0 {
		r.Ports.OTLPHTTP = st.OTLPHTTP
	}
	if st.Alloy > 0 {
		r.Ports.Alloy = st.Alloy
	}
	deriveURLs(r)
	wireOtelFromObservability(r)
}

func wireOtelFromObservability(r *Resolved) {
	if r.otelExplicit || !r.ObservabilityEnabled {
		return
	}
	if r.Ports.OTLPHTTP <= 0 {
		return
	}
	r.OtelEndpoint = fmt.Sprintf("http://host.docker.internal:%d", r.Ports.OTLPHTTP)
}

func deriveURLs(r *Resolved) {
	// Use localhost (not 127.0.0.1): JWT iss is the Auth origin the browser hits.
	// Gateway AUTH_ISSUER must match token iss exactly (localhost ≠ 127.0.0.1).
	if !r.urlExplicit.Gateway {
		r.GatewayURL = fmt.Sprintf("http://localhost:%d", r.Ports.Gateway)
	}
	if !r.urlExplicit.Registry {
		r.RegistryURL = fmt.Sprintf("http://localhost:%d", r.Ports.Registry)
	}
	if !r.urlExplicit.Auth {
		r.AuthURL = fmt.Sprintf("http://localhost:%d", r.Ports.Auth)
	}
	if r.Ports.Grafana > 0 {
		r.GrafanaURL = fmt.Sprintf("http://localhost:%d", r.Ports.Grafana)
	}
	if r.Ports.Alloy > 0 {
		r.AlloyURL = fmt.Sprintf("http://localhost:%d", r.Ports.Alloy)
	}
}

func findYAML() (*File, string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, "", err
	}
	for {
		for _, name := range []string{"plat5.yaml", "plat5.yml"} {
			p := filepath.Join(dir, name)
			data, err := os.ReadFile(p)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, "", fmt.Errorf("read %s: %w", p, err)
			}
			var f File
			if err := yaml.Unmarshal(data, &f); err != nil {
				return nil, "", fmt.Errorf("parse %s: %w", p, err)
			}
			abs, err := filepath.Abs(p)
			if err != nil {
				return nil, "", err
			}
			return &f, abs, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil, "", nil
}

func resolveRouteFiles(configDir string, routes []string) []string {
	if len(routes) == 0 {
		return []string{filepath.Join(configDir, "routes.yml")}
	}
	out := make([]string, 0, len(routes))
	for _, r := range routes {
		out = append(out, resolveAgainst(configDir, r))
	}
	return out
}

func resolveAgainst(base, p string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(base, p))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func sanitizeProjectID(id string) string {
	id = strings.TrimSpace(id)
	id = projectIDSanitizer.ReplaceAllString(id, "-")
	id = strings.Trim(id, "-")
	if len(id) > 64 {
		id = id[:64]
	}
	return strings.ToLower(id)
}

// NeedsHostGateway reports whether endpoint targets host.docker.internal
// (containers need extra_hosts host-gateway to reach a host-published collector).
func NeedsHostGateway(endpoint string) bool {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return false
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		// bare host:port
		host := endpoint
		if i := strings.Index(endpoint, "/"); i >= 0 {
			host = endpoint[:i]
		}
		if h, _, ok := strings.Cut(host, ":"); ok {
			return strings.EqualFold(h, "host.docker.internal")
		}
		return strings.EqualFold(host, "host.docker.internal")
	}
	host := u.Hostname()
	return strings.EqualFold(host, "host.docker.internal")
}
