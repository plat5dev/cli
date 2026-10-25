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
	"github.com/plat5dev/cli/internal/template"
	"gopkg.in/yaml.v3"
)

// What plat5 init writes from an API starter, beside the project files.
const (
	apiDir             = "api"
	customerConsoleDir = template.KindCustomerConsole
	projectCompose     = "docker-compose.yml"
	operatorRoutesFile = "operator.routes.yml"
	projectReadmeFile  = "README.md"
)

// operatorShippedRoutes is operator's shipped route list (operator's routes.yml),
// which operator.routes.yml starts from.
//
//go:embed operatorfiles/routes.yml
var operatorShippedRoutes []byte

// layout is a project laid out from an API starter: the starter in api/, the
// customer console in customer-console/, the starter's service file in
// services/, a docker-compose.yml that includes each app's, and
// operator.routes.yml.
type layout struct {
	starter *template.Template
	// customerConsole is nil with --no-customer-console.
	customerConsole *template.Template
	// service is the starter's service name; serviceFile, its service file.
	service     string
	serviceFile []byte
	// operatorRoutes is operator.routes.yml.
	operatorRoutes []byte
}

// loadLayout fetches the starter and the customer console, and reads what init
// writes from them, before anything is written.
func loadLayout(opts template.ResolveOptions, starter string, customerConsole bool) (_ *layout, err error) {
	l := &layout{}
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
	m := l.starter.Manifest
	if l.serviceFile, err = l.starter.ReadFile(m.Service); err != nil {
		return nil, err
	}
	if l.service, err = serviceName(m.Service, l.serviceFile); err != nil {
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
}

// serviceName is a service file's one top-level key. Identity's and audit's
// files are the project's own.
func serviceName(rel string, data []byte) (string, error) {
	var file map[string]any
	if err := yaml.Unmarshal(data, &file); err != nil {
		return "", fmt.Errorf("the starter's service file %s: %w", rel, err)
	}
	if len(file) != 1 {
		return "", fmt.Errorf("the starter's service file %s: must hold exactly one service, keyed by its name (it holds %d keys)", rel, len(file))
	}
	for name := range file {
		if name == "identity" || name == "audit" {
			return "", fmt.Errorf("the starter's service file %s: %s is Plat5's own service", rel, name)
		}
		return name, nil
	}
	return "", nil
}

// targets is what the layout writes, relative to the project.
func (l *layout) targets() []string {
	out := []string{"plat5.yml", l.serviceRel(), apiDir}
	if l.customerConsole != nil {
		out = append(out, customerConsoleDir)
	}
	return append(out, projectCompose, operatorRoutesFile, projectReadmeFile)
}

func (l *layout) serviceRel() string { return "services/" + l.service + ".yml" }

// web is the customer console's origin, or empty without one.
func (l *layout) web() string {
	if l.customerConsole == nil {
		return ""
	}
	return strings.TrimRight(l.customerConsole.Manifest.App.Web, "/")
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

// write lays out the apps and their files in dir. plat5.yml and identity's and
// audit's service files are writeInitFiles'.
func (l *layout) write(dir, projectID string) error {
	if err := writeFile(dir, l.serviceRel(), l.serviceFile); err != nil {
		return err
	}
	apps := []struct {
		folder, what string
		t            *template.Template
	}{{apiDir, "your API (" + l.starter.Manifest.Name + ")", l.starter}, {customerConsoleDir, "your customers' console", l.customerConsole}}
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

// plat5YML is init's plat5.yml with the starter's service file listed, and the
// customer console's origin allowed at the gateway.
func (l *layout) plat5YML(base []byte) ([]byte, error) {
	head, list, ok := bytes.Cut(base, []byte("\nservices:\n"))
	if !ok {
		return nil, fmt.Errorf("init's plat5.yml has no services: list")
	}
	var b bytes.Buffer
	b.Write(head)
	b.WriteString("\n")
	if web := l.web(); web != "" {
		b.WriteString("# The browser origins the gateway allows (plat5 docs/reference/config/cors.md):\n")
		b.WriteString("# customer-console/'s.\n")
		fmt.Fprintf(&b, "cors:\n  allowed_origins: [%q]\n", web)
	}
	b.WriteString("services:\n")
	b.Write(list)
	fmt.Fprintf(&b, "  - %s\n", l.serviceRel())
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
	return b.String()
}

// operatorRoutes is operator.routes.yml: operator's shipped list, with the
// starter's routes.operator.yml services (starterRoutes, nil for none) added
// under its services:.
func operatorRoutes(shipped []byte, starter string, starterRoutes []byte) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("# The route list for an operator gateway (operator docs/reference/routes.md):\n")
	b.WriteString("# what operators may call, by service, at each service's own paths. plat5 init\n")
	b.WriteString("# wrote operator's shipped list and the starter's operator routes. The project\n")
	b.WriteString("# owns it: plat5 start runs no operator gateway; one you run mounts this file\n")
	b.WriteString("# as its ROUTES_FILE.\n#\n")
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
		"Service":  l.service,
		"Web":      l.web(),
		"Next":     l.starter.Manifest.Next,
		"DevEmail": config.DevEmail,
		"Password": config.DevPassword,
	})
	return b.String(), err
}
