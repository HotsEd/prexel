package client

import (
	"crypto/x509"
	"time"
)

// parseCert parses a DER-encoded X.509 cert.
func parseCert(der []byte) (*x509.Certificate, error) {
	return x509.ParseCertificate(der)
}

// verifyAgainstSystem reports whether the cert chain validates against the
// system root pool for the given hostname. Returns false on any failure (no
// chain, expired, hostname mismatch, etc.) so the caller can fall back to the
// known_hosts pin.
func verifyAgainstSystem(host string, leaf *x509.Certificate, intermediatesDER [][]byte) bool {
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		return false
	}
	inters := x509.NewCertPool()
	for _, der := range intermediatesDER {
		if c, err := x509.ParseCertificate(der); err == nil {
			inters.AddCert(c)
		}
	}
	opts := x509.VerifyOptions{
		Roots:         roots,
		Intermediates: inters,
		DNSName:       host,
		CurrentTime:   time.Now(),
	}
	_, err = leaf.Verify(opts)
	return err == nil
}
