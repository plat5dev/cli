package cmd

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	texttemplate "text/template"
	"time"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/template"
)

// Project folders plat5 init writes beside the project files.
const (
	apiDir     = "api"
	webDir     = "web"
	consoleDir = "console"

	operatorRoutesFile = "operator.routes.yml"
)

// layout is what plat5 init put in the project, for plat5.yml and the README.
type layout struct {
	// starter is the API starter. Nil: none (--template none).
	starter *template.Template
	// api is the folder the starter is in: "api", or "" for a starter copied to
	// the project root (one from before the api/ web/ console/ layout).
	api string
	// web and console are the apps beside it. Nil: not added.
	web, console *template.Template
	// operatorRoutes is whether operator.routes.yml was written.
	operatorRoutes bool
}

// scaffold lays out a project from an API starter: the starter in api/, the
// customer web app in web/, and the staff console in console/, each a starting
// point the project owns. The project files (route files, roles.yml) move up to
// the root, and a root docker-compose.yml includes each app's own.
func scaffold(dir, projectID string, ch initChoices, versions map[string]string, opts template.ResolveOptions, starter *template.Template) (layout, error) {
	lay := layout{starter: starter, api: apiDir}

	starter.Manifest.Exclude = append(starter.Manifest.Exclude, ".github")
	if err := copyApp(starter, filepath.Join(dir, apiDir), projectID); err != nil {
		return lay, err
	}
	for _, f := range projectFiles(starter.Manifest) {
		from := filepath.Join(dir, apiDir, f)
		if _, err := os.Stat(from); err != nil {
			continue
		}
		to := filepath.Join(dir, f)
		if _, err := os.Stat(to); err == nil {
			return lay, fmt.Errorf("refusing to overwrite existing file: %s", to)
		}
		if err := os.Rename(from, to); err != nil {
			return lay, err
		}
	}
	fmt.Printf("  %-9s your API (%s), from %s\n", apiDir+"/", starter.Manifest.Name, starter.Origin)

	add := func(kind, folder, what string) (*template.Template, error) {
		app, err := template.ResolveApp(opts, kind)
		if err != nil {
			return nil, err
		}
		app.Manifest.Exclude = append(app.Manifest.Exclude, ".github")
		if err := copyApp(app, filepath.Join(dir, folder), projectID); err != nil {
			app.Close()
			return nil, err
		}
		fmt.Printf("  %-9s %s, from %s\n", folder+"/", what, app.Origin)
		return app, nil
	}
	var err error
	if !ch.noWeb {
		if lay.web, err = add(template.KindWeb, webDir, "your customers' web app"); err != nil {
			return lay, err
		}
	}
	if ch.modules[config.ModuleOperator] && !ch.noConsole {
		if lay.console, err = add(template.KindConsole, consoleDir, "your staff console"); err != nil {
			return lay, err
		}
	}

	if err := writeNew(dir, "docker-compose.yml", rootCompose(lay)); err != nil {
		return lay, err
	}
	if ch.modules[config.ModuleOperator] {
		body, err := operatorRoutes(ch.sources[config.VersionOperator], versions[config.VersionOperator])
		if err != nil {
			fmt.Printf("  note: no %s (%v); the operator gateway uses its image's list\n", operatorRoutesFile, err)
		} else {
			if err := writeNew(dir, operatorRoutesFile, body); err != nil {
				return lay, err
			}
			lay.operatorRoutes = true
		}
	}
	readme, err := projectReadme(projectID, lay, ch)
	if err != nil {
		return lay, err
	}
	if err := writeNew(dir, "README.md", readme); err != nil {
		return lay, err
	}
	return lay, nil
}

// close removes the downloaded templates' temp dirs.
func (l layout) close() {
	for _, t := range []*template.Template{l.starter, l.web, l.console} {
		if t != nil {
			t.Close()
		}
	}
}

func copyApp(t *template.Template, dest, projectID string) error {
	if err := t.Copy(dest); err != nil {
		return err
	}
	return t.Substitute(dest, projectID)
}

// projectFiles are a starter's files that belong to the whole project: its route
// files and roles.yml. They move to the project root.
func projectFiles(m template.Manifest) []string {
	out := []string{"roles.yml"}
	for _, r := range m.Routes {
		out = append(out, filepath.Clean(strings.TrimPrefix(r, "./")))
	}
	return out
}

// rootCompose includes each app's compose file. Removing an app is deleting its
// folder and its line here.
func rootCompose(l layout) string {
	var b strings.Builder
	b.WriteString("# Your apps. plat5 start runs this file after Plat5, and passes each app\n")
	b.WriteString("# where Plat5 is. Each app's own compose file is in its folder; to drop an\n")
	b.WriteString("# app, delete its folder and its line here.\n")
	b.WriteString("include:\n")
	for _, app := range []struct {
		folder string
		t      *template.Template
	}{{apiDir, l.starter}, {webDir, l.web}, {consoleDir, l.console}} {
		if app.t == nil {
			continue
		}
		compose := app.t.Manifest.App.Compose
		if compose == "" {
			compose = "docker-compose.yml"
		}
		fmt.Fprintf(&b, "  - %s/%s\n", app.folder, strings.TrimPrefix(compose, "./"))
	}
	return b.String()
}

// operatorRouteRef is where the operator's route list is read at a version.
var operatorRouteRef = "https://raw.githubusercontent.com/plat5dev/operator/%s/routes.yml"

// operatorRoutes is the operator gateway's route list, as the pinned operator
// version ships it (or the source: checkout's), with a commented place for the
// project's API.
func operatorRoutes(src, version string) ([]byte, error) {
	var data []byte
	var from string
	if src != "" {
		from = filepath.Join(src, "routes.yml")
		b, err := os.ReadFile(from)
		if err != nil {
			return nil, err
		}
		data = b
	} else {
		if version == "" {
			return nil, fmt.Errorf("no operator version")
		}
		from = fmt.Sprintf(operatorRouteRef, version)
		b, err := httpGet(from)
		if err != nil {
			return nil, err
		}
		data = b
	}
	var b bytes.Buffer
	b.WriteString("# What staff may call through the operator gateway, by service. plat5 init\n")
	b.WriteString("# copied it from the operator's own list (" + from + ");\n")
	b.WriteString("# it is the project's now. plat5 start binds each upstream from plat5.yml and\n")
	b.WriteString("# restarts the operator gateway when this file changes.\n#\n")
	b.Write(data)
	if !bytes.HasSuffix(data, []byte("\n")) {
		b.WriteString("\n")
	}
	b.WriteString(`
  # Your API, for staff. plat5.yml's upstreams.api says where it is. Staff paths
  # are your API's own (the listen paths), with the org or person in the path:
  # the operator gateway forwards them unchanged.
  # api:
  #   routes:
  #     - path: /organizations/{organization_id}/projects
  #       methods: [GET]
`)
	return b.Bytes(), nil
}

func httpGet(url string) ([]byte, error) {
	c := &http.Client{Timeout: 15 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// writeNew writes a file the project doesn't have yet.
func writeNew(dir, name string, body any) error {
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("refusing to overwrite existing file: %s", path)
	}
	var data []byte
	switch v := body.(type) {
	case string:
		data = []byte(v)
	case []byte:
		data = v
	}
	return os.WriteFile(path, data, 0o644)
}

//go:embed project_readme.md.tmpl
var projectReadmeTmpl string

func projectReadme(projectID string, l layout, ch initChoices) (string, error) {
	t, err := texttemplate.New("readme").Parse(projectReadmeTmpl)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	err = t.Execute(&b, map[string]any{
		"Project":        projectID,
		"Starter":        l.starter.Manifest.Name,
		"Web":            l.web != nil,
		"Console":        l.console != nil,
		"Auth":           ch.modules[config.ModuleAuth],
		"Operator":       ch.modules[config.ModuleOperator],
		"OperatorRoutes": l.operatorRoutes,
		"DevEmail":       config.DevEmail,
		"StaffEmail":     config.StaffEmail,
		"Password":       config.DevPassword,
	})
	return b.String(), err
}
