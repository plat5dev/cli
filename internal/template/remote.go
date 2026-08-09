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

	"github.com/plat5dev/cli/internal/xdg"
)

// DefaultTemplateRef is the git ref used for GitHub archive fetches.
const DefaultTemplateRef = "master"

// ResolveOptions control where templates are loaded from.
type ResolveOptions struct {
	// LocalRoot, if set, is a directory of template folders (path mode / --templates-dir).
	LocalRoot string
	// Ref is the git branch or tag for GitHub archives (default master).
	// Override with PLAT5_TEMPLATE_REF.
	Ref string
	// HTTPClient optional; defaults to a short-timeout client.
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
	if v := strings.TrimSpace(os.Getenv("PLAT5_TEMPLATE_REF")); v != "" {
		return v
	}
	return DefaultTemplateRef
}

func (o ResolveOptions) githubBase() string {
	if o.GitHubBase != "" {
		return strings.TrimRight(o.GitHubBase, "/")
	}
	return "https://github.com"
}

// ListAvailable returns local templates or the official first-party list.
func ListAvailable(opts ResolveOptions) ([]Summary, error) {
	if opts.LocalRoot != "" {
		return List(opts.LocalRoot)
	}
	out := make([]Summary, 0, len(Official))
	for _, e := range Official {
		out = append(out, Summary{Name: e.Name, Description: e.Description})
	}
	return out, nil
}

// Resolve loads a template by short name, owner/repo, or archive/URL.
func Resolve(opts ResolveOptions, name string) (*Template, error) {
	if name == "" {
		return nil, fmt.Errorf("template name is empty")
	}
	if opts.LocalRoot != "" {
		// Local: short name under root, or absolute path to a template dir.
		if strings.Contains(name, string(os.PathSeparator)) || filepath.IsAbs(name) {
			dir, err := filepath.Abs(name)
			if err != nil {
				return nil, err
			}
			return LoadFromDir(dir)
		}
		return Load(opts.LocalRoot, name)
	}
	src, err := resolveSource(opts, name)
	if err != nil {
		return nil, err
	}
	dir, err := ensureCached(opts, src)
	if err != nil {
		return nil, err
	}
	return LoadFromDir(dir)
}

// LoadFromDir loads a template whose root is dir (must contain plat5.template.yml).
func LoadFromDir(dir string) (*Template, error) {
	mp := filepath.Join(dir, ManifestName)
	m, err := loadManifest(mp)
	if err != nil {
		return nil, err
	}
	if m.Name == "" {
		m.Name = filepath.Base(dir)
	}
	return &Template{Dir: dir, Manifest: m}, nil
}

// source is a downloadable template archive.
type source struct {
	// cacheKey is a filesystem-safe unique id under the cache dir.
	cacheKey string
	// display is used in errors.
	display string
	// url is the tar.gz to download.
	url string
}

func resolveSource(opts ResolveOptions, name string) (source, error) {
	name = strings.TrimSpace(name)
	ref := opts.ref()

	// Full URL to a .tar.gz (or any archive we can extract as tar.gz).
	if strings.HasPrefix(name, "https://") || strings.HasPrefix(name, "http://") {
		return source{
			cacheKey: "url-" + sanitizeCacheKey(name),
			display:  name,
			url:      name,
		}, nil
	}

	// Official short name.
	if off := lookupOfficial(name); off != nil {
		owner, repo, err := splitOwnerRepo(off.Repo)
		if err != nil {
			return source{}, err
		}
		return githubArchive(opts, owner, repo, ref, name), nil
	}

	// github.com/owner/repo[/...]
	if strings.HasPrefix(name, "github.com/") {
		rest := strings.TrimPrefix(name, "github.com/")
		owner, repo, err := splitOwnerRepo(rest)
		if err != nil {
			return source{}, fmt.Errorf("template %q: %w", name, err)
		}
		return githubArchive(opts, owner, repo, ref, owner+"-"+repo), nil
	}

	// owner/repo
	if owner, repo, err := splitOwnerRepo(name); err == nil {
		return githubArchive(opts, owner, repo, ref, owner+"-"+repo), nil
	}

	return source{}, fmt.Errorf(
		"unknown template %q (official: %s; or pass owner/repo or https://…/archive/….tar.gz)",
		name, strings.Join(OfficialNames(), ", "),
	)
}

func githubArchive(opts ResolveOptions, owner, repo, ref, cacheKey string) source {
	// Prefer refs/tags/ for v* / other tags; heads/ for branch-like refs.
	kind := "heads"
	if looksLikeTag(ref) {
		kind = "tags"
	}
	u := fmt.Sprintf("%s/%s/%s/archive/refs/%s/%s.tar.gz",
		opts.githubBase(), owner, repo, kind, ref)
	return source{
		cacheKey: sanitizeCacheKey(cacheKey + "@" + ref),
		display:  owner + "/" + repo + "@" + ref,
		url:      u,
	}
}

func looksLikeTag(ref string) bool {
	if ref == "" || ref == "master" || ref == "main" {
		return false
	}
	// v1.2.3 or 1.2.3
	if matched, _ := regexp.MatchString(`^v?\d+\.\d+`, ref); matched {
		return true
	}
	return false
}

var ownerRepoRe = regexp.MustCompile(`^([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)(?:/.*)?$`)

func splitOwnerRepo(s string) (owner, repo string, err error) {
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "github.com/")
	s = strings.Trim(s, "/")
	m := ownerRepoRe.FindStringSubmatch(s)
	if m == nil {
		return "", "", fmt.Errorf("expected owner/repo, got %q", s)
	}
	return m[1], m[2], nil
}

func sanitizeCacheKey(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	out = strings.Trim(out, "-")
	if len(out) > 120 {
		out = out[:120]
	}
	if out == "" {
		out = "template"
	}
	return out
}

func ensureCached(opts ResolveOptions, src source) (string, error) {
	cacheRoot, err := templateCacheDir(opts.ref())
	if err != nil {
		return "", err
	}
	dest := filepath.Join(cacheRoot, src.cacheKey)
	if _, err := os.Stat(filepath.Join(dest, ManifestName)); err == nil {
		return dest, nil
	}

	tmpTar, err := os.CreateTemp("", "plat5-tpl-*.tar.gz")
	if err != nil {
		return "", err
	}
	tmpPath := tmpTar.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if err := downloadFile(opts.client(), src.url, tmpTar); err != nil {
		_ = tmpTar.Close()
		return "", fmt.Errorf("download template %s: %w", src.display, err)
	}
	if err := tmpTar.Close(); err != nil {
		return "", err
	}

	staging := dest + ".staging"
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return "", err
	}
	if err := extractTarGz(tmpPath, staging); err != nil {
		_ = os.RemoveAll(staging)
		return "", fmt.Errorf("extract template %s: %w", src.display, err)
	}
	root := staging
	if entries, err := os.ReadDir(staging); err == nil && len(entries) == 1 && entries[0].IsDir() {
		root = filepath.Join(staging, entries[0].Name())
	}
	if _, err := os.Stat(filepath.Join(root, ManifestName)); err != nil {
		_ = os.RemoveAll(staging)
		return "", fmt.Errorf("template %s: missing %s after extract (not a Plat5 template?)", src.display, ManifestName)
	}
	_ = os.RemoveAll(dest)
	if err := os.Rename(root, dest); err != nil {
		if err := copyDir(root, dest); err != nil {
			_ = os.RemoveAll(staging)
			return "", err
		}
		_ = os.RemoveAll(staging)
	} else {
		_ = os.RemoveAll(staging)
	}
	return dest, nil
}

func templateCacheDir(ref string) (string, error) {
	base, err := xdg.Plat5CacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "templates", sanitizeCacheKey(ref))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func downloadFile(client *http.Client, rawURL string, w io.Writer) error {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	// GitHub archive endpoints redirect; default client follows.
	req.Header.Set("Accept", "application/vnd.github+json, application/octet-stream, */*")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("%s: HTTP %d %s", rawURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

func extractTarGz(src, dest string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(hdr.Name)
		if name == "." || strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			continue
		}
		target := filepath.Join(dest, name)
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) && target != filepath.Clean(dest) {
			return fmt.Errorf("tar path escapes dest: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := hdr.FileInfo().Mode()
			if mode == 0 {
				mode = 0o644
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		default:
			// skip symlinks etc.
		}
	}
	return nil
}

func copyDir(src, dest string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dest, 0o755)
		}
		target := filepath.Join(dest, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}
