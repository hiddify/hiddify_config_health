package proxyuri

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// vmessJSON is the standard v2rayN base64-JSON share format.
type vmessJSON struct {
	PS   string      `json:"ps"`
	Add  string      `json:"add"`
	Port interface{} `json:"port"`
	ID   string      `json:"id"`
	Aid  interface{} `json:"aid"`
	Net  string      `json:"net"`
	Type string      `json:"type"`
	Host string      `json:"host"`
	Path string      `json:"path"`
	TLS  string      `json:"tls"`
	SNI  string      `json:"sni"`
	ALPN string      `json:"alpn"`
}

// parseVMess parses vmess://<base64-json>
func parseVMess(uri string) (*Proxy, error) {
	raw := strings.TrimPrefix(uri, "vmess://")
	dec, err := decodeB64Any(raw)
	if err != nil {
		return nil, fmt.Errorf("vmess: base64: %w", err)
	}
	var v vmessJSON
	if err := json.Unmarshal(dec, &v); err != nil {
		return nil, fmt.Errorf("vmess: json: %w", err)
	}
	p := &Proxy{Protocol: "vmess", Raw: uri, Tag: v.PS, Server: v.Add, UUID: v.ID}
	p.Port = toInt(v.Port)
	if p.Server == "" || p.Port == 0 || p.UUID == "" {
		return nil, fmt.Errorf("vmess: missing add/port/id")
	}
	p.AlterID = toInt(v.Aid)
	p.Transport = normTransport(firstNonEmpty(v.Net, "tcp"))
	p.Host = v.Host
	p.Path = v.Path
	if v.Net == "grpc" {
		p.ServiceName = v.Path
	}
	if v.TLS == "tls" {
		p.Security = "tls"
	} else {
		p.Security = "none"
	}
	p.SNI = firstNonEmpty(v.SNI, v.Host)
	if v.ALPN != "" {
		p.ALPN = strings.Split(v.ALPN, ",")
	}
	if p.Tag == "" {
		p.Tag = p.Server
	}
	return p, nil
}

func decodeB64Any(s string) ([]byte, error) {
	s = stripWhitespace(s)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if dec, err := enc.DecodeString(s); err == nil {
			return dec, nil
		}
	}
	return nil, fmt.Errorf("not valid base64")
}

func toInt(v interface{}) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(x))
		return n
	case int:
		return x
	}
	return 0
}
