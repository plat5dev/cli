package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStoreSaveLoadDeleteAndPermissions(t *testing.T) {
	store := &Store{Path: filepath.Join(t.TempDir(), "credentials.json")}
	cred := &StoredCredential{
		Issuer:      "https://idp.example.com",
		IssuerName:  "okta",
		ClientID:    "cli-pub",
		AccessToken: "eyJ.access.sig",
		IDToken:     "eyJ.id.sig",
		TokenSource: "id_token",
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
	}
	if err := store.Save("https://cp.example.com/", cred); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("expected 0600 permissions, got %o", perm)
	}
	loaded, err := store.Load("https://cp.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || loaded.ActiveToken() != "eyJ.id.sig" {
		t.Fatalf("unexpected loaded cred: %+v", loaded)
	}
	if loaded.Expired(time.Now()) {
		t.Fatal("fresh token should not be expired")
	}
	deleted, err := store.Delete("https://cp.example.com")
	if err != nil || !deleted {
		t.Fatalf("delete: %v %v", deleted, err)
	}
	again, err := store.Load("https://cp.example.com")
	if err != nil || again != nil {
		t.Fatalf("expected nil after delete, got %+v %v", again, err)
	}
}

func TestReadTokenFileAndExecTokenCmd(t *testing.T) {
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte("  sa-jwt-123 \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadTokenFile(p)
	if err != nil || got != "sa-jwt-123" {
		t.Fatalf("ReadTokenFile: %q %v", got, err)
	}

	got, err = ExecTokenCmd(context.Background(), "printf ' helper-jwt-456 \\n'")
	if err != nil || got != "helper-jwt-456" {
		t.Fatalf("ExecTokenCmd: %q %v", got, err)
	}
}

func TestFetchGitHubOIDC(t *testing.T) {
	var gotAuth, gotAud string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAud = r.URL.Query().Get("audience")
		_ = json.NewEncoder(w).Encode(map[string]string{"value": "gh-oidc-jwt"})
	}))
	defer srv.Close()

	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", srv.URL+"?api-version=2.0")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "gh-req-secret")

	tok, err := FetchGitHubOIDC(context.Background(), srv.Client(), "https://cp.prod.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if tok != "gh-oidc-jwt" || gotAuth != "Bearer gh-req-secret" || gotAud != "https://cp.prod.example.com" {
		t.Fatalf("unexpected: tok=%q auth=%q aud=%q", tok, gotAuth, gotAud)
	}
}

func TestLoginPKCEFlow(t *testing.T) {
	var expectedChallenge string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(DiscoveryDoc{
				Issuer:                srv.URL,
				AuthorizationEndpoint: srv.URL + "/authorize",
				TokenEndpoint:         srv.URL + "/token",
			})
		case "/token":
			_ = r.ParseForm()
			if r.FormValue("grant_type") != "authorization_code" || r.FormValue("code") != "code-123" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if pkceS256(r.FormValue("code_verifier")) != expectedChallenge {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "pkce-access-jwt",
				"refresh_token": "pkce-refresh",
				"expires_in":    3600,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	var out bytes.Buffer
	cred, err := LoginPKCE(context.Background(), LoginParams{
		Issuer:     srv.URL,
		IssuerName: "okta",
		ClientID:   "cli-pub",
		Scopes:     []string{"openid", "email"},
		HTTPClient: srv.Client(),
		Out:        &out,
		OpenBrowser: func(authURL string) error {
			u, err := url.Parse(authURL)
			if err != nil {
				return err
			}
			expectedChallenge = u.Query().Get("code_challenge")
			cb := u.Query().Get("redirect_uri") + "?code=code-123&state=" + url.QueryEscape(u.Query().Get("state"))
			go func() {
				resp, err := http.Get(cb)
				if err == nil {
					resp.Body.Close()
				}
			}()
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cred.ActiveToken() != "pkce-access-jwt" || cred.RefreshToken != "pkce-refresh" {
		t.Fatalf("unexpected cred: %+v", cred)
	}
}

func TestLoginDeviceCodeAndRefresh(t *testing.T) {
	var polls atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(DiscoveryDoc{
				Issuer:                      srv.URL,
				TokenEndpoint:               srv.URL + "/token",
				DeviceAuthorizationEndpoint: srv.URL + "/device",
			})
		case "/device":
			_ = json.NewEncoder(w).Encode(deviceAuthResponse{
				DeviceCode:      "dev-code-1",
				UserCode:        "ABCD-1234",
				VerificationURI: "https://idp.example.com/activate",
				ExpiresIn:       60,
				Interval:        1,
			})
		case "/token":
			_ = r.ParseForm()
			if r.FormValue("grant_type") == "refresh_token" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token": "refreshed-jwt",
					"expires_in":   1800,
				})
				return
			}
			if polls.Add(1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "device-jwt",
				"refresh_token": "device-refresh",
				"expires_in":    600,
			})
		}
	}))
	defer srv.Close()

	var out bytes.Buffer
	cred, err := LoginDeviceCode(context.Background(), LoginParams{
		Issuer:     srv.URL,
		ClientID:   "cli-pub",
		HTTPClient: srv.Client(),
		Out:        &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ABCD-1234") || cred.ActiveToken() != "device-jwt" {
		t.Fatalf("unexpected device login: out=%q cred=%+v", out.String(), cred)
	}

	refreshed, err := RefreshToken(context.Background(), srv.Client(), cred)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.ActiveToken() != "refreshed-jwt" || refreshed.RefreshToken != "device-refresh" {
		t.Fatalf("unexpected refreshed cred: %+v", refreshed)
	}
}
