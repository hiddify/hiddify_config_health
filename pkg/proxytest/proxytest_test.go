package proxytest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hiddify/hiddify_config_health/internal/freeport"
)

// TestProxyAgainstLocalSingbox stands up a real local sing-box VLESS+TCP
// server (no TLS) and verifies TestProxy connects through it on sing-box.
// Skips if no sing-box binary is available.
func TestProxyAgainstLocalSingbox(t *testing.T) {
	bin := os.Getenv("SINGBOX_BIN")
	if bin == "" {
		if p, err := exec.LookPath("sing-box"); err == nil {
			bin = p
		}
	}
	if bin == "" {
		t.Skip("no sing-box binary (set SINGBOX_BIN)")
	}

	id := uuid.New().String()
	port, _ := freeport.Free()

	serverCfg := map[string]any{
		"log": map[string]any{"level": "error"},
		"inbounds": []map[string]any{{
			"type": "vless", "tag": "in", "listen": "127.0.0.1", "listen_port": port,
			"users": []map[string]any{{"uuid": id}},
		}},
		"outbounds": []map[string]any{{"type": "direct", "tag": "direct"}},
	}
	cfgFile, _ := os.CreateTemp("", "srv-*.json")
	defer os.Remove(cfgFile.Name())
	enc := json.NewEncoder(cfgFile)
	_ = enc.Encode(serverCfg)
	_ = cfgFile.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "run", "-c", cfgFile.Name())
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	time.Sleep(500 * time.Millisecond)

	uri := fmt.Sprintf("vless://%s@127.0.0.1:%d?type=tcp&security=none#local", id, port)

	res, err := TestProxy(context.Background(), uri, Options{
		Cores:    []string{"sing-box", "xray"},
		Checks:   []string{"dns", "http"},
		Timeout:  8 * time.Second,
		Binaries: map[string]string{"sing-box": bin},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.PerCore) != 2 {
		t.Fatalf("want 2 core results, got %d", len(res.PerCore))
	}

	var sb *CoreResult
	for i := range res.PerCore {
		if res.PerCore[i].Core == "sing-box" {
			sb = &res.PerCore[i]
		}
	}
	if sb == nil {
		t.Fatal("no sing-box result")
	}
	if !sb.Pass {
		t.Errorf("sing-box should pass through local server; err=%q checks=%+v", sb.Err, sb.Checks)
	}
}

func TestOptionsDefaults(t *testing.T) {
	o := Options{}
	if len(o.cores()) != 2 {
		t.Error("default cores")
	}
	c, _ := o.checks()
	if len(c) == 0 {
		t.Error("default checks empty")
	}
	if o.Full {
		t.Error("Full should default false")
	}
	o2 := Options{Full: true}
	full, opt := o2.checks()
	if len(full) <= len(fastChecks) || len(opt) == 0 {
		t.Error("full suite should be larger + have optional")
	}
}

func TestUnsupportedCoreClean(t *testing.T) {
	// hysteria2 on xray must be Supported=false, not an error.
	res, err := TestProxy(context.Background(), "hysteria2://pw@127.0.0.1:1?sni=x#n",
		Options{Cores: []string{"xray"}, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.PerCore) != 1 || res.PerCore[0].Supported {
		t.Errorf("hysteria2 on xray should be unsupported: %+v", res.PerCore)
	}
}
