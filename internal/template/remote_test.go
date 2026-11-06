package template

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
)

func TestResolveSource(t *testing.T) {
	opts := ResolveOptions{Ref: "v1.2.0"}
	for name, want := range map[string]string{
		"acme/my-api":               "https://github.com/acme/my-api/archive/v1.2.0.tar.gz",
		"github.com/acme/my-api":    "https://github.com/acme/my-api/archive/v1.2.0.tar.gz",
		"https://x.test/my-api.tgz": "https://x.test/my-api.tgz",
		"nope":                      "",
		"acme/my-api/sub":           "",
	} {
		src, err := resolveSource(opts, name)
		if want == "" {
			if err == nil || !strings.Contains(err.Error(), "go-fiber-api") {
				t.Errorf("%s: want an error naming the first-party starters, got %v", name, err)
			}
			continue
		}
		if err != nil || src.url != want {
			t.Errorf("%s: %q %v, want %q", name, src.url, err, want)
		}
	}
	if src := githubArchive(ResolveOptions{}, "plat5dev/template-go-fiber-api"); src.display != "plat5dev/template-go-fiber-api@master" {
		t.Errorf("default ref: %s", src.display)
	}
}

// A first-party name is the repo plat5dev/template-<name>, or its checkout of
// that name in the sources directory.
func TestResolveOfficial(t *testing.T) {
	root := t.TempDir()
	writeStarter(t, filepath.Join(root, "template-go-fiber-api"))
	tpl, err := Resolve(ResolveOptions{LocalRoot: root}, "go-fiber-api")
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Dir != filepath.Join(root, "template-go-fiber-api") || tpl.Manifest.Config != "plat5.yml" {
		t.Fatalf("%+v", tpl)
	}
	if err := os.Mkdir(filepath.Join(root, "template-bun-effect-api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(ResolveOptions{LocalRoot: root}, "bun-effect-api"); err == nil || !strings.Contains(err.Error(), "template-bun-effect-api: a checkout with no "+ManifestName) {
		t.Fatalf("a checkout without a manifest names its folder: %v", err)
	}

	srv := archiveServer(t, map[string]string{"/plat5dev/template-customer-console/archive/dev.tar.gz": "template-customer-console-dev"},
		map[string]string{ManifestName: "kind: customer-console\napp: {compose: docker-compose.yml, web: \"http://localhost:3001\"}\n"})
	console, err := ResolveCustomerConsole(ResolveOptions{Ref: "dev", GitHubBase: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer console.Close()
	if console.Origin != "plat5dev/template-customer-console@dev (0123456)" || console.Manifest.App.Web != "http://localhost:3001" {
		t.Fatalf("%+v", console)
	}

	// A template with no checkout in the sources directory is downloaded, and its
	// origin says so.
	srv = archiveServer(t, map[string]string{"/plat5dev/template-node-fastify-api/archive/master.tar.gz": "template-node-fastify-api-master"},
		map[string]string{ManifestName: apiManifest})
	fallback, err := Resolve(ResolveOptions{LocalRoot: root, GitHubBase: srv.URL, HTTPClient: srv.Client()}, "node-fastify-api")
	if err != nil {
		t.Fatal(err)
	}
	defer fallback.Close()
	if want := "plat5dev/template-node-fastify-api@master (0123456), no checkout at " + filepath.Join(root, "template-node-fastify-api"); fallback.Origin != want {
		t.Fatalf("origin %q, want %q", fallback.Origin, want)
	}

	srv = archiveServer(t, map[string]string{"/plat5dev/template-operator-console/archive/dev.tar.gz": "template-operator-console-dev"},
		map[string]string{ManifestName: "kind: operator-console\napp: {compose: docker-compose.yml, web: \"http://localhost:5412\"}\n"})
	ops, err := ResolveOperatorConsole(ResolveOptions{Ref: "dev", GitHubBase: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer ops.Close()
	if ops.Origin != "plat5dev/template-operator-console@dev (0123456)" || ops.Manifest.App.Web != "http://localhost:5412" {
		t.Fatalf("%+v", ops)
	}
}

// A downloaded template is extracted to its own temp dir each time, and Close
// removes it.
func TestResolveFromArchive(t *testing.T) {
	srv := archiveServer(t, map[string]string{"/acme/demo/archive/master.tar.gz": "demo-master"}, map[string]string{
		ManifestName:  apiManifest,
		"src/main.ts": "export {}\n",
	})
	opts := ResolveOptions{GitHubBase: srv.URL, HTTPClient: srv.Client()}
	tpl, err := Resolve(opts, "acme/demo")
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Manifest.Name != "demo-api" || tpl.Origin != "acme/demo@master (0123456)" {
		t.Fatalf("%+v", tpl)
	}
	if _, err := os.Stat(filepath.Join(tpl.Dir, "src", "main.ts")); err != nil {
		t.Fatal(err)
	}
	again, err := Resolve(opts, "acme/demo")
	if err != nil {
		t.Fatal(err)
	}
	if again.Dir == tpl.Dir {
		t.Fatalf("second resolve reused %s", tpl.Dir)
	}
	tpl.Close()
	again.Close()
	if _, err := os.Stat(tpl.Dir); !os.IsNotExist(err) {
		t.Fatalf("Close left %s", tpl.Dir)
	}

	if _, err := Resolve(opts, "acme/missing"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("a missing archive: %v", err)
	}
}

// archiveServer serves, at each path, a git-archive-style tar.gz of files under
// its top folder.
func archiveServer(t *testing.T, paths map[string]string, files map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, top := range paths {
		body := tarGz(t, top, files)
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) })
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func tarGz(t *testing.T, top string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// git archive records the commit in a global header, as GitHub's archives do.
	headers := []*tar.Header{
		{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: map[string]string{"comment": "0123456789abcdef0123456789abcdef01234567"}},
		{Typeflag: tar.TypeDir, Name: top + "/", Mode: 0o755},
	}
	for _, h := range headers {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: top + "/" + name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
