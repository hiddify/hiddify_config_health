package proxyuri

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// parseShadowsocks parses both SIP002 (ss://base64(method:pass)@host:port#tag)
// and the legacy fully-base64 form (ss://base64(method:pass@host:port)#tag).
func parseShadowsocks(uri string) (*Proxy, error) {
	body := strings.TrimPrefix(uri, "ss://")
	tag := ""
	if i := strings.IndexByte(body, '#'); i >= 0 {
		tag, _ = url.QueryUnescape(body[i+1:])
		body = body[:i]
	}
	// Strip any query (plugin opts) — not needed for a basic SS test.
	if i := strings.IndexByte(body, '?'); i >= 0 {
		body = body[:i]
	}

	p := &Proxy{Protocol: "shadowsocks", Raw: uri, Tag: tag, Security: "none"}

	if at := strings.LastIndex(body, "@"); at >= 0 {
		// SIP002: userinfo may be base64(method:password) or plain.
		userinfo := body[:at]
		hostport := body[at+1:]
		if dec, err := decodeB64Any(userinfo); err == nil && strings.Contains(string(dec), ":") {
			userinfo = string(dec)
		}
		mp := strings.SplitN(userinfo, ":", 2)
		if len(mp) != 2 {
			return nil, fmt.Errorf("ss: bad method:password")
		}
		p.Cipher, p.Password = mp[0], mp[1]
		if err := splitHostPort(p, hostport); err != nil {
			return nil, err
		}
	} else {
		// Legacy: whole thing is base64(method:password@host:port).
		dec, err := decodeB64Any(body)
		if err != nil {
			return nil, fmt.Errorf("ss: base64: %w", err)
		}
		s := string(dec)
		at := strings.LastIndex(s, "@")
		if at < 0 {
			return nil, fmt.Errorf("ss: legacy form missing @")
		}
		mp := strings.SplitN(s[:at], ":", 2)
		if len(mp) != 2 {
			return nil, fmt.Errorf("ss: legacy bad method:password")
		}
		p.Cipher, p.Password = mp[0], mp[1]
		if err := splitHostPort(p, s[at+1:]); err != nil {
			return nil, err
		}
	}
	if p.Tag == "" {
		p.Tag = p.Server
	}
	return p, nil
}

func splitHostPort(p *Proxy, hostport string) error {
	h := strings.LastIndex(hostport, ":")
	if h < 0 {
		return fmt.Errorf("%s: bad host:port %q", p.Protocol, hostport)
	}
	p.Server = strings.Trim(hostport[:h], "[]")
	port, err := strconv.Atoi(hostport[h+1:])
	if err != nil {
		return fmt.Errorf("%s: bad port: %w", p.Protocol, err)
	}
	p.Port = port
	return nil
}
