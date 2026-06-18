// Package dpi classifies the first bytes of a proxy handshake against an
// embedded table of documented DPI signatures (the patterns censors' deep
// packet inspection looks for), to estimate whether a flow "would be flagged".
// Pure-Go, no nDPI.
package dpi

import "bytes"

// Verdict is the classification outcome.
type Verdict struct {
	ClassifiedAs string // tls | http | unknown | <proto>
	WouldFlag    bool   // matches a known proxy/plaintext-leak signature
	Reason       string
}

// signature is one DPI pattern. A flow is flagged if a flagging signature
// matches the captured opening bytes.
type signature struct {
	name   string
	flag   bool
	match  func(b []byte) bool
	reason string
}

var signatures = []signature{
	{
		name:   "tls-clienthello",
		flag:   false,
		match:  func(b []byte) bool { return len(b) >= 3 && b[0] == 0x16 && b[1] == 0x03 },
		reason: "looks like a normal TLS handshake",
	},
	{
		name:  "http-plaintext",
		flag:  true,
		match: func(b []byte) bool {
			for _, m := range [][]byte{[]byte("GET "), []byte("POST "), []byte("HTTP/")} {
				if bytes.HasPrefix(b, m) {
					return true
				}
			}
			return false
		},
		reason: "plaintext HTTP visible — trivially fingerprinted",
	},
	{
		name:  "ss-stream-low-entropy",
		flag:  true,
		match: func(b []byte) bool {
			// Classic Shadowsocks stream ciphers leak no TLS record header and
			// have no structure; flag opening bytes that are neither TLS nor
			// HTTP but are short & low-entropy (a weak heuristic placeholder).
			if len(b) < 8 {
				return false
			}
			if b[0] == 0x16 { // TLS — not this
				return false
			}
			return lowEntropy(b[:min(len(b), 32)])
		},
		reason: "no TLS framing + low entropy (legacy SS pattern)",
	},
}

// Classify inspects the opening bytes of a handshake.
func Classify(opening []byte) Verdict {
	if len(opening) == 0 {
		return Verdict{ClassifiedAs: "unknown", Reason: "no bytes captured"}
	}
	v := Verdict{ClassifiedAs: "unknown"}
	for _, s := range signatures {
		if s.match(opening) {
			if s.flag {
				return Verdict{ClassifiedAs: s.name, WouldFlag: true, Reason: s.reason}
			}
			// First non-flagging match sets the benign classification.
			if v.ClassifiedAs == "unknown" {
				v.ClassifiedAs = s.name
				v.Reason = s.reason
			}
		}
	}
	if v.ClassifiedAs == "unknown" {
		v.Reason = "opaque — no known signature matched (good)"
	}
	return v
}

// lowEntropy returns true if b has very few distinct byte values.
func lowEntropy(b []byte) bool {
	var seen [256]bool
	distinct := 0
	for _, c := range b {
		if !seen[c] {
			seen[c] = true
			distinct++
		}
	}
	return distinct*4 < len(b) // <25% distinct
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
