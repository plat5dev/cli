// Package ports is the local stack's host ports: one fixed block, so every doc and
// every default URL is right. A busy port is an error, never a silent move.
package ports

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

// Name is a published port, as plat5.yml ports: names it.
type Name string

const (
	Gateway      Name = "gateway"
	Auth         Name = "auth"
	ControlPlane Name = "control_plane"
	Grafana      Name = "grafana"
	Operator     Name = "operator"
	StaffAuth    Name = "staff_auth"
	OTLPGRPC     Name = "otlp_grpc"
	OTLPHTTP     Name = "otlp_http"
)

// Defaults is the block. Plat5 is 540x, the operator plane 541x. OTLP keeps its
// conventional ports so SDK defaults work.
var Defaults = Set{
	Gateway:      5400,
	Auth:         5401,
	ControlPlane: 5402,
	Grafana:      5403,
	Operator:     5410,
	StaffAuth:    5411,
	OTLPGRPC:     4317,
	OTLPHTTP:     4318,
}

// Names is every port name, in the block's order.
func Names() []Name {
	names := make([]Name, 0, len(Defaults))
	for n := range Defaults {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return Defaults[names[i]] < Defaults[names[j]] })
	return names
}

// Set maps a port name to a host port.
type Set map[Name]int

// With is s over the defaults: a pin replaces its default.
func With(pins Set) Set {
	out := Set{}
	for n, p := range Defaults {
		out[n] = p
	}
	for n, p := range pins {
		out[n] = p
	}
	return out
}

// Busy returns the ports in want that something else holds. owned reports
// whether this project's own running stack holds a port; those count as free,
// so a second start is a no-op rather than a conflict.
func Busy(want map[Name]int, owned func(port int) bool) []string {
	var busy []string
	for _, n := range sortedNames(want) {
		p := want[n]
		if owned != nil && owned(p) {
			continue
		}
		if !Free(p) {
			busy = append(busy, fmt.Sprintf("%s %d", n, p))
		}
	}
	return busy
}

// Check fails, naming every busy port, if any port in want is taken.
func Check(want map[Name]int, owned func(port int) bool) error {
	busy := Busy(want, owned)
	if len(busy) == 0 {
		return nil
	}
	return fmt.Errorf("ports in use: %s\n  Stop what holds them (another Plat5 project: plat5 stop there),\n  or pin other ports in plat5.yml under ports:", strings.Join(busy, ", "))
}

// Validate rejects a port out of range, or two names on one port.
func Validate(s Set) error {
	seen := map[int]Name{}
	for _, n := range sortedNames(s) {
		p := s[n]
		if p < 1 || p > 65535 {
			return fmt.Errorf("ports.%s: %d is not a port", n, p)
		}
		if other, dup := seen[p]; dup {
			return fmt.Errorf("ports.%s and ports.%s are both %d", other, n, p)
		}
		seen[p] = n
	}
	return nil
}

func sortedNames(m map[Name]int) []Name {
	names := make([]Name, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	return names
}

// Free reports whether a host port is available for Docker publish.
// Checks IPv4/IPv6 all-interfaces binds and whether anything accepts on loopback
// (Docker Desktop on macOS can leave 127.0.0.1 bindable while 0.0.0.0 is taken).
func Free(port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	if listenErr("tcp4", fmt.Sprintf("0.0.0.0:%d", port)) != nil {
		return false
	}
	if err := listenErr("tcp6", fmt.Sprintf("[::]:%d", port)); err != nil && !ignoreIPv6(err) {
		return false
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 150*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return false
	}
	return true
}

func listenErr(network, addr string) error {
	ln, err := net.Listen(network, addr)
	if err != nil {
		return err
	}
	_ = ln.Close()
	return nil
}

func ignoreIPv6(err error) bool {
	s := err.Error()
	return strings.Contains(s, "protocol not supported") ||
		strings.Contains(s, "address family not supported") ||
		strings.Contains(s, "cannot assign requested address")
}
