package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/state"
	"github.com/plat5dev/cli/internal/template"
)

// runInitFlags runs plat5 init dir -y with --sources, --project-id and a
// starter, pinned so no tag is looked up, and returns what it printed.
func runInitFlags(t *testing.T, sources, projectID, starter, dir string) (string, error) {
	t.Helper()
	initTemplate, initSourcesDir, initProjectID, initYes = starter, sources, projectID, true
	initVersions = []string{"plat5=v0.9.0", "auth=v0.2.2", "operator=v0.5.0"}
	defer func() {
		initTemplate, initSourcesDir, initProjectID, initYes = noTemplate, "", "", false
		initVersions = nil
	}()
	var err error
	out := captureStdout(t, func() { err = runInit(initCmd, []string{dir}) })
	return out, err
}

// --sources runs every module and template with a checkout there from it, and
// says where each module runs from: here plat5 and operator are checkouts,
// auth is its release image.
func TestInitSources(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	src := fixtureTemplates(t)
	for _, m := range []string{"plat5", "operator"} {
		if err := os.Mkdir(filepath.Join(src, m), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(t.TempDir(), "demo")
	out, err := runInitFlags(t, src, "", "go-fiber-api", dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"api/               your API (go-fiber-api), from " + filepath.Join(src, "template-go-fiber-api") + "\n",
		"customer-console/  your customers' console, from " + filepath.Join(src, "template-customer-console") + "\n",
		"operator-console/  your operators' console, from " + filepath.Join(src, "template-operator-console") + "\n",
		"Plat5 and its modules run from:\n" +
			"  plat5     checkout " + filepath.Join(src, "plat5") + "\n" +
			"  auth      release image v0.2.2, no checkout at " + filepath.Join(src, "auth") + "\n" +
			"  operator  checkout " + filepath.Join(src, "operator") + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	t.Chdir(dir)
	cfg := mustLoadConfig(t)
	if cfg.Source["plat5"] != filepath.Join(src, "plat5") || cfg.Source["operator"] != filepath.Join(src, "operator") || cfg.Source["auth"] != "" {
		t.Errorf("source: %v", cfg.Source)
	}
	if cfg.Versions["auth"] != "v0.2.2" || cfg.Versions["plat5"] != "" {
		t.Errorf("versions: %v", cfg.Versions)
	}
}

// Without --sources every module is its release image, and init says which.
func TestInitWithoutSourcesPrintsReleases(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	out, err := runInitFlags(t, "", "", noTemplate, filepath.Join(t.TempDir(), "alone"))
	if err != nil {
		t.Fatal(err)
	}
	want := "Plat5 and its modules run from:\n" +
		"  plat5     release image v0.9.0\n" +
		"  auth      release image v0.2.2\n" +
		"  operator  release image v0.5.0\n"
	if !strings.Contains(out, want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
}

// A template with no checkout in --sources is downloaded at --template-ref, and
// its line says there was no checkout.
func TestInitSourcesMissingCheckout(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	src := fixtureTemplates(t)
	if err := os.RemoveAll(filepath.Join(src, "template-operator-console")); err != nil {
		t.Fatal(err)
	}
	srv := consoleArchive(t, "/plat5dev/template-operator-console/archive/master.tar.gz", "template-operator-console-master", map[string]string{
		template.ManifestName: "name: operator-console\nkind: operator-console\napp:\n  compose: docker-compose.yml\n  web: http://localhost:5412\n",
		"docker-compose.yml":  "services:\n  operator-console: {}\n",
	})
	initFetch = template.ResolveOptions{GitHubBase: srv.URL, HTTPClient: srv.Client()}
	defer func() { initFetch = template.ResolveOptions{} }()

	dir := filepath.Join(t.TempDir(), "demo")
	out, err := runInitFlags(t, src, "", "go-fiber-api", dir)
	if err != nil {
		t.Fatal(err)
	}
	want := "operator-console/  your operators' console, from plat5dev/template-operator-console@master (0123456), no checkout at " +
		filepath.Join(src, "template-operator-console") + "\n"
	if !strings.Contains(out, want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
	if _, err := os.Stat(filepath.Join(dir, operatorConsoleDir, "docker-compose.yml")); err != nil {
		t.Errorf("the downloaded console: %v", err)
	}
	for _, m := range config.Sources {
		if !strings.Contains(out, "no checkout at "+filepath.Join(src, m)+"\n") {
			t.Errorf("%s: no notice:\n%s", m, out)
		}
	}
}

// --sources must name a directory.
func TestInitSourcesNotADirectory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	missing := filepath.Join(t.TempDir(), "nope")
	_, err := runInitFlags(t, missing, "", noTemplate, filepath.Join(t.TempDir(), "x"))
	if err == nil || !strings.Contains(err.Error(), "--sources "+missing+": no directory at") {
		t.Fatalf("want a --sources error, got %v", err)
	}
}

// --project-id is written to plat5.dev.yml, whatever the folder's name, and is
// the id every command reads; the starter's {{project_id}} is it too. An id that
// isn't one as plat5 makes them is refused, naming the one it would be.
func TestInitProjectID(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "Some Folder")
	if _, err := runInitFlags(t, fixtureTemplates(t), "scratch-1", "go-fiber-api", dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, config.FileName)); !strings.Contains(string(b), "\nproject_id: scratch-1\n") {
		t.Errorf("plat5.dev.yml:\n%s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "api/go.mod")); string(b) != "module scratch-1\n" {
		t.Errorf("api/go.mod: %s", b)
	}
	t.Chdir(dir)
	if cfg := mustLoadConfig(t); cfg.ProjectID != "scratch-1" || cfg.ComposeProject != "plat5-scratch-1" {
		t.Errorf("loaded %q %q", cfg.ProjectID, cfg.ComposeProject)
	}

	for id, want := range map[string]string{
		"Scratch One": `--project-id "Scratch One": a project id is lowercase letters, digits, - and _, at most 64 (--project-id scratch-one)`,
		"!!!":         `--project-id "!!!": a project id is letters, digits, - and _`,
	} {
		dir := filepath.Join(t.TempDir(), "x")
		if _, err := runInitFlags(t, "", id, noTemplate, dir); err == nil || err.Error() != want {
			t.Errorf("%q: got %v, want %s", id, err, want)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%q: init made %s", id, dir)
		}
	}
}

// The ownership guard keys on --project-id, not the folder's name: init refuses
// an id another folder owns, takes a fresh one, and then the commands from that
// folder are guarded by plat5.dev.yml's id.
func TestInitProjectIDOwnerGuard(t *testing.T) {
	a, b := ownerProjects(t)
	if err := state.Save(state.State{ProjectID: "scratch", Owner: a}); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(filepath.Dir(filepath.Dir(b)), "c", "work")
	_, err := runInitFlags(t, "", "scratch", noTemplate, fresh)
	want := `project id "scratch" belongs to the project in ` + a + `, not this one in ` + fresh +
		`: the stack (compose project plat5-scratch, its volumes) and the state are that project's.
Give this project its own id:

  plat5 init --project-id c-work`
	if err == nil || err.Error() != want {
		t.Fatalf("init:\n got %v\nwant %s", err, want)
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Errorf("init made %s: %v", fresh, err)
	}

	// The folder name "work" is a's too, but the id is what's owned.
	if err := state.Save(state.State{ProjectID: "work", Owner: a}); err != nil {
		t.Fatal(err)
	}
	if _, err := runInitFlags(t, "", "scratch-c", noTemplate, fresh); err != nil {
		t.Fatal(err)
	}
	t.Chdir(fresh)
	if _, err := loadOwned(mustLoadConfig(t)); err != nil {
		t.Errorf("its own id: %v", err)
	}
	if err := state.Save(state.State{ProjectID: "scratch-c", Owner: a}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOwned(mustLoadConfig(t)); err == nil || !strings.Contains(err.Error(), `project id "scratch-c" belongs to the project in `+a) {
		t.Errorf("an id another folder owns: %v", err)
	}
}

// consoleArchive serves one git-archive-style tar.gz at path, its files under
// top, with a commit id GitHub-style.
func consoleArchive(t *testing.T, path, top string, files map[string]string) *httptest.Server {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: map[string]string{"comment": "0123456789abcdef0123456789abcdef01234567"}}); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: top + "/" + name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(buf.Bytes()) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}
