package proxyuri

import (
	"fmt"
	"net/url"
)

// parseTUIC parses tuic://uuid:password@host:port?sni=...&alpn=h3#tag
func parseTUIC(uri string) (*Proxy, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	p := &Proxy{Protocol: "tuic", Raw: uri, Transport: "quic", Security: "tls"}
	if u.User == nil || u.User.Username() == "" {
		return nil, fmt.Errorf("tuic: missing uuid")
	}
	p.UUID = u.User.Username()
	if pw, ok := u.User.Password(); ok {
		p.Password = pw
	}
	if err := setHostPort(p, u); err != nil {
		return nil, err
	}
	q := u.Query()
	p.SNI = q.Get("sni")
	if alpn := q.Get("alpn"); alpn != "" {
		p.ALPN = []string{alpn}
	}
	p.Insecure = q.Get("allow_insecure") == "1" || q.Get("insecure") == "1"
	p.Tag = tagFrom(u)
	return p, nil
}
