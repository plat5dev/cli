package template

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const ManifestName = "plat5.template.yml"

// Manifest is the per-template config file.
type Manifest struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	Upstreams   map[string]string `yaml:"upstreams"`
	Routes      []string          `yaml:"routes"`
	Substitute  []string          `yaml:"substitute"`
	Next        []string          `yaml:"next"`
	Exclude     []string          `yaml:"exclude"`
}

// Template is a loaded template on disk.
type Template struct {
	Dir      string
	Manifest Manifest
}

// Summary is a short listing entry.
type Summary struct {
	Name        string
	Description string
}

// defaultExcludes are never copied into the consumer project.
var defaultExcludes = []string{
	"node_modules",
	".git",
	".env",
	"plat5.yml",
	"plat5.yml.example",
	ManifestName,
	"data/*.db",
	"data/*.db-*",
	"dist",
	".DS_Store",
	"*.log",
}

// List returns templates under root that have a manifest (name = directory).
func List(root string) ([]Summary, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []Summary
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		mp := filepath.Join(root, e.Name(), ManifestName)
		m, err := loadManifest(mp)
		if err != nil {
			continue
		}
		desc := m.Description
		out = append(out, Summary{Name: e.Name(), Description: desc})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Load finds and loads a template by directory name under root.
func Load(root, name string) (*Template, error) {
	if name == "" {
		return nil, fmt.Errorf("template name is empty")
	}
	dir := filepath.Join(root, name)
	mp := filepath.Join(dir, ManifestName)
	st, err := os.Stat(mp)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("template %q not found under %s", name, root)
		}
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("template %q not found under %s", name, root)
	}
	m, err := loadManifest(mp)
	if err != nil {
		return nil, err
	}
	if m.Name == "" {
		m.Name = name
	}
	return &Template{Dir: dir, Manifest: m}, nil
}

func loadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// Copy copies the template into destDir.
// Fails if any destination path already exists (no overwrite).
func (t *Template) Copy(destDir string) error {
	excludes := append([]string{}, defaultExcludes...)
	excludes = append(excludes, t.Manifest.Exclude...)

	return filepath.WalkDir(t.Dir, func(src string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(t.Dir, src)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if shouldExclude(rel, d.IsDir(), excludes) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		dst := filepath.Join(destDir, rel)
		if d.IsDir() {
			if _, err := os.Stat(dst); err == nil {
				// dir exists — ok to merge into
				return nil
			}
			return os.MkdirAll(dst, 0o755)
		}
		if _, err := os.Stat(dst); err == nil {
			return fmt.Errorf("refusing to overwrite existing file: %s", dst)
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return copyFile(src, dst)
	})
}

// Substitute replaces {{project_id}} in manifest substitute paths under destDir.
func (t *Template) Substitute(destDir, projectID string) error {
	for _, rel := range t.Manifest.Substitute {
		rel = filepath.Clean(rel)
		if rel == "." || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("invalid substitute path: %s", rel)
		}
		path := filepath.Join(destDir, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("substitute %s: %w", rel, err)
		}
		updated := strings.ReplaceAll(string(data), "{{project_id}}", projectID)
		if updated == string(data) {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(updated), info.Mode()); err != nil {
			return err
		}
	}
	return nil
}

func shouldExclude(rel string, isDir bool, patterns []string) bool {
	base := filepath.Base(rel)
	relSlash := filepath.ToSlash(rel)
	for _, p := range patterns {
		p = filepath.ToSlash(p)
		if p == relSlash || p == base {
			return true
		}
		if isDir && (base == p || strings.HasSuffix(relSlash, "/"+p)) {
			return true
		}
		if matched, _ := filepath.Match(p, base); matched {
			return true
		}
		if matched, _ := filepath.Match(p, relSlash); matched {
			return true
		}
	}
	return false
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode())
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("refusing to overwrite existing file: %s", dst)
		}
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}
