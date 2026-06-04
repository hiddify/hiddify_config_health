package tmpl

import (
	"bytes"
	"testing"
)

func TestRender_EnvAccess(t *testing.T) {
	t.Setenv("HCH_TEST_VAR", "hello-from-env")
	src := []byte(`{"val": "{{ env.HCH_TEST_VAR }}"}`)
	out, _, err := Render(src, map[string]string{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !bytes.Contains(out, []byte("hello-from-env")) {
		t.Errorf("env var not substituted: %s", out)
	}
}

func TestRender_EnvConditional(t *testing.T) {
	t.Setenv("HCH_TEST_FLAG", "1")
	src := []byte(`{"ok": {% if env.HCH_TEST_FLAG %}true{% else %}false{% endif %}}`)
	out, _, err := Render(src, map[string]string{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !bytes.Contains(out, []byte("true")) {
		t.Errorf("env conditional not working: %s", out)
	}
}

func TestRender_EnvMissing(t *testing.T) {
	// Unset env var should render as empty string, not error.
	src := []byte(`{"val": "{{ env.HCH_DEFINITELY_NOT_SET_XYZ123 }}"}`)
	out, _, err := Render(src, map[string]string{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if bytes.Contains(out, []byte("HCH_DEFINITELY")) {
		t.Errorf("missing env var leaked placeholder: %s", out)
	}
}

func TestRender_BuiltinDefaults(t *testing.T) {
	// VLESS_ENC, VLESS_DEC, LOG_LEVEL should have defaults when not set.
	src := []byte(`{"enc":"{{ VLESS_ENC }}","dec":"{{ VLESS_DEC }}","log":"{{ LOG_LEVEL }}"}`)
	out, _, err := Render(src, map[string]string{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !bytes.Contains(out, []byte(`"none"`)) {
		t.Errorf("VLESS_ENC default not applied: %s", out)
	}
	if !bytes.Contains(out, []byte(`"error"`)) {
		t.Errorf("LOG_LEVEL default not applied: %s", out)
	}
}

func TestRender_BuiltinOverridable(t *testing.T) {
	// Caller can override built-in defaults.
	src := []byte(`{"enc":"{{ VLESS_ENC }}"}`)
	out, _, err := Render(src, map[string]string{"VLESS_ENC": "xtls"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !bytes.Contains(out, []byte("xtls")) {
		t.Errorf("VLESS_ENC override not applied: %s", out)
	}
}
