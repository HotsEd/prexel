package domains

import (
	"net"
	"strconv"
	"testing"
)

func TestNormalizeName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Example.COM", "example.com"},
		{"  https://example.com/  ", "example.com"},
		{"http://www.example.com", "example.com"},
		{"www.example.com", "example.com"},
		{"example.com/", "example.com"},
		{"sub.Example.com", "sub.example.com"},
	}
	for _, c := range cases {
		got := normalizeName(c.in)
		if got != c.want {
			t.Errorf("normalizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidateName(t *testing.T) {
	good := []string{"example.com", "sub.example.com", "a.b.co", "my-app.deploy.example.io"}
	for _, s := range good {
		if err := validateName(s); err != nil {
			t.Errorf("validateName(%q) unexpected err: %v", s, err)
		}
	}
	bad := map[string]string{
		"":                "empty",
		"path":            "no dot",
		"localhost":       "localhost",
		"foo.com/bar":     "has path",
		"*.example.com":   "wildcard",
		"127.0.0.1":       "IPv4 literal",
		"::1":             "IPv6 literal",
		"foo..com":        "double dot",
		"-foo.com":        "leading hyphen",
		"foo-.com":        "trailing hyphen",
		"foo.123":         "TLD must be letters",
	}
	for s, why := range bad {
		if err := validateName(s); err == nil {
			t.Errorf("validateName(%q) should have failed (%s)", s, why)
		}
	}
}

func TestFindAvailablePort(t *testing.T) {
	port, err := FindAvailablePort(PortRangeStart, PortRangeEnd)
	if err != nil {
		t.Fatalf("FindAvailablePort: %v", err)
	}
	if port < PortRangeStart || port >= PortRangeEnd {
		t.Errorf("port %d outside range [%d, %d)", port, PortRangeStart, PortRangeEnd)
	}
	// Confirm we can actually bind to it (the function already verified, but
	// double-check the contract).
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Errorf("port %d not actually bindable: %v", port, err)
		return
	}
	_ = l.Close()
}

func TestFindAvailablePort_InvalidRange(t *testing.T) {
	if _, err := FindAvailablePort(0, 0); err == nil {
		t.Error("expected error for [0, 0)")
	}
	if _, err := FindAvailablePort(100, 50); err == nil {
		t.Error("expected error for inverted range")
	}
}

func TestFindAvailablePort_HoldsBusyPort(t *testing.T) {
	// Bind one port manually, then ask for a range starting at that port. The
	// helper should skip it and return a higher port.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = l.Close() }()
	busy := l.Addr().(*net.TCPAddr).Port

	port, err := FindAvailablePort(busy, busy+50)
	if err != nil {
		t.Fatalf("FindAvailablePort: %v", err)
	}
	if port == busy {
		t.Errorf("returned busy port %d", port)
	}
}
