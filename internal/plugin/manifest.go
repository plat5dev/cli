// Package plugin loads and validates declarative Plat5 plugin manifests (plat5.plugin.yml).
package plugin

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const ManifestName = "plat5.plugin.yml"

// Plugin types (plat5.plugin.yml type:).
const (
	TypePreset          = "preset"
	TypeIdP             = "idp"
	TypeAuthorizer      = "authorizer"
	TypeService         = "service"
	TypeCustomerConsole = "customer-console"
	TypeOperatorConsole = "operator-console"
	TypeObservability   = "observability"
	TypeCI              = "ci"
	TypeDeploy          = "deploy"
	TypeRecipe          = "recipe"
)

// Project slots where plugins attach.
const (
	SlotPreset          = "preset"
	SlotAuth            = "auth"
	SlotAuthZ           = "authz"
	SlotService         = "service"
	SlotCustomerConsole = "customer-console"
	SlotOperatorConsole = "operator-console"
	SlotOperatorAuth    = "operator-auth"
	SlotOperatorAuthZ   = "operator-authz"
	SlotObservability   = "observability"
	SlotCI              = "ci"
	SlotDeploy          = "deploy"
)

var validTypes = map[string]bool{
	TypePreset:          true,
	TypeIdP:             true,
	TypeAuthorizer:      true,
	TypeService:         true,
	TypeCustomerConsole: true,
	TypeOperatorConsole: true,
	TypeObservability:   true,
	TypeCI:              true,
	TypeDeploy:          true,
	TypeRecipe:          true,
}

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var promptKeyRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var envVarRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

// Manifest is a plugin's plat5.plugin.yml.
type Manifest struct {
	Schema       int          `yaml:"schema"`
	Name         string       `yaml:"name"`
	Type         string       `yaml:"type"`
	ServiceName  string       `yaml:"service_name"`
	Description  string       `yaml:"description"`
	CLI          string       `yaml:"cli"`
	Requires     Requires     `yaml:"requires"`
	Conflicts    []Conflict   `yaml:"conflicts"`
	Capabilities Capabilities `yaml:"capabilities"`
	Prompts      []Prompt     `yaml:"prompts"`
	Verify       *Verify      `yaml:"verify"`
	Target       string       `yaml:"target"`
	Render       []string     `yaml:"render"`
	Exclude      []string     `yaml:"exclude"`
	Fragments    Fragments    `yaml:"fragments"`
	App          App          `yaml:"app"`
	Next         []string     `yaml:"next"`
	Recipe       RecipeMap    `yaml:"recipe"`
}

// Requires declares compatibility prerequisites.
type Requires struct {
	Kinds      []string `yaml:"kinds"`
	AuthZ      string   `yaml:"authz"`
	AuthZStyle string   `yaml:"authz_style"`
	Operator   bool     `yaml:"operator"`
}

// Conflict declares an explicit incompatibility.
type Conflict struct {
	ID         string `yaml:"id"`
	Type       string `yaml:"type"`
	AuthZStyle string `yaml:"authz_style"`
}

// Capabilities declares traits exposed to downstream layers.
type Capabilities struct {
	PermissionSeparator string `yaml:"permission_separator"`
	AuthZStyle          string `yaml:"authz_style"`
	DevSeed             bool   `yaml:"dev_seed"`
}

// Prompt is a declarative user input collected during init or add.
type Prompt struct {
	Key         string         `yaml:"key"`
	Type        string         `yaml:"type"`
	Title       string         `yaml:"title"`
	Help        string         `yaml:"help"`
	Placeholder string         `yaml:"placeholder"`
	Default     string         `yaml:"default"`
	Options     []PromptOption `yaml:"options"`
	Validate    string         `yaml:"validate"`
	Required    *bool          `yaml:"required"`
	Env         string         `yaml:"env"`
	When        string         `yaml:"when"`
}

// PromptOption is one choice for a select prompt.
type PromptOption struct {
	Label string `yaml:"label"`
	Value string `yaml:"value"`
}

// IsRequired reports whether the prompt requires a non-empty value.
func (p Prompt) IsRequired() bool {
	if p.Required != nil {
		return *p.Required
	}
	return p.Default == "" && p.Type != "confirm"
}

// Verify is an optional read-only HTTP preflight check.
type Verify struct {
	HTTPGet      string            `yaml:"http_get"`
	HTTPHead     string            `yaml:"http_head"`
	Headers      map[string]string `yaml:"headers"`
	ExpectStatus int               `yaml:"expect_status"`
	Timeout      string            `yaml:"timeout"`
	Label        string            `yaml:"label"`
}

// Fragments holds structured patches merged into project root files.
type Fragments struct {
	Plat5        map[string]any    `yaml:"plat5"`
	Operator     map[string]any    `yaml:"operator"`
	Local        map[string]any    `yaml:"local"`
	Services     []ServiceFragment `yaml:"services"`
	Compose      string            `yaml:"compose"`
	InfraCompose string            `yaml:"infra_compose"`
	Env          []EnvEntry        `yaml:"env"`
	Gitignore    []string          `yaml:"gitignore"`
}

// ServiceFragment copies/renders a service file from the plugin into services/.
type ServiceFragment struct {
	Src  string `yaml:"src"`
	Dest string `yaml:"dest"`
}

// UnmarshalYAML accepts either a plain path string or a {src, dest} mapping.
func (s *ServiceFragment) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		s.Src = value.Value
		s.Dest = value.Value
		return nil
	}
	type raw ServiceFragment
	var r raw
	if err := value.Decode(&r); err != nil {
		return err
	}
	*s = ServiceFragment(r)
	if s.Dest == "" {
		s.Dest = s.Src
	}
	return nil
}

// EnvEntry describes one .env.example / .env.local variable.
type EnvEntry struct {
	Name    string `yaml:"name"`
	Value   string `yaml:"value"`
	Secret  bool   `yaml:"secret"`
	Comment string `yaml:"comment"`
}

// App holds app compose and browser origin metadata.
type App struct {
	Compose string `yaml:"compose"`
	Web     string `yaml:"web"`
}

// RecipeMap maps slot names to one or more plugin references.
type RecipeMap map[string][]string

// UnmarshalYAML accepts either a scalar string or a list of strings per slot.
func (r *RecipeMap) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("recipe must be a mapping")
	}
	out := RecipeMap{}
	for i := 0; i+1 < len(value.Content); i += 2 {
		k := value.Content[i].Value
		v := value.Content[i+1]
		switch v.Kind {
		case yaml.ScalarNode:
			out[k] = []string{v.Value}
		case yaml.SequenceNode:
			var list []string
			if err := v.Decode(&list); err != nil {
				return err
			}
			out[k] = list
		default:
			return fmt.Errorf("recipe.%s: expected string or list of strings", k)
		}
	}
	*r = out
	return nil
}

// Plugin is a loaded plugin directory on disk.
type Plugin struct {
	Dir      string
	Manifest Manifest
	Origin   string
	cleanup  func()

	// Instance-level overrides set during assembly.
	Slot        string
	ServiceName string
	Prompts     map[string]string
}

// SetCleanup registers a cleanup callback (e.g. removing a downloaded temp dir).
func (p *Plugin) SetCleanup(fn func()) { p.cleanup = fn }

// Close removes any temporary directory associated with a downloaded plugin.
func (p *Plugin) Close() {
	if p != nil && p.cleanup != nil {
		p.cleanup()
		p.cleanup = nil
	}
}

// LoadDir loads and validates a plugin directory containing plat5.plugin.yml.
func LoadDir(dir string) (*Plugin, error) {
	path := filepath.Join(dir, ManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, err := ParseManifest(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	p := &Plugin{
		Dir:         dir,
		Manifest:    m,
		Origin:      dir,
		Slot:        DefaultSlot(m.Type),
		ServiceName: m.DefaultServiceName(),
		Prompts:     map[string]string{},
	}
	return p, nil
}

// ParseManifest decodes and validates plat5.plugin.yml strictly.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, err
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Validate checks manifest invariants.
func (m Manifest) Validate() error {
	if m.Schema != 1 {
		return fmt.Errorf("schema must be 1 (got %d)", m.Schema)
	}
	if !nameRE.MatchString(m.Name) {
		return fmt.Errorf("name %q: must match [a-z][a-z0-9-]* (max 64)", m.Name)
	}
	if !validTypes[m.Type] {
		return fmt.Errorf("type %q: unknown plugin type", m.Type)
	}
	if strings.TrimSpace(m.Description) == "" {
		return fmt.Errorf("description is required")
	}
	if m.ServiceName != "" && !nameRE.MatchString(m.ServiceName) {
		return fmt.Errorf("service_name %q: must match [a-z][a-z0-9-]* (max 64)", m.ServiceName)
	}
	if m.Capabilities.PermissionSeparator != "" {
		switch m.Capabilities.PermissionSeparator {
		case ":", "_", ".", "-":
		default:
			return fmt.Errorf("capabilities.permission_separator %q: want :, _, ., or -", m.Capabilities.PermissionSeparator)
		}
	}
	if m.Type == TypeCustomerConsole || m.Type == TypeOperatorConsole {
		if m.App.Compose == "" || m.App.Web == "" {
			return fmt.Errorf("%s requires app.compose and app.web", m.Type)
		}
	}
	if m.Type == TypeRecipe && len(m.Recipe) == 0 {
		return fmt.Errorf("recipe plugin requires non-empty recipe: map")
	}
	seenKeys := map[string]bool{}
	for _, p := range m.Prompts {
		if !promptKeyRE.MatchString(p.Key) {
			return fmt.Errorf("prompts.%q: invalid key", p.Key)
		}
		if seenKeys[p.Key] {
			return fmt.Errorf("prompts.%q: duplicate key", p.Key)
		}
		seenKeys[p.Key] = true
		switch p.Type {
		case "input", "secret", "select", "confirm":
		default:
			return fmt.Errorf("prompts.%s: unknown type %q", p.Key, p.Type)
		}
		if p.Type == "secret" && !envVarRE.MatchString(p.Env) {
			return fmt.Errorf("prompts.%s: secret prompt requires valid env var name", p.Key)
		}
		if p.Type == "select" && len(p.Options) == 0 {
			return fmt.Errorf("prompts.%s: select prompt requires options", p.Key)
		}
	}
	for _, e := range m.Fragments.Env {
		if !envVarRE.MatchString(e.Name) {
			return fmt.Errorf("fragments.env: invalid env name %q", e.Name)
		}
	}
	return nil
}

// DefaultServiceName returns service_name if set, else name.
func (m Manifest) DefaultServiceName() string {
	if m.ServiceName != "" {
		return m.ServiceName
	}
	return m.Name
}

// DefaultTarget returns the default project-relative destination directory for
// copied files when target: is omitted.
func (m Manifest) DefaultTarget() string {
	if m.Target != "" {
		return m.Target
	}
	switch m.Type {
	case TypePreset, TypeRecipe:
		return ""
	case TypeIdP:
		return "idp"
	case TypeAuthorizer:
		return "authorizer"
	case TypeService:
		return "[[.ServiceName]]"
	case TypeCustomerConsole:
		return "customer-console"
	case TypeOperatorConsole:
		return "operator-console"
	case TypeObservability:
		return "observability"
	case TypeCI, TypeDeploy:
		return "."
	default:
		return m.Name
	}
}

// DefaultSlot returns the default project slot for a plugin type.
func DefaultSlot(typ string) string {
	switch typ {
	case TypePreset:
		return SlotPreset
	case TypeIdP:
		return SlotAuth
	case TypeAuthorizer:
		return SlotAuthZ
	case TypeService:
		return SlotService
	case TypeCustomerConsole:
		return SlotCustomerConsole
	case TypeOperatorConsole:
		return SlotOperatorConsole
	case TypeObservability:
		return SlotObservability
	case TypeCI:
		return SlotCI
	case TypeDeploy:
		return SlotDeploy
	default:
		return typ
	}
}

// AllowsSlot reports whether a plugin of typ may attach to slot.
func AllowsSlot(typ, slot string) bool {
	if DefaultSlot(typ) == slot {
		return true
	}
	if typ == TypeIdP && slot == SlotOperatorAuth {
		return true
	}
	if typ == TypeAuthorizer && slot == SlotOperatorAuthZ {
		return true
	}
	return false
}

// Noise and metadata never copied from a plugin directory.
var noise = []string{".git", "node_modules", "dist", ".env", ".env.local", "*.local", ".DS_Store", "*.log"}
var rootMeta = []string{ManifestName, "plat5.template.yml", "plat5.yml", "plat5.local.yml", "services", "data/*.db", "data/*.db-*"}

// IsExcluded reports whether a plugin-relative path should be skipped when copying files.
func (p *Plugin) IsExcluded(rel string) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	base := filepath.Base(rel)
	for _, pat := range noise {
		if ok, _ := filepath.Match(pat, base); ok {
			return true
		}
	}
	// Preserve .github for CI / deploy plugins targeting project root.
	if rel == ".github" && p.Manifest.Type != TypeCI && p.Manifest.Type != TypeDeploy {
		return true
	}
	for _, pat := range rootMeta {
		if ok, _ := filepath.Match(filepath.ToSlash(filepath.Clean(pat)), rel); ok {
			return true
		}
	}
	for _, pat := range p.Manifest.Exclude {
		if ok, _ := filepath.Match(filepath.ToSlash(filepath.Clean(pat)), rel); ok {
			return true
		}
	}
	return false
}

// IsRendered reports whether rel is listed in manifest.Render.
func (p *Plugin) IsRendered(rel string) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	for _, r := range p.Manifest.Render {
		if filepath.ToSlash(filepath.Clean(r)) == rel {
			return true
		}
	}
	return false
}
