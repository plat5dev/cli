// Package release finds the latest vX.Y.Z git tag on a plat5dev repo.
//
// Image workflows tag the repo. They do not publish a GitHub Release, so
// /releases/latest is not the pin.
package release

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	RepoPlat5    = "plat5dev/plat5"
	RepoAuth     = "plat5dev/auth"
	RepoOperator = "plat5dev/operator"
)

var semverTag = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// Client talks to the GitHub API. Zero value uses api.github.com.
type Client struct {
	HTTP *http.Client
	Base string
}

func (c Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c Client) base() string {
	if c.Base != "" {
		return strings.TrimRight(c.Base, "/")
	}
	return "https://api.github.com"
}

// Latest is the highest vX.Y.Z tag in repo ("owner/name").
func Latest(repo string) (string, error) {
	return Client{}.Latest(repo)
}

// Latest is the highest vX.Y.Z tag in repo ("owner/name").
func (c Client) Latest(repo string) (string, error) {
	if repo == "" || strings.Count(repo, "/") != 1 {
		return "", fmt.Errorf("release: repo must be owner/name, got %q", repo)
	}
	url := c.base() + "/repos/" + repo + "/tags?per_page=100"
	var best string
	for page := 0; url != "" && page < 10; page++ {
		names, next, err := c.tagPage(url)
		if err != nil {
			return "", fmt.Errorf("latest tag %s: %w", repo, err)
		}
		for _, name := range names {
			if !semverTag.MatchString(name) {
				continue
			}
			if best == "" || tagLess(best, name) {
				best = name
			}
		}
		url = next
	}
	if best == "" {
		return "", fmt.Errorf("latest tag %s: no vX.Y.Z tag", repo)
	}
	return best, nil
}

func (c Client) tagPage(url string) (names []string, next string, err error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "plat5-cli")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var tags []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &tags); err != nil {
		return nil, "", err
	}
	names = make([]string, 0, len(tags))
	for _, t := range tags {
		names = append(names, t.Name)
	}
	return names, nextLink(resp.Header.Get("Link")), nil
}

func nextLink(header string) string {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start >= 0 && end > start {
			return part[start+1 : end]
		}
	}
	return ""
}

// tagLess reports whether a is an older vX.Y.Z tag than b.
func tagLess(a, b string) bool {
	ap, aok := parseTag(a)
	bp, bok := parseTag(b)
	if !aok || !bok {
		return a < b
	}
	for i := 0; i < 3; i++ {
		if ap[i] != bp[i] {
			return ap[i] < bp[i]
		}
	}
	return false
}

func parseTag(s string) ([3]int, bool) {
	m := semverTag.FindStringSubmatch(s)
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}
