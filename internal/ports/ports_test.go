package ports

import (
	"fmt"
	"net"
	"testing"
)

func TestResolveDefaultsFree(t *testing.T) {
	// May fail if ports in use on the machine; still validates pinned path.
	got, err := Resolve(Set{}, Explicit{})
	if err != nil {
		t.Skipf("defaults busy on this machine: %v", err)
	}
	if got.Gateway == 0 || got.Registry == 0 || got.Auth == 0 {
		t.Fatalf("zero port: %+v", got)
	}
	if got.Gateway == got.Registry || got.Gateway == got.Auth || got.Registry == got.Auth {
		t.Fatalf("ports must be distinct: %+v", got)
	}
}

func TestResolvePinnedBusyFails(t *testing.T) {
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	_, err = Resolve(Set{Gateway: port, Registry: DefaultRegistry, Auth: DefaultAuth}, Explicit{Gateway: true})
	if err == nil {
		t.Fatal("expected pinned busy error")
	}
}

func TestResolveUnpinnedBusyAllocates(t *testing.T) {
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	busy := ln.Addr().(*net.TCPAddr).Port

	got, err := Resolve(Set{Gateway: busy}, Explicit{Gateway: false})
	if err != nil {
		t.Fatal(err)
	}
	if got.Gateway == busy {
		t.Fatalf("expected reallocation away from %d", busy)
	}
}

func TestFreeDetectsAllInterfacesBind(t *testing.T) {
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	if Free(port) {
		t.Fatalf("expected port %d held on 0.0.0.0 to be busy", port)
	}
}

func TestFreeDetectsLoopbackAccept(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	if Free(port) {
		t.Fatalf("expected port %d with loopback listener to be busy", port)
	}
}

func TestResolveDistinctWhenAllBusy(t *testing.T) {
	var lns []net.Listener
	defer func() {
		for _, ln := range lns {
			_ = ln.Close()
		}
	}()

	hold := func(port int) {
		t.Helper()
		ln, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", port))
		if err != nil {
			t.Skipf("could not hold %d: %v", port, err)
		}
		lns = append(lns, ln)
	}

	a, b, c := mustEphemeral(t), mustEphemeral(t), mustEphemeral(t)
	hold(a)
	hold(b)
	hold(c)

	got, err := Resolve(Set{Gateway: a, Registry: b, Auth: c}, Explicit{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{got.Gateway: true, got.Registry: true, got.Auth: true}
	if len(seen) != 3 {
		t.Fatalf("expected 3 distinct ports, got %+v", got)
	}
	for _, p := range []int{a, b, c} {
		if seen[p] {
			t.Fatalf("reused busy port %d in %+v", p, got)
		}
	}
}

func mustEphemeral(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}
