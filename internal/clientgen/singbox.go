package clientgen

import (
	"encoding/json"
	"fmt"

	"github.com/hiddify/hiddify_config_health/internal/proxyuri"
)

// SingboxClient builds a sing-box client config: a mixed inbound on socksPort
// routing all traffic to an outbound built from p.
func SingboxClient(p proxyuri.Proxy, socksPort int) ([]byte, error) {
	out, err := singboxOutbound(p)
	if err != nil {
		return nil, err
	}
	cfg := jsonObj{
		"log": jsonObj{"level": "error"},
		"dns": jsonObj{"servers": []jsonObj{{"type": "local", "tag": "default"}}},
		"inbounds": []jsonObj{{
			"type": "mixed", "tag": "in", "listen": "127.0.0.1", "listen_port": socksPort,
		}},
		"outbounds": []any{out, jsonObj{"type": "direct", "tag": "direct"}},
		"route": jsonObj{
			"final": "proxy", "default_domain_resolver": "default", "auto_detect_interface": true,
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func singboxOutbound(p proxyuri.Proxy) (jsonObj, error) {
	o := jsonObj{"tag": "proxy", "server": p.Server, "server_port": p.Port}
	switch p.Protocol {
	case "vless":
		o["type"] = "vless"
		o["uuid"] = p.UUID
		if p.Flow != "" {
			o["flow"] = p.Flow
		}
	case "vmess":
		o["type"] = "vmess"
		o["uuid"] = p.UUID
		o["security"] = "auto"
		o["alter_id"] = p.AlterID
	case "trojan":
		o["type"] = "trojan"
		o["password"] = p.Password
	case "shadowsocks":
		o["type"] = "shadowsocks"
		o["method"] = p.Cipher
		o["password"] = p.Password
	case "hysteria2":
		o["type"] = "hysteria2"
		o["password"] = p.Password
	case "tuic":
		o["type"] = "tuic"
		o["uuid"] = p.UUID
		o["password"] = p.Password
	default:
		return nil, fmt.Errorf("%w: sing-box %q", ErrUnsupported, p.Protocol)
	}

	if tls := singboxTLS(p); tls != nil {
		o["tls"] = tls
	}
	if tr := singboxTransport(p); tr != nil {
		o["transport"] = tr
	}
	return o, nil
}

func singboxTLS(p proxyuri.Proxy) jsonObj {
	// quic-based protocols always use TLS implicitly; reality/tls => explicit.
	if p.Security == "none" && p.Protocol != "hysteria2" && p.Protocol != "tuic" {
		return nil
	}
	tls := jsonObj{"enabled": true}
	if p.SNI != "" {
		tls["server_name"] = p.SNI
	}
	if len(p.ALPN) > 0 {
		tls["alpn"] = p.ALPN
	}
	if p.Insecure {
		tls["insecure"] = true
	}
	if p.Security == "reality" {
		r := jsonObj{"enabled": true, "public_key": p.PublicKey}
		if p.ShortID != "" {
			r["short_id"] = p.ShortID
		}
		tls["reality"] = r
		// Reality requires a uTLS fingerprint.
		tls["utls"] = jsonObj{"enabled": true, "fingerprint": "chrome"}
	}
	return tls
}

func singboxTransport(p proxyuri.Proxy) jsonObj {
	switch p.Transport {
	case "ws":
		t := jsonObj{"type": "ws"}
		if p.Path != "" {
			t["path"] = p.Path
		}
		if p.Host != "" {
			t["headers"] = jsonObj{"Host": p.Host}
		}
		return t
	case "grpc":
		return jsonObj{"type": "grpc", "service_name": p.ServiceName}
	case "xhttp", "h2":
		t := jsonObj{"type": "http"}
		if p.Host != "" {
			t["host"] = p.Host
		}
		if p.Path != "" {
			t["path"] = p.Path
		}
		return t
	default: // tcp / quic — no transport block
		return nil
	}
}
