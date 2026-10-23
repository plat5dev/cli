package ports

import (
	"net"
	"strings"
	"testing"
)

func TestDefaultsAreTheBlock(t *testing.T) {
	if err := Validate(Defaults); err != nil {
		t.Fatal(err)
	}
	if Defaults[Gateway] != 5400 || Defaults[Auth] != 5401 || Defaults[ControlPlane] != 5402 {
		t.Fatalf("defaults moved: %v", Defaults)
	}
	names := Names()
	if names[0] != OTLPGRPC || names[len(names)-1] != Grafana {
		t.Fatalf("order: %v", names)
	}
}

func TestWithPins(t *testing.T) {
	s := With(Set{Gateway: 6400})
	if s[Gateway] != 6400 || s[Auth] != 5401 {
		t.Fatalf("%v", s)
	}
	if Defaults[Gateway] != 5400 {
		t.Fatal("With changed Defaults")
	}
}

func TestValidate(t *testing.T) {
	if err := Validate(Set{Gateway: 0}); err == nil {
		t.Fatal("want range error")
	}
	if err := Validate(Set{Gateway: 6000, Auth: 6000}); err == nil || !strings.Contains(err.Error(), "both 6000") {
		t.Fatalf("want duplicate error, got %v", err)
	}
}

func TestCheckNamesBusyPorts(t *testing.T) {
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	err = Check(map[Name]int{Gateway: port}, nil)
	if err == nil || !strings.Contains(err.Error(), "gateway") {
		t.Fatalf("want busy gateway, got %v", err)
	}
	// This project's own stack holding the port is not a conflict.
	if err := Check(map[Name]int{Gateway: port}, func(p int) bool { return p == port }); err != nil {
		t.Fatal(err)
	}
}
