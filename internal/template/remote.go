package template

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DefaultRef is the git ref of a starter's GitHub archive.
const DefaultRef = "master"

// ResolveOptions say where templates are loaded from.
type ResolveOptions struct {
	// LocalRoot, if set, is a directory of first-party template checkouts, each
	// in its repo's folder (template-go-fiber-api, template-customer-console).
	LocalRoot string
	// Ref is the git branch, tag, or commit of a GitHub archive. Empty: DefaultRef.
	Ref string
	// HTTPClient, if set, downloads archives (tests).
	HTTPClient *http.Client
	// GitHubBase overrides https://github.com (tests).
	GitHubBase string
}

func (o ResolveOptions) client() *http.Client {
	if o.HTTPClient != nil {
		return o.HTTPClient
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (o ResolveOptions) ref() string {
	if o.Ref != "" {
		return o.Ref
	}
	return DefaultRef
}

func (o ResolveOptions) githubBase() string {
	if o.GitHubBase != "" {
		return strings.TrimRight(o.GitHubBase, "/")
	}
	return "https://github.com"
}

// Resolve loads an API starter by first-party name, owner/repo on GitHub, or a
// .tar.gz URL. Close removes a downloaded one.
func Resolve(opts ResolveOptions, name string) (*Template, error) {
	name = strings.TrimSpace(name)
	if off, ok := lookupOfficial(name); ok {
		return resolveOfficial(opts, off)
	}
	src, err := resolveSource(opts, name)
	if err != nil {
		return nil, err
	}
	return resolveRemote(opts, src)
}

// ResolveCustomerConsole loads the customer console the way Resolve loads a
// first-party starter: from the local templates directory, else GitHub at the
// same ref.
func ResolveCustomerConsole(opts ResolveOptions) (*Template, error) {
	return resolveOfficial(opts, CustomerConsole)
}

// ResolveOperatorConsole loads the operator console as ResolveCustomerConsole
// loads the customer console.
func ResolveOperatorConsole(opts ResolveOptions) (*Template, error) {
	return resolveOfficial(opts, OperatorConsole)
}

func resolveOfficial(opts ResolveOptions, off OfficialTemplate) (*Template, error) {
	if opts.LocalRoot != "" {
		dir := filepath.Join(opts.LocalRoot, off.Folder())
		if _, err := os.Stat(filepath.Join(dir, ManifestName)); err != nil {
			return nil, fmt.Errorf("%s: no %s (the templates directory holds each template in its repo's folder)", dir, ManifestName)
		}
		return LoadDir(dir)
	}
	return resolveRemote(opts, githubArchive(opts, off.Repo()))
}

// resolveRemote downloads src into a temp dir and loads it.
func resolveRemote(opts ResolveOptions, src source) (*Template, error) {
	dir, commit, cleanup, err := fetch(opts, src)
	if err != nil {
		return nil, err
	}
	tpl, err := LoadDir(dir)
	if err != nil {
		cleanup()
		return nil, err
	}
	tpl.Origin = src.display
	if commit != "" {
		tpl.Origin += " (" + commit[:7] + ")"
	}
	tpl.cleanup = cleanup
	return tpl, nil
}

// source is a downloadable template archive.
type source struct {
	// display names it in output and errors: owner/repo@ref, or the URL.
	display string
	// url is the tar.gz to download.
	url string
}

var ownerRepoRE = regexp.MustCompile(`^([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)$`)

func resolveSource(opts ResolveOptions, name string) (source, error) {
	if strings.HasPrefix(name, "https://") || strings.HasPrefix(name, "http://") {
		return source{display: name, url: name}, nil
	}
	repo := strings.TrimPrefix(name, "github.com/")
	if ownerRepoRE.MatchString(repo) {
		return githubArchive(opts, repo), nil
	}
	return source{}, fmt.Errorf("unknown starter %q (first-party: %s, or none; or owner/repo, or a .tar.gz URL)",
		name, strings.Join(OfficialNames(), ", "))
}

// githubArchive is repo's archive at the ref: GitHub serves a branch, a tag,
// or a commit at the same URL.
func githubArchive(opts ResolveOptions, repo string) source {
	ref := opts.ref()
	return source{
		display: repo + "@" + ref,
		url:     fmt.Sprintf("%s/%s/archive/%s.tar.gz", opts.githubBase(), repo, ref),
	}
}

// fetch downloads src and extracts it into a fresh temp dir. Nothing is kept:
// a template is fetched once per project, and a branch moves. It returns the
// template root, the commit the archive was made from (GitHub archives say;
// empty otherwise), and a func that removes the temp dir.
func fetch(opts ResolveOptions, src source) (root, commit string, cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", "plat5-template-*")
	if err != nil {
		return "", "", nil, err
	}
	remove := func() { _ = os.RemoveAll(tmp) }
	defer func() {
		if err != nil {
			remove()
		}
	}()

	resp, err := opts.client().Get(src.url)
	if err != nil {
		return "", "", nil, fmt.Errorf("download %s: %w", src.display, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", nil, fmt.Errorf("download %s: %s: HTTP %d", src.display, src.url, resp.StatusCode)
	}
	if commit, err = extractTarGz(resp.Body, tmp); err != nil {
		return "", "", nil, fmt.Errorf("extract %s: %w", src.display, err)
	}
	root = tmp
	if entries, err := os.ReadDir(tmp); err == nil && len(entries) == 1 && entries[0].IsDir() {
		root = filepath.Join(tmp, entries[0].Name())
	}
	if _, err := os.Stat(filepath.Join(root, ManifestName)); err != nil {
		return "", "", nil, fmt.Errorf("%s: no %s (not a Plat5 template?)", src.display, ManifestName)
	}
	return root, commit, remove, nil
}

// commitRE is a full git commit id.
var commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// extractTarGz extracts r into dest. It returns the commit a git archive
// records in its global header (git archive's "comment"), or empty.
func extractTarGz(r io.Reader, dest string) (commit string, err error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return commit, nil
		}
		if err != nil {
			return "", err
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			if c := hdr.PAXRecords["comment"]; commitRE.MatchString(c) {
				commit = c
			}
			continue
		}
		name := filepath.Clean(hdr.Name)
		if name == "." || strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return "", fmt.Errorf("tar path escapes the archive: %s", hdr.Name)
		}
		target := filepath.Join(dest, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return "", err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, hdr.FileInfo().Mode().Perm()|0o600)
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return "", err
			}
			if err := out.Close(); err != nil {
				return "", err
			}
		}
	}
}
