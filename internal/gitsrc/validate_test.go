package gitsrc

import (
	"strings"
	"testing"
)

// TestValidateRepoURL_AllowsPublicHTTPS exercises the happy path that
// every real user hits. github.com resolves to a public address — if
// this test starts failing it means either the resolver is broken or
// the allowlist logic regressed (both worth catching).
func TestValidateRepoURL_AllowsPublicHTTPS(t *testing.T) {
	if err := ValidateRepoURL("https://github.com/foo/bar"); err != nil {
		t.Errorf("expected github.com to validate, got %v", err)
	}
	if err := ValidateRepoURL("https://github.com/foo/bar.git"); err != nil {
		t.Errorf("expected .git suffix to validate, got %v", err)
	}
}

// TestValidateRepoURL_AllowsSSHForms covers the two SSH spellings git
// itself accepts: explicit ssh:// scheme and the SCP-style shortcut.
func TestValidateRepoURL_AllowsSSHForms(t *testing.T) {
	if err := ValidateRepoURL("ssh://git@github.com/foo/bar.git"); err != nil {
		t.Errorf("ssh:// rejected: %v", err)
	}
	if err := ValidateRepoURL("git@github.com:foo/bar.git"); err != nil {
		t.Errorf("scp-style rejected: %v", err)
	}
}

// TestValidateRepoURL_RejectsBadSchemes locks the scheme allowlist.
func TestValidateRepoURL_RejectsBadSchemes(t *testing.T) {
	cases := []string{
		"file:///etc/passwd",
		"http://github.com/foo/bar", // plain http: downgrade
		"git://github.com/foo/bar",  // unauthenticated git protocol
		"ftp://example.com/repo",
		"javascript:alert(1)",
	}
	for _, u := range cases {
		t.Run(u, func(t *testing.T) {
			err := ValidateRepoURL(u)
			if err == nil {
				t.Errorf("expected %q to be rejected", u)
			}
		})
	}
}

// TestValidateRepoURL_RejectsLocalhost covers the loopback path. The
// hostname "localhost" should fail DNS to 127.0.0.1 and trip the
// IsLoopback check.
func TestValidateRepoURL_RejectsLocalhost(t *testing.T) {
	err := ValidateRepoURL("https://localhost/foo/bar")
	if err == nil {
		t.Fatal("expected localhost to be rejected")
	}
	if !strings.Contains(err.Error(), "blocked") &&
		!strings.Contains(err.Error(), "resolve") {
		t.Errorf("unexpected error shape: %v", err)
	}
}

// TestValidateRepoURL_RejectsMetadataEndpoint guards against an
// operator pointing the clone at the cloud metadata endpoint.
func TestValidateRepoURL_RejectsMetadataEndpoint(t *testing.T) {
	err := ValidateRepoURL("https://169.254.169.254/latest/meta-data/")
	if err == nil {
		t.Fatal("expected metadata endpoint to be rejected")
	}
}

// TestValidateRepoURL_RejectsPrivateIP covers the RFC1918 ranges via a
// literal IP. We don't rely on DNS for this case so the test is
// deterministic.
func TestValidateRepoURL_RejectsPrivateIP(t *testing.T) {
	cases := []string{
		"https://10.0.0.1/repo.git",
		"https://192.168.1.1/repo.git",
		"https://172.16.0.1/repo.git",
		"https://127.0.0.1/repo.git",
	}
	for _, u := range cases {
		t.Run(u, func(t *testing.T) {
			err := ValidateRepoURL(u)
			if err == nil {
				t.Errorf("expected %q to be rejected", u)
			}
		})
	}
}

// TestValidateRepoURL_RejectsEmpty + sundry malformed inputs.
func TestValidateRepoURL_RejectsEmpty(t *testing.T) {
	if err := ValidateRepoURL(""); err == nil {
		t.Error("empty url accepted")
	}
	if err := ValidateRepoURL("   "); err == nil {
		t.Error("whitespace-only url accepted")
	}
	if err := ValidateRepoURL("not-a-url"); err == nil {
		t.Error("garbage url accepted")
	}
}

// TestValidateRepoURL_RejectsSCPLikeToPrivateIP makes sure the
// SCP-style branch also resolves the host (so `git@10.0.0.1:repo.git`
// can't bypass the IP filter just by skipping the scheme).
func TestValidateRepoURL_RejectsSCPLikeToPrivateIP(t *testing.T) {
	if err := ValidateRepoURL("git@10.0.0.1:foo/bar.git"); err == nil {
		t.Error("scp-style to private IP accepted")
	}
}
