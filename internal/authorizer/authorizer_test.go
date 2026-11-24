package authorizer

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSyncTopazManifestAndWaitReady(t *testing.T) {
	var gotCT, gotBody string
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/directory/manifest" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodGet {
			calls++
			if calls == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodPost {
			gotCT = r.Header.Get("Content-Type")
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()

	c := Client{HTTP: srv.Client()}
	if err := c.WaitTopazReady(srv.URL, 5*time.Second); err != nil {
		t.Fatalf("WaitTopazReady: %v", err)
	}
	manifestPath := filepath.Join(t.TempDir(), "manifest.yaml")
	const manifest = "model:\n  version: 3\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.SyncTopazManifest(srv.URL, manifestPath); err != nil {
		t.Fatalf("SyncTopazManifest: %v", err)
	}
	if gotCT != "application/yaml" || gotBody != manifest {
		t.Fatalf("ct=%q body=%q", gotCT, gotBody)
	}
}

func TestInvalidateGatewayCache(t *testing.T) {
	var got InvalidateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/internal/authorizer-cache/invalidate" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"invalidated":true}`))
	}))
	defer srv.Close()

	c := Client{HTTP: srv.Client()}
	if err := c.InvalidateGatewayCache(srv.URL, InvalidateRequest{}); err == nil || !strings.Contains(err.Error(), "--all") {
		t.Fatalf("empty request should fail, got %v", err)
	}
	req := InvalidateRequest{Authorizer: "topaz", ResourceType: "document", ResourceID: "doc-1"}
	if err := c.InvalidateGatewayCache(srv.URL, req); err != nil {
		t.Fatalf("InvalidateGatewayCache: %v", err)
	}
	if got != req {
		t.Fatalf("got %+v, want %+v", got, req)
	}
}
