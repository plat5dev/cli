// Package registry manages first-party and third-party Plat5 plugin registries,
// version resolution against CLI semver constraints, and plugin fetching.
package registry

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/plugin"
	"gopkg.in/yaml.v3"
)

const (
	IndexFileName         = "registry.yml"
	DefaultRegistryName   = "official"
	DefaultRegistrySource = "plat5dev/registry"
	DefaultRef            = "master"
)

// Index is a registry's registry.yml.
type Index struct {
	Schema      int     `yaml:"schema"`
	Name        string  `yaml:"name"`
	Description string  `yaml:"description"`
	Entries     []Entry `yaml:"entries"`
}

// Entry is one plugin in a registry index.
type Entry struct {
	ID          string    `yaml:"id"`
	Type        string    `yaml:"type"`
	Description string    `yaml:"description"`
	Tags        []string  `yaml:"tags"`
	Deprecated  string    `yaml:"deprecated"`
	Versions    []Version `yaml:"versions"`
}

// Version is one released version of a registry entry.
type Version struct {
	Version string `yaml:"version"`
	CLI     string `yaml:"cli"`
	Plat5   string `yaml:"plat5"`
	Path    string `yaml:"path"`
	Source  string `yaml:"source"`
	SHA256  string `yaml:"sha256"`
}

// Config is $XDG_CONFIG_HOME/plat5/registries.yml.
type Config struct {
	Registries []RegistryRef `yaml:"registries"`
}

// RegistryRef is one configured registry.
type RegistryRef struct {
	Name    string `yaml:"name"`
	Source  string `yaml:"source"`
	Default bool   `yaml:"default,omitempty"`
}

// SearchResult is one plugin matched by Resolver.Search.
type SearchResult struct {
	Registry    string
	Entry       Entry
	Compatible  bool
	LatestMatch *Version
}

// ParseIndex strictly decodes and validates registry.yml.
func ParseIndex(data []byte) (Index, error) {
	var idx Index
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&idx); err != nil {
		return Index{}, err
	}
	if err := idx.Validate(); err != nil {
		return Index{}, err
	}
	return idx, nil
}

// Validate checks registry.yml invariants.
func (idx Index) Validate() error {
	if idx.Schema != 1 {
		return fmt.Errorf("schema must be 1 (got %d)", idx.Schema)
	}
	if strings.TrimSpace(idx.Name) == "" {
		return fmt.Errorf("name is required")
	}
	seen := map[string]bool{}
	for _, e := range idx.Entries {
		if strings.TrimSpace(e.ID) == "" {
			return fmt.Errorf("entry id is required")
		}
		if seen[e.ID] {
			return fmt.Errorf("duplicate entry id %q", e.ID)
		}
		seen[e.ID] = true
		if strings.TrimSpace(e.Type) == "" {
			return fmt.Errorf("entry %s: type is required", e.ID)
		}
		if len(e.Versions) == 0 {
			return fmt.Errorf("entry %s: at least one version is required", e.ID)
		}
		for _, v := range e.Versions {
			if _, ok := parseSemver(v.Version); !ok {
				return fmt.Errorf("entry %s: invalid version %q", e.ID, v.Version)
			}
			if (v.Path == "") == (v.Source == "") {
				return fmt.Errorf("entry %s@%s: must set exactly one of path or source", e.ID, v.Version)
			}
		}
	}
	return nil
}

// ConfigPath returns $XDG_CONFIG_HOME/plat5/registries.yml.
func ConfigPath() (string, error) {
	base := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "plat5", "registries.yml"), nil
}

// CacheDir returns $XDG_CACHE_HOME/plat5/registries.
func CacheDir() (string, error) {
	base := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "plat5", "registries"), nil
}

// LoadConfig loads registries.yml, implicitly ensuring the default official registry exists.
func LoadConfig() (Config, error) {
	p, err := ConfigPath()
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	data, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return Config{}, err
	}
	if err == nil && len(bytes.TrimSpace(data)) > 0 {
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("%s: %w", p, err)
		}
	}
	hasDefault := false
	for _, r := range cfg.Registries {
		if r.Default || r.Name == DefaultRegistryName {
			hasDefault = true
			break
		}
	}
	if !hasDefault {
		cfg.Registries = append([]RegistryRef{{
			Name:    DefaultRegistryName,
			Source:  DefaultRegistrySource,
			Default: true,
		}}, cfg.Registries...)
	}
	return cfg, nil
}

// SaveConfig writes registries.yml.
func SaveConfig(cfg Config) error {
	p, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(p, buf.Bytes(), 0o644)
}

// Resolver resolves plugin references from registries, GitHub repos/subpaths,
// archive URLs, or local directories.
type Resolver struct {
	// SourcesDir (--sources): resolves plat5dev/<repo> from <SourcesDir>/<repo>
	// when present (e.g. ../registry, ../templates).
	SourcesDir string
	// RegistryOverride (--registry): replaces the default registry source.
	RegistryOverride string
	// Ref overrides the default git ref for GitHub downloads.
	Ref        string
	HTTPClient *http.Client
	GitHubBase string
	Token      string
	CLIVersion string
}

func (r *Resolver) client() *http.Client {
	if r.HTTPClient != nil {
		return r.HTTPClient
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (r *Resolver) githubBase() string {
	if r.GitHubBase != "" {
		return strings.TrimRight(r.GitHubBase, "/")
	}
	return "https://github.com"
}

func (r *Resolver) token() string {
	if r.Token != "" {
		return r.Token
	}
	if t := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); t != "" {
		return t
	}
	return strings.TrimSpace(os.Getenv("GH_TOKEN"))
}

func (r *Resolver) cliVersion() string {
	if r.CLIVersion != "" {
		return r.CLIVersion
	}
	return config.CLIVersion
}

func (r *Resolver) defaultRef() string {
	if r.Ref != "" {
		return r.Ref
	}
	return DefaultRef
}

type loadedRegistry struct {
	Ref   RegistryRef
	Index Index
	// LocalDir is non-empty when the registry was loaded from a local directory.
	LocalDir string
}

// ListRegistries returns all active registries with overrides applied.
func (r *Resolver) ListRegistries() ([]RegistryRef, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	out := make([]RegistryRef, len(cfg.Registries))
	copy(out, cfg.Registries)
	if r.RegistryOverride != "" {
		for i := range out {
			if out[i].Default || out[i].Name == DefaultRegistryName {
				out[i].Source = r.RegistryOverride
			}
		}
	}
	return out, nil
}

func (r *Resolver) loadRegistries() ([]loadedRegistry, error) {
	refs, err := r.ListRegistries()
	if err != nil {
		return nil, err
	}
	var out []loadedRegistry
	for _, ref := range refs {
		lr, err := r.loadOneRegistry(ref, false)
		if err != nil {
			return nil, fmt.Errorf("registry %s (%s): %w", ref.Name, ref.Source, err)
		}
		out = append(out, lr)
	}
	return out, nil
}

// Update refreshes cached index(es). Empty name updates all registries.
func (r *Resolver) Update(name string) error {
	refs, err := r.ListRegistries()
	if err != nil {
		return err
	}
	found := false
	for _, ref := range refs {
		if name != "" && ref.Name != name {
			continue
		}
		found = true
		if _, err := r.loadOneRegistry(ref, true); err != nil {
			return fmt.Errorf("update registry %s: %w", ref.Name, err)
		}
	}
	if name != "" && !found {
		return fmt.Errorf("unknown registry %q", name)
	}
	return nil
}

func (r *Resolver) loadOneRegistry(ref RegistryRef, forceRefresh bool) (loadedRegistry, error) {
	// Check local path or --sources checkout first.
	if localDir := r.localCheckout(ref.Source); localDir != "" {
		data, err := os.ReadFile(filepath.Join(localDir, IndexFileName))
		if err != nil {
			return loadedRegistry{}, err
		}
		idx, err := ParseIndex(data)
		if err != nil {
			return loadedRegistry{}, err
		}
		return loadedRegistry{Ref: ref, Index: idx, LocalDir: localDir}, nil
	}

	// Remote registry: fetch registry.yml with ETag caching.
	indexURL, err := r.registryIndexURL(ref.Source)
	if err != nil {
		return loadedRegistry{}, err
	}
	data, err := r.fetchCachedIndex(ref.Name, indexURL, forceRefresh)
	if err != nil {
		if (ref.Default || ref.Name == DefaultRegistryName) && len(fallbackRegistryYML) > 0 {
			idx, ferr := ParseIndex(fallbackRegistryYML)
			if ferr == nil {
				return loadedRegistry{Ref: ref, Index: idx}, nil
			}
		}
		return loadedRegistry{}, err
	}
	idx, err := ParseIndex(data)
	if err != nil {
		return loadedRegistry{}, err
	}
	return loadedRegistry{Ref: ref, Index: idx}, nil
}

func (r *Resolver) localCheckout(src string) string {
	// Explicit local path.
	if strings.HasPrefix(src, ".") || strings.HasPrefix(src, "/") || strings.HasPrefix(src, "~/") {
		p := expandHome(src)
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
		return ""
	}
	// --sources shortcut for plat5dev/<repo>[@ref]
	if r.SourcesDir != "" {
		repo, _, _ := splitRepoSubdirRef(src)
		if rest, ok := strings.CutPrefix(repo, "plat5dev/"); ok && !strings.Contains(rest, "/") {
			cand := filepath.Join(r.SourcesDir, rest)
			if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
				return cand
			}
		}
	}
	return ""
}

func (r *Resolver) registryIndexURL(src string) (string, error) {
	if strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "http://") {
		if strings.HasSuffix(src, ".yml") || strings.HasSuffix(src, ".yaml") {
			return src, nil
		}
		return strings.TrimRight(src, "/") + "/" + IndexFileName, nil
	}
	repo, subdir, ref := splitRepoSubdirRef(src)
	if !ownerRepoRE.MatchString(repo) {
		return "", fmt.Errorf("invalid registry source %q", src)
	}
	if ref == "" {
		ref = r.defaultRef()
	}
	path := IndexFileName
	if subdir != "" {
		path = strings.Trim(subdir, "/") + "/" + IndexFileName
	}
	// If custom GitHubBase (tests), fetch from base/<repo>/raw/<ref>/<path>.
	if r.GitHubBase != "" {
		return fmt.Sprintf("%s/%s/raw/%s/%s", r.githubBase(), repo, ref, path), nil
	}
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", repo, ref, path), nil
}

func (r *Resolver) fetchCachedIndex(name, rawURL string, forceRefresh bool) ([]byte, error) {
	cacheBase, _ := CacheDir()
	var cachePath, etagPath string
	if cacheBase != "" {
		dir := filepath.Join(cacheBase, name)
		cachePath = filepath.Join(dir, IndexFileName)
		etagPath = filepath.Join(dir, IndexFileName+".etag")
		if !forceRefresh {
			if st, err := os.Stat(cachePath); err == nil && time.Since(st.ModTime()) < 15*time.Minute {
				if data, err := os.ReadFile(cachePath); err == nil {
					return data, nil
				}
			}
		}
	}

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if tok := r.token(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if !forceRefresh && etagPath != "" {
		if etag, err := os.ReadFile(etagPath); err == nil && len(bytes.TrimSpace(etag)) > 0 {
			req.Header.Set("If-None-Match", strings.TrimSpace(string(etag)))
		}
	}

	resp, err := r.client().Do(req)
	if err != nil {
		// Offline fallback to cached index if present.
		if cachePath != "" {
			if data, rerr := os.ReadFile(cachePath); rerr == nil {
				return data, nil
			}
		}
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified && cachePath != "" {
		_ = os.Chtimes(cachePath, time.Now(), time.Now())
		return os.ReadFile(cachePath)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", rawURL, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if cachePath != "" {
		_ = os.MkdirAll(filepath.Dir(cachePath), 0o755)
		_ = os.WriteFile(cachePath, data, 0o644)
		if etag := resp.Header.Get("ETag"); etag != "" {
			_ = os.WriteFile(etagPath, []byte(etag), 0o644)
		}
	}
	return data, nil
}

// Search lists entries across all configured registries matching query and optional type.
func (r *Resolver) Search(query, filterType string) ([]SearchResult, error) {
	regs, err := r.loadRegistries()
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var out []SearchResult
	for _, lr := range regs {
		for _, e := range lr.Index.Entries {
			if filterType != "" && e.Type != filterType {
				continue
			}
			if q != "" && !matchesQuery(e, q) {
				continue
			}
			v := selectCompatibleVersion(e.Versions, r.cliVersion())
			out = append(out, SearchResult{
				Registry:    lr.Ref.Name,
				Entry:       e,
				Compatible:  v != nil,
				LatestMatch: v,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Entry.Type != out[j].Entry.Type {
			return out[i].Entry.Type < out[j].Entry.Type
		}
		if out[i].Registry != out[j].Registry {
			return out[i].Registry < out[j].Registry
		}
		return out[i].Entry.ID < out[j].Entry.ID
	})
	return out, nil
}

func matchesQuery(e Entry, q string) bool {
	if strings.Contains(strings.ToLower(e.ID), q) || strings.Contains(strings.ToLower(e.Description), q) {
		return true
	}
	for _, tag := range e.Tags {
		if strings.Contains(strings.ToLower(tag), q) {
			return true
		}
	}
	return false
}

// Resolve loads a plugin by registry ID, reg/id[@ver], owner/repo[//subdir][@ref],
// .tar.gz URL, or local directory path.
func (r *Resolver) Resolve(ref string) (*plugin.Plugin, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("empty plugin reference")
	}

	// 1. Explicit local path.
	if strings.HasPrefix(ref, ".") || strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "~/") {
		p := expandHome(ref)
		return plugin.LoadDir(p)
	}

	// 2. Direct archive URL.
	if strings.HasPrefix(ref, "https://") || strings.HasPrefix(ref, "http://") {
		return r.fetchArchivePlugin(ref, ref, "", "")
	}

	// 3. Monorepo subpath (owner/repo//subdir[@ref]) is always a direct repo source.
	if strings.Contains(ref, "//") {
		return r.resolveSource(ref, "")
	}

	// 4. Bare ID or reg/id[@ver] checked against registries first.
	idPart, pinnedVer, hasPin := strings.Cut(ref, "@")
	if regName, entryID, hasSlash := strings.Cut(idPart, "/"); hasSlash {
		regs, err := r.loadRegistries()
		if err == nil {
			for _, lr := range regs {
				if lr.Ref.Name == regName {
					return r.resolveFromRegistry(lr, entryID, pinnedVer, hasPin)
				}
			}
		}
		// Not a registered alias → treat as GitHub owner/repo[@ref].
		return r.resolveSource(ref, "")
	}

	// Bare ID: search default registry first, then 3rd-party registries.
	regs, err := r.loadRegistries()
	if err != nil {
		return nil, err
	}
	var matches []loadedRegistry
	for _, lr := range regs {
		for _, e := range lr.Index.Entries {
			if e.ID == idPart {
				if lr.Ref.Default || lr.Ref.Name == DefaultRegistryName {
					return r.resolveFromRegistry(lr, idPart, pinnedVer, hasPin)
				}
				matches = append(matches, lr)
			}
		}
	}
	if len(matches) == 1 {
		return r.resolveFromRegistry(matches[0], idPart, pinnedVer, hasPin)
	}
	if len(matches) > 1 {
		var names []string
		for _, m := range matches {
			names = append(names, m.Ref.Name+"/"+idPart)
		}
		return nil, fmt.Errorf("ambiguous plugin %q (matches %s); qualify with <registry>/<id>", idPart, strings.Join(names, ", "))
	}
	return nil, fmt.Errorf("unknown plugin %q (run `plat5 registry search` to list available plugins)", ref)
}

func (r *Resolver) resolveFromRegistry(lr loadedRegistry, entryID, pinnedVer string, hasPin bool) (*plugin.Plugin, error) {
	var entry *Entry
	for i := range lr.Index.Entries {
		if lr.Index.Entries[i].ID == entryID {
			entry = &lr.Index.Entries[i]
			break
		}
	}
	if entry == nil {
		return nil, fmt.Errorf("plugin %q not found in registry %s", entryID, lr.Ref.Name)
	}

	var chosen *Version
	if hasPin {
		for i := range entry.Versions {
			if entry.Versions[i].Version == pinnedVer {
				chosen = &entry.Versions[i]
				break
			}
		}
		if chosen == nil {
			return nil, fmt.Errorf("%s/%s@%s: version not found", lr.Ref.Name, entryID, pinnedVer)
		}
		if !matchesCLIConstraint(r.cliVersion(), chosen.CLI) {
			return nil, fmt.Errorf("%s@%s requires plat5 CLI %s (running %s; upgrade: %s)", entryID, chosen.Version, chosen.CLI, r.cliVersion(), config.UpgradeHint)
		}
	} else {
		chosen = selectCompatibleVersion(entry.Versions, r.cliVersion())
		if chosen == nil {
			return nil, fmt.Errorf("%s requires a newer plat5 CLI (latest requires %s; running %s; upgrade: %s)", entryID, entry.Versions[0].CLI, r.cliVersion(), config.UpgradeHint)
		}
	}

	var (
		p   *plugin.Plugin
		err error
	)
	if chosen.Path != "" {
		if lr.LocalDir != "" {
			dir := filepath.Join(lr.LocalDir, filepath.FromSlash(chosen.Path))
			p, err = plugin.LoadDir(dir)
			if err == nil {
				p.Origin = fmt.Sprintf("%s/%s@%s (%s)", lr.Ref.Name, entryID, chosen.Version, dir)
			}
		} else {
			repo, _, ref := splitRepoSubdirRef(lr.Ref.Source)
			src := repo + "//" + chosen.Path
			if ref != "" {
				src += "@" + ref
			}
			p, err = r.resolveSource(src, chosen.SHA256)
			if err == nil {
				p.Origin = fmt.Sprintf("%s/%s@%s (%s)", lr.Ref.Name, entryID, chosen.Version, src)
			}
		}
	} else {
		p, err = r.resolveSource(chosen.Source, chosen.SHA256)
		if err == nil {
			p.Origin = fmt.Sprintf("%s/%s@%s (%s)", lr.Ref.Name, entryID, chosen.Version, chosen.Source)
		}
	}
	if err != nil {
		return nil, err
	}
	if p.Manifest.Type != entry.Type {
		p.Close()
		return nil, fmt.Errorf("%s/%s: registry says type %q, manifest says %q", lr.Ref.Name, entryID, entry.Type, p.Manifest.Type)
	}
	return p, nil
}

func (r *Resolver) resolveSource(src, wantSHA256 string) (*plugin.Plugin, error) {
	if strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "http://") {
		return r.fetchArchivePlugin(src, src, "", wantSHA256)
	}
	repo, subdir, ref := splitRepoSubdirRef(src)
	if !ownerRepoRE.MatchString(repo) {
		return nil, fmt.Errorf("invalid plugin source %q", src)
	}
	// Check --sources local checkout for plat5dev/<repo>.
	if localRepo := r.localCheckout(repo); localRepo != "" {
		dir := localRepo
		if subdir != "" {
			dir = filepath.Join(localRepo, filepath.FromSlash(subdir))
		}
		p, err := plugin.LoadDir(dir)
		if err != nil {
			return nil, err
		}
		p.Origin = dir
		return p, nil
	}
	if ref == "" {
		ref = r.defaultRef()
	}
	archiveURL := fmt.Sprintf("%s/%s/archive/%s.tar.gz", r.githubBase(), repo, ref)
	display := repo
	if subdir != "" {
		display += "//" + subdir
	}
	display += "@" + ref
	return r.fetchArchivePlugin(display, archiveURL, subdir, wantSHA256)
}

func (r *Resolver) fetchArchivePlugin(display, archiveURL, subdir, wantSHA256 string) (*plugin.Plugin, error) {
	tmp, err := os.MkdirTemp("", "plat5-plugin-*")
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }

	req, err := http.NewRequest(http.MethodGet, archiveURL, nil)
	if err != nil {
		cleanup()
		return nil, err
	}
	if tok := r.token(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := r.client().Do(req)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("download %s: %w", display, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		cleanup()
		return nil, fmt.Errorf("download %s: HTTP %d", display, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		cleanup()
		return nil, err
	}
	if wantSHA256 != "" {
		sum := sha256.Sum256(body)
		got := hex.EncodeToString(sum[:])
		if !strings.EqualFold(got, strings.TrimSpace(wantSHA256)) {
			cleanup()
			return nil, fmt.Errorf("%s: sha256 mismatch (got %s, want %s)", display, got, wantSHA256)
		}
	}
	if err := extractTarGz(bytes.NewReader(body), tmp); err != nil {
		cleanup()
		return nil, fmt.Errorf("extract %s: %w", display, err)
	}
	root := tmp
	if entries, err := os.ReadDir(tmp); err == nil && len(entries) == 1 && entries[0].IsDir() {
		root = filepath.Join(tmp, entries[0].Name())
	}
	if subdir != "" {
		clean := filepath.Clean(subdir)
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			cleanup()
			return nil, fmt.Errorf("%s: invalid subdir %q", display, subdir)
		}
		root = filepath.Join(root, clean)
	}
	p, err := plugin.LoadDir(root)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("%s: %w", display, err)
	}
	p.Origin = display
	p.SetCleanup(cleanup)
	return p, nil
}

// ValidateDir checks a registry directory's registry.yml and every inline plugin manifest.
func ValidateDir(dir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, IndexFileName))
	if err != nil {
		return nil, err
	}
	idx, err := ParseIndex(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", IndexFileName, err)
	}
	var problems []string
	for _, e := range idx.Entries {
		for _, v := range e.Versions {
			if v.Path == "" {
				continue
			}
			pDir := filepath.Join(dir, filepath.FromSlash(v.Path))
			p, err := plugin.LoadDir(pDir)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s@%s (%s): %v", e.ID, v.Version, v.Path, err))
				continue
			}
			if p.Manifest.Type != e.Type {
				problems = append(problems, fmt.Sprintf("%s@%s: registry type %q != manifest type %q", e.ID, v.Version, e.Type, p.Manifest.Type))
			}
			if p.Manifest.Name != e.ID {
				problems = append(problems, fmt.Sprintf("%s@%s: registry id %q != manifest name %q", e.ID, v.Version, e.ID, p.Manifest.Name))
			}
		}
	}
	return problems, nil
}

var ownerRepoRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// splitRepoSubdirRef splits "owner/repo//sub/dir@ref" into (repo, subdir, ref).
func splitRepoSubdirRef(raw string) (repo, subdir, ref string) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "github.com/")
	if before, after, ok := strings.Cut(raw, "@"); ok {
		raw, ref = before, after
	}
	if before, after, ok := strings.Cut(raw, "//"); ok {
		repo, subdir = before, strings.Trim(after, "/")
	} else {
		repo = raw
	}
	return repo, subdir, ref
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

var semverRE = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

func parseSemver(s string) ([3]int, bool) {
	m := semverRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := range out {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out, true
}

func cmpSemver(a, b [3]int) int {
	for i := range a {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

// matchesCLIConstraint checks space-separated constraints like ">=0.18.0" or ">=0.18.0 <1.0.0".
// Dev builds (non-semver) match all constraints.
func matchesCLIConstraint(cliVer, constraint string) bool {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return true
	}
	have, ok := parseSemver(cliVer)
	if !ok {
		return true
	}
	for _, tok := range strings.Fields(constraint) {
		switch {
		case strings.HasPrefix(tok, ">="):
			min, ok := parseSemver(tok[2:])
			if ok && cmpSemver(have, min) < 0 {
				return false
			}
		case strings.HasPrefix(tok, "<="):
			max, ok := parseSemver(tok[2:])
			if ok && cmpSemver(have, max) > 0 {
				return false
			}
		case strings.HasPrefix(tok, "<"):
			max, ok := parseSemver(tok[1:])
			if ok && cmpSemver(have, max) >= 0 {
				return false
			}
		case strings.HasPrefix(tok, ">"):
			min, ok := parseSemver(tok[1:])
			if ok && cmpSemver(have, min) <= 0 {
				return false
			}
		}
	}
	return true
}

func selectCompatibleVersion(versions []Version, cliVer string) *Version {
	sorted := make([]Version, len(versions))
	copy(sorted, versions)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, _ := parseSemver(sorted[i].Version)
		b, _ := parseSemver(sorted[j].Version)
		return cmpSemver(a, b) > 0
	})
	for i := range sorted {
		if matchesCLIConstraint(cliVer, sorted[i].CLI) {
			v := sorted[i]
			return &v
		}
	}
	return nil
}

func extractTarGz(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader || hdr.Typeflag == tar.TypeXHeader {
			continue
		}
		name := filepath.Clean(hdr.Name)
		if name == "." || strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("tar path escapes archive: %s", hdr.Name)
		}
		target := filepath.Join(dest, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, hdr.FileInfo().Mode().Perm()|0o600)
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
		}
	}
}
