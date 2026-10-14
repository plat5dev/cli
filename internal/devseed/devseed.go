// Package devseed gives a fresh local stack something to call with: the dev person,
// an org they own, a member key in it, and a user key when the project publishes
// that route. It goes through the gateway's public routes
// like any client, with a token from local Auth's dev mode. Nothing here reaches
// past the gateway.
package devseed

import (
	"bytes"
	"encoding/json"
	"errors"
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

// Ensure returns working dev keys: prev's while they still work, else new ones.
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
	member := header{"X-API-Key", session.Token}
	memberKey, err := c.memberKey(member)
	if err != nil {
		return nil, err
	}
	// A project may leave user keys out of its routes; the dev person then has none.
	var userKey struct {
		Key string `json:"key"`
	}
	if err := c.call("POST", "/user/api-keys", bearer, map[string]string{"name": keyName}, &userKey); err != nil && !isNotFound(err) {
		return nil, err
	}
	return &state.Dev{
		Email:          c.Email,
		UserID:         me.ID,
		OrganizationID: orgID,
		MemberID:       session.MemberID,
		UserKey:        userKey.Key,
		MemberKey:      memberKey,
	}, nil
}

const keyName = "plat5 dev"

// memberKey mints a key in the dev org with the session: the dev member's own, or,
// when the project leaves member keys out of its routes, a service account's. The
// service account holds the creator role, so the key can call what the owner can.
func (c Client) memberKey(session header) (string, error) {
	var out struct {
		Key string `json:"key"`
	}
	name := map[string]string{"name": keyName}
	err := c.call("POST", "/member/api-keys", session, name, &out)
	if !isNotFound(err) {
		return out.Key, err
	}
	saID, err := c.serviceAccount(session)
	if err != nil {
		return "", err
	}
	if err := c.call("POST", "/org/service-accounts/"+saID+"/api-keys", session, name, &out); err != nil {
		return "", err
	}
	return out.Key, nil
}

// serviceAccount is the dev org's "plat5 dev" service account, made on first use.
func (c Client) serviceAccount(session header) (string, error) {
	var list struct {
		ServiceAccounts []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"service_accounts"`
	}
	if err := c.call("GET", "/org/service-accounts", session, nil, &list); err != nil {
		return "", err
	}
	for _, sa := range list.ServiceAccounts {
		if sa.Name == keyName {
			return sa.ID, nil
		}
	}
	var roles struct {
		CreatorRole *string `json:"creator_role"`
	}
	if err := c.call("GET", "/org/roles", session, nil, &roles); err != nil {
		return "", err
	}
	body := map[string]any{"name": keyName}
	if roles.CreatorRole != nil {
		body["role"] = *roles.CreatorRole
	}
	var sa struct {
		ID string `json:"id"`
	}
	if err := c.call("POST", "/org/service-accounts", session, body, &sa); err != nil {
		return "", err
	}
	return sa.ID, nil
}

// works reports whether the keys still authenticate (the data may have been reset).
// A probe route the project does not publish is 404 before any key is checked, so
// the member key tries the next one. 403 still means the key authenticated.
func (c Client) works(d *state.Dev) bool {
	if d.MemberKey == "" {
		return false
	}
	if d.UserKey != "" && c.call("GET", "/user", header{"X-API-Key", d.UserKey}, nil, nil) != nil {
		return false
	}
	for _, path := range []string{"/member", "/org/roles", "/org"} {
		err := c.call("GET", path, header{"X-API-Key", d.MemberKey}, nil, nil)
		if !isNotFound(err) {
			return err == nil || isStatus(err, http.StatusForbidden)
		}
	}
	return false
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

// isNotFound is the gateway's answer for a route the project does not publish.
func isNotFound(err error) bool { return isStatus(err, http.StatusNotFound) }
