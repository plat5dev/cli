package devseed

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/plat5dev/cli/internal/state"
)

// fake is the gateway and Auth, enough for Ensure.
type fake struct {
	orgs      []string // names of orgs the dev person is in
	keys      map[string]bool
	created   int
	requests  []string
	sawBearer bool
	// serviceAccountsOnly: the project publishes no /member or key routes of its
	// own, only the org's service accounts (like a product that sells to machines).
	serviceAccountsOnly bool
	saBody              map[string]any
	name                string
}

func (f *fake) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		write := func(code int, v any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(v)
		}
		if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			f.sawBearer = true
		}
		route := r.Method + " " + r.URL.Path
		if f.serviceAccountsOnly {
			switch route {
			case "GET /member", "POST /member/api-keys", "POST /user/api-keys":
				write(404, map[string]string{})
				return
			}
		}
		switch route {
		case "GET /org/service-accounts":
			write(200, map[string]any{"service_accounts": []any{}})
		case "GET /org/roles":
			if k := r.Header.Get("X-API-Key"); k != "plat5-s-2-x" && !f.keys[k] {
				write(401, map[string]string{})
				return
			}
			write(200, map[string]any{"creator_role": "admin"})
		case "POST /org/service-accounts":
			_ = json.NewDecoder(r.Body).Decode(&f.saBody)
			write(201, map[string]string{"id": "sa_1"})
		case "POST /org/service-accounts/sa_1/api-keys":
			f.keys["plat5-k-2-sa"] = true
			write(201, map[string]string{"key": "plat5-k-2-sa"})
		case "POST /dev/token":
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["email"] != "dev@example.com" || in["client_id"] != "plat5" {
				t.Errorf("dev token body %v", in)
			}
			write(200, map[string]string{"access_token": "jwt"})
		case "GET /user":
			if k := r.Header.Get("X-API-Key"); k != "" && !f.keys[k] {
				write(401, map[string]string{})
				return
			}
			write(200, map[string]any{"id": "usr_1", "display_name": nil})
		case "PATCH /user":
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.name = in["display_name"]
			write(200, map[string]string{"id": "usr_1"})
		case "GET /member":
			if !f.keys[r.Header.Get("X-API-Key")] {
				write(401, map[string]string{})
				return
			}
			write(200, map[string]string{"id": "mem_1"})
		case "GET /user/memberships":
			var ms []map[string]any
			for i, name := range f.orgs {
				ms = append(ms, map[string]any{"organization": map[string]string{"id": "org_" + string(rune('a'+i)), "name": name}})
			}
			write(200, map[string]any{"memberships": ms})
		case "POST /user/organizations":
			f.created++
			f.orgs = append(f.orgs, "Dev")
			write(201, map[string]string{"id": "org_new"})
		case "POST /user/organizations/org_new/session", "POST /user/organizations/org_b/session":
			write(201, map[string]string{"token": "plat5-s-2-x", "member_id": "mem_1"})
		case "POST /member/api-keys":
			if r.Header.Get("X-API-Key") != "plat5-s-2-x" {
				t.Errorf("member key minted without the session")
			}
			f.keys["plat5-k-2-member"] = true
			write(201, map[string]string{"key": "plat5-k-2-member"})
		case "POST /user/api-keys":
			f.keys["plat5-k-2-user"] = true
			write(201, map[string]string{"key": "plat5-k-2-user"})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			write(404, map[string]string{})
		}
	})
}

func client(url string) Client {
	return Client{GatewayURL: url, AuthURL: url, ClientID: "plat5", Email: "dev@example.com", Name: "Dev User", OrgName: "Dev"}
}

func TestEnsureSeedsOnce(t *testing.T) {
	f := &fake{keys: map[string]bool{}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	d, err := Ensure(client(srv.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.UserKey != "plat5-k-2-user" || d.MemberKey != "plat5-k-2-member" || d.OrganizationID != "org_new" || d.UserID != "usr_1" {
		t.Fatalf("%+v", d)
	}
	if f.created != 1 {
		t.Fatalf("created %d orgs", f.created)
	}
	if f.name != "Dev User" {
		t.Fatalf("display name %q", f.name)
	}

	// Keys still work: nothing new.
	f.requests = nil
	again, err := Ensure(client(srv.URL), d)
	if err != nil || again != d {
		t.Fatalf("%+v %v", again, err)
	}
	for _, r := range f.requests {
		if strings.HasPrefix(r, "POST") {
			t.Fatalf("reseeded: %v", f.requests)
		}
	}
}

func TestEnsureReusesOrgByName(t *testing.T) {
	f := &fake{keys: map[string]bool{}, orgs: []string{"Other", "Dev"}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	// Old keys no longer work (the data was reset or the keys revoked).
	d, err := Ensure(client(srv.URL), &state.Dev{Email: "dev@example.com", UserKey: "gone", MemberKey: "gone"})
	if err != nil {
		t.Fatal(err)
	}
	if f.created != 0 || d.OrganizationID != "org_b" {
		t.Fatalf("created %d, org %s", f.created, d.OrganizationID)
	}
}

func TestEnsureReportsGatewayErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dev/token" {
			_, _ = w.Write([]byte(`{"access_token":"jwt"}`))
			return
		}
		http.Error(w, `{"error":{"code":"NOT_FOUND"}}`, http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := Ensure(client(srv.URL), nil)
	if err == nil || !strings.Contains(err.Error(), "GET /user: 404") {
		t.Fatalf("want a 404 naming the route, got %v", err)
	}
}

func TestEnsureFallsBackToAServiceAccount(t *testing.T) {
	f := &fake{keys: map[string]bool{}, serviceAccountsOnly: true}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	d, err := Ensure(client(srv.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.MemberKey != "plat5-k-2-sa" || d.UserKey != "" {
		t.Fatalf("%+v", d)
	}
	if f.saBody["role"] != "admin" || f.saBody["name"] != "plat5 dev" {
		t.Fatalf("service account body %v", f.saBody)
	}

	// The key still works, through a probe route the project publishes.
	f.requests = nil
	again, err := Ensure(client(srv.URL), d)
	if err != nil || again != d {
		t.Fatalf("%+v %v", again, err)
	}
	for _, r := range f.requests {
		if strings.HasPrefix(r, "POST") {
			t.Fatalf("reseeded: %v", f.requests)
		}
	}
}
