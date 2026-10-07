package upstreams

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Expand turns a plat5.yml upstream value into a route-config url, exactly
// http://host:port (no path, no query, no other scheme).
//
//	3000                             → http://host.docker.internal:3000  (host process, gateway in Docker)
//	localhost:3000                   → http://localhost:3000
//	127.0.0.1:3000                   → http://127.0.0.1:3000
//	api:3000                         → http://api:3000
//	http://host.docker.internal:3000 → http://host.docker.internal:3000
func Expand(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("empty upstream")
	}

	if port, err := strconv.Atoi(s); err == nil {
		if port < 1 || port > 65535 {
			return "", fmt.Errorf("invalid port %d", port)
		}
		return fmt.Sprintf("http://host.docker.internal:%d", port), nil
	}

	hostPort := s
	if scheme, rest, ok := strings.Cut(s, "://"); ok {
		switch strings.ToLower(scheme) {
		case "http":
		case "https":
			return "", fmt.Errorf("TLS (https) upstreams aren't supported yet; use http://host:port")
		default:
			return "", fmt.Errorf("invalid upstream %q: unsupported scheme %q; use http://host:port", raw, scheme)
		}
		hostPort = rest
	}
	if strings.ContainsAny(hostPort, "/?#") {
		return "", fmt.Errorf("invalid upstream %q: use http://host:port (no path or query)", raw)
	}

	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		return "", fmt.Errorf("invalid upstream %q: missing port; use http://host:port", raw)
	}
	if host == "" {
		return "", fmt.Errorf("invalid upstream %q", raw)
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return "", fmt.Errorf("invalid port in upstream %q", raw)
	}
	return "http://" + net.JoinHostPort(host, port), nil
}

// ParseMap normalizes yaml upstream values (int or string) to strings.
func ParseMap(raw map[string]any) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(raw))
	for name, v := range raw {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("upstreams: empty service name")
		}
		s, err := scalarString(v)
		if err != nil {
			return nil, fmt.Errorf("upstreams.%s: %w", name, err)
		}
		if _, err := Expand(s); err != nil {
			return nil, fmt.Errorf("upstreams.%s: %w", name, err)
		}
		out[name] = s
	}
	return out, nil
}

func scalarString(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", fmt.Errorf("value is empty")
	case string:
		if strings.TrimSpace(t) == "" {
			return "", fmt.Errorf("value is empty")
		}
		return strings.TrimSpace(t), nil
	case int:
		return strconv.Itoa(t), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case uint64:
		return strconv.FormatUint(t, 10), nil
	case float64:
		if t != float64(int64(t)) {
			return "", fmt.Errorf("expected port or string, got %v", t)
		}
		return strconv.FormatInt(int64(t), 10), nil
	default:
		return "", fmt.Errorf("expected port or string, got %T", v)
	}
}

// routesFile is the routes.yml shape we patch (url only).
type routesFile struct {
	Services map[string]map[string]any `yaml:"services"`
}

// Bind injects expanded upstream addresses into a routes document.
// Matching service names get url set (overwrites file url). Other fields unchanged.
// It returns the upstream names it used, so callers can warn about unused ones.
// A service left with no url is an error that points at plat5.yml (the
// route-registry's own message doesn't know about plat5.yml).
// Returns the original bytes when nothing was bound.
func Bind(data []byte, ups map[string]string) ([]byte, []string, error) {
	var doc routesFile
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("parse routes: %w", err)
	}
	if doc.Services == nil {
		return nil, nil, fmt.Errorf("parse routes: missing services")
	}

	expanded := make(map[string]string, len(ups))
	for name, raw := range ups {
		u, err := Expand(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("upstreams.%s: %w", name, err)
		}
		expanded[name] = u
	}

	var used []string
	names := make([]string, 0, len(doc.Services))
	for name := range doc.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		svc := doc.Services[name]
		u, ok := expanded[name]
		if !ok {
			if s, _ := svc["url"].(string); strings.TrimSpace(s) == "" {
				return nil, nil, fmt.Errorf("service %q has no url: add it under upstreams: in plat5.yml, or set url in the routes file", name)
			}
			continue
		}
		if svc == nil {
			svc = map[string]any{}
			doc.Services[name] = svc
		}
		svc["url"] = u
		used = append(used, name)
	}
	if len(used) == 0 {
		return data, nil, nil
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, nil, err
	}
	return out, used, nil
}

// Unused returns the upstreams keys not in used, sorted.
func Unused(ups map[string]string, used map[string]bool) []string {
	var out []string
	for name := range ups {
		if !used[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
