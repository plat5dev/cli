package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/plat5dev/cli/internal/xdg"
)

// StoredCredential is one cached login session for a control-plane base URL.
type StoredCredential struct {
	Issuer        string    `json:"issuer"`
	IssuerName    string    `json:"issuer_name,omitempty"`
	ClientID      string    `json:"client_id"`
	Audience      string    `json:"audience,omitempty"`
	Scopes        []string  `json:"scopes,omitempty"`
	TokenSource   string    `json:"token_source,omitempty"` // "access_token" (default) or "id_token"
	TokenEndpoint string    `json:"token_endpoint,omitempty"`
	AccessToken   string    `json:"access_token,omitempty"`
	IDToken       string    `json:"id_token,omitempty"`
	RefreshToken  string    `json:"refresh_token,omitempty"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
}

// ActiveToken returns the JWT the control plane expects according to TokenSource.
func (c *StoredCredential) ActiveToken() string {
	if c == nil {
		return ""
	}
	if c.TokenSource == "id_token" {
		if c.IDToken != "" {
			return c.IDToken
		}
		return c.AccessToken
	}
	if c.AccessToken != "" {
		return c.AccessToken
	}
	return c.IDToken
}

// Expired reports whether the token is expired (or within a 30s clock-skew buffer).
func (c *StoredCredential) Expired(now time.Time) bool {
	if c == nil || c.ExpiresAt.IsZero() {
		return false
	}
	return !now.Add(30 * time.Second).Before(c.ExpiresAt)
}

// Store persists login credentials in $XDG_STATE_HOME/plat5/credentials.json (0600).
type Store struct {
	Path string
}

type credFile struct {
	ControlPlanes map[string]*StoredCredential `json:"control_planes"`
}

// DefaultStore returns the XDG credential store.
func DefaultStore() (*Store, error) {
	dir, err := xdg.Plat5StateDir()
	if err != nil {
		return nil, err
	}
	return &Store{Path: filepath.Join(dir, "credentials.json")}, nil
}

func normalizeURL(u string) string {
	return strings.TrimRight(strings.TrimSpace(u), "/")
}

func (s *Store) read() (*credFile, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return &credFile{ControlPlanes: map[string]*StoredCredential{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var cf credFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.Path, err)
	}
	if cf.ControlPlanes == nil {
		cf.ControlPlanes = map[string]*StoredCredential{}
	}
	return &cf, nil
}

func (s *Store) write(cf *credFile) error {
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".credentials-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, s.Path)
}

func (s *Store) Load(baseURL string) (*StoredCredential, error) {
	cf, err := s.read()
	if err != nil {
		return nil, err
	}
	return cf.ControlPlanes[normalizeURL(baseURL)], nil
}

func (s *Store) Save(baseURL string, cred *StoredCredential) error {
	cf, err := s.read()
	if err != nil {
		return err
	}
	cf.ControlPlanes[normalizeURL(baseURL)] = cred
	return s.write(cf)
}

func (s *Store) Delete(baseURL string) (bool, error) {
	cf, err := s.read()
	if err != nil {
		return false, err
	}
	key := normalizeURL(baseURL)
	if _, ok := cf.ControlPlanes[key]; !ok {
		return false, nil
	}
	delete(cf.ControlPlanes, key)
	return true, s.write(cf)
}

func (s *Store) ClearAll() error {
	if err := os.Remove(s.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
