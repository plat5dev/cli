// Package devseed gives a fresh local stack something to call with: the dev
// person and their name, a node named Dev of each root kind, and a personal
// key, which reaches every tree its person can. It goes through identity's
// routes at the gateway like any client, with a token from local Auth's dev
// mode. Nothing here reaches past the gateway (plat5 docs/reference/cli.md#the-local-stack).
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

// RootKind is a root kind of the config and its collection at the gateway:
// orgs, for /orgs/{organization}.
type RootKind struct {
	Kind       string
	Collection string
}

// Client reaches the local stack.
type Client struct {
	GatewayURL string
	AuthURL    string
	// ClientID is the Auth client the dev token is minted for (the gateway's audience).
	ClientID string
	Email    string
	// Name is the dev person's name, set with PATCH /user while they have none:
	// local Auth's tokens carry no name.
	Name string
	// NodeName is the display_name of the node seeded of each root kind.
	NodeName string
	Roots    []RootKind
	HTTP     *http.Client
	// Warn gets what the seed skipped and why; nil drops it.
	Warn func(string)
}

func (c Client) warn(msg string) {
	if c.Warn != nil {
		c.Warn(msg)
	}
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// Ensure returns working dev data: prev's while its key still works, else new.
// Nodes are reused by display name, so a lost state file does not make a second one.
// Either way the dev person ends up named, unless the IdP owns the name
// (users.name.sync: each_sign_in): that is a warning, not an error.
func Ensure(c Client, prev *state.Dev) (*state.Dev, error) {
	if prev != nil && prev.Email == c.Email {
		if u, ok := c.works(prev); ok {
			if err := c.ensureName(prev.Key, u); err != nil {
				return nil, err
			}
			return prev, nil
		}
	}
	token, err := c.devToken()
	if err != nil {
		return nil, err
	}
	var u user
	if err := c.call("GET", "/user", token, nil, &u); err != nil {
		return nil, err
	}
	if err := c.ensureName(token, u); err != nil {
		return nil, err
	}
	dev := &state.Dev{Email: c.Email, UserID: u.ID}
	for _, root := range c.Roots {
		n, err := c.ensureRoot(token, root)
		if err != nil {
			return nil, err
		}
		dev.Nodes = append(dev.Nodes, n)
	}
	var key struct {
		Key string `json:"key"`
	}
	if err := c.call("POST", "/user/keys", token, map[string]string{"name": keyName}, &key); err != nil {
		return nil, err
	}
	dev.Key = key.Key
	return dev, nil
}

const keyName = "plat5 dev"

// user is GET /user's answer: name is null until it's set.
type user struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
}

// ensureName names the dev person with credential, a token or their key, while
// they have no name. A name already set, by the seed or by the person, stays.
func (c Client) ensureName(credential string, u user) error {
	if u.Name != nil || c.Name == "" {
		return nil
	}
	err := c.call("PATCH", "/user", credential, map[string]string{"name": c.Name}, nil)
	switch {
	case isStatus(err, http.StatusConflict):
		c.warn("the dev person has no name: users.name.sync is each_sign_in, so only the IdP's token names a person, and local Auth's tokens carry no name")
		return nil
	case IsNotFound(err):
		c.warn("the dev person has no name: the config has no PATCH /user")
		return nil
	}
	return err
}

type node struct {
	ID          string  `json:"id"`
	Slug        string  `json:"slug"`
	DisplayName string  `json:"display_name"`
	ParentID    *string `json:"parent_id"`
}

// ensureRoot is the dev person's root of this kind named NodeName, made on
// first use.
func (c Client) ensureRoot(token string, root RootKind) (state.DevNode, error) {
	list := "/user/nodes?kind=" + url.QueryEscape(root.Kind)
	path := list
	for {
		var page struct {
			Nodes   []node `json:"nodes"`
			HasMore bool   `json:"has_more"`
		}
		if err := c.call("GET", path, token, nil, &page); err != nil {
			return state.DevNode{}, err
		}
		for _, n := range page.Nodes {
			if n.ParentID == nil && n.DisplayName == c.NodeName {
				return root.devNode(n), nil
			}
		}
		if !page.HasMore || len(page.Nodes) == 0 {
			break
		}
		path = list + "&starting_after=" + url.QueryEscape(page.Nodes[len(page.Nodes)-1].ID)
	}
	var n node
	if err := c.call("POST", "/"+root.Collection, token, map[string]string{"display_name": c.NodeName}, &n); err != nil {
		return state.DevNode{}, err
	}
	return root.devNode(n), nil
}

func (r RootKind) devNode(n node) state.DevNode {
	return state.DevNode{Kind: r.Kind, ID: n.ID, Slug: n.Slug, Path: "/" + r.Collection + "/" + n.Slug}
}

// works reports whether the key still authenticates (the data may have been
// reset), and the person it acts as.
func (c Client) works(d *state.Dev) (user, bool) {
	var u user
	return u, d.Key != "" && c.call("GET", "/user", d.Key, nil, &u) == nil
}

func (c Client) devToken() (string, error) {
	body, _ := json.Marshal(map[string]string{"email": c.Email, "client_id": c.ClientID})
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

// call sends one request to the gateway with credential, a token or a key, as
// Authorization: Bearer.
func (c Client) call(method, path, credential string, in, out any) error {
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
	req.Header.Set("Authorization", "Bearer "+credential)
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
