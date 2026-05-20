package dockersvc

import (
	"context"
	"errors"
	"testing"
)

func TestParseMemory(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		// Empty string is treated as "no limit" per the helpers contract.
		{"", 0, false},
		// Plain integers fall through with multiplier=1.
		{"1024", 1024, false},
		// Suffix handling: case-insensitive, with optional 'b' tail.
		{"512m", 512 * 1024 * 1024, false},
		{"512M", 512 * 1024 * 1024, false},
		{"512mb", 512 * 1024 * 1024, false},
		{"1g", 1 * 1024 * 1024 * 1024, false},
		{"1G", 1 * 1024 * 1024 * 1024, false},
		{"1gb", 1 * 1024 * 1024 * 1024, false},
		{"1k", 1024, false},
		{"1kb", 1024, false},
		// Fractional values use float math (1.5g = 1.5 GiB).
		{"1.5g", int64(1.5 * float64(1024*1024*1024)), false},
		// Garbage rejects with ErrInvalidResource for caller introspection.
		{"garbage", 0, true},
		{"abc m", 0, true},
	}
	for _, tc := range cases {
		got, err := ParseMemory(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseMemory(%q): expected error, got %d", tc.in, got)
				continue
			}
			if !errors.Is(err, ErrInvalidResource) {
				t.Errorf("ParseMemory(%q): err = %v, want wraps ErrInvalidResource", tc.in, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMemory(%q): unexpected err %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseMemory(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseMemorySwap(t *testing.T) {
	// parseMemorySwap shares the suffix parser with parseMemory but adds
	// the docker-specific "-1" sentinel (unlimited swap). Both shapes
	// must round-trip cleanly because the deploy engine forwards the
	// app's limits_memory_swap value verbatim.
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"", 0, false},
		{"-1", -1, false},
		{"512m", 512 * 1024 * 1024, false},
		{"1g", 1 * 1024 * 1024 * 1024, false},
		{"garbage", 0, true},
	}
	for _, tc := range cases {
		got, err := ParseMemorySwap(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseMemorySwap(%q): expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMemorySwap(%q): unexpected err %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseMemorySwap(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseCPUs(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		// Empty string is the "no limit" sentinel here too.
		{"", 0, false},
		{"0.5", 500_000_000, false},
		{"1", 1_000_000_000, false},
		{"1.0", 1_000_000_000, false},
		{"2.5", 2_500_000_000, false},
		{"garbage", 0, true},
		{"1m", 0, true}, // suffixes not allowed for CPUs
	}
	for _, tc := range cases {
		got, err := ParseCPUs(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseCPUs(%q): expected error, got %d", tc.in, got)
				continue
			}
			if !errors.Is(err, ErrInvalidResource) {
				t.Errorf("ParseCPUs(%q): err = %v, want wraps ErrInvalidResource", tc.in, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseCPUs(%q): unexpected err %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseCPUs(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseResources_Combined(t *testing.T) {
	// parseResources combines both knobs into container.Resources; verify the
	// happy path so RunContainer can rely on it without per-call rechecks.
	r, err := ParseResources("256m", "0.5")
	if err != nil {
		t.Fatalf("ParseResources: %v", err)
	}
	if r.Memory != 256*1024*1024 {
		t.Errorf("Memory = %d, want %d", r.Memory, 256*1024*1024)
	}
	if r.NanoCPUs != 500_000_000 {
		t.Errorf("NanoCPUs = %d, want %d", r.NanoCPUs, 500_000_000)
	}

	// Errors from either parser surface unchanged.
	if _, err := ParseResources("garbage", "0.5"); err == nil {
		t.Error("expected memory parse error")
	}
	if _, err := ParseResources("256m", "junk"); err == nil {
		t.Error("expected cpus parse error")
	}
}

func TestVersionAtLeast(t *testing.T) {
	cases := []struct {
		got, want string
		ok        bool
	}{
		{"20.10", "20.10", true},
		{"20.10.7", "20.10", true},
		{"20.10.7-ce", "20.10", true},
		{"23.0", "20.10", true},
		{"19.03", "20.10", false},
		{"20.9.99", "20.10", false},
		// Empty strings: parsed as []int{} which makes "" >= anything fail.
		{"", "20.10", false},
	}
	for _, tc := range cases {
		got := VersionAtLeast(tc.got, tc.want)
		if got != tc.ok {
			t.Errorf("VersionAtLeast(%q, %q) = %v, want %v", tc.got, tc.want, got, tc.ok)
		}
	}
}

func TestParseDottedVersion(t *testing.T) {
	// Stops at first non-digit/non-dot — production uses this to ignore "-ce".
	got := ParseDottedVersion("20.10.7-ce")
	if len(got) != 3 || got[0] != 20 || got[1] != 10 || got[2] != 7 {
		t.Errorf("ParseDottedVersion(20.10.7-ce) = %v, want [20 10 7]", got)
	}
}

func TestNewRemoteProvider_Validation(t *testing.T) {
	// Each empty-required-field case should bubble a meaningful error BEFORE
	// the temp key file is created (so we don't leak fs state for invalid
	// input). The non-empty key path is exercised by the happy path further
	// down.
	cases := []struct {
		name string
		args func() (string, string, int, string, []byte)
	}{
		{
			"empty name",
			func() (string, string, int, string, []byte) { return "", "host", 22, "user", []byte("PEM") },
		},
		{
			"empty host",
			func() (string, string, int, string, []byte) { return "n", "", 22, "user", []byte("PEM") },
		},
		{
			"empty user",
			func() (string, string, int, string, []byte) { return "n", "h", 22, "", []byte("PEM") },
		},
		{
			"empty key",
			func() (string, string, int, string, []byte) { return "n", "h", 22, "u", nil },
		},
		{
			"empty key (zero-len byte slice)",
			func() (string, string, int, string, []byte) { return "n", "h", 22, "u", []byte{} },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name, host, port, user, key := tc.args()
			p, err := NewRemoteProvider(name, host, port, user, key)
			if err == nil {
				_ = p.Close()
				t.Fatal("expected error")
			}
		})
	}
}

func TestNewRemoteProvider_DefaultsPort(t *testing.T) {
	// Pass port=0 → provider should default to 22 (we can't inspect the
	// private field, but Close should succeed and produce no error).
	p, err := NewRemoteProvider("test", "example.com", 0, "user", []byte("garbage PEM data"))
	if err != nil {
		t.Fatalf("NewRemoteProvider: %v", err)
	}
	if p == nil {
		t.Fatal("nil provider")
	}
	if got := p.Type(); got != "remote" {
		t.Errorf("Type() = %q, want remote", got)
	}
	if got := p.Name(); got != "test" {
		t.Errorf("Name() = %q, want test", got)
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestLocalProvider_TypeAndName(t *testing.T) {
	p := NewLocalProvider("primary")
	if got := p.Type(); got != "local" {
		t.Errorf("Type() = %q, want local", got)
	}
	if got := p.Name(); got != "primary" {
		t.Errorf("Name() = %q, want primary", got)
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	// Default name when empty.
	p2 := NewLocalProvider("")
	if got := p2.Name(); got != "local" {
		t.Errorf("Name() = %q, want local (default)", got)
	}
	if err := p2.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestLocalProvider_ClientAfterCloseFails(t *testing.T) {
	// Closing the provider should make subsequent Client() calls return an
	// error rather than silently re-initialising. This protects callers that
	// keep a stale provider around (e.g. background goroutines).
	p := NewLocalProvider("x")
	_ = p.Close()
	if _, err := p.Client(context.Background()); err == nil {
		t.Error("expected error after Close")
	}
}

func TestEnsureNetwork_EmptyName(t *testing.T) {
	// EnsureNetwork rejects empty names BEFORE talking to docker, so we can
	// exercise the validation without a real provider.
	p := NewLocalProvider("test")
	defer func() { _ = p.Close() }()
	if err := EnsureNetwork(context.Background(), p, ""); err == nil {
		t.Error("expected error for empty network name")
	}
}

func TestRunContainer_RequiredFields(t *testing.T) {
	// RunContainer must reject empty Name/Image with a clear error, prior to
	// any docker call. This is a guard rail used by the deploy engine.
	p := NewLocalProvider("test")
	defer func() { _ = p.Close() }()
	if _, err := RunContainer(context.Background(), p, ContainerOpts{Image: "x"}); err == nil {
		t.Error("expected error for empty Name")
	}
	if _, err := RunContainer(context.Background(), p, ContainerOpts{Name: "x"}); err == nil {
		t.Error("expected error for empty Image")
	}
}

func TestPrexelNetwork_Const(t *testing.T) {
	// Sanity: PrexelNetwork is the canonical bridge name. A typo here would
	// silently sever all container ↔ caddy communication. Lock the value down.
	if PrexelNetwork != "prexel-net" {
		t.Errorf("PrexelNetwork = %q, want prexel-net", PrexelNetwork)
	}
}
