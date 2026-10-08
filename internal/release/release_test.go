package release

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLatestPicksHighestSemver(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"name":"v0.10.0"},{"name":"not-a-tag"}]`))
			return
		}
		w.Header().Set("Link", "<"+srv.URL+"/repos/plat5dev/plat5/tags?page=2>; rel=\"next\"")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name":"v0.4.4"},{"name":"v0.4.10"},{"name":"v0.4.4-rc1"}]`))
	}))
	defer srv.Close()

	got, err := (Client{HTTP: srv.Client(), Base: srv.URL}).Latest("plat5dev/plat5")
	if err != nil {
		t.Fatal(err)
	}
	if got != "v0.10.0" {
		t.Fatalf("got %s", got)
	}
}

func TestLatestNone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"latest"}]`))
	}))
	defer srv.Close()
	_, err := (Client{HTTP: srv.Client(), Base: srv.URL}).Latest("plat5dev/plat5")
	if err == nil || !strings.Contains(err.Error(), "no vX.Y.Z tag") {
		t.Fatalf("err=%v", err)
	}
}
