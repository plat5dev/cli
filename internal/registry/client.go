package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/plat5dev/cli/internal/upstreams"
)

// Client talks to the route-registry admin API.
type Client struct {
	BaseURL    string
	AdminToken string
	HTTP       *http.Client
}

func New(baseURL, adminToken string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		AdminToken: adminToken,
		HTTP:       &http.Client{Timeout: 30 * time.Second},
	}
}

// APIError is a Plat5 error envelope.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	Raw        string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s: %s (HTTP %d)", e.Code, e.Message, e.StatusCode)
	}
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Raw)
}

type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// ApplyResult is one service from POST /apply.
type ApplyResult struct {
	Service  string `json:"service"`
	Status   string `json:"status"`
	Revision int64  `json:"revision,omitempty"`
	Error    string `json:"error,omitempty"`
}

type applyResponse struct {
	Results []ApplyResult `json:"results"`
}

// Apply posts a routes file to /apply.
// upstreams injects service URLs (see internal/upstreams) before upload.
func (c *Client) Apply(path string, upstreams map[string]string) ([]ApplyResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ct := "application/json"
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".yml" || ext == ".yaml" {
		ct = "application/yaml"
	}
	return c.ApplyBody(data, ct, upstreams)
}

// ApplyBody posts routes bytes to /apply after optional upstream bind.
func (c *Client) ApplyBody(data []byte, contentType string, ups map[string]string) ([]ApplyResult, error) {
	if len(ups) > 0 {
		bound, err := upstreams.Bind(data, ups)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(bound, data) {
			data = bound
			contentType = "application/yaml"
		}
	}
	if contentType == "" {
		contentType = "application/yaml"
	}
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+"/apply", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	c.auth(req)
	req.Header.Set("Content-Type", contentType)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out applyResponse
	if err := json.Unmarshal(body, &out); err == nil && len(out.Results) > 0 {
		if resp.StatusCode == http.StatusOK {
			return out.Results, nil
		}
	}
	if resp.StatusCode >= 400 {
		return nil, parseErr(resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode apply response: %w", err)
	}
	return out.Results, nil
}

// List returns registered services map (raw JSON object under services).
func (c *Client) List() (map[string]json.RawMessage, error) {
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+"/services", nil)
	if err != nil {
		return nil, err
	}
	c.auth(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, parseErr(resp.StatusCode, body)
	}
	var wrap struct {
		Services map[string]json.RawMessage `json:"services"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, fmt.Errorf("decode list: %w", err)
	}
	return wrap.Services, nil
}

// Get returns one service config JSON.
func (c *Client) Get(name string) (json.RawMessage, error) {
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+"/services/"+name, nil)
	if err != nil {
		return nil, err
	}
	c.auth(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, parseErr(resp.StatusCode, body)
	}
	return json.RawMessage(body), nil
}

// Delete removes a service.
func (c *Client) Delete(name string) error {
	u := c.BaseURL + "/services/" + name
	req, err := http.NewRequest(http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	c.auth(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return parseErr(resp.StatusCode, body)
	}
	return nil
}

// Ready returns true if list succeeds.
func (c *Client) Ready() bool {
	_, err := c.List()
	return err == nil
}

func (c *Client) auth(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.AdminToken)
}

func parseErr(code int, body []byte) error {
	var env errorEnvelope
	if json.Unmarshal(body, &env) == nil && env.Error.Code != "" {
		return &APIError{StatusCode: code, Code: env.Error.Code, Message: env.Error.Message, Raw: string(body)}
	}
	return &APIError{StatusCode: code, Raw: strings.TrimSpace(string(body))}
}

// WaitReady polls until Ready or timeout.
func (c *Client) WaitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		if c.Ready() {
			return nil
		}
		last = fmt.Errorf("registry not ready")
		time.Sleep(2 * time.Second)
	}
	if last != nil {
		return fmt.Errorf("timeout waiting for route-registry at %s", c.BaseURL)
	}
	return nil
}
