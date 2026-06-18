package clientgen

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/hiddify/hiddify_config_health/internal/proxyuri"
)

func mustParse(t *testing.T, uri string) proxyuri.Proxy {
	t.Helper()
	p, err := proxyuri.ParseURI(uri)
	if err != nil {
		t.Fatalf("parse %q: %v", uri, err)
	}
	return *p
}

func assertJSON(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, b)
	}
	return m
}

func TestSingboxVLESSReality(t *testing.T) {
	p := mustParse(t, "vless://uuid@1.2.3.4:443?type=tcp&security=reality&sni=ms.com&pbk=KEY&sid=ab&flow=xtls-rprx-vision#n")
	b, err := SingboxClient(p, 10800)
	if err != nil {
		t.Fatal(err)
	}
	m := assertJSON(t, b)
	out := m["outbounds"].([]any)[0].(map[string]any)
	if out["type"] != "vless" || out["flow"] != "xtls-rprx-vision" {
		t.Errorf("outbound: %v", out)
	}
	tls := out["tls"].(map[string]any)
	if tls["reality"] == nil || tls["utls"] == nil {
		t.Errorf("reality/utls missing: %v", tls)
	}
}

func TestSingboxShadowsocks(t *testing.T) {
	p := mustParse(t, "ss://YWVzLTI1Ni1nY206cHc@1.1.1.1:8388#s")
	b, err := SingboxClient(p, 10800)
	if err != nil {
		t.Fatal(err)
	}
	out := assertJSON(t, b)["outbounds"].([]any)[0].(map[string]any)
	if out["type"] != "shadowsocks" || out["method"] != "aes-256-gcm" {
		t.Errorf("%v", out)
	}
}

func TestXrayVLESSWs(t *testing.T) {
	p := mustParse(t, "vless://uuid@1.2.3.4:443?type=ws&security=tls&sni=h.com&path=%2Fws&host=h.com#n")
	b, err := XrayClient(p, 10800)
	if err != nil {
		t.Fatal(err)
	}
	out := assertJSON(t, b)["outbounds"].([]any)[0].(map[string]any)
	if out["protocol"] != "vless" {
		t.Errorf("proto: %v", out["protocol"])
	}
	ss := out["streamSettings"].(map[string]any)
	if ss["network"] != "ws" || ss["security"] != "tls" {
		t.Errorf("stream: %v", ss)
	}
}

func TestXrayUnsupportedHysteria2(t *testing.T) {
	p := mustParse(t, "hysteria2://pw@h.com:443?sni=h.com#n")
	_, err := XrayClient(p, 10800)
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("want ErrUnsupported, got %v", err)
	}
}

func TestSingboxHysteria2Supported(t *testing.T) {
	p := mustParse(t, "hysteria2://pw@h.com:443?sni=h.com#n")
	b, err := SingboxClient(p, 10800)
	if err != nil {
		t.Fatal(err)
	}
	out := assertJSON(t, b)["outbounds"].([]any)[0].(map[string]any)
	if out["type"] != "hysteria2" || out["password"] != "pw" {
		t.Errorf("%v", out)
	}
}

func TestGenerateDispatch(t *testing.T) {
	p := mustParse(t, "trojan://pw@1.1.1.1:443?sni=x#n")
	for _, core := range []string{"sing-box", "xray"} {
		if _, err := Generate(core, p, 10800); err != nil {
			t.Errorf("Generate(%s): %v", core, err)
		}
	}
	if _, err := Generate("nope", p, 10800); !errors.Is(err, ErrUnsupported) {
		t.Errorf("unknown core should be ErrUnsupported, got %v", err)
	}
}
