// Package plat5yml reads and writes a deployment's config: plat5.yml, the
// service files its services list names, one service each (plat5
// docs/concepts/config.md#the-config), and optional overlay files merged over
// it. The control plane takes them as one flat object; its errors name field
// paths, and this package says which file each came from.
package plat5yml

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/plat5dev/cli/internal/schema"
)

// Config is a plat5.yml (plus any overlays) and its service files, assembled.
type Config struct {
	// Path is the base plat5.yml's path; service files in the base are relative to its directory.
	Path string
	// Paths is every loaded config file, base first, then overlays in order.
	Paths []string
	// Flat is what the control plane takes: plat5.yml's keys, services a map of
	// name to service.
	Flat map[string]any
	// Files is each service's file (as listed in services:, or the overlay that added it).
	Files map[string]string
	// sources maps each dotted field path to the file that last set it.
	sources map[string]string
}

// Load reads plat5.yml and every service file it lists. A service file holding
// anything but one service, or two files naming the same service, is an error
// naming the file.
func Load(path string) (*Config, error) {
	return LoadMany(path)
}

// LoadMany reads a base plat5.yml and merges zero or more overlay files on top
// of it using RFC 7396 merge-patch semantics: maps merge recursively, scalars
// and lists replace, and null deletes a key. An overlay's services: may be a
// list of service files (resolved relative to the overlay's directory) or a
// mapping merged directly onto services.
func LoadMany(paths ...string) (*Config, error) {
	if len(paths) == 0 {
		return nil, errors.New("plat5yml: at least one config file is required")
	}
	basePath := paths[0]
	top, err := readMap(basePath)
	if err != nil {
		return nil, err
	}
	baseDir := filepath.Dir(basePath)
	baseLabel := filepath.Base(basePath)
	c := &Config{
		Path:    basePath,
		Paths:   append([]string(nil), paths...),
		Flat:    top,
		Files:   map[string]string{},
		sources: map[string]string{},
	}
	listed, err := ServiceFiles(top, baseLabel)
	if err != nil {
		return nil, err
	}
	services, svcFiles, err := loadServiceFiles(baseDir, baseDir, true, listed)
	if err != nil {
		return nil, err
	}
	for k, v := range top {
		if k == "services" {
			continue
		}
		recordSources(c.sources, k, v, baseLabel)
	}
	c.sources["services"] = baseLabel
	for svc, body := range services {
		c.Files[svc] = svcFiles[svc]
		recordSources(c.sources, "services."+svc, body, svcFiles[svc])
	}
	top["services"] = services

	for _, ovPath := range paths[1:] {
		if err := c.applyOverlay(baseDir, ovPath); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (c *Config) applyOverlay(baseDir, ovPath string) error {
	ov, err := readMap(ovPath)
	if err != nil {
		return err
	}
	ovLabel := displayPath(baseDir, ovPath)

	rawSvc, hasSvc := ov["services"]
	if hasSvc {
		delete(ov, "services")
	}
	mergePatch(c.Flat, ov, "", ovLabel, c.sources)

	if !hasSvc {
		return nil
	}
	switch s := rawSvc.(type) {
	case nil:
		deleteSources(c.sources, "services", c.Flat["services"])
		delete(c.Flat, "services")
		c.Files = map[string]string{}
	case []any:
		listed, err := parseServiceList(s, ovLabel)
		if err != nil {
			return err
		}
		svcMap, svcFiles, err := loadServiceFiles(filepath.Dir(ovPath), baseDir, false, listed)
		if err != nil {
			return err
		}
		dst := c.ensureServicesMap(ovLabel)
		for svc, body := range svcMap {
			file := svcFiles[svc]
			if body == nil {
				deleteSources(c.sources, "services."+svc, dst[svc])
				delete(dst, svc)
				delete(c.Files, svc)
				continue
			}
			c.Files[svc] = file
			mergeValue(dst, svc, body, "services."+svc, file, c.sources)
		}
	case map[string]any:
		dst := c.ensureServicesMap(ovLabel)
		for svc, body := range s {
			if body == nil {
				deleteSources(c.sources, "services."+svc, dst[svc])
				delete(dst, svc)
				delete(c.Files, svc)
				continue
			}
			if _, exists := c.Files[svc]; !exists {
				c.Files[svc] = ovLabel
			}
			mergeValue(dst, svc, body, "services."+svc, ovLabel, c.sources)
		}
	default:
		return fmt.Errorf("%s: services must be a list of service files or a mapping", ovLabel)
	}
	return nil
}

func (c *Config) ensureServicesMap(file string) map[string]any {
	if m, ok := c.Flat["services"].(map[string]any); ok {
		return m
	}
	m := map[string]any{}
	c.Flat["services"] = m
	c.sources["services"] = file
	return m
}

func loadServiceFiles(dir, baseDir string, keepRel bool, listed []string) (map[string]any, map[string]string, error) {
	services := map[string]any{}
	files := map[string]string{}
	for _, rel := range listed {
		full := filepath.Join(dir, rel)
		label := rel
		if !keepRel {
			label = displayPath(baseDir, full)
		}
		file, err := readMap(full)
		if err != nil {
			return nil, nil, err
		}
		if len(file) != 1 {
			return nil, nil, fmt.Errorf("%s: must hold exactly one service, keyed by its name (it holds %d keys)", label, len(file))
		}
		for svc, body := range file {
			if other, ok := files[svc]; ok {
				return nil, nil, fmt.Errorf("%s: service %s is already in %s", label, svc, other)
			}
			files[svc] = label
			services[svc] = body
		}
	}
	return services, files, nil
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
	return parseServiceList(list, name)
}

func parseServiceList(list []any, name string) ([]string, error) {
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

func mergePatch(dst, patch map[string]any, prefix, file string, sources map[string]string) {
	for k, v := range patch {
		path := joinPath(prefix, k)
		if v == nil {
			deleteSources(sources, path, dst[k])
			delete(dst, k)
			continue
		}
		mergeValue(dst, k, v, path, file, sources)
	}
}

func mergeValue(dst map[string]any, key string, v any, path, file string, sources map[string]string) {
	if pm, ok := v.(map[string]any); ok {
		if dm, ok := dst[key].(map[string]any); ok {
			mergePatch(dm, pm, path, file, sources)
			return
		}
	}
	deleteSources(sources, path, dst[key])
	dst[key] = v
	recordSources(sources, path, v, file)
}

func recordSources(sources map[string]string, prefix string, v any, file string) {
	if prefix != "" {
		sources[prefix] = file
	}
	if m, ok := v.(map[string]any); ok {
		for k, child := range m {
			recordSources(sources, joinPath(prefix, k), child, file)
		}
	}
}

func deleteSources(sources map[string]string, prefix string, old any) {
	delete(sources, prefix)
	if m, ok := old.(map[string]any); ok {
		for k, child := range m {
			deleteSources(sources, joinPath(prefix, k), child)
		}
	}
}

func joinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func displayPath(baseDir, path string) string {
	if rel, err := filepath.Rel(baseDir, path); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
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

// FileOf is the file a field path came from: the last file (base, service file,
// or overlay) that set the field or its nearest enclosing key.
func (c *Config) FileOf(path string) string {
	main := filepath.Base(c.Path)
	if len(c.sources) > 0 {
		for cur := path; cur != ""; {
			if f, ok := c.sources[cur]; ok {
				return f
			}
			idx := strings.LastIndexAny(cur, ".[")
			if idx <= 0 {
				break
			}
			cur = cur[:idx]
		}
		return main
	}
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
	"url", "timeouts", "max_body_bytes",
	"deletion_grace", "reserved_slugs", "policy_claims", "kinds", "roles", "creator_role", "custom_roles", "authorizer",
	"defaults", "rate_limits", "cors", "services",
	"parent", "path", "list_permission", "bind_creator", "host", "includes", "permissions", "bindable_at",
	"timeout", "cache_ttl", "negative_cache_ttl", "token_env",
	"connect", "read", "requests", "window", "count_at", "tiers", "allowed_origins", "allowed_headers",
	"scopes", "upstream_prefix", "routes", "upstream", "methods",
	"rate_limit", "audit", "allow_deleted", "resource",
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
