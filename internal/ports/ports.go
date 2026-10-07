package ports

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// Defaults for local Plat5 stacks.
const (
	DefaultGateway  = 5001
	DefaultRegistry = 5002
	DefaultAuth     = 5000
	DefaultOperator = 5004
	// DefaultOperatorIdP is the local staff IdP (Dex) beside Operator.
	DefaultOperatorIdP = 5556
	DefaultGrafana     = 3002
	DefaultOTLPGRPC    = 4317
	DefaultOTLPHTTP    = 4318
	DefaultAlloy       = 12345
)

// Set is host ports for published services.
type Set struct {
	Gateway     int
	Registry    int
	Auth        int
	Operator    int
	OperatorIdP int
	Grafana     int
	OTLPGRPC    int
	OTLPHTTP    int
	Alloy       int
}

// Explicit tracks which ports were pinned in plat5.yml.
type Explicit struct {
	Gateway     bool
	Registry    bool
	Auth        bool
	Operator    bool
	OperatorIdP bool
	Grafana     bool
	OTLPGRPC    bool
	OTLPHTTP    bool
	Alloy       bool
}

// Resolve picks host ports.
// owned holds ports this project's running stack already publishes (0 = none).
// An owned port counts as free, and an unpinned port keeps its owned value,
// so re-running start reuses the same ports instead of moving them.
// Pinned ports must be free (or owned) or Resolve fails.
// Unpinned ports try the default, then allocate a free port if busy.
// When a default is skipped, a line is written to stdout (CLI DX).
func Resolve(want Set, exp Explicit, owned Set) (Set, error) {
	used := make(map[int]struct{}, 8)
	out := Set{}

	var err error
	out.Gateway, err = resolveOne("gateway", want.Gateway, DefaultGateway, exp.Gateway, owned.Gateway, used)
	if err != nil {
		return Set{}, err
	}
	out.Registry, err = resolveOne("registry", want.Registry, DefaultRegistry, exp.Registry, owned.Registry, used)
	if err != nil {
		return Set{}, err
	}
	out.Auth, err = resolveOne("auth", want.Auth, DefaultAuth, exp.Auth, owned.Auth, used)
	if err != nil {
		return Set{}, err
	}
	out.Operator, err = resolveOne("operator", want.Operator, DefaultOperator, exp.Operator, owned.Operator, used)
	if err != nil {
		return Set{}, err
	}
	out.OperatorIdP, err = resolveOne("operator_idp", want.OperatorIdP, DefaultOperatorIdP, exp.OperatorIdP, owned.OperatorIdP, used)
	if err != nil {
		return Set{}, err
	}
	out.Grafana, err = resolveOne("grafana", want.Grafana, DefaultGrafana, exp.Grafana, owned.Grafana, used)
	if err != nil {
		return Set{}, err
	}
	out.OTLPGRPC, err = resolveOne("otlp_grpc", want.OTLPGRPC, DefaultOTLPGRPC, exp.OTLPGRPC, owned.OTLPGRPC, used)
	if err != nil {
		return Set{}, err
	}
	out.OTLPHTTP, err = resolveOne("otlp_http", want.OTLPHTTP, DefaultOTLPHTTP, exp.OTLPHTTP, owned.OTLPHTTP, used)
	if err != nil {
		return Set{}, err
	}
	out.Alloy, err = resolveOne("alloy", want.Alloy, DefaultAlloy, exp.Alloy, owned.Alloy, used)
	if err != nil {
		return Set{}, err
	}
	return out, nil
}

func resolveOne(name string, want, def int, pinned bool, own int, used map[int]struct{}) (int, error) {
	candidate := def
	if want != 0 {
		candidate = want
	}
	if !pinned && own != 0 {
		candidate = own
	}
	free := func(p int) bool { return (own != 0 && p == own) || Free(p) }

	if pinned {
		if _, taken := used[candidate]; taken {
			return 0, fmt.Errorf("%s port %d already allocated to another service", name, candidate)
		}
		if !free(candidate) {
			return 0, fmt.Errorf("%s port %d is in use (pinned in plat5.yml)", name, candidate)
		}
		used[candidate] = struct{}{}
		return candidate, nil
	}

	if _, taken := used[candidate]; !taken && free(candidate) {
		used[candidate] = struct{}{}
		return candidate, nil
	}

	for i := 0; i < 64; i++ {
		p, err := freePort()
		if err != nil {
			return 0, fmt.Errorf("%s: allocate free port: %w", name, err)
		}
		if _, taken := used[p]; taken {
			continue
		}
		if !Free(p) {
			continue
		}
		used[p] = struct{}{}
		fmt.Fprintf(os.Stdout, "%s port %d in use → %d\n", name, candidate, p)
		return p, nil
	}
	return 0, fmt.Errorf("%s: could not allocate a free port", name)
}

// Free reports whether a host port is available for Docker publish.
// Checks IPv4/IPv6 all-interfaces binds and whether anything accepts on loopback
// (Docker Desktop on macOS can leave 127.0.0.1 bindable while 0.0.0.0 is taken).
func Free(port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	if !canListen("tcp4", fmt.Sprintf("0.0.0.0:%d", port)) {
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

func canListen(network, addr string) bool {
	return listenErr(network, addr) == nil
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
	if err == nil {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "protocol not supported") ||
		strings.Contains(s, "address family not supported") ||
		strings.Contains(s, "cannot assign requested address")
}

func freePort() (int, error) {
	// Bind all-interfaces so the port is free for Docker's 0.0.0.0 publish.
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected addr %T", ln.Addr())
	}
	return addr.Port, nil
}
