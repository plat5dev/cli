package devseed

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/plat5dev/cli/internal/state"
)

// fake is the gateway and local Auth, enough for Ensure.
type fake struct {
	mu       sync.Mutex
	roots    []map[string]any // the dev person's roots of kind organization
	keys     map[string]bool
	created  int
	requests []string
	token    map[string]string // the last /dev/token body
	name     any               // the dev person's name: nil until set
	ownsName bool              // users.name.sync is each_sign_in: PATCH /user is 409
	missing  string            // a route the config leaves out: 404
	failUser bool
}

func (f *fake) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		route := r.Method + " " + r.URL.Path
		f.requests = append(f.requests, route)
		write := func(code int, v any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(v)
		}
		if route == f.missing {
			write(404, map[string]any{"error": map[string]string{"code": "NOT_FOUND"}})
			return
		}
		if route == "POST /dev/token" {
			_ = json.NewDecoder(r.Body).Decode(&f.token)
			write(200, map[string]string{"access_token": "jwt"})
			return
		}
		credential, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if credential != "jwt" && !f.keys[credential] {
			write(401, map[string]any{})
			return
		}
		switch route {
		case "GET /user":
			if f.failUser {
				write(503, map[string]any{"error": map[string]string{"code": "UNAVAILABLE"}})
				return
			}
			write(200, map[string]any{"id": "01jbx9q3t5vk8m2n4p6r7s8t9w", "name": f.name, "status": "active"})
		case "PATCH /user":
			if f.ownsName {
				write(409, map[string]any{"error": map[string]string{"code": "CONFLICT"}})
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.name = body["name"]
			write(200, map[string]any{"id": "01jbx9q3t5vk8m2n4p6r7s8t9w", "name": f.name, "status": "active"})
		case "GET /user/nodes":
			if r.URL.Query().Get("kind") != "organization" {
				write(200, map[string]any{"nodes": []any{}, "has_more": false})
				return
			}
			write(200, map[string]any{"nodes": f.roots, "has_more": false})
		case "POST /orgs":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.created++
			n := map[string]any{"id": "01jbx9q3t5vk8m2n4p6r7s8t90", "kind": "organization", "slug": "dev", "display_name": body["display_name"], "parent_id": nil}
			f.roots = append(f.roots, n)
			write(201, n)
		case "POST /user/keys":
			f.keys["plat5-k-2-dev"] = true
			write(201, map[string]string{"id": "01jbx9q3t5vk8m2n4p6r7s8t91", "key": "plat5-k-2-dev"})
		default:
			write(404, map[string]any{})
		}
	})
}

func newFake(t *testing.T) (*fake, Client) {
	f := &fake{keys: map[string]bool{}}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return f, Client{
		GatewayURL: srv.URL,
		AuthURL:    srv.URL,
		ClientID:   "plat5",
		Email:      "dev@example.com",
		Name:       "Dev Person",
		NodeName:   "Dev",
		Roots:      []RootKind{{Kind: "organization", Collection: "orgs"}},
	}
}

func TestEnsureSeedsOnce(t *testing.T) {
	f, c := newFake(t)
	dev, err := Ensure(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dev.Key != "plat5-k-2-dev" || dev.UserID != "01jbx9q3t5vk8m2n4p6r7s8t9w" || len(dev.Nodes) != 1 {
		t.Fatalf("%+v", dev)
	}
	if n := dev.Nodes[0]; n.Kind != "organization" || n.ID != "01jbx9q3t5vk8m2n4p6r7s8t90" || n.Path != "/orgs/dev" {
		t.Fatalf("node: %+v", n)
	}
	if f.roots[0]["display_name"] != "Dev" {
		t.Fatalf("created %v", f.roots[0])
	}
	// Local Auth's tokens carry no name: the seed asks for none, and sets it
	// with PATCH /user after the first GET /user admits the person.
	if _, ok := f.token["name"]; ok || f.token["email"] != "dev@example.com" || f.token["client_id"] != "plat5" || len(f.token) != 2 {
		t.Fatalf("dev token body: %v", f.token)
	}
	if f.name != "Dev Person" {
		t.Fatalf("name %v", f.name)
	}
	if got := strings.Join(f.requests[:3], ","); got != "POST /dev/token,GET /user,PATCH /user" {
		t.Fatalf("requests %v", f.requests)
	}

	// A second start reuses the state while the key works.
	f.requests = nil
	again, err := Ensure(c, dev)
	if err != nil {
		t.Fatal(err)
	}
	if again != dev || strings.Join(f.requests, ",") != "GET /user" {
		t.Fatalf("reused: %v, requests %v", again == dev, f.requests)
	}
}

func TestEnsureReusesTheNodeByName(t *testing.T) {
	f, c := newFake(t)
	f.roots = []map[string]any{
		{"id": "01jbx9q3t5vk8m2n4p6r7s8t92", "slug": "acme", "display_name": "Acme", "parent_id": nil},
		{"id": "01jbx9q3t5vk8m2n4p6r7s8t93", "slug": "dev-2", "display_name": "Dev", "parent_id": nil},
	}
	// A lost state file, or a reset key: the node is found, not made again.
	dev, err := Ensure(c, &state.Dev{Email: "dev@example.com", Key: "plat5-k-2-gone"})
	if err != nil {
		t.Fatal(err)
	}
	if f.created != 0 || dev.Nodes[0].ID != "01jbx9q3t5vk8m2n4p6r7s8t93" || dev.Nodes[0].Path != "/orgs/dev-2" {
		t.Fatalf("created %d, %+v", f.created, dev.Nodes)
	}
}

func TestEnsureReportsGatewayErrors(t *testing.T) {
	f, c := newFake(t)
	f.failUser = true
	if _, err := Ensure(c, nil); err == nil || !strings.Contains(err.Error(), "GET /user") {
		t.Fatalf("want the failing call named, got %v", err)
	}
	f.failUser = false
	f.missing = "POST /user/keys"
	if _, err := Ensure(c, nil); err == nil || !IsNotFound(err) {
		t.Fatalf("a missing route is a 404 error for start to warn about, got %v", err)
	}
}

func TestEnsureEveryRootKind(t *testing.T) {
	f, c := newFake(t)
	c.Roots = nil
	dev, err := Ensure(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(dev.Nodes) != 0 || f.created != 0 || dev.Key == "" {
		t.Fatalf("no root kinds: no nodes, still a key: %+v", dev)
	}
}

// A name already set, by the person or an earlier seed, stays; a seed whose
// state was lost, or whose key was reset, makes no second PATCH.
func TestEnsureKeepsAName(t *testing.T) {
	f, c := newFake(t)
	f.name = "Ada"
	if _, err := Ensure(c, nil); err != nil {
		t.Fatal(err)
	}
	if f.name != "Ada" {
		t.Fatalf("name %v", f.name)
	}
	for _, r := range f.requests {
		if r == "PATCH /user" {
			t.Fatalf("renamed: %v", f.requests)
		}
	}
}

// Under each_sign_in the IdP names people: the seed doesn't call PATCH /user
// and says nothing about names, on a first start or a later one.
func TestEnsureLeavesTheNameToTheIdP(t *testing.T) {
	f, c := newFake(t)
	f.ownsName = true
	c.IdPNames = true
	var warnings []string
	c.Warn = func(msg string) { warnings = append(warnings, msg) }
	dev, err := Ensure(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Ensure(c, dev)
	if err != nil {
		t.Fatal(err)
	}
	if dev.Key == "" || len(dev.Nodes) != 1 || again != dev || f.name != nil || len(warnings) != 0 {
		t.Fatalf("%+v, reused %v, name %v, warnings %v", dev, again == dev, f.name, warnings)
	}
	for _, r := range f.requests {
		if r == "PATCH /user" {
			t.Fatalf("named: %v", f.requests)
		}
	}
}

// Under first_sign_in, identity still running with each_sign_in refuses
// PATCH /user with a 409: a warning, and the rest is seeded. A later start,
// identity caught up, names the person with the key it kept.
func TestEnsureWarnsWhenIdentityRefusesTheName(t *testing.T) {
	f, c := newFake(t)
	f.ownsName = true
	var warnings []string
	c.Warn = func(msg string) { warnings = append(warnings, msg) }
	dev, err := Ensure(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dev.Key == "" || len(dev.Nodes) != 1 || f.name != nil {
		t.Fatalf("%+v, name %v", dev, f.name)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "409") {
		t.Fatalf("warnings %v", warnings)
	}

	f.ownsName = false
	f.requests = nil
	again, err := Ensure(c, dev)
	if err != nil {
		t.Fatal(err)
	}
	if again != dev || f.name != "Dev Person" || strings.Join(f.requests, ",") != "GET /user,PATCH /user" {
		t.Fatalf("reused: %v, name %v, requests %v", again == dev, f.name, f.requests)
	}
}

// A config without PATCH /user leaves the person unnamed, with a warning.
func TestEnsureWithoutTheNameRoute(t *testing.T) {
	f, c := newFake(t)
	f.missing = "PATCH /user"
	var warnings []string
	c.Warn = func(msg string) { warnings = append(warnings, msg) }
	dev, err := Ensure(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dev.Key == "" || f.name != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "PATCH /user") {
		t.Fatalf("%+v, name %v, warnings %v", dev, f.name, warnings)
	}
}
