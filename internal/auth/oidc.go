package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// DiscoveryDoc is the subset of OIDC Discovery (RFC 8414 / OpenID Connect Discovery 1.0)
// endpoints used by `plat5 login`.
type DiscoveryDoc struct {
	Issuer                      string `json:"issuer"`
	AuthorizationEndpoint       string `json:"authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
	JWKSURI                     string `json:"jwks_uri"`
}

// Discover fetches {issuer}/.well-known/openid-configuration and verifies
// RFC 8414 §3.3 issuer equality.
func Discover(ctx context.Context, client *http.Client, issuer string) (*DiscoveryDoc, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	u := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery %s: %w", u, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc discovery %s: HTTP %d", u, resp.StatusCode)
	}
	var doc DiscoveryDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("decode oidc discovery %s: %w", u, err)
	}
	if doc.Issuer != issuer {
		return nil, fmt.Errorf("oidc discovery issuer mismatch: expected %q, got %q", issuer, doc.Issuer)
	}
	return &doc, nil
}

// LoginParams configures an OAuth 2.0 login flow (PKCE or Device Flow).
type LoginParams struct {
	Issuer                      string
	IssuerName                  string
	ClientID                    string
	Audience                    string
	Scopes                      []string
	TokenSource                 string // "access_token" or "id_token"
	AuthorizationEndpoint       string
	TokenEndpoint               string
	DeviceAuthorizationEndpoint string
	NoBrowser                   bool
	HTTPClient                  *http.Client
	Out                         io.Writer
	// OpenBrowser overrides browser launching (used in tests).
	OpenBrowser func(string) error
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	IDToken          string `json:"id_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int64  `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func randomURLSafe(nBytes int) (string, error) {
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func resolveEndpoints(ctx context.Context, p *LoginParams) (authz, token, device string, err error) {
	authz = p.AuthorizationEndpoint
	token = p.TokenEndpoint
	device = p.DeviceAuthorizationEndpoint
	if authz != "" && token != "" && device != "" {
		return authz, token, device, nil
	}
	doc, derr := Discover(ctx, p.HTTPClient, p.Issuer)
	if derr != nil {
		if token != "" && (authz != "" || device != "") {
			return authz, token, device, nil
		}
		return "", "", "", derr
	}
	if authz == "" {
		authz = doc.AuthorizationEndpoint
	}
	if token == "" {
		token = doc.TokenEndpoint
	}
	if device == "" {
		device = doc.DeviceAuthorizationEndpoint
	}
	return authz, token, device, nil
}

func scopesOrDefault(scopes []string) []string {
	if len(scopes) == 0 {
		return []string{"openid"}
	}
	return scopes
}

// LoginPKCE runs RFC 8252 Authorization Code + PKCE (RFC 7636 S256) with an
// ephemeral loopback callback listener.
func LoginPKCE(ctx context.Context, p LoginParams) (*StoredCredential, error) {
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	authzEndpoint, tokenEndpoint, _, err := resolveEndpoints(ctx, &p)
	if err != nil {
		return nil, err
	}
	if authzEndpoint == "" || tokenEndpoint == "" {
		return nil, fmt.Errorf("issuer %q does not advertise authorization_endpoint and token_endpoint", p.Issuer)
	}

	verifier, err := randomURLSafe(32)
	if err != nil {
		return nil, err
	}
	state, err := randomURLSafe(16)
	if err != nil {
		return nil, err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen loopback callback: %w", err)
	}
	defer ln.Close()
	redirectURI := fmt.Sprintf("http://%s/callback", ln.Addr().String())

	type callbackResult struct {
		code string
		err  error
	}
	resultCh := make(chan callbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			select {
			case resultCh <- callbackResult{err: errors.New("oauth callback state mismatch")}:
			default:
			}
			return
		}
		if e := q.Get("error"); e != "" {
			desc := q.Get("error_description")
			http.Error(w, "authentication failed: "+e, http.StatusBadRequest)
			select {
			case resultCh <- callbackResult{err: fmt.Errorf("oauth error %s: %s", e, desc)}:
			default:
			}
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing authorization code", http.StatusBadRequest)
			select {
			case resultCh <- callbackResult{err: errors.New("missing authorization code")}:
			default:
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><html><body><h2>Signed in to Plat5.</h2><p>You can close this tab and return to your terminal.</p></body></html>")
		select {
		case resultCh <- callbackResult{code: code}:
		default:
		}
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	scopes := scopesOrDefault(p.Scopes)
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {p.ClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {strings.Join(scopes, " ")},
		"state":                 {state},
		"code_challenge":        {pkceS256(verifier)},
		"code_challenge_method": {"S256"},
	}
	if p.Audience != "" {
		q.Set("audience", p.Audience)
	}
	sep := "?"
	if strings.Contains(authzEndpoint, "?") {
		sep = "&"
	}
	authURL := authzEndpoint + sep + q.Encode()

	out := p.Out
	if out == nil {
		out = os.Stdout
	}
	if !p.NoBrowser {
		opener := p.OpenBrowser
		if opener == nil {
			opener = openBrowser
		}
		if err := opener(authURL); err != nil {
			fmt.Fprintf(out, "Open this URL in your browser to sign in:\n  %s\n", authURL)
		} else {
			fmt.Fprintf(out, "Opening browser to sign in...\nIf it didn't open, visit:\n  %s\n", authURL)
		}
	} else {
		fmt.Fprintf(out, "Open this URL in your browser to sign in:\n  %s\n", authURL)
	}

	var code string
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-resultCh:
		if res.err != nil {
			return nil, res.err
		}
		code = res.code
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {p.ClientID},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	}
	tok, err := postTokenForm(ctx, client, tokenEndpoint, form)
	if err != nil {
		return nil, err
	}
	return buildStoredCredential(p, tokenEndpoint, scopes, tok, time.Now()), nil
}

type deviceAuthResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int64  `json:"expires_in"`
	Interval                int64  `json:"interval"`
	Error                   string `json:"error"`
	ErrorDescription        string `json:"error_description"`
}

// LoginDeviceCode runs RFC 8628 OAuth 2.0 Device Authorization Grant for headless/SSH terminals.
func LoginDeviceCode(ctx context.Context, p LoginParams) (*StoredCredential, error) {
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	_, tokenEndpoint, deviceEndpoint, err := resolveEndpoints(ctx, &p)
	if err != nil {
		return nil, err
	}
	if deviceEndpoint == "" || tokenEndpoint == "" {
		return nil, fmt.Errorf("issuer %q does not advertise device_authorization_endpoint and token_endpoint", p.Issuer)
	}

	scopes := scopesOrDefault(p.Scopes)
	form := url.Values{
		"client_id": {p.ClientID},
		"scope":     {strings.Join(scopes, " ")},
	}
	if p.Audience != "" {
		form.Set("audience", p.Audience)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deviceEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("device authorization request: %w", err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()

	var dev deviceAuthResponse
	if err := json.Unmarshal(body, &dev); err != nil {
		return nil, fmt.Errorf("decode device authorization response (HTTP %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode >= 400 || dev.Error != "" {
		return nil, fmt.Errorf("device authorization failed (%s): %s", dev.Error, dev.ErrorDescription)
	}
	if dev.DeviceCode == "" || dev.VerificationURI == "" {
		return nil, errors.New("incomplete device authorization response from IdP")
	}

	out := p.Out
	if out == nil {
		out = os.Stdout
	}
	if dev.VerificationURIComplete != "" {
		fmt.Fprintf(out, "Visit this URL to sign in:\n  %s\n(code: %s)\n", dev.VerificationURIComplete, dev.UserCode)
	} else {
		fmt.Fprintf(out, "Visit %s and enter code: %s\n", dev.VerificationURI, dev.UserCode)
	}

	interval := time.Duration(dev.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(dev.ExpiresIn) * time.Second)
	if dev.ExpiresIn <= 0 {
		deadline = time.Now().Add(10 * time.Minute)
	}

	for {
		if time.Now().After(deadline) {
			return nil, errors.New("device code expired before authorization completed")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}

		pollForm := url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"client_id":   {p.ClientID},
			"device_code": {dev.DeviceCode},
		}
		tok, pollErr := pollDeviceToken(ctx, client, tokenEndpoint, pollForm)
		if pollErr != nil {
			switch pollErr.code {
			case "authorization_pending":
				continue
			case "slow_down":
				interval += 5 * time.Second
				continue
			default:
				return nil, fmt.Errorf("device login failed (%s): %s", pollErr.code, pollErr.desc)
			}
		}
		return buildStoredCredential(p, tokenEndpoint, scopes, tok, time.Now()), nil
	}
}

// RefreshToken refreshes an expired StoredCredential using its OAuth refresh_token.
func RefreshToken(ctx context.Context, client *http.Client, cred *StoredCredential) (*StoredCredential, error) {
	if cred == nil || cred.RefreshToken == "" || cred.TokenEndpoint == "" || cred.ClientID == "" {
		return nil, errors.New("credential cannot be refreshed; run `plat5 login`")
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {cred.ClientID},
		"refresh_token": {cred.RefreshToken},
	}
	tok, err := postTokenForm(ctx, client, cred.TokenEndpoint, form)
	if err != nil {
		return nil, err
	}
	updated := *cred
	if tok.AccessToken != "" {
		updated.AccessToken = tok.AccessToken
	}
	if tok.IDToken != "" {
		updated.IDToken = tok.IDToken
	}
	if tok.RefreshToken != "" {
		updated.RefreshToken = tok.RefreshToken
	}
	if tok.ExpiresIn > 0 {
		updated.ExpiresAt = time.Now().UTC().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	return &updated, nil
}

type oauthPollError struct {
	code string
	desc string
}

func pollDeviceToken(ctx context.Context, client *http.Client, endpoint string, form url.Values) (*tokenResponse, *oauthPollError) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, &oauthPollError{code: "request_error", desc: err.Error()}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return nil, &oauthPollError{code: "network_error", desc: err.Error()}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, &oauthPollError{code: "decode_error", desc: err.Error()}
	}
	if resp.StatusCode >= 400 || tok.Error != "" {
		code := tok.Error
		if code == "" {
			code = fmt.Sprintf("http_%d", resp.StatusCode)
		}
		return nil, &oauthPollError{code: code, desc: tok.ErrorDescription}
	}
	return &tok, nil
}

func postTokenForm(ctx context.Context, client *http.Client, endpoint string, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange %s: %w", endpoint, err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("decode token response (HTTP %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode >= 400 || tok.Error != "" {
		return nil, fmt.Errorf("token exchange failed (%s): %s", tok.Error, tok.ErrorDescription)
	}
	if tok.AccessToken == "" && tok.IDToken == "" {
		return nil, errors.New("token response contained neither access_token nor id_token")
	}
	return &tok, nil
}

func buildStoredCredential(p LoginParams, tokenEndpoint string, scopes []string, tok *tokenResponse, now time.Time) *StoredCredential {
	ts := p.TokenSource
	if ts == "" {
		ts = "access_token"
	}
	var exp time.Time
	if tok.ExpiresIn > 0 {
		exp = now.UTC().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	return &StoredCredential{
		Issuer:        p.Issuer,
		IssuerName:    p.IssuerName,
		ClientID:      p.ClientID,
		Audience:      p.Audience,
		Scopes:        scopes,
		TokenSource:   ts,
		TokenEndpoint: tokenEndpoint,
		AccessToken:   tok.AccessToken,
		IDToken:       tok.IDToken,
		RefreshToken:  tok.RefreshToken,
		ExpiresAt:     exp,
	}
}

// ReadTokenFile reads a bearer token from a file (e.g. Kubernetes projected ServiceAccount token).
func ReadTokenFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read token file %s: %w", path, err)
	}
	tok := strings.TrimSpace(string(data))
	if tok == "" {
		return "", fmt.Errorf("token file %s is empty", path)
	}
	return tok, nil
}

// ExecTokenCmd runs an external credential helper command and returns its trimmed stdout.
func ExecTokenCmd(ctx context.Context, command string) (string, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("token command failed: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("token command failed: %w", err)
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" {
		return "", errors.New("token command produced empty output")
	}
	return tok, nil
}

// FetchGitHubOIDC requests a short-lived workload OIDC JWT from GitHub Actions'
// standard ACTIONS_ID_TOKEN_REQUEST_URL endpoint.
func FetchGitHubOIDC(ctx context.Context, client *http.Client, audience string) (string, error) {
	reqURL := strings.TrimSpace(os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL"))
	reqTok := strings.TrimSpace(os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN"))
	if reqURL == "" || reqTok == "" {
		return "", nil
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	u, err := url.Parse(reqURL)
	if err != nil {
		return "", fmt.Errorf("invalid ACTIONS_ID_TOKEN_REQUEST_URL: %w", err)
	}
	if audience != "" {
		q := u.Query()
		q.Set("audience", audience)
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+reqTok)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch GitHub Actions OIDC token: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch GitHub Actions OIDC token: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Value == "" {
		return "", errors.New("GitHub Actions OIDC response missing token value")
	}
	return out.Value, nil
}

// GitLabOIDCToken returns the standard GitLab CI id_tokens export if set.
func GitLabOIDCToken() string {
	return strings.TrimSpace(os.Getenv("PLAT5_OIDC_TOKEN"))
}

func openBrowser(targetURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", targetURL)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", targetURL)
	default:
		cmd = exec.Command("xdg-open", targetURL)
	}
	return cmd.Start()
}
