// Package config reads plat5.dev.yml: how plat5 start runs the project on your
// machine, and how to reach its control plane. A deployment never reads it; the
// deployment's config is plat5.yml beside it (internal/plat5yml).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/plat5dev/cli/internal/ports"
	"gopkg.in/yaml.v3"
)

const (
	DefaultAdminToken  = "dev-admin-token"
	DefaultAPIKeyBrand = "plat5"
	// DefaultAuthClient is Plat5 Auth's default client, and so the gateway's audience.
	DefaultAuthClient = "plat5"
)

// The local dev login, seeded into local Auth, and what start seeds for it. Same
// class of local default as admin_token: never a deployment's.
const (
	DevEmail    = "dev@example.com"
	DevPassword = "password"
	DevName     = "Dev Person"
	// DevNodeName is the display_name of the node start seeds of each root kind.
	DevNodeName = "Dev"
)

// The operator module's local login: the operator IdP's dev user, and the public
// client the operator console signs in with, which is also the operator gateway's
// audience. Its password is DevPassword.
const (
	OperatorEmail  = "operator@example.com"
	OperatorClient = "operator-console"
)

// Modules run beside Plat5. All on unless plat5.dev.yml turns one off.
const (
	ModuleAuth          = "auth"
	ModuleObservability = "observability"
	ModuleOperator      = "operator"
)

// Modules is every module, in start order.
var Modules = []string{ModuleAuth, ModuleObservability, ModuleOperator}

// Image pins: versions: keys, and the repos plat5 init reads the latest tag from.
const (
	VersionPlat5    = "plat5"
	VersionAuth     = "auth"
	VersionOperator = "operator"
)

// VersionRepos is each pin's GitHub repo.
var VersionRepos = map[string]string{
	VersionPlat5:    "plat5dev/plat5",
	VersionAuth:     "plat5dev/auth",
	VersionOperator: "plat5dev/operator",
}

// Sources are the checkouts source: may build from, by the same keys as versions.
var Sources = []string{VersionPlat5, VersionAuth, VersionOperator}

// FileName is the project file; ConfigFileName is the deployment's config beside it.
const (
	FileName       = "plat5.dev.yml"
	ConfigFileName = "plat5.yml"
)

const maxAPIKeyBrandLen = 32

var projectIDSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// File is plat5.dev.yml.
type File struct {
	// CLI is the oldest CLI the project works with: ">=X.Y.Z" (plat5 init writes it).
	CLI         string            `yaml:"cli"`
	ProjectID   string            `yaml:"project_id"`
	Versions    map[string]string `yaml:"versions"`
	Modules     map[string]bool   `yaml:"modules"`
	App         AppBlock          `yaml:"app"`
	Ports       map[string]int    `yaml:"ports"`
	AdminToken  string            `yaml:"admin_token"`
	APIKeyBrand string            `yaml:"apikey_brand"`
	Auth        AuthBlock         `yaml:"auth"`
	IdP         IdPBlock          `yaml:"idp"`
	Otel        OtelBlock         `yaml:"otel"`
	Operator    OperatorBlock     `yaml:"operator"`
	Source      map[string]string `yaml:"source"`
}

// OperatorBlock is what the operator module runs from the project.
type OperatorBlock struct {
	// Routes is the operator gateway's route list, its ROUTES_FILE.
	Routes string `yaml:"routes"`
	// Console is the operator console: its compose file, which app.compose
	// includes and plat5 start runs only with the module, and its browser origin,
	// allowed to sign in with the operator IdP and to call the operator gateway.
	Console AppBlock `yaml:"console"`
	// Origins is more browser origins the operator side allows, beyond the console's.
	Origins []string `yaml:"origins"`
}

// AppBlock is the project's own app, which plat5 start runs after Plat5.
type AppBlock struct {
	// Compose is the app's compose file. Empty: you run the app yourself.
	Compose string `yaml:"compose"`
	// Web is the app's browser origin, allowed to sign in with local Auth.
	Web string `yaml:"web"`
}

// AuthBlock adds to local Auth's allowlists, beyond the app's web origin.
type AuthBlock struct {
	Clients      []string `yaml:"clients"`
	RedirectURIs []string `yaml:"redirect_uris"`
	Origins      []string `yaml:"origins"`
	// ThemeFile is an OpenAuth Theme JSON for the sign-in page.
	ThemeFile string `yaml:"theme_file"`
}

// IdPBlock is the customer IdP when the auth module is off.
type IdPBlock struct {
	Issuer    string   `yaml:"issuer"`
	JWKSURI   string   `yaml:"jwks_uri"`
	Audiences []string `yaml:"audiences"`
	// SubjectClaim is the dotted claim path for the person's IdP subject.
	SubjectClaim string `yaml:"subject_claim"`
}

// OtelBlock sends telemetry to your own collector instead of the observability module.
type OtelBlock struct {
	Endpoint string `yaml:"endpoint"`
}

// Overrides are CLI flags for commands that may talk to a control plane that is
// not the local one. PLAT5_CONTROL_PLANE_URL and PLAT5_ADMIN_TOKEN
// do the same from the environment.
type Overrides struct {
	ControlPlaneURL string
	AdminToken      string
}

// Resolved is plat5.dev.yml with defaults applied and paths made absolute.
type Resolved struct {
	ProjectID string
	// Path is plat5.dev.yml's; Dir is the project directory.
	Path string
	Dir  string
	// ConfigPath is the deployment's config, plat5.yml beside it.
	ConfigPath string

	Versions map[string]string
	// Modules is each module's on/off from plat5.dev.yml (before start -x).
	Modules map[string]bool

	AppCompose string // absolute; empty = no app
	AppWeb     string // origin, no trailing slash

	Ports    ports.Set
	PortPins ports.Set

	AdminToken      string
	ControlPlaneURL string
	APIKeyBrand     string

	Auth         AuthBlock
	IdP          IdPBlock
	OtelEndpoint string

	// OperatorRoutes is the operator gateway's route file. Absolute; empty = unset.
	OperatorRoutes string
	// OperatorConsole is the operator console's compose file (absolute), and
	// OperatorWeb its origin. Empty: no operator console.
	OperatorConsole string
	OperatorWeb     string
	// OperatorExtraOrigins is operator.origins: more origins the operator side allows.
	OperatorExtraOrigins []string

	// Source is checkouts to build images from, by version key. Absolute.
	Source map[string]string

	// ComposeProject is the local stack's compose project.
	ComposeProject string
}

// ErrNoProject means no plat5.dev.yml was found.
type ErrNoProject struct{}

func (ErrNoProject) Error() string { return "no " + FileName + " found; run plat5 init" }

// Load finds plat5.dev.yml (walking up from the working directory) and resolves it.
func Load(o Overrides) (Resolved, error) {
	file, path, err := find()
	if err != nil {
		return Resolved{}, err
	}
	if file == nil {
		return Resolved{}, ErrNoProject{}
	}
	r, err := Resolve(file, path, o)
	if err != nil {
		return Resolved{}, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

// Resolve applies defaults and checks a parsed plat5.dev.yml found at path.
func Resolve(f *File, path string, o Overrides) (Resolved, error) {
	dir := filepath.Dir(path)
	r := Resolved{
		Path:       path,
		Dir:        dir,
		ConfigPath: filepath.Join(dir, ConfigFileName),
		Versions:   map[string]string{},
		Modules:    map[string]bool{},
		Source:     map[string]string{},
		AdminToken: DefaultAdminToken,
	}

	r.ProjectID = sanitizeProjectID(firstNonEmpty(f.ProjectID, filepath.Base(dir)))
	if r.ProjectID == "" {
		return Resolved{}, fmt.Errorf("project_id: invalid")
	}
	r.ComposeProject = "plat5-" + r.ProjectID

	for k, v := range f.Modules {
		if !contains(Modules, k) {
			return Resolved{}, fmt.Errorf("modules.%s: unknown module (want %s)", k, strings.Join(Modules, ", "))
		}
		r.Modules[k] = v
	}
	for _, m := range Modules {
		if _, set := r.Modules[m]; !set {
			r.Modules[m] = true
		}
	}

	for k, v := range f.Source {
		if !contains(Sources, k) {
			return Resolved{}, fmt.Errorf("source.%s: unknown (want %s)", k, strings.Join(Sources, ", "))
		}
		if v = strings.TrimSpace(v); v != "" {
			r.Source[k] = resolveAgainst(dir, v)
		}
	}
	for k, v := range f.Versions {
		if _, known := VersionRepos[k]; !known {
			return Resolved{}, fmt.Errorf("versions.%s: unknown (want %s)", k, strings.Join(sortedKeys(VersionRepos), ", "))
		}
		r.Versions[k] = strings.TrimSpace(v)
	}

	r.OperatorRoutes = resolveAgainst(dir, strings.TrimSpace(f.Operator.Routes))
	r.OperatorConsole = resolveAgainst(dir, strings.TrimSpace(f.Operator.Console.Compose))
	if web := strings.TrimSpace(f.Operator.Console.Web); web != "" {
		origin, err := parseOrigin(web)
		if err != nil {
			return Resolved{}, fmt.Errorf("operator.console.web: %w", err)
		}
		r.OperatorWeb = origin
	}
	r.OperatorExtraOrigins = trimOrigins(f.Operator.Origins)

	r.AppCompose = resolveAgainst(dir, strings.TrimSpace(f.App.Compose))
	if web := strings.TrimSpace(f.App.Web); web != "" {
		origin, err := parseOrigin(web)
		if err != nil {
			return Resolved{}, fmt.Errorf("app.web: %w", err)
		}
		r.AppWeb = origin
	}
	for _, k := range r.NeededVersions() {
		if r.Versions[k] == "" && r.Source[k] == "" {
			hint := ""
			if k == VersionOperator {
				hint = ", or turn modules.operator off"
			}
			return Resolved{}, fmt.Errorf("versions.%s is required (plat5 init writes the latest tag%s)", k, hint)
		}
	}

	r.PortPins = ports.Set{}
	for k, v := range f.Ports {
		n := ports.Name(k)
		if _, known := ports.Defaults[n]; !known {
			return Resolved{}, fmt.Errorf("ports.%s: unknown port", k)
		}
		r.PortPins[n] = v
	}
	r.Ports = ports.With(r.PortPins)
	if err := ports.Validate(r.Ports); err != nil {
		return Resolved{}, err
	}

	if f.AdminToken != "" {
		r.AdminToken = f.AdminToken
	}
	r.ControlPlaneURL = r.URL(ports.ControlPlane)
	if v := firstNonEmpty(o.ControlPlaneURL, os.Getenv("PLAT5_CONTROL_PLANE_URL")); v != "" {
		r.ControlPlaneURL = strings.TrimRight(v, "/")
	}
	if v := firstNonEmpty(o.AdminToken, os.Getenv("PLAT5_ADMIN_TOKEN")); v != "" {
		r.AdminToken = v
	}

	var err error
	if r.APIKeyBrand, err = parseAPIKeyBrand(firstNonEmpty(f.APIKeyBrand, DefaultAPIKeyBrand)); err != nil {
		return Resolved{}, err
	}

	r.Auth = AuthBlock{
		Clients:      trimNonEmpty(f.Auth.Clients),
		RedirectURIs: trimNonEmpty(f.Auth.RedirectURIs),
		Origins:      trimOrigins(f.Auth.Origins),
		ThemeFile:    resolveAgainst(dir, strings.TrimSpace(f.Auth.ThemeFile)),
	}
	r.IdP = IdPBlock{
		Issuer:       strings.TrimSpace(f.IdP.Issuer),
		JWKSURI:      strings.TrimSpace(f.IdP.JWKSURI),
		Audiences:    trimNonEmpty(f.IdP.Audiences),
		SubjectClaim: strings.TrimSpace(f.IdP.SubjectClaim),
	}
	if !r.Modules[ModuleAuth] && (r.IdP.Issuer == "" || r.IdP.JWKSURI == "" || len(r.IdP.Audiences) == 0) {
		return Resolved{}, fmt.Errorf("modules.auth is off: set idp.issuer, idp.jwks_uri, and idp.audiences to your IdP")
	}
	if r.Modules[ModuleAuth] && r.IdP.Issuer != "" {
		return Resolved{}, fmt.Errorf("idp: is for your own IdP; turn modules.auth off to use it")
	}
	r.OtelEndpoint = strings.TrimSpace(f.Otel.Endpoint)
	return r, nil
}

// NeededVersions is the image pins the enabled modules need. The operator IdP
// is Plat5 Auth at the auth pin.
func (r Resolved) NeededVersions() []string {
	need := []string{VersionPlat5}
	if r.Modules[ModuleAuth] || r.Modules[ModuleOperator] {
		need = append(need, VersionAuth)
	}
	if r.Modules[ModuleOperator] {
		need = append(need, VersionOperator)
	}
	return need
}

// URL is the local URL of a published port.
func (r Resolved) URL(n ports.Name) string {
	return fmt.Sprintf("http://localhost:%d", r.Ports[n])
}

// GatewayURL is where clients call the deployment.
func (r Resolved) GatewayURL() string { return r.URL(ports.Gateway) }

// AuthClients is local Auth's client ids, which are also the gateway's audiences
// (Plat5 Auth puts the client id in the token's aud).
func (r Resolved) AuthClients() []string {
	return uniq(append([]string{DefaultAuthClient}, r.Auth.Clients...))
}

// AuthOrigins is the browser origins local Auth allows: the app's, and any extra.
func (r Resolved) AuthOrigins() []string {
	var out []string
	if r.AppWeb != "" {
		out = append(out, r.AppWeb)
	}
	return uniq(append(out, r.Auth.Origins...))
}

// AuthRedirectURIs is <origin>/callback for each allowed origin, and any extra.
func (r Resolved) AuthRedirectURIs() []string {
	var out []string
	for _, o := range r.AuthOrigins() {
		out = append(out, o+"/callback")
	}
	return uniq(append(out, r.Auth.RedirectURIs...))
}

// OperatorOrigins is the browser origins the operator IdP and the operator
// gateway allow: the operator console's, and any extra.
func (r Resolved) OperatorOrigins() []string {
	var out []string
	if r.OperatorWeb != "" {
		out = append(out, r.OperatorWeb)
	}
	return uniq(append(out, r.OperatorExtraOrigins...))
}

// OperatorRedirectURIs is <origin>/callback for each operator origin.
func (r Resolved) OperatorRedirectURIs() []string {
	var out []string
	for _, o := range r.OperatorOrigins() {
		out = append(out, o+"/callback")
	}
	return out
}

var unknownFieldRE = regexp.MustCompile(`^line (\d+): field (\S+) not found in type `)

// Parse decodes plat5.dev.yml strictly: an unknown key is an error naming the key.
func Parse(data []byte) (*File, error) {
	if err := checkCLI(data); err != nil {
		return nil, err
	}
	var f File
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		var te *yaml.TypeError
		if !errors.As(err, &te) {
			return nil, err
		}
		var msgs []string
		unknown := false
		for _, e := range te.Errors {
			if m := unknownFieldRE.FindStringSubmatch(e); m != nil {
				msgs = append(msgs, fmt.Sprintf("line %s: unknown key %q", m[1], m[2]))
				unknown = true
				continue
			}
			msgs = append(msgs, e)
		}
		msg := strings.Join(msgs, "; ")
		if unknown {
			msg += " (" + unknownKeyHint() + ")"
		}
		return nil, errors.New(msg)
	}
	return &f, nil
}

func find() (*File, string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, "", err
	}
	for {
		for _, name := range []string{FileName} {
			p := filepath.Join(dir, name)
			data, err := os.ReadFile(p)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, "", fmt.Errorf("read %s: %w", p, err)
			}
			f, err := Parse(data)
			if err != nil {
				return nil, "", fmt.Errorf("parse %s: %w", p, err)
			}
			abs, err := filepath.Abs(p)
			if err != nil {
				return nil, "", err
			}
			return f, abs, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, "", nil
		}
		dir = parent
	}
}

// CheckFile fails if a configured file is missing or a directory. Empty is fine.
func CheckFile(key, path string) error {
	if path == "" {
		return nil
	}
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s: file not found: %s", key, path)
		}
		return fmt.Errorf("%s: %w", key, err)
	}
	if st.IsDir() {
		return fmt.Errorf("%s: not a file: %s", key, path)
	}
	return nil
}

// NeedsHostGateway reports whether endpoint targets host.docker.internal.
func NeedsHostGateway(endpoint string) bool {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	return err == nil && strings.EqualFold(u.Hostname(), "host.docker.internal")
}

func parseOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" {
		return "", fmt.Errorf("%q is not an origin (scheme://host[:port])", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

func trimOrigins(in []string) []string {
	out := trimNonEmpty(in)
	for i, o := range out {
		out[i] = strings.TrimRight(o, "/")
	}
	return out
}

func resolveAgainst(base, p string) string {
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(base, p))
}

func parseAPIKeyBrand(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if len(s) > maxAPIKeyBrandLen {
		return "", fmt.Errorf("apikey_brand longer than %d characters", maxAPIKeyBrandLen)
	}
	for i, c := range s {
		letter := c >= 'a' && c <= 'z'
		digit := i > 0 && c >= '0' && c <= '9'
		if !letter && !digit {
			return "", fmt.Errorf("apikey_brand must be [a-z][a-z0-9]*, max %d", maxAPIKeyBrandLen)
		}
	}
	return s, nil
}

// SanitizeProjectID is the project id plat5 uses for a name.
func SanitizeProjectID(id string) string { return sanitizeProjectID(id) }

func sanitizeProjectID(id string) string {
	id = projectIDSanitizer.ReplaceAllString(strings.TrimSpace(id), "-")
	id = strings.Trim(id, "-")
	if len(id) > 64 {
		id = id[:64]
	}
	return strings.ToLower(id)
}

func trimNonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
