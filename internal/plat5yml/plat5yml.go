// Package plat5yml reads and writes a deployment's config: plat5.yml, and the
// service files its services list names, one service each (plat5
// docs/concepts/config.md#the-config). The control plane takes them as one flat
// object; its errors name field paths, and this package says which file each is in.
package plat5yml

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/plat5dev/cli/internal/schema"
)

// Config is a plat5.yml and its service files, assembled.
type Config struct {
	// Path is plat5.yml's path; service files are relative to its directory.
	Path string
	// Flat is what the control plane takes: plat5.yml's keys, services a map of
	// name to service.
	Flat map[string]any
	// Files is each service's file, as listed in plat5.yml.
	Files map[string]string
}

// Load reads plat5.yml and every service file it lists. A service file holding
// anything but one service, or two files naming the same service, is an error
// naming the file.
func Load(path string) (*Config, error) {
	top, err := readMap(path)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(path)
	c := &Config{Path: path, Flat: top, Files: map[string]string{}}
	services := map[string]any{}
	listed, err := ServiceFiles(top, name)
	if err != nil {
		return nil, err
	}
	for _, rel := range listed {
		file, err := readMap(filepath.Join(filepath.Dir(path), rel))
		if err != nil {
			return nil, err
		}
		if len(file) != 1 {
			return nil, fmt.Errorf("%s: must hold exactly one service, keyed by its name (it holds %d keys)", rel, len(file))
		}
		for svc, body := range file {
			if other, ok := c.Files[svc]; ok {
				return nil, fmt.Errorf("%s: service %s is already in %s", rel, svc, other)
			}
			c.Files[svc] = rel
			services[svc] = body
		}
	}
	top["services"] = services
	return c, nil
}

// ServiceFiles is plat5.yml's services list: service file paths.
func ServiceFiles(top map[string]any, name string) ([]string, error) {
	raw, ok := top["services"]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: services must be a list of service files", name)
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok || s == "" {
			return nil, fmt.Errorf("%s: services must be a list of service files", name)
		}
		out = append(out, s)
	}
	return out, nil
}

func readMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: must be a mapping", path)
	}
	return m, nil
}

// FileOf is the file a field path is in: services.<name>… is that service's
// file, everything else plat5.yml. Service names may hold dots, so the
// longest name that fits wins.
func (c *Config) FileOf(path string) string {
	main := filepath.Base(c.Path)
	rest, ok := strings.CutPrefix(path, "services.")
	if !ok {
		return main
	}
	best := ""
	for name := range c.Files {
		if (rest == name || strings.HasPrefix(rest, name+".") || strings.HasPrefix(rest, name+"[")) && len(name) > len(best) {
			best = name
		}
	}
	if best == "" {
		return main
	}
	return c.Files[best]
}

// Validate checks the flat config against the schema, offline. Each error names
// its file.
func (c *Config) Validate() ([]string, error) {
	errs, err := schema.ValidateConfig(c.Flat)
	if err != nil {
		return nil, err
	}
	return c.Describe(errs), nil
}

// Describe writes field errors as "file: path: message".
func (c *Config) Describe(errs []schema.FieldError) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, c.FileOf(e.Path)+": "+e.Error())
	}
	return out
}

// Write writes a flat config (a revision, as the control plane gives it back) as
// plat5.yml and services/<service>.yml in dir.
func Write(dir string, flat map[string]any) ([]string, error) {
	top := map[string]any{}
	for k, v := range flat {
		if k != "services" {
			top[k] = v
		}
	}
	services, _ := flat["services"].(map[string]any)
	names := make([]string, 0, len(services))
	for n := range services {
		names = append(names, n)
	}
	sort.Strings(names)
	var written []string
	list := make([]any, 0, len(names))
	if len(names) > 0 {
		if err := os.MkdirAll(filepath.Join(dir, "services"), 0o755); err != nil {
			return nil, err
		}
	}
	for _, n := range names {
		rel := "services/" + n + ".yml"
		if err := WriteYAML(filepath.Join(dir, rel), map[string]any{n: services[n]}, ""); err != nil {
			return nil, err
		}
		list = append(list, rel)
		written = append(written, rel)
	}
	top["services"] = list
	if err := WriteYAML(filepath.Join(dir, "plat5.yml"), top, ""); err != nil {
		return nil, err
	}
	return append([]string{"plat5.yml"}, written...), nil
}

// keyOrder puts a config's own keys in the order the docs write them. Names
// (kinds, roles, services, policies) sort after, alphabetically.
var keyOrder = []string{
	"url", "timeouts", "max_body_bytes", "resources",
	"deletion_grace", "reserved_slugs", "users", "name_claim", "kinds", "roles", "creator_role",
	"defaults", "rate_limits", "cors", "services",
	"parent", "path", "list_permission", "includes", "permissions", "bindable_at",
	"connect", "read", "requests", "window_seconds", "count_at", "allowed_origins", "allowed_headers",
	"scopes", "upstream_prefix", "routes", "upstream", "methods",
	"required_permissions", "rate_limit", "audit", "allow_deleted",
	"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD",
}

func rank(k string) int {
	for i, o := range keyOrder {
		if o == k {
			return i
		}
	}
	return len(keyOrder)
}

// Node is v as a YAML node: config keys in the docs' order, lists of scalars on
// one line.
func Node(v any) *yaml.Node {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.SliceStable(keys, func(i, j int) bool {
			ri, rj := rank(keys[i]), rank(keys[j])
			if ri != rj {
				return ri < rj
			}
			return keys[i] < keys[j]
		})
		n := &yaml.Node{Kind: yaml.MappingNode}
		for _, k := range keys {
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, Node(t[k]))
		}
		return n
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode}
		flow := true
		for _, item := range t {
			child := Node(item)
			if child.Kind != yaml.ScalarNode {
				flow = false
			}
			n.Content = append(n.Content, child)
		}
		if flow {
			n.Style = yaml.FlowStyle
		}
		return n
	default:
		n := &yaml.Node{}
		if err := n.Encode(t); err != nil {
			return &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprint(t)}
		}
		return n
	}
}

// WriteYAML writes v to path, after a comment header if one is given.
func WriteYAML(path string, v any, header string) error {
	var buf bytes.Buffer
	buf.WriteString(header)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(Node(v)); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
