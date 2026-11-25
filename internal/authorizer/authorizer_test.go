package authorizer

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
