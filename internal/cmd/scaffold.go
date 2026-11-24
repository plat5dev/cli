package cmd

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	texttemplate "text/template"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/plat5yml"
	"github.com/plat5dev/cli/internal/template"
	"gopkg.in/yaml.v3"
)

// What plat5 init writes from an API starter, beside the project files.
const (
	apiDir             = "api"
	customerConsoleDir = template.KindCustomerConsole
	operatorConsoleDir = template.KindOperatorConsole
	projectCompose     = "docker-compose.yml"
	operatorRoutesFile = "operator.routes.yml"
	projectReadmeFile  = "README.md"
)

// operatorShippedRoutes is operator's shipped route list (operator's routes.yml),
// which operator.routes.yml starts from.
//
//go:embed operatorfiles/routes.yml
var operatorShippedRoutes []byte

// layout is a project laid out from an API starter: the starter's plat5.yml and
// service files at the root, the starter in api/, the customer console in
// customer-console/, the operator console in operator-console/, a
// docker-compose.yml that includes each app's, and operator.routes.yml.
type layout struct {
	starter *template.Template
	// customerConsole is nil with --no-customer-console.
	customerConsole *template.Template
	// operatorConsole is nil with --no-operator-console.
	operatorConsole *template.Template
	// config is the starter's plat5.yml, with the customer console's origin.
	config []byte
	// serviceFiles is the service files it lists, by path from the project.
	serviceFiles []projectFile
	// operatorRoutes is operator.routes.yml.
	operatorRoutes []byte
	// authorizer is the opt-in local AuthZEN PDP ("topaz"), empty for none.
	authorizer string
}

// loadLayout fetches the starter and the consoles, and reads what init writes
// from them, before anything is written.
func loadLayout(opts template.ResolveOptions, starter string, customerConsole, operatorConsole bool, authorizer string) (_ *layout, err error) {
	l := &layout{authorizer: authorizer}
	defer func() {
		if err != nil {
			l.close()
		}
	}()
	if l.starter, err = template.Resolve(opts, starter); err != nil {
		return nil, err
	}
	if err := l.starter.Check(template.KindAPI); err != nil {
		return nil, err
	}
	if customerConsole {
		if l.customerConsole, err = template.ResolveCustomerConsole(opts); err != nil {
			return nil, err
		}
		if err := l.customerConsole.Check(template.KindCustomerConsole); err != nil {
			return nil, err
		}
	}
	if operatorConsole {
		if l.operatorConsole, err = template.ResolveOperatorConsole(opts); err != nil {
			return nil, err
		}
		if err := l.operatorConsole.Check(template.KindOperatorConsole); err != nil {
			return nil, err
		}
	}
	m := l.starter.Manifest
	if err := l.readConfig(); err != nil {
		return nil, err
	}
	var starterRoutes []byte
	if m.OperatorRoutes != "" {
		if starterRoutes, err = l.starter.ReadFile(m.OperatorRoutes); err != nil {
			return nil, err
		}
	}
	if l.operatorRoutes, err = operatorRoutes(operatorShippedRoutes, m.Name, starterRoutes); err != nil {
		return nil, err
	}
	return l, nil
}

// close removes the downloaded templates' temp dirs.
func (l *layout) close() {
	l.starter.Close()
	l.customerConsole.Close()
	l.operatorConsole.Close()
}

// projectFile is a file init writes at a path in the project.
type projectFile struct {
	rel  string
	data []byte
}

// readConfig reads the starter's plat5.yml and the service files it lists. The
// starter owns them: init adds the customer console's origin under cors, so a
// plat5.yml that sets cors itself is refused.
func (l *layout) readConfig() error {
	m := l.starter.Manifest
	base, err := l.starter.ReadFile(m.Config)
	if err != nil {
		return err
	}
	var top map[string]any
	if err := yaml.Unmarshal(base, &top); err != nil {
		return fmt.Errorf("the starter's %s: %w", m.Config, err)
	}
	if _, ok := top["cors"]; ok {
		return fmt.Errorf("the starter's %s sets cors: init adds the customer console's origin itself", m.Config)
	}
	files, err := plat5yml.ServiceFiles(top, "the starter's "+m.Config)
	if err != nil {
		return err
	}
	dir := filepath.Dir(filepath.Clean(m.Config))
	for _, f := range files {
		rel := filepath.Clean(f)
		if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("the starter's %s: service file %s is not inside the project", m.Config, f)
		}
		data, err := l.starter.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			return fmt.Errorf("the starter's %s: %w", m.Config, err)
		}
		l.serviceFiles = append(l.serviceFiles, projectFile{filepath.ToSlash(rel), data})
	}
	l.config, err = l.withCORS(base)
	if err != nil {
		return err
	}
	if l.authorizer == authorizerTopaz {
		l.config, err = injectTopazAuthorizer(l.config)
		if err != nil {
			return fmt.Errorf("the starter's %s: %w", m.Config, err)
		}
	}
	return nil
}

// targets is what the layout writes, relative to the project.
func (l *layout) targets() []string {
	out := []string{"plat5.yml"}
	for _, f := range l.serviceFiles {
		out = append(out, f.rel)
	}
	out = append(out, apiDir)
	if l.customerConsole != nil {
		out = append(out, customerConsoleDir)
	}
	if l.operatorConsole != nil {
		out = append(out, operatorConsoleDir)
	}
	if l.authorizer == authorizerTopaz {
		out = append(out, topazManifestFile)
	}
	return append(out, projectCompose, operatorRoutesFile, projectReadmeFile)
}

// web is the customer console's origin, or empty without one.
func (l *layout) web() string {
	if l.customerConsole == nil {
		return ""
	}
	return strings.TrimRight(l.customerConsole.Manifest.App.Web, "/")
}

// operatorWeb is the operator console's origin, or empty without one.
func (l *layout) operatorWeb() string {
	if l.operatorConsole == nil {
		return ""
	}
	return strings.TrimRight(l.operatorConsole.Manifest.App.Web, "/")
}

// operatorConsoleCompose is the operator console's compose file, relative to
// the project, or empty without one.
func (l *layout) operatorConsoleCompose() string {
	if l.operatorConsole == nil {
		return ""
	}
	return operatorConsoleDir + "/" + filepath.ToSlash(filepath.Clean(l.operatorConsole.Manifest.App.Compose))
}

// checkFree refuses a project that has any of the layout's files: a starter
// writes them all.
func (l *layout) checkFree(dir string) error {
	for _, rel := range l.targets() {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			return fmt.Errorf("%s already exists: a starter writes it (init into a new directory, or --template none)", filepath.Join(dir, rel))
		}
	}
	return nil
}

// write lays out the starter's config, the apps and their files in dir.
func (l *layout) write(dir, projectID string) error {
	if err := writeFile(dir, "plat5.yml", l.config); err != nil {
		return err
	}
	for _, f := range l.serviceFiles {
		if err := writeFile(dir, f.rel, f.data); err != nil {
			return err
		}
	}
	apps := []struct {
		folder, what string
		t            *template.Template
	}{
		{apiDir, "your API (" + l.starter.Manifest.Name + ")", l.starter},
		{customerConsoleDir, "your customers' console", l.customerConsole},
		{operatorConsoleDir, "your operators' console", l.operatorConsole},
	}
	for _, app := range apps {
		if app.t == nil {
			continue
		}
		if err := app.t.Copy(filepath.Join(dir, app.folder), projectID); err != nil {
			return err
		}
		fmt.Printf("  %-18s %s, from %s\n", app.folder+"/", app.what, app.t.Origin)
	}
	if err := writeFile(dir, projectCompose, []byte(l.compose())); err != nil {
		return err
	}
	if err := writeFile(dir, operatorRoutesFile, l.operatorRoutes); err != nil {
		return err
	}
	if l.authorizer == authorizerTopaz {
		if err := writeFile(dir, topazManifestFile, []byte(topazStarterManifest)); err != nil {
			return err
		}
	}
	readme, err := l.readme(projectID)
	if err != nil {
		return err
	}
	return writeFile(dir, projectReadmeFile, []byte(readme))
}

func writeFile(dir, rel string, data []byte) error {
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	fmt.Println("  wrote", rel)
	return nil
}

// withCORS is the starter's plat5.yml with the customer console's origin
// allowed at the gateway, before its services list. Without the console, it's
// the starter's as it is.
func (l *layout) withCORS(base []byte) ([]byte, error) {
	web := l.web()
	if web == "" {
		return base, nil
	}
	head, list, ok := bytes.Cut(append([]byte("\n"), base...), []byte("\nservices:"))
	if !ok {
		return nil, fmt.Errorf("the starter's %s has no services: list", l.starter.Manifest.Config)
	}
	var b bytes.Buffer
	b.Write(head[1:])
	b.WriteString("\n# The browser origins the gateway allows (https://github.com/plat5dev/plat5/blob/master/docs/reference/config/cors.md):\n")
	b.WriteString("# customer-console/'s, added by plat5 init.\n")
	fmt.Fprintf(&b, "cors:\n  allowed_origins: [%q]\n", web)
	b.WriteString("services:")
	b.Write(list)
	return b.Bytes(), nil
}

// compose is the project's docker-compose.yml: it includes each app's own.
func (l *layout) compose() string {
	var b strings.Builder
	b.WriteString("# Your apps. plat5 start runs this file after Plat5, and passes each app\n")
	b.WriteString("# where Plat5 is. Each app's own compose file is in its folder; to drop an\n")
	b.WriteString("# app, delete its folder and its line here.\n")
	b.WriteString("include:\n")
	fmt.Fprintf(&b, "  - %s/%s\n", apiDir, filepath.ToSlash(filepath.Clean(l.starter.Manifest.App.Compose)))
	if l.customerConsole != nil {
		fmt.Fprintf(&b, "  - %s/%s\n", customerConsoleDir, filepath.ToSlash(filepath.Clean(l.customerConsole.Manifest.App.Compose)))
	}
	if c := l.operatorConsoleCompose(); c != "" {
		b.WriteString("  # Runs with the operator module (operator.console in plat5.local.yml).\n")
		fmt.Fprintf(&b, "  - %s\n", c)
	}
	return b.String()
}

// operatorRoutes is operator.routes.yml: operator's shipped list, with the
// starter's routes.operator.yml services (starterRoutes, nil for none) added
// under its services:.
func operatorRoutes(shipped []byte, starter string, starterRoutes []byte) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("# The operator gateway's route list (https://github.com/plat5dev/operator/blob/master/docs/reference/routes.md): what\n")
	b.WriteString("# operators may call, by service, at each service's own paths. plat5 init wrote\n")
	if starterRoutes == nil {
		b.WriteString("# operator's shipped list. The project owns it: plat5 start's operator gateway\n")
		b.WriteString("# mounts it as its ROUTES_FILE (operator.routes in plat5.local.yml); a deployed\n")
		b.WriteString("# one mounts a copy with the deployment's addresses.\n#\n")
	} else {
		b.WriteString("# operator's shipped list and the starter's operator routes. The project owns\n")
		b.WriteString("# it: plat5 start's operator gateway mounts it as its ROUTES_FILE (operator.routes\n")
		b.WriteString("# in plat5.local.yml); a deployed one mounts a copy with the deployment's addresses.\n#\n")
	}
	b.Write(shipped)
	if starterRoutes == nil {
		return b.Bytes(), nil
	}

	var doc map[string]map[string]any
	if err := yaml.Unmarshal(starterRoutes, &doc); err != nil {
		return nil, fmt.Errorf("the starter's operator routes: %w", err)
	}
	if len(doc) != 1 || len(doc["services"]) == 0 {
		return nil, fmt.Errorf("the starter's operator routes: want a services: block alone")
	}
	_, services, ok := bytes.Cut(append([]byte("\n"), starterRoutes...), []byte("\nservices:\n"))
	if !ok {
		return nil, fmt.Errorf("the starter's operator routes: want a line services: at the top level")
	}
	fmt.Fprintf(&b, "  # %s's API, from its routes.operator.yml.\n", starter)
	b.Write(services)

	var merged struct {
		Services map[string]any `yaml:"services"`
	}
	var own map[string]map[string]any
	if err := yaml.Unmarshal(shipped, &own); err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b.Bytes(), &merged); err != nil {
		return nil, fmt.Errorf("the starter's operator routes don't fit operator's list: %w", err)
	}
	if len(merged.Services) != len(own["services"])+len(doc["services"]) {
		return nil, fmt.Errorf("the starter's operator routes don't fit operator's list: a service is named twice, or services: isn't its last key")
	}
	return b.Bytes(), nil
}

//go:embed project_readme.md.tmpl
var projectReadmeTmpl string

func (l *layout) readme(projectID string) (string, error) {
	t, err := texttemplate.New("readme").Parse(projectReadmeTmpl)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	err = t.Execute(&b, map[string]any{
		"Project":  projectID,
		"Starter":  l.starter.Manifest.Name,
		"Web":      l.web(),
		"Operator": l.operatorWeb(),
		"OpEmail":  config.OperatorEmail,
		"Next":     l.starter.Manifest.Next,
		"DevEmail": config.DevEmail,
		"Password": config.DevPassword,
	})
	return b.String(), err
}
