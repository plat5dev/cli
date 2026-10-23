// Package devseed gives a fresh local stack something to call with: the dev
// person, a node labelled Dev of each root kind, and a personal key, which
// reaches every tree its person can. It goes through the gateway's generated
// routes like any client, with a token from local Auth's dev mode. Nothing here
// reaches past the gateway (plat5 docs/reference/cli.md#the-local-stack).
package devseed

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/plat5dev/cli/internal/state"
)

// RootKind is a root kind of the config, other than the sign-in kind, and its
// path segment at the gateway.
type RootKind struct {
	Kind    string
	Segment string
}

// Client reaches the local stack.
type Client struct {
	GatewayURL string
	AuthURL    string
	// ClientID is the Auth client the dev token is minted for (the gateway's audience).
	ClientID string
	Email    string
	// Name is the dev person's name. A person's name comes only from the IdP, so
	// it goes in the dev token as its name claim.
	Name      string
	NodeLabel string
	Roots     []RootKind
	HTTP      *http.Client
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// Ensure returns working dev data: prev's while its key still works, else new.
// Nodes are reused by label, so a lost state file does not make a second one.
func Ensure(c Client, prev *state.Dev) (*state.Dev, error) {
	if prev != nil && prev.Email == c.Email && c.works(prev) {
		return prev, nil
	}
	token, err := c.devToken()
	if err != nil {
		return nil, err
	}
	bearer := header{"Authorization", "Bearer " + token}

	var me struct {
		ID string `json:"id"`
	}
	if err := c.call("GET", "/me", bearer, nil, &me); err != nil {
		return nil, err
	}
	dev := &state.Dev{Email: c.Email, MemberID: me.ID}
	for _, root := range c.Roots {
		n, err := c.ensureRoot(bearer, root)
		if err != nil {
			return nil, err
		}
		dev.Nodes = append(dev.Nodes, n)
	}
	var key struct {
		Key string `json:"key"`
	}
	if err := c.call("POST", "/me/keys", bearer, map[string]string{"name": keyName}, &key); err != nil {
		return nil, err
	}
	dev.Key = key.Key
	return dev, nil
}

const keyName = "plat5 dev"

type node struct {
	ID       string  `json:"id"`
	Slug     string  `json:"slug"`
	Label    string  `json:"label"`
	ParentID *string `json:"parent_id"`
}

// ensureRoot is the dev person's root of this kind labelled NodeLabel, made on
// first use.
func (c Client) ensureRoot(bearer header, root RootKind) (state.DevNode, error) {
	path := "/me/nodes?kind=" + url.QueryEscape(root.Kind)
	for {
		var page struct {
			Nodes   []node `json:"nodes"`
			HasMore bool   `json:"has_more"`
		}
		if err := c.call("GET", path, bearer, nil, &page); err != nil {
			return state.DevNode{}, err
		}
		for _, n := range page.Nodes {
			if n.ParentID == nil && n.Label == c.NodeLabel {
				return state.DevNode{Kind: root.Kind, ID: n.ID, Slug: n.Slug, Path: "/" + root.Segment + "/" + n.Slug}, nil
			}
		}
		if !page.HasMore || len(page.Nodes) == 0 {
			break
		}
		path = "/me/nodes?kind=" + url.QueryEscape(root.Kind) + "&starting_after=" + url.QueryEscape(page.Nodes[len(page.Nodes)-1].ID)
	}
	var n node
	if err := c.call("POST", "/"+root.Segment, bearer, map[string]string{"label": c.NodeLabel}, &n); err != nil {
		return state.DevNode{}, err
	}
	return state.DevNode{Kind: root.Kind, ID: n.ID, Slug: n.Slug, Path: "/" + root.Segment + "/" + n.Slug}, nil
}

// works reports whether the key still authenticates (the data may have been reset).
func (c Client) works(d *state.Dev) bool {
	return d.Key != "" && c.call("GET", "/me", header{"X-API-Key", d.Key}, nil, nil) == nil
}

func (c Client) devToken() (string, error) {
	body, _ := json.Marshal(map[string]string{"email": c.Email, "client_id": c.ClientID, "name": c.Name})
	resp, err := c.http().Post(strings.TrimRight(c.AuthURL, "/")+"/dev/token", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("dev token: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("dev token: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.AccessToken == "" {
		return "", fmt.Errorf("dev token: no access_token in response")
	}
	return out.AccessToken, nil
}

type header struct{ name, value string }

func (c Client) call(method, path string, h header, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, strings.TrimRight(c.GatewayURL, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set(h.name, h.value)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return statusError{resp.StatusCode, fmt.Sprintf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(data)))}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: %w", method, path, err)
		}
	}
	return nil
}

// statusError is a gateway answer that was not 2xx.
type statusError struct {
	code int
	msg  string
}

func (e statusError) Error() string { return e.msg }

func isStatus(err error, code int) bool {
	var se statusError
	return errors.As(err, &se) && se.code == code
}

// IsNotFound is the gateway's answer for a route the config leaves out.
func IsNotFound(err error) bool { return isStatus(err, http.StatusNotFound) }
