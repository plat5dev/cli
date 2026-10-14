// Package devseed gives a fresh local stack something to call with: the dev person,
// an org they own, and a key for each. It goes through the gateway's public routes
// like any client, with a token from local Auth's dev mode. Nothing here reaches
// past the gateway.
package devseed

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/plat5dev/cli/internal/state"
)

// Client reaches the local stack.
type Client struct {
	GatewayURL string
	AuthURL    string
	// ClientID is the Auth client the dev token is minted for (the gateway's audience).
	ClientID string
	Email    string
	OrgName  string
	HTTP     *http.Client
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// Ensure returns working dev keys: prev's while both still work, else new ones.
// The org is reused by name, so a lost state file does not make a second one.
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
	if err := c.call("GET", "/user", bearer, nil, &me); err != nil {
		return nil, err
	}

	orgID, err := c.findOrg(bearer)
	if err != nil {
		return nil, err
	}
	if orgID == "" {
		var org struct {
			ID string `json:"id"`
		}
		if err := c.call("POST", "/user/organizations", bearer, map[string]string{"name": c.OrgName}, &org); err != nil {
			return nil, err
		}
		orgID = org.ID
	}

	var session struct {
		Token    string `json:"token"`
		MemberID string `json:"member_id"`
	}
	if err := c.call("POST", "/user/organizations/"+orgID+"/session", bearer, nil, &session); err != nil {
		return nil, err
	}
	var memberKey, userKey struct {
		Key string `json:"key"`
	}
	name := map[string]string{"name": "plat5 dev"}
	if err := c.call("POST", "/member/api-keys", header{"X-API-Key", session.Token}, name, &memberKey); err != nil {
		return nil, err
	}
	if err := c.call("POST", "/user/api-keys", bearer, name, &userKey); err != nil {
		return nil, err
	}
	return &state.Dev{
		Email:          c.Email,
		UserID:         me.ID,
		OrganizationID: orgID,
		MemberID:       session.MemberID,
		UserKey:        userKey.Key,
		MemberKey:      memberKey.Key,
	}, nil
}

// works reports whether both keys still authenticate (the data may have been reset).
func (c Client) works(d *state.Dev) bool {
	if d.UserKey == "" || d.MemberKey == "" {
		return false
	}
	return c.call("GET", "/user", header{"X-API-Key", d.UserKey}, nil, nil) == nil &&
		c.call("GET", "/member", header{"X-API-Key", d.MemberKey}, nil, nil) == nil
}

func (c Client) findOrg(bearer header) (string, error) {
	var list struct {
		Memberships []struct {
			Organization struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"organization"`
		} `json:"memberships"`
	}
	if err := c.call("GET", "/user/memberships", bearer, nil, &list); err != nil {
		return "", err
	}
	for _, m := range list.Memberships {
		if m.Organization.Name == c.OrgName {
			return m.Organization.ID, nil
		}
	}
	return "", nil
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
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(data)))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: %w", method, path, err)
		}
	}
	return nil
}
