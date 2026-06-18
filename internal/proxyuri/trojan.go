package proxyuri

import (
	"fmt"
	"net/url"
)

// parseTrojan parses trojan://password@host:port?type=ws&sni=...#tag
func parseTrojan(uri string) (*Proxy, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	p := &Proxy{Protocol: "trojan", Raw: uri}
	if u.User == nil || u.User.Username() == "" {
		return nil, fmt.Errorf("trojan: missing password")
	}
	p.Password = u.User.Username()
	if err := setHostPort(p, u); err != nil {
		return nil, err
	}
	q := u.Query()
	applyCommonQuery(p, q)
	// Trojan is TLS by default.
	if p.Security == "none" {
		p.Security = "tls"
	}
	p.Tag = tagFrom(u)
	return p, nil
}
