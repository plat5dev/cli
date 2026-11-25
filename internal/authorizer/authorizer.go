// Package authorizer talks to a local Topaz Directory v3 API to upload ReBAC
// manifests.
package authorizer

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client talks to Topaz's Directory v3 REST API.
type Client struct {
	HTTP *http.Client
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// WaitTopazReady waits until Topaz's Directory v3 REST gateway answers HTTP requests.
func (c Client) WaitTopazReady(directoryURL string, wait time.Duration) error {
	base := strings.TrimRight(directoryURL, "/")
	deadline := time.Now().Add(wait)
	var lastErr error
	for {
		resp, err := c.http().Get(base + "/api/v3/directory/manifest")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
			lastErr = fmt.Errorf("topaz directory answered %s", resp.Status)
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("topaz directory at %s not ready: %w", base, lastErr)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// SyncTopazManifest uploads a YAML ReBAC manifest to Topaz's Directory v3 REST API.
func (c Client) SyncTopazManifest(directoryURL, manifestPath string) error {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest %s: %w", manifestPath, err)
	}
	return c.SyncTopazManifestBytes(directoryURL, data)
}

// SyncTopazManifestBytes uploads raw YAML manifest bytes to Topaz's Directory v3 REST API.
func (c Client) SyncTopazManifestBytes(directoryURL string, data []byte) error {
	u := strings.TrimRight(directoryURL, "/") + "/api/v3/directory/manifest"
	req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/yaml")
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("upload manifest to %s: %w", u, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("topaz manifest upload (%s): %s: %s", u, resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}
