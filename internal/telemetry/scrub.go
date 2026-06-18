// Package telemetry builds anonymous, PII-stripped reports for optional
// submission to the central health-check server, and submits them.
package telemetry

import (
	"github.com/hiddify/hiddify_config_health/internal/proxyuri"
)

// Shape is the anonymised structural description of a proxy — ONLY the fields
// that matter for censorship/quality analysis, with every identifying or
// secret field removed (no server IP/host, no uuid/password/keys, no SNI or
// host headers, no tag). This is what may be submitted anonymously.
type Shape struct {
	Protocol  string `json:"protocol"`            // vless | vmess | trojan | shadowsocks | hysteria2 | tuic
	Transport string `json:"transport"`           // tcp | ws | grpc | xhttp | h2 | quic
	Security  string `json:"security"`            // tls | reality | none
	HasFlow   bool   `json:"has_flow,omitempty"`  // vless vision flow present
	HasReality bool  `json:"has_reality,omitempty"`
	HasALPN   bool   `json:"has_alpn,omitempty"`
	Cipher    string `json:"cipher,omitempty"`    // ss method (a class, not a secret)
}

// Scrub reduces a parsed proxy to its anonymous structural Shape. It NEVER
// copies Server, Port, UUID, Password, SNI, Host, PublicKey, ShortID, Path,
// Tag or Raw — those can identify the user or endpoint.
func Scrub(p proxyuri.Proxy) Shape {
	return Shape{
		Protocol:   p.Protocol,
		Transport:  emptyTo(p.Transport, "tcp"),
		Security:   emptyTo(p.Security, "none"),
		HasFlow:    p.Flow != "",
		HasReality: p.Security == "reality",
		HasALPN:    len(p.ALPN) > 0,
		Cipher:     p.Cipher, // e.g. "aes-256-gcm" — algorithm class, not the key
	}
}

func emptyTo(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
