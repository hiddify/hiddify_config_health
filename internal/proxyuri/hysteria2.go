package proxyuri

import (
	"fmt"
	"net/url"
)

// parseHysteria2 parses hysteria2://password@host:port?sni=...&insecure=1#tag
func parseHysteria2(uri string) (*Proxy, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	p := &Proxy{Protocol: "hysteria2", Raw: uri, Transport: "quic", Security: "tls"}
	if u.User != nil {
		p.Password = u.User.Username()
	}
	if p.Password == "" {
		return nil, fmt.Errorf("hysteria2: missing password")
	}
	if err := setHostPort(p, u); err != nil {
		return nil, err
	}
	q := u.Query()
	p.SNI = q.Get("sni")
	p.Insecure = q.Get("insecure") == "1"
	p.Tag = tagFrom(u)
	return p, nil
}
