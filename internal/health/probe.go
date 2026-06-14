package health

import (
	"crypto/rand"
	"fmt"
	"net"
	"strings"
	"time"
)

// probeResult is the outcome of one active probe sent to the server.
type probeResult struct {
	name      string
	connected bool
	responded bool   // server sent any bytes back
	respBytes int
	closedRST bool   // server closed/reset without speaking
	dur       time.Duration
}

// activeProbe dials the server host:port directly (NOT through the proxy) and
// runs a battery of probes that a censor's active-prober would send. A real
// VLESS/Reality server silently drops or RST-closes unknown probes (looks like
// a black hole); a fingerprintable server answers with a TLS alert, HTTP error
// or a consistent banner. We also compare timing between a valid-looking TLS
// hello and garbage to detect timing leaks.
//
// Returns a verdict: resistant | fingerprintable | timing-leak | unreachable.
func activeProbe(cfg Config) (verdict, extra string, err error) {
	addr := net.JoinHostPort(cfg.ServerHost, cfg.ServerPort)
	to := cfg.timeoutFor("active-probe")
	if to <= 0 {
		to = 5 * time.Second
	}

	// Reachability check first.
	c, err := net.DialTimeout("tcp", addr, to)
	if err != nil {
		return "unreachable", "server not reachable: " + err.Error(), nil
	}
	_ = c.Close()

	tlsHello := buildClientHello("example.com")
	randBytes := make([]byte, 256)
	_, _ = rand.Read(randBytes)

	probes := []struct {
		name    string
		payload []byte
	}{
		{"http-get", []byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n")},
		{"tls-hello", tlsHello},
		{"random", randBytes},
		{"empty", nil},
	}

	var results []probeResult
	for _, p := range probes {
		results = append(results, sendProbe(addr, p.name, p.payload, to))
	}

	// Timing leak: run the valid-looking TLS hello a few times and the random
	// payload a few times; a large mean-time difference suggests the server
	// processes valid handshakes differently (a detectable signal).
	tlsT := meanProbeTime(addr, tlsHello, to, 3)
	randT := meanProbeTime(addr, randBytes, to, 3)
	timingLeak := false
	if tlsT > 0 && randT > 0 {
		ratio := float64(tlsT) / float64(randT)
		if ratio > 1.8 || ratio < 0.55 {
			timingLeak = true
		}
	}

	responded := 0
	for _, r := range results {
		if r.responded {
			responded++
		}
	}

	switch {
	case responded == 0 && !timingLeak:
		verdict = "resistant"
	case timingLeak:
		verdict = "timing-leak"
	default:
		verdict = "fingerprintable"
	}

	var parts []string
	for _, r := range results {
		state := "silent"
		if r.responded {
			state = fmt.Sprintf("replied %dB", r.respBytes)
		} else if r.closedRST {
			state = "closed"
		}
		parts = append(parts, r.name+"="+state)
	}
	parts = append(parts, fmt.Sprintf("tls=%s rand=%s", FormatDuration(tlsT), FormatDuration(randT)))
	return verdict, strings.Join(parts, " "), nil
}

func sendProbe(addr, name string, payload []byte, to time.Duration) probeResult {
	r := probeResult{name: name}
	start := time.Now()
	c, err := net.DialTimeout("tcp", addr, to)
	if err != nil {
		return r
	}
	defer c.Close()
	r.connected = true
	_ = c.SetDeadline(time.Now().Add(to))
	if len(payload) > 0 {
		if _, err := c.Write(payload); err != nil {
			r.closedRST = true
			r.dur = time.Since(start)
			return r
		}
	}
	buf := make([]byte, 1024)
	n, err := c.Read(buf)
	r.dur = time.Since(start)
	if n > 0 {
		r.responded = true
		r.respBytes = n
	} else if err != nil {
		r.closedRST = true
	}
	return r
}

func meanProbeTime(addr string, payload []byte, to time.Duration, n int) time.Duration {
	var tot time.Duration
	got := 0
	for i := 0; i < n; i++ {
		r := sendProbe(addr, "t", payload, to)
		if r.connected {
			tot += r.dur
			got++
		}
	}
	if got == 0 {
		return 0
	}
	return tot / time.Duration(got)
}

// buildClientHello returns a minimal but well-formed TLS 1.2 ClientHello record
// for the given SNI. Enough to look like a real handshake attempt to a server.
func buildClientHello(sni string) []byte {
	// Handshake body.
	var body []byte
	body = append(body, 0x03, 0x03)               // client version TLS 1.2
	rnd := make([]byte, 32)
	_, _ = rand.Read(rnd)
	body = append(body, rnd...)                    // random
	body = append(body, 0x00)                      // session id len
	body = append(body, 0x00, 0x02, 0x13, 0x01)    // cipher suites (TLS_AES_128_GCM_SHA256)
	body = append(body, 0x01, 0x00)                // compression: null

	// SNI extension.
	host := []byte(sni)
	sniEntry := append([]byte{0x00}, uint16b(len(host))...) // name type 0 + len
	sniEntry = append(sniEntry, host...)
	sniList := append(uint16b(len(sniEntry)), sniEntry...)
	sniExt := append([]byte{0x00, 0x00}, uint16b(len(sniList))...) // ext type 0
	sniExt = append(sniExt, sniList...)
	exts := sniExt
	body = append(body, uint16b(len(exts))...)
	body = append(body, exts...)

	// Handshake header: type 1 (ClientHello) + 3-byte length.
	hs := append([]byte{0x01}, uint24b(len(body))...)
	hs = append(hs, body...)

	// Record header: type 22 (handshake), TLS 1.0, length.
	rec := append([]byte{0x16, 0x03, 0x01}, uint16b(len(hs))...)
	rec = append(rec, hs...)
	return rec
}

func uint16b(n int) []byte { return []byte{byte(n >> 8), byte(n)} }
func uint24b(n int) []byte { return []byte{byte(n >> 16), byte(n >> 8), byte(n)} }
