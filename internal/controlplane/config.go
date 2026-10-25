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
// revisions, reader status.

// Plan is a dry run's answer (plat5 docs/concepts/config.md#the-plan).
type Plan struct {
	Revision int64    `json:"revision"`
	Summary  Summary  `json:"summary"`
	Changes  []Change `json:"changes"`
}

type Summary struct {
	Add     int64 `json:"add"`
	Change  int64 `json:"change"`
	Remove  int64 `json:"remove"`
	Blocked int64 `json:"blocked"`
}

// Change is one plan line. Before and After hold only the fields that differ,
// as the server sent them: Before is absent on an add, After on a remove.
type Change struct {
	Op      string          `json:"op"`
	What    string          `json:"what"`
	Name    string          `json:"name"`
	Before  json.RawMessage `json:"before,omitempty"`
	After   json.RawMessage `json:"after,omitempty"`
	Routes  []RouteChange   `json:"routes,omitempty"`
	Blocked *Blocked        `json:"blocked,omitempty"`
}

// RouteChange is one expanded route on a service line.
type RouteChange struct {
	Op     string          `json:"op"`
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Before json.RawMessage `json:"before,omitempty"`
	After  json.RawMessage `json:"after,omitempty"`
}

type Blocked struct {
	Reason   string   `json:"reason"`
	InTheWay InTheWay `json:"in_the_way"`
}

type InTheWay struct {
	Nodes  int64    `json:"nodes"`
	Sample []string `json:"sample"`
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

// ApplyConfig is POST /apply: the config with expect.
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

// Status is where each reader replica stands.
type Status struct {
	Revision int64            `json:"revision"`
	Gateway  []GatewayReplica `json:"gateway"`
}

// GatewayReplica is a gateway replica that long-polled in the last minute: the
// revision it serves, and the newest it refused, if any, with why.
type GatewayReplica struct {
	Replica string   `json:"replica"`
	Loaded  int64    `json:"loaded"`
	Refused *Refusal `json:"refused"`
	SeenAt  string   `json:"seen_at"`
}

type Refusal struct {
	Revision int64  `json:"revision"`
	Reason   string `json:"reason"`
}

// Status is GET /status.
func (c *Client) Status() (*Status, error) {
	var out Status
	if err := c.do(http.MethodGet, "/status", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
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
