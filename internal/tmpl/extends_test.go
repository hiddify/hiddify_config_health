package tmpl

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRenderFile_ExtendsWithInclude(t *testing.T) {
	// Build a temp dir tree mirroring the xray example structure.
	root := t.TempDir()
	baseDir := filepath.Join(root, "templates", "base")
	tlsDir := filepath.Join(root, "templates", "tls")
	exDir := filepath.Join(root, "vless-xhttp")
	os.MkdirAll(baseDir, 0o755)
	os.MkdirAll(tlsDir, 0o755)
	os.MkdirAll(exDir, 0o755)

	// Base template with block.
	os.WriteFile(filepath.Join(baseDir, "client.json.j2"), []byte(`{
  "inbounds": [{"tag":"socks-in","port":{{ SOCKS_PORT }}}],
  "outbounds": [{% block outbound %}{"tag":"direct"}{% endblock %}]
}`), 0o644)

	// TLS partial.
	os.WriteFile(filepath.Join(tlsDir, "client.tpl"), []byte(`"security": "{% if TLS %}tls{% else %}none{% endif %}",`), 0o644)

	// Child template using extends + include.
	child := filepath.Join(exDir, "client.json.j2")
	os.WriteFile(child, []byte(`{% extends "../templates/base/client.json.j2" %}
{% block outbound %}
{
  "tag": "vless-out",
  "streamSettings": {
    {% include "../templates/tls/client.tpl" %}
    "network": "xhttp"
  }
}
{% endblock %}`), 0o644)

	vars := map[string]string{"SOCKS_PORT": "1080", "TLS": "1"}
	out, _, err := RenderFile(child, vars)
	if err != nil {
		t.Fatalf("RenderFile: %v", err)
	}
	// Must have base inbound (socks-in) from extends.
	if !bytes.Contains(out, []byte("socks-in")) {
		t.Errorf("base inbound missing: %s", out)
	}
	// Must have child outbound (vless-out) from block.
	if !bytes.Contains(out, []byte("vless-out")) {
		t.Errorf("child outbound missing: %s", out)
	}
	// Must have TLS from include.
	if !bytes.Contains(out, []byte(`"tls"`)) {
		t.Errorf("tls include missing: %s", out)
	}
}
