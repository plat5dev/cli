package upstreams

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Expand turns a plat5.yml upstream value into a gateway peer address (host:port).
// The gateway HttpPeer expects host:port without a scheme (same as platform services).
//
//	3000                         → host.docker.internal:3000  (host process, gateway in Docker)
//	localhost:3000               → localhost:3000
//	127.0.0.1:3000               → 127.0.0.1:3000
//	http://host.docker.internal:3000 → host.docker.internal:3000
//	api:3000                     → api:3000
func Expand(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("empty upstream")
	}

	if port, err := strconv.Atoi(s); err == nil {
		if port < 1 || port > 65535 {
			return "", fmt.Errorf("invalid port %d", port)
		}
		return fmt.Sprintf("host.docker.internal:%d", port), nil
	}

	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return "", fmt.Errorf("invalid upstream URL %q", raw)
		}
		host := u.Host
		if _, _, err := net.SplitHostPort(host); err != nil {
			// host without port
			switch u.Scheme {
			case "https":
				host = net.JoinHostPort(u.Hostname(), "443")
			case "http":
				host = net.JoinHostPort(u.Hostname(), "80")
			default:
				return "", fmt.Errorf("invalid upstream URL %q: missing port", raw)
			}
		}
		return host, nil
	}

	// host:port or bare host
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		if strings.Contains(s, "/") {
			return "", fmt.Errorf("invalid upstream %q: use host:port or bare port", raw)
		}
		return "", fmt.Errorf("invalid upstream %q: missing port", raw)
	}
	if host == "" {
		return "", fmt.Errorf("invalid upstream %q", raw)
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return "", fmt.Errorf("invalid port in upstream %q", raw)
	}
	return net.JoinHostPort(host, port), nil
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
// Returns the original data when upstreams is empty.
func Bind(data []byte, ups map[string]string) ([]byte, error) {
	if len(ups) == 0 {
		return data, nil
	}

	var doc routesFile
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse routes: %w", err)
	}
	if doc.Services == nil {
		return nil, fmt.Errorf("parse routes: missing services")
	}

	expanded := make(map[string]string, len(ups))
	for name, raw := range ups {
		u, err := Expand(raw)
		if err != nil {
			return nil, fmt.Errorf("upstreams.%s: %w", name, err)
		}
		expanded[name] = u
	}

	matched := 0
	for name, svc := range doc.Services {
		u, ok := expanded[name]
		if !ok {
			continue
		}
		if svc == nil {
			svc = map[string]any{}
			doc.Services[name] = svc
		}
		svc["url"] = u
		matched++
	}
	if matched == 0 {
		// Still allow apply — file may be platform-only; upstreams apply to other files.
		return data, nil
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, err
	}
	return out, nil
}
