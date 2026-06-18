package clientgen

import (
	"encoding/json"
	"fmt"

	"github.com/hiddify/hiddify_config_health/internal/proxyuri"
)

// XrayClient builds an xray client config: a socks inbound on socksPort
// routing all traffic to a "proxy" outbound built from p. Returns
// ErrUnsupported for protocols xray cannot run (hysteria2, tuic).
func XrayClient(p proxyuri.Proxy, socksPort int) ([]byte, error) {
	out, err := xrayOutbound(p)
	if err != nil {
		return nil, err
	}
	cfg := jsonObj{
		"log": jsonObj{"loglevel": "error"},
		"inbounds": []jsonObj{{
			"tag": "socks-in", "port": socksPort, "listen": "127.0.0.1",
			"protocol": "socks", "settings": jsonObj{"auth": "noauth", "udp": true},
		}},
		"routing": jsonObj{
			"domainStrategy": "AsIs",
			"rules":          []jsonObj{{"type": "field", "port": "0-65535", "outboundTag": "proxy"}},
		},
		"outbounds": []any{out, jsonObj{"protocol": "freedom", "tag": "direct"}},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func xrayOutbound(p proxyuri.Proxy) (jsonObj, error) {
	o := jsonObj{"tag": "proxy"}
	switch p.Protocol {
	case "vless":
		user := jsonObj{"id": p.UUID, "encryption": "none"}
		if p.Flow != "" {
			user["flow"] = p.Flow
		}
		o["protocol"] = "vless"
		o["settings"] = jsonObj{"vnext": []jsonObj{{
			"address": p.Server, "port": p.Port, "users": []jsonObj{user},
		}}}
	case "vmess":
		o["protocol"] = "vmess"
		o["settings"] = jsonObj{"vnext": []jsonObj{{
			"address": p.Server, "port": p.Port,
			"users": []jsonObj{{"id": p.UUID, "alterId": p.AlterID, "security": "auto"}},
		}}}
	case "trojan":
		o["protocol"] = "trojan"
		o["settings"] = jsonObj{"servers": []jsonObj{{
			"address": p.Server, "port": p.Port, "password": p.Password,
		}}}
	case "shadowsocks":
		o["protocol"] = "shadowsocks"
		o["settings"] = jsonObj{"servers": []jsonObj{{
			"address": p.Server, "port": p.Port, "method": p.Cipher, "password": p.Password,
		}}}
	default:
		// hysteria2 / tuic are not xray protocols.
		return nil, fmt.Errorf("%w: xray %q", ErrUnsupported, p.Protocol)
	}
	o["streamSettings"] = xrayStream(p)
	return o, nil
}

func xrayStream(p proxyuri.Proxy) jsonObj {
	ss := jsonObj{}
	// network
	switch p.Transport {
	case "ws":
		ss["network"] = "ws"
		ws := jsonObj{}
		if p.Path != "" {
			ws["path"] = p.Path
		}
		if p.Host != "" {
			ws["host"] = p.Host
		}
		ss["wsSettings"] = ws
	case "grpc":
		ss["network"] = "grpc"
		ss["grpcSettings"] = jsonObj{"serviceName": p.ServiceName}
	case "xhttp":
		ss["network"] = "xhttp"
		xh := jsonObj{"mode": "auto"}
		if p.Path != "" {
			xh["path"] = p.Path
		}
		if p.Host != "" {
			xh["host"] = p.Host
		}
		ss["xhttpSettings"] = xh
	case "h2":
		ss["network"] = "http"
		h := jsonObj{}
		if p.Path != "" {
			h["path"] = p.Path
		}
		if p.Host != "" {
			h["host"] = []string{p.Host}
		}
		ss["httpSettings"] = h
	default:
		ss["network"] = "tcp"
	}

	// security
	switch p.Security {
	case "tls":
		ss["security"] = "tls"
		t := jsonObj{}
		if p.SNI != "" {
			t["serverName"] = p.SNI
		}
		if len(p.ALPN) > 0 {
			t["alpn"] = p.ALPN
		}
		if p.Insecure {
			t["allowInsecure"] = true
		}
		t["fingerprint"] = "chrome"
		ss["tlsSettings"] = t
	case "reality":
		ss["security"] = "reality"
		r := jsonObj{"serverName": p.SNI, "publicKey": p.PublicKey, "fingerprint": "chrome"}
		if p.ShortID != "" {
			r["shortId"] = p.ShortID
		}
		ss["realitySettings"] = r
	}
	return ss
}
