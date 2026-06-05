package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeRunJSON(t *testing.T, dir, name string, v interface{}) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRunConfig_InheritanceVars(t *testing.T) {
	root := t.TempDir()
	coreDir := filepath.Join(root, "xray")
	exDir := filepath.Join(coreDir, "vless-xhttp")

	// Root: common server vars
	writeRunJSON(t, root, "run.json", map[string]interface{}{
		"checks":      []string{"dns", "http"},
		"timeout_sec": 30,
		"vars": []interface{}{
			map[string]interface{}{"TITLE": "T1", "SERVER": "127.0.0.1", "PORT": "{{AUTO_PORT}}", "SOCKS_PORT": "{{AUTO_SOCKS_PORT}}"},
		},
	})

	// Core level: binary paths, no extra vars
	writeRunJSON(t, coreDir, "run.json", map[string]interface{}{
		"client_process_path": "env.XRAY_BIN",
		"client_arg":          "run -c ",
		"vars":                []interface{}{},
	})

	// Example level: protocol variants
	writeRunJSON(t, exDir, "run.json", map[string]interface{}{
		"name":          "VLESS xHTTP",
		"server_config": "server.json",
		"client_config": "client.json",
		"vars": []interface{}{
			map[string]interface{}{"TITLE": "plain-tls", "TLS": "1", "UUID": "{{AUTO_UUID}}"},
			map[string]interface{}{"TITLE": "vless-flow", "TLS": "1", "UUID": "{{AUTO_UUID}}", "VLESS_FLOW": "xtls-rprx-vision"},
		},
	})

	cfg, err := loadRunConfig(exDir)
	if err != nil {
		t.Fatalf("loadRunConfig: %v", err)
	}

	variants := cfg.Variants()

	// Should have 2 variants (from child), each with SERVER inherited from root.
	if len(variants) != 2 {
		t.Fatalf("want 2 variants, got %d", len(variants))
	}

	for _, v := range variants {
		if v.Vars["SERVER"] != "127.0.0.1" {
			t.Errorf("variant %q: SERVER = %q, want 127.0.0.1", v.Title, v.Vars["SERVER"])
		}
		if v.Vars["TLS"] != "1" {
			t.Errorf("variant %q: TLS = %q, want 1", v.Title, v.Vars["TLS"])
		}
	}

	// VLESS_FLOW only in second variant.
	if variants[1].Vars["VLESS_FLOW"] != "xtls-rprx-vision" {
		t.Errorf("variant[1] VLESS_FLOW = %q, want xtls-rprx-vision", variants[1].Vars["VLESS_FLOW"])
	}
	if variants[0].Vars["VLESS_FLOW"] != "" {
		t.Errorf("variant[0] VLESS_FLOW should be empty, got %q", variants[0].Vars["VLESS_FLOW"])
	}

	// Binary path from core level.
	if cfg.ClientProcessPath != "env.XRAY_BIN" {
		t.Errorf("ClientProcessPath = %q, want env.XRAY_BIN", cfg.ClientProcessPath)
	}
	if cfg.Checks[0] != "dns" {
		t.Errorf("Checks = %v, want [dns http] from root", cfg.Checks)
	}
}

func TestLoadRunConfig_SingleVariantMap(t *testing.T) {
	dir := t.TempDir()
	writeRunJSON(t, dir, "run.json", map[string]interface{}{
		"name": "test",
		"vars": map[string]interface{}{
			"SERVER": "1.2.3.4",
			"PORT":   "8388",
		},
		"checks": []string{"http"},
	})

	cfg, err := loadRunConfig(dir)
	if err != nil {
		t.Fatalf("loadRunConfig: %v", err)
	}
	variants := cfg.Variants()
	if len(variants) != 1 {
		t.Fatalf("want 1 variant, got %d", len(variants))
	}
	if variants[0].Vars["SERVER"] != "1.2.3.4" {
		t.Errorf("SERVER = %q", variants[0].Vars["SERVER"])
	}
}

func TestVariants_Title(t *testing.T) {
	cfg := RunConfig{
		Name: "MyExample",
		VarsRaw: []interface{}{
			map[string]interface{}{"TITLE": "alpha", "X": "1"},
			map[string]interface{}{"TITLE": "beta", "X": "2"},
		},
	}
	v := cfg.Variants()
	if v[0].Title != "alpha" || v[1].Title != "beta" {
		t.Errorf("titles = %q %q, want alpha beta", v[0].Title, v[1].Title)
	}
	// TITLE removed from Vars.
	if _, ok := v[0].Vars["TITLE"]; ok {
		t.Error("TITLE key should be removed from Vars")
	}
}

func TestVariants_BoolTrueBecomesOne(t *testing.T) {
	cfg := RunConfig{
		VarsRaw: []interface{}{
			map[string]interface{}{"TLS": true},
		},
	}
	v := cfg.Variants()
	if v[0].Vars["TLS"] != "1" {
		t.Errorf("bool true should become '1', got %q", v[0].Vars["TLS"])
	}
}
