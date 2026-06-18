package proxyuri

import (
	"encoding/base64"
	"testing"
)

func TestParseVLESS(t *testing.T) {
	uri := "vless://550e8400-e29b-41d4-a716-446655440000@1.2.3.4:443?type=ws&security=reality&sni=example.com&pbk=PUBKEY&sid=ab12&flow=xtls-rprx-vision&path=%2Fws&host=cdn.example.com#my-node"
	p, err := ParseURI(uri)
	if err != nil {
		t.Fatal(err)
	}
	if p.Protocol != "vless" || p.Server != "1.2.3.4" || p.Port != 443 {
		t.Errorf("server/port: %+v", p)
	}
	if p.UUID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("uuid: %s", p.UUID)
	}
	if p.Security != "reality" || p.SNI != "example.com" || p.PublicKey != "PUBKEY" || p.ShortID != "ab12" {
		t.Errorf("reality fields: %+v", p)
	}
	if p.Transport != "ws" || p.Path != "/ws" || p.Host != "cdn.example.com" {
		t.Errorf("transport: %+v", p)
	}
	if p.Flow != "xtls-rprx-vision" {
		t.Errorf("flow: %s", p.Flow)
	}
	if p.Tag != "my-node" {
		t.Errorf("tag: %s", p.Tag)
	}
}

func TestParseTrojan(t *testing.T) {
	p, err := ParseURI("trojan://secretpass@host.example:8443?sni=host.example&type=grpc&serviceName=gun#t")
	if err != nil {
		t.Fatal(err)
	}
	if p.Password != "secretpass" || p.Port != 8443 || p.Security != "tls" {
		t.Errorf("%+v", p)
	}
	if p.Transport != "grpc" || p.ServiceName != "gun" {
		t.Errorf("grpc: %+v", p)
	}
}

func TestParseVMess(t *testing.T) {
	js := `{"v":"2","ps":"node1","add":"5.6.7.8","port":"443","id":"id-uuid","aid":"0","net":"ws","host":"h.com","path":"/p","tls":"tls","sni":"h.com"}`
	uri := "vmess://" + base64.StdEncoding.EncodeToString([]byte(js))
	p, err := ParseURI(uri)
	if err != nil {
		t.Fatal(err)
	}
	if p.Server != "5.6.7.8" || p.Port != 443 || p.UUID != "id-uuid" {
		t.Errorf("%+v", p)
	}
	if p.Transport != "ws" || p.Path != "/p" || p.Security != "tls" {
		t.Errorf("transport/tls: %+v", p)
	}
	if p.Tag != "node1" {
		t.Errorf("tag: %s", p.Tag)
	}
}

func TestParseShadowsocksSIP002(t *testing.T) {
	userinfo := base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:mypassword"))
	p, err := ParseURI("ss://" + userinfo + "@9.9.9.9:8388#ss-node")
	if err != nil {
		t.Fatal(err)
	}
	if p.Cipher != "aes-256-gcm" || p.Password != "mypassword" || p.Server != "9.9.9.9" || p.Port != 8388 {
		t.Errorf("%+v", p)
	}
}

func TestParseShadowsocksLegacy(t *testing.T) {
	blob := base64.StdEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:pw@1.1.1.1:8388"))
	p, err := ParseURI("ss://" + blob + "#legacy")
	if err != nil {
		t.Fatal(err)
	}
	if p.Cipher != "chacha20-ietf-poly1305" || p.Password != "pw" || p.Port != 8388 {
		t.Errorf("%+v", p)
	}
}

func TestParseHysteria2(t *testing.T) {
	p, err := ParseURI("hysteria2://pw@h.example:443?sni=h.example&insecure=1#hy2")
	if err != nil {
		t.Fatal(err)
	}
	if p.Protocol != "hysteria2" || p.Password != "pw" || !p.Insecure || p.Transport != "quic" {
		t.Errorf("%+v", p)
	}
}

func TestParseTUIC(t *testing.T) {
	p, err := ParseURI("tuic://uuid-x:pw@t.example:443?sni=t.example&alpn=h3#tuic")
	if err != nil {
		t.Fatal(err)
	}
	if p.UUID != "uuid-x" || p.Password != "pw" || len(p.ALPN) != 1 || p.ALPN[0] != "h3" {
		t.Errorf("%+v", p)
	}
}

func TestParseListAndErrors(t *testing.T) {
	text := "vless://u@1.1.1.1:443#a\n# comment\n\nbogus://x\ntrojan://p@2.2.2.2:443#b\n"
	proxies, errs := ParseList(text)
	if len(proxies) != 2 {
		t.Errorf("want 2 proxies, got %d", len(proxies))
	}
	if len(errs) != 1 {
		t.Errorf("want 1 error, got %d", len(errs))
	}
}

func TestDecodeMaybeBase64(t *testing.T) {
	plain := "vless://u@1.1.1.1:443#a\ntrojan://p@2.2.2.2:443#b"
	b64 := base64.StdEncoding.EncodeToString([]byte(plain))
	if got := decodeMaybeBase64(b64); got != plain {
		t.Errorf("base64 not decoded:\n%q", got)
	}
	if got := decodeMaybeBase64(plain); got != plain {
		t.Errorf("plain text mangled")
	}
}

func TestUnsupportedScheme(t *testing.T) {
	if _, err := ParseURI("naive+https://x@y:443"); err == nil {
		t.Error("expected error for unsupported scheme")
	}
}
