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
	missing  string            // a route the config leaves out: 404
	failMe   bool
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
		authed := r.Header.Get("Authorization") == "Bearer jwt" || f.keys[r.Header.Get("X-API-Key")]
		if !authed {
			write(401, map[string]any{})
			return
		}
		switch route {
		case "GET /me":
			if f.failMe {
				write(503, map[string]any{"error": map[string]string{"code": "UNAVAILABLE"}})
				return
			}
			write(200, map[string]any{"id": "m_dev", "principal": "person", "name": "Dev User", "status": "active"})
		case "GET /me/nodes":
			if r.URL.Query().Get("kind") != "organization" {
				write(200, map[string]any{"nodes": []any{}, "has_more": false})
				return
			}
			write(200, map[string]any{"nodes": f.roots, "has_more": false})
		case "POST /orgs":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.created++
			n := map[string]any{"id": "n_org", "kind": "organization", "slug": "dev", "label": body["label"], "parent_id": nil}
			f.roots = append(f.roots, n)
			write(201, n)
		case "POST /me/keys":
			f.keys["plat5-k-2-dev"] = true
			write(201, map[string]string{"id": "k_1", "key": "plat5-k-2-dev"})
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
		Name:       "Dev User",
		NodeLabel:  "Dev",
		Roots:      []RootKind{{Kind: "organization", Segment: "orgs"}},
	}
}

func TestEnsureSeedsOnce(t *testing.T) {
	f, c := newFake(t)
	dev, err := Ensure(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dev.Key != "plat5-k-2-dev" || dev.MemberID != "m_dev" || len(dev.Nodes) != 1 {
		t.Fatalf("%+v", dev)
	}
	if n := dev.Nodes[0]; n.Kind != "organization" || n.ID != "n_org" || n.Path != "/orgs/dev" {
		t.Fatalf("node: %+v", n)
	}
	// The name rides in the dev token as its name claim; there is no name write.
	if f.token["name"] != "Dev User" || f.token["email"] != "dev@example.com" || f.token["client_id"] != "plat5" {
		t.Fatalf("dev token body: %v", f.token)
	}
	for _, r := range f.requests {
		if strings.HasPrefix(r, "PATCH") {
			t.Fatalf("no writes beyond the node and key: %v", f.requests)
		}
	}

	// A second start reuses the state while the key works.
	f.requests = nil
	again, err := Ensure(c, dev)
	if err != nil {
		t.Fatal(err)
	}
	if again != dev || strings.Join(f.requests, ",") != "GET /me" {
		t.Fatalf("reused: %v, requests %v", again == dev, f.requests)
	}
}

func TestEnsureReusesTheNodeByLabel(t *testing.T) {
	f, c := newFake(t)
	f.roots = []map[string]any{
		{"id": "n_other", "slug": "acme", "label": "Acme", "parent_id": nil},
		{"id": "n_old", "slug": "dev-2", "label": "Dev", "parent_id": nil},
	}
	// A lost state file, or a reset key: the node is found, not made again.
	dev, err := Ensure(c, &state.Dev{Email: "dev@example.com", Key: "plat5-k-2-gone"})
	if err != nil {
		t.Fatal(err)
	}
	if f.created != 0 || dev.Nodes[0].ID != "n_old" || dev.Nodes[0].Path != "/orgs/dev-2" {
		t.Fatalf("created %d, %+v", f.created, dev.Nodes)
	}
}

func TestEnsureReportsGatewayErrors(t *testing.T) {
	f, c := newFake(t)
	f.failMe = true
	if _, err := Ensure(c, nil); err == nil || !strings.Contains(err.Error(), "GET /me") {
		t.Fatalf("want the failing call named, got %v", err)
	}
	f.failMe = false
	f.missing = "POST /me/keys"
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
