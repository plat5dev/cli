package controlplane

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client talks to the control plane's admin API (config.go has the calls).
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

// Ready reports whether the admin API answers with this token: GET /revisions,
// which answers before the first apply too.
func (c *Client) Ready() bool {
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+"/revisions", nil)
	if err != nil {
		return false
	}
	c.auth(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
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
	for time.Now().Before(deadline) {
		if c.Ready() {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timeout waiting for the control plane at %s", c.BaseURL)
}
