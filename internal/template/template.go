// Package template loads the starters plat5 init copies into a project: an API
// starter, and the customer console beside it. Each is a repo with a manifest,
// plat5.template.yml, at its root.
package template

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const ManifestName = "plat5.template.yml"

// Kinds of template.
const (
	// KindAPI is an API starter: --template names it, and plat5 init puts it in api/.
	KindAPI = "api"
	// KindCustomerConsole is the customer console, which plat5 init puts in
	// customer-console/ beside an API starter.
	KindCustomerConsole = "customer-console"
)

// Manifest is a template's plat5.template.yml.
type Manifest struct {
	Name        string `yaml:"name"`
	Kind        string `yaml:"kind"`
	Description string `yaml:"description"`
	// Service is an API starter's service file, relative to its root. plat5 init
	// writes it to the project's services/ and lists it in plat5.yml.
	Service string `yaml:"service"`
	// OperatorRoutes is an API starter's routes for an operator gateway: a
	// services: block in operator's route format. plat5 init adds its services to
	// operator.routes.yml.
	OperatorRoutes string `yaml:"operator_routes"`
	// Substitute is the files whose {{project_id}} plat5 init replaces with the
	// project id.
	Substitute []string `yaml:"substitute"`
	// App is how plat5 start runs it: its compose file, and its browser origin.
	App App `yaml:"app"`
	// Next is how to run the app on your own machine instead (plat5 start -x app).
	Next []string `yaml:"next"`
	// Exclude is paths, relative to the root, that plat5 init doesn't copy.
	Exclude []string `yaml:"exclude"`
}

// App is the template's compose file (relative to its root) and the browser
// origin of its web app, if it has one.
type App struct {
	Compose string `yaml:"compose"`
	Web     string `yaml:"web"`
}

// Template is a loaded template on disk.
type Template struct {
	Dir      string
	Manifest Manifest
	// Origin says where it came from: owner/repo@ref (commit), a URL, or a directory.
	Origin string
	// cleanup removes a downloaded template's temp dir.
	cleanup func()
}

// Close removes a downloaded template's files. A local one is left alone.
func (t *Template) Close() {
	if t != nil && t.cleanup != nil {
		t.cleanup()
		t.cleanup = nil
	}
}

// noise is never copied, at any depth: build output, dependencies, local state.
var noise = []string{".git", "node_modules", "dist", ".env", "*.local", ".DS_Store", "*.log"}

// projectFiles are a template's own project, for running it alone, and its repo
// setup, at its root. plat5 init writes the project's own instead.
var projectFiles = []string{
	ManifestName, "plat5.yml", "plat5.dev.yml", "services", ".github",
	"data/*.db", "data/*.db-*",
}

// LoadDir loads the template whose root is dir.
func LoadDir(dir string) (*Template, error) {
	m, err := loadManifest(filepath.Join(dir, ManifestName))
	if err != nil {
		return nil, err
	}
	return &Template{Dir: dir, Manifest: m, Origin: dir}, nil
}

// loadManifest decodes plat5.template.yml strictly: an unknown key is an error.
func loadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// Check says whether the template is a kind plat5 init can use as want, with
// the fields that kind needs.
func (t *Template) Check(want string) error {
	m := t.Manifest
	if m.Kind != want {
		return fmt.Errorf("%s: kind is %q, want %s", t.Origin, m.Kind, want)
	}
	var missing []string
	if m.App.Compose == "" {
		missing = append(missing, "app.compose")
	}
	switch want {
	case KindAPI:
		if m.Service == "" {
			missing = append(missing, "service")
		}
	case KindCustomerConsole:
		if m.App.Web == "" {
			missing = append(missing, "app.web")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s: %s needs %s", t.Origin, ManifestName, strings.Join(missing, ", "))
	}
	for _, p := range append([]string{m.App.Compose, m.Service, m.OperatorRoutes}, m.Substitute...) {
		if _, err := t.Path(p); p != "" && err != nil {
			return err
		}
	}
	return nil
}

// Path is a manifest path, inside the template.
func (t *Template) Path(rel string) (string, error) {
	clean := filepath.Clean(rel)
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("%s: %s: %s is not a path inside the template", t.Origin, ManifestName, rel)
	}
	return filepath.Join(t.Dir, clean), nil
}

// ReadFile reads a manifest path.
func (t *Template) ReadFile(rel string) ([]byte, error) {
	path, err := t.Path(rel)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// Copy copies the template into dest, leaving out its project files, its
// operator routes, its exclude list and noise, and then replaces {{project_id}}
// in its substitute files. An existing file in dest is an error.
func (t *Template) Copy(dest, projectID string) error {
	root := append([]string{}, projectFiles...)
	if t.Manifest.OperatorRoutes != "" {
		root = append(root, filepath.Clean(t.Manifest.OperatorRoutes))
	}
	root = append(root, t.Manifest.Exclude...)

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	err := filepath.WalkDir(t.Dir, func(src string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(t.Dir, src)
		if err != nil || rel == "." {
			return err
		}
		if excluded(rel, root) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		dst := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(src, dst)
	})
	if err != nil {
		return err
	}
	return t.substitute(dest, projectID)
}

// excluded is whether rel is noise (by name, at any depth) or matches one of
// root's paths (relative to the template root).
func excluded(rel string, root []string) bool {
	rel = filepath.ToSlash(rel)
	base := filepath.Base(rel)
	for _, p := range noise {
		if ok, _ := filepath.Match(p, base); ok {
			return true
		}
	}
	for _, p := range root {
		if ok, _ := filepath.Match(filepath.ToSlash(filepath.Clean(p)), rel); ok {
			return true
		}
	}
	return false
}

// substitute replaces {{project_id}} in the substitute files under dest.
func (t *Template) substitute(dest, projectID string) error {
	for _, rel := range t.Manifest.Substitute {
		path := filepath.Join(dest, filepath.Clean(rel))
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
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
