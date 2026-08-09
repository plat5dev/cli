package template

import (
	"archive/tar"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListAvailableOfficial(t *testing.T) {
	list, err := ListAvailable(ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != len(Official) {
		t.Fatalf("got %d want %d", len(list), len(Official))
	}
	if list[0].Name != "bun-effect-api" {
		t.Fatalf("first %#v", list[0])
	}
}

func TestResolveSourceOfficialAndRepo(t *testing.T) {
	opts := ResolveOptions{Ref: "master", GitHubBase: "https://github.com"}
	src, err := resolveSource(opts, "bun-effect-api")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src.url, "plat5dev/template-bun-effect-api/archive/refs/heads/master.tar.gz") {
		t.Fatalf("url %s", src.url)
	}

	src, err = resolveSource(opts, "acme/my-template")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src.url, "acme/my-template/archive/refs/heads/master.tar.gz") {
		t.Fatalf("url %s", src.url)
	}

	src, err = resolveSource(opts, "github.com/acme/my-template")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src.url, "acme/my-template/archive/") {
		t.Fatalf("url %s", src.url)
	}

	_, err = resolveSource(opts, "nope")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveRemoteFromGitHubArchive(t *testing.T) {
	tplDir := t.TempDir()
	inner := filepath.Join(tplDir, "template-demo-tpl-master")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name: demo-tpl\ndescription: test\nupstreams:\n  api: \"3000\"\nroutes:\n  - ./routes.yml\n"
	if err := os.WriteFile(filepath.Join(inner, ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, "routes.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tarPath := filepath.Join(t.TempDir(), "t.tar.gz")
	if err := writeTarGz(tarPath, "template-demo-tpl-master", inner); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(tarPath)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/acme/demo-tpl/archive/refs/heads/master.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(raw)
	})

	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)

	opts := ResolveOptions{
		Ref:        "master",
		GitHubBase: srv.URL,
		HTTPClient: srv.Client(),
	}

	tpl, err := Resolve(opts, "acme/demo-tpl")
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Manifest.Name != "demo-tpl" {
		t.Fatalf("manifest %#v", tpl.Manifest)
	}
	tpl2, err := Resolve(opts, "acme/demo-tpl")
	if err != nil {
		t.Fatal(err)
	}
	if tpl2.Dir != tpl.Dir {
		t.Fatalf("cache dir changed %s vs %s", tpl.Dir, tpl2.Dir)
	}
}

func TestLooksLikeTag(t *testing.T) {
	if looksLikeTag("master") || looksLikeTag("main") {
		t.Fatal("branch")
	}
	if !looksLikeTag("v0.1.1") || !looksLikeTag("1.2.3") {
		t.Fatal("tag")
	}
}

func writeTarGz(dest, topName, srcDir string) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		name := filepath.Join(topName, rel)
		if info.IsDir() {
			hdr, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			hdr.Name = name + "/"
			return tw.WriteHeader(hdr)
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = name
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = tw.Write(b)
		return err
	})
}
