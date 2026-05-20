package app

import (
	"strings"
	"testing"
)

func TestNameRegex(t *testing.T) {
	good := []string{"app", "my-app", "abc123", "Aa-1", "a1"}
	// Regex itself accepts single chars; service enforces 3-50 separately.
	bad := []string{"-app", "app-", "app!", "app_x", "  ", ""}
	for _, s := range good {
		if !nameRegex.MatchString(s) {
			t.Errorf("nameRegex rejected %q (should match)", s)
		}
	}
	for _, s := range bad {
		if nameRegex.MatchString(s) && s != "" {
			t.Errorf("nameRegex accepted %q (should not match)", s)
		}
	}
}

func TestValidatePort(t *testing.T) {
	good := []int{1, 80, 443, 8080, 65535}
	bad := []int{0, -1, 65536, 100000}
	for _, p := range good {
		p := p
		if err := validatePort(&p); err != nil {
			t.Errorf("validatePort(%d): %v", p, err)
		}
	}
	for _, p := range bad {
		p := p
		if err := validatePort(&p); err == nil {
			t.Errorf("validatePort(%d) should fail", p)
		}
	}
	if err := validatePort(nil); err != nil {
		t.Errorf("nil should be allowed: %v", err)
	}
}

func TestValidateLimits(t *testing.T) {
	ok := func(mem, cpus string) {
		if err := validateLimits(&mem, &cpus); err != nil {
			t.Errorf("validateLimits(%q,%q): %v", mem, cpus, err)
		}
	}
	fail := func(mem, cpus string) {
		if err := validateLimits(&mem, &cpus); err == nil {
			t.Errorf("validateLimits(%q,%q) should fail", mem, cpus)
		}
	}
	ok("512m", "1")
	ok("1g", "0.5")
	ok("", "")
	fail("512xx", "1")
	fail("1g", "abc")
}

func TestValidateEnvMap(t *testing.T) {
	if err := validateEnvMap(map[string]string{"DATABASE_URL": "x", "FOO_BAR": "y"}); err != nil {
		t.Errorf("unexpected: %v", err)
	}
	bad := []map[string]string{
		{"lowercase": "x"},
		{"1NUMERIC_PREFIX": "x"},
		{"DASH-KEY": "x"},
		{"WITH SPACE": "x"},
	}
	for _, m := range bad {
		if err := validateEnvMap(m); err == nil {
			t.Errorf("expected error for %v", m)
		}
	}
}

func TestNormaliseHealthCheck_Defaults(t *testing.T) {
	out, err := normaliseHealthCheck(HealthCheckOpts{})
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	hc := out.Unwrap()
	if !hc.Enabled || hc.Path != "/" || hc.Method != "GET" || hc.ReturnCode != 200 {
		t.Errorf("defaults wrong: %+v", hc)
	}
	if hc.Interval != 30 || hc.Timeout != 60 || hc.Retries != 3 || hc.StartPeriod != 10 {
		t.Errorf("timer defaults wrong: %+v", hc)
	}
}

func TestNormaliseHealthCheck_RangeChecks(t *testing.T) {
	cases := []struct {
		name string
		in   HealthCheckOpts
		fail bool
	}{
		{"method ok HEAD", HealthCheckOpts{Method: "HEAD"}, false},
		{"method bad", HealthCheckOpts{Method: "POST"}, true},
		{"return code out of range", HealthCheckOpts{ReturnCode: 50}, true},
		{"timeout too small", HealthCheckOpts{Timeout: 0, Retries: 1}, false}, // zero gets defaulted to 60
		{"timeout too large", HealthCheckOpts{Timeout: 1000}, true},
		{"retries too high", HealthCheckOpts{Retries: 50}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normaliseHealthCheck(tc.in)
			if (err != nil) != tc.fail {
				t.Errorf("got err=%v want fail=%v", err, tc.fail)
			}
		})
	}
}

func TestNormaliseHealthCheck_PreservesCustom(t *testing.T) {
	disabled := false
	port := 8080
	out, err := normaliseHealthCheck(HealthCheckOpts{
		Enabled:    &disabled,
		Path:       "/health",
		Port:       &port,
		Method:     "head",
		ReturnCode: 204,
		Interval:   60,
		Timeout:    30,
		Retries:    5,
	})
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	hc := out.Unwrap()
	if hc.Enabled {
		t.Error("Enabled should be false")
	}
	if hc.Path != "/health" {
		t.Errorf("Path: %q", hc.Path)
	}
	if hc.Method != "HEAD" {
		t.Errorf("Method should upper-case: %q", hc.Method)
	}
	if !strings.EqualFold(hc.Method, "HEAD") {
		t.Errorf("Method: %q", hc.Method)
	}
}
