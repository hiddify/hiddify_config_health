// Package proxyuri parses proxy share-links (vless://, vmess://, ss://,
// trojan://, hysteria2://, tuic://) and subscription documents into a
// normalised Proxy struct that the client-config builders can consume.
package proxyuri

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Proxy is a normalised, core-agnostic description of one proxy endpoint.
type Proxy struct {
	Protocol string // vless | vmess | trojan | shadowsocks | hysteria2 | tuic
	Tag      string // human label (from URL fragment) — never contains secrets in logs
	Server   string
	Port     int

	// Credentials (one of these is set depending on protocol).
	UUID     string // vless/vmess/tuic
	Password string // trojan/shadowsocks/hysteria2/tuic-password

	// TLS / security.
	Security string // tls | reality | none
	SNI      string
	ALPN     []string
	Insecure bool

	// Reality.
	PublicKey string
	ShortID   string

	// VLESS flow.
	Flow string

	// Transport.
	Transport   string // tcp | ws | grpc | xhttp | h2 | quic
	Path        string
	Host        string
	ServiceName string // grpc

	// Shadowsocks.
	Cipher string

	// VMess extras.
	AlterID int

	// Raw retains the original URI (kept in-memory only; never persisted).
	Raw string
}

// ParseURI parses a single proxy share-link.
func ParseURI(uri string) (*Proxy, error) {
	uri = strings.TrimSpace(uri)
	switch {
	case strings.HasPrefix(uri, "vless://"):
		return parseVLESS(uri)
	case strings.HasPrefix(uri, "vmess://"):
		return parseVMess(uri)
	case strings.HasPrefix(uri, "trojan://"):
		return parseTrojan(uri)
	case strings.HasPrefix(uri, "ss://"):
		return parseShadowsocks(uri)
	case strings.HasPrefix(uri, "hysteria2://"), strings.HasPrefix(uri, "hy2://"):
		return parseHysteria2(uri)
	case strings.HasPrefix(uri, "tuic://"):
		return parseTUIC(uri)
	default:
		scheme := uri
		if i := strings.Index(uri, "://"); i >= 0 {
			scheme = uri[:i]
		}
		return nil, fmt.Errorf("unsupported scheme %q", scheme)
	}
}

// ParseList parses a blob of share-links (one per line). Blank lines and
// lines starting with # are ignored. Returns the parsed proxies plus a
// per-line error list so one bad line does not abort the batch.
func ParseList(text string) ([]Proxy, []error) {
	var proxies []Proxy
	var errs []error
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		p, err := ParseURI(line)
		if err != nil {
			errs = append(errs, fmt.Errorf("%.24s…: %w", line, err))
			continue
		}
		proxies = append(proxies, *p)
	}
	return proxies, errs
}

// ParseSubscription fetches a subscription URL and parses its proxies. The
// body may be base64-encoded (common) or a plain newline list. The raw
// subscription URL is never stored.
func ParseSubscription(ctx context.Context, subURL string) ([]Proxy, []error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, subURL, nil)
	if err != nil {
		return nil, []error{err}
	}
	req.Header.Set("User-Agent", "hiddify-health/subscription")
	cl := &http.Client{Timeout: 20 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, []error{fmt.Errorf("fetch subscription: %w", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, []error{fmt.Errorf("subscription HTTP %d", resp.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, []error{err}
	}
	return ParseList(decodeMaybeBase64(string(body)))
}

// decodeMaybeBase64 returns the base64-decoded text if the whole blob decodes
// cleanly and yields proxy-looking content, else the original text.
func decodeMaybeBase64(s string) string {
	trimmed := strings.TrimSpace(s)
	// Subscriptions are often standard or URL-safe base64 with/without padding.
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if dec, err := enc.DecodeString(stripWhitespace(trimmed)); err == nil {
			out := string(dec)
			if strings.Contains(out, "://") {
				return out
			}
		}
	}
	return s
}

func stripWhitespace(s string) string {
	return strings.NewReplacer("\n", "", "\r", "", "\t", "", " ", "").Replace(s)
}
