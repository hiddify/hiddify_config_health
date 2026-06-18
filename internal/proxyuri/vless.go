package proxyuri

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// parseVLESS parses vless://uuid@host:port?type=ws&security=reality&sni=...#tag
func parseVLESS(uri string) (*Proxy, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	p := &Proxy{Protocol: "vless", Raw: uri}
	if u.User == nil || u.User.Username() == "" {
		return nil, fmt.Errorf("vless: missing uuid")
	}
	p.UUID = u.User.Username()
	if err := setHostPort(p, u); err != nil {
		return nil, err
	}
	q := u.Query()
	applyCommonQuery(p, q)
	p.Flow = q.Get("flow")
	p.Tag = tagFrom(u)
	return p, nil
}

// setHostPort fills Server/Port from a URL's host:port.
func setHostPort(p *Proxy, u *url.URL) error {
	p.Server = u.Hostname()
	if p.Server == "" {
		return fmt.Errorf("%s: missing host", p.Protocol)
	}
	ps := u.Port()
	if ps == "" {
		return fmt.Errorf("%s: missing port", p.Protocol)
	}
	port, err := strconv.Atoi(ps)
	if err != nil {
		return fmt.Errorf("%s: bad port %q", p.Protocol, ps)
	}
	p.Port = port
	return nil
}

// applyCommonQuery maps the shared transport/security query params used by
// vless/trojan share-links.
func applyCommonQuery(p *Proxy, q url.Values) {
	p.Security = firstNonEmpty(q.Get("security"), "none")
	p.SNI = firstNonEmpty(q.Get("sni"), q.Get("peer"))
	if alpn := q.Get("alpn"); alpn != "" {
		p.ALPN = strings.Split(alpn, ",")
	}
	p.Insecure = q.Get("allowInsecure") == "1" || q.Get("insecure") == "1"
	p.PublicKey = q.Get("pbk")
	p.ShortID = q.Get("sid")

	p.Transport = normTransport(firstNonEmpty(q.Get("type"), "tcp"))
	p.Path = q.Get("path")
	p.Host = firstNonEmpty(q.Get("host"), q.Get("Host"))
	p.ServiceName = firstNonEmpty(q.Get("serviceName"), q.Get("servicename"))
	if p.Host == "" && p.SNI != "" {
		p.Host = p.SNI
	}
}

func normTransport(t string) string {
	switch strings.ToLower(t) {
	case "", "tcp", "raw", "none":
		return "tcp"
	case "ws", "websocket":
		return "ws"
	case "grpc":
		return "grpc"
	case "xhttp", "splithttp":
		return "xhttp"
	case "h2", "http":
		return "h2"
	case "quic":
		return "quic"
	default:
		return strings.ToLower(t)
	}
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func tagFrom(u *url.URL) string {
	if u.Fragment != "" {
		if dec, err := url.QueryUnescape(u.Fragment); err == nil {
			return dec
		}
		return u.Fragment
	}
	return u.Hostname()
}
