package core

import (
	"context"
	"io"
	"testing"
)

func TestRegistry_KnownCores(t *testing.T) {
	for _, name := range []string{"sing-box", "xray", "hiddify-core"} {
		c := New(name, "")
		if c == nil {
			t.Errorf("New(%q) returned nil — not registered", name)
		}
	}
}

func TestRegistry_UnknownCore(t *testing.T) {
	c := New("nonexistent-core", "")
	if c != nil {
		t.Errorf("New(unknown) should return nil, got %T", c)
	}
}

func TestNames_ContainsKnown(t *testing.T) {
	names := Names()
	want := map[string]bool{"sing-box": true, "xray": true, "hiddify-core": true}
	for _, n := range names {
		delete(want, n)
	}
	if len(want) > 0 {
		t.Errorf("missing registered cores: %v", want)
	}
}

func TestNewRaw_Name(t *testing.T) {
	c := NewRaw("/usr/bin/mycore", []string{"run", "-c"})
	if c.Name() != "/usr/bin/mycore" {
		t.Errorf("Name = %q", c.Name())
	}
}

func TestNewRaw_Version_NotFound(t *testing.T) {
	// Non-existent binary → Version() returns "".
	c := NewRaw("/nonexistent/binary", nil)
	v := c.Version()
	if v != "" {
		t.Errorf("Version for nonexistent binary = %q, want empty", v)
	}
}

func TestProcessCore_Stop_NilCmd(t *testing.T) {
	// Stop before Start should be a no-op, not panic.
	c := New("sing-box", "/nonexistent/singbox")
	if err := c.Stop(); err != nil {
		t.Errorf("Stop before Start: %v", err)
	}
}

func TestProcessCore_Start_NonExistentBinary(t *testing.T) {
	c := NewRaw("/nonexistent/binary", []string{"run"})
	err := c.Start(context.Background(), "/tmp/config.json", io.Discard)
	if err == nil {
		_ = c.Stop()
		t.Error("expected error starting nonexistent binary")
	}
}
