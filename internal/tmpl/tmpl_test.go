package tmpl

import (
	"bytes"
	"testing"
)

func TestRender_Basic(t *testing.T) {
	src := []byte(`{"server":"{{SERVER}}","port":{{PORT}}}`)
	out, resolved, err := Render(src, map[string]string{
		"SERVER": "1.2.3.4",
		"PORT":   "8388",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("1.2.3.4")) {
		t.Errorf("SERVER not substituted: %s", out)
	}
	if resolved["PORT"] != "8388" {
		t.Errorf("PORT = %q, want 8388", resolved["PORT"])
	}
}

func TestRender_AutoPort(t *testing.T) {
	src := []byte(`{"port":{{PORT}}}`)
	out, resolved, err := Render(src, map[string]string{"PORT": "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("auto")) {
		t.Errorf("auto not resolved: %s", out)
	}
	if resolved["PORT"] == "auto" || resolved["PORT"] == "" {
		t.Errorf("PORT not auto-resolved, got %q", resolved["PORT"])
	}
}

func TestRender_AutoUUID(t *testing.T) {
	src := []byte(`{"uuid":"{{UUID}}"}`)
	out, _, err := Render(src, map[string]string{"UUID": "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("auto")) {
		t.Errorf("UUID not resolved: %s", out)
	}
}

func TestRender_AutoPassword(t *testing.T) {
	src := []byte(`{"pass":"{{PASSWORD}}"}`)
	out, _, err := Render(src, map[string]string{"PASSWORD": "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("auto")) {
		t.Errorf("PASSWORD not resolved: %s", out)
	}
}

func TestRender_UnknownKey_Left(t *testing.T) {
	src := []byte(`{{UNKNOWN}}`)
	out, _, _ := Render(src, map[string]string{})
	if !bytes.Equal(out, src) {
		t.Errorf("unknown placeholder should be left unchanged; got %s", out)
	}
}
