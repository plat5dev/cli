package controlplane

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/plat5dev/cli/internal/schema"
)

// The config API (plat5 docs/reference/api/control-plane.md): dry run, apply,
// revisions, the generated catalog.

// Plan is a dry run's answer (plat5 docs/concepts/config.md#the-plan).
type Plan struct {
	Revision int64              `json:"revision"`
	Summary  Summary            `json:"summary"`
	Changes  []Change           `json:"changes"`
	Confirm  map[string]Confirm `json:"confirm"`
}

type Summary struct {
	Add     int64 `json:"add"`
	Change  int64 `json:"change"`
	Remove  int64 `json:"remove"`
	Blocked int64 `json:"blocked"`
}

type Change struct {
	Class    string       `json:"class"`
	Op       string       `json:"op"`
	What     string       `json:"what"`
	Name     string       `json:"name"`
	Routes   *RouteCounts `json:"routes,omitempty"`
	Reason   string       `json:"reason,omitempty"`
	InTheWay *InTheWay    `json:"in_the_way,omitempty"`
	Effects  *Effects     `json:"effects,omitempty"`
}

type RouteCounts struct {
	Add    int64 `json:"add"`
	Change int64 `json:"change"`
	Remove int64 `json:"remove"`
}

type InTheWay struct {
	Nodes  int64    `json:"nodes"`
	Sample []string `json:"sample"`
}

type Effects struct {
	Nodes           int64 `json:"nodes"`
	Bindings        int64 `json:"bindings"`
	ServiceAccounts int64 `json:"service_accounts"`
	Keys            int64 `json:"keys"`
}

type Confirm struct {
	Nodes int64 `json:"nodes"`
}

// Empty reports a plan with nothing to change.
func (p *Plan) Empty() bool { return len(p.Changes) == 0 }

// Applied is an apply's answer.
type Applied struct {
	Revision int64   `json:"revision"`
	Summary  Summary `json:"summary"`
}

// Revision is one applied config, as sent.
type Revision struct {
	Revision  int64          `json:"revision"`
	CreatedAt string         `json:"created_at"`
	Config    map[string]any `json:"config"`
}

// ConfigError is a refused config or apply, with the envelope's details:
// field errors for VALIDATION_ERROR, the precondition for PLAN_STALE.
type ConfigError struct {
	*APIError
	Fields  []schema.FieldError
	Details map[string]any
}

func (e *ConfigError) Unwrap() error { return e.APIError }

// DryRun is POST /apply?dry_run=true.
func (c *Client) DryRun(body any) (*Plan, error) {
	var plan Plan
	if err := c.do(http.MethodPost, "/apply?dry_run=true", body, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

// ApplyConfig is POST /apply: the config with expect and confirm.
func (c *Client) ApplyConfig(body any) (*Applied, error) {
	var out Applied
	if err := c.do(http.MethodPost, "/apply", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetRevision is GET /revisions/{n}, or GET /config when n is 0.
func (c *Client) GetRevision(n int64) (*Revision, error) {
	path := "/config"
	if n > 0 {
		path = "/revisions/" + strconv.FormatInt(n, 10)
	}
	var out Revision
	if err := c.do(http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Catalog is POST /catalog: identity's and audit's routes for these kinds, by
// service name.
func (c *Client) Catalog(kinds any) (map[string]any, error) {
	var out struct {
		Services map[string]any `json:"services"`
	}
	if err := c.do(http.MethodPost, "/catalog", map[string]any{"version": 2, "kinds": kinds}, &out); err != nil {
		return nil, err
	}
	return out.Services, nil
}

func (c *Client) do(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	c.auth(req)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("control plane at %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return configErr(resp.StatusCode, data)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s %s: %w", method, path, err)
	}
	return nil
}

func configErr(code int, body []byte) error {
	base := parseErr(code, body).(*APIError)
	var env struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	out := &ConfigError{APIError: base, Details: env.Error.Details}
	if fields, ok := env.Error.Details["fields"].([]any); ok {
		for _, f := range fields {
			m, _ := f.(map[string]any)
			path, _ := m["path"].(string)
			msg, _ := m["message"].(string)
			out.Fields = append(out.Fields, schema.FieldError{Path: path, Message: msg})
		}
	}
	return out
}
