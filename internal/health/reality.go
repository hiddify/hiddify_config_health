package health

import (
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
)

// realityVerify performs a deeper check of a Reality/TLS endpoint than the JA3
// fingerprint: it confirms the server completes a real TLS handshake to the
// configured SNI with a browser-like (uTLS Chrome) ClientHello, and that the
// presented certificate names the SNI host — i.e. the endpoint genuinely
// masquerades as the destination site (Reality's security premise), rather
// than answering as an obvious proxy.
//
// Verdicts:
//
//	reality-ok      handshake completed, cert matches SNI host
//	sni-mismatch    handshake completed but cert does not cover the SNI
//	handshake-fail  TLS handshake did not complete (often = proxy not fronting a real site)
//	no-sni          no SNI configured to verify against
func realityVerify(cfg Config) (verdict, extra string, err error) {
	if cfg.SNI == "" {
		return "no-sni", "no SNI to verify", nil
	}
	addr := net.JoinHostPort(cfg.ServerHost, cfg.ServerPort)
	to := cfg.timeoutFor("reality-verify")
	if to <= 0 {
		to = 6 * time.Second
	}

	raw, err := net.DialTimeout("tcp", addr, to)
	if err != nil {
		return "handshake-fail", "dial: " + err.Error(), nil
	}
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(to))

	// Browser-like ClientHello via uTLS, SNI = configured server name.
	uc := utls.UClient(raw, &utls.Config{ServerName: cfg.SNI, InsecureSkipVerify: true}, utls.HelloChrome_Auto)
	defer uc.Close()
	if err := uc.Handshake(); err != nil {
		return "handshake-fail", "tls handshake: " + err.Error(), nil
	}

	state := uc.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return "handshake-fail", "no peer certificate", nil
	}
	leaf := state.PeerCertificates[0]

	// Does the presented cert actually cover the SNI host?
	if certCoversHost(leaf.DNSNames, leaf.Subject.CommonName, cfg.SNI) {
		return "reality-ok", fmt.Sprintf("cn=%s tls=%s", leaf.Subject.CommonName, tlsVersionName(state.Version)), nil
	}
	return "sni-mismatch",
		fmt.Sprintf("cert cn=%s sans=%v does not cover sni=%s", leaf.Subject.CommonName, leaf.DNSNames, cfg.SNI),
		nil
}

func certCoversHost(sans []string, cn, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if matchHost(cn, host) {
		return true
	}
	for _, s := range sans {
		if matchHost(s, host) {
			return true
		}
	}
	return false
}

// matchHost supports exact and single-level wildcard (*.example.com) matches.
func matchHost(pattern, host string) bool {
	pattern = strings.ToLower(strings.TrimSuffix(pattern, "."))
	if pattern == host {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".example.com"
		if strings.HasSuffix(host, suffix) && strings.Count(host, ".") == strings.Count(pattern, ".") {
			return true
		}
	}
	return false
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "1.3"
	case tls.VersionTLS12:
		return "1.2"
	default:
		return fmt.Sprintf("0x%04x", v)
	}
}
