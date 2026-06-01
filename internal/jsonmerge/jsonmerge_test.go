package jsonmerge

import (
	"encoding/json"
	"testing"
)

func TestMerge_ScalarOverride(t *testing.T) {
	base := `{"a":1,"b":2}`
	over := `{"b":99,"c":3}`
	out, err := Merge([]byte(base), []byte(over))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	json.Unmarshal(out, &m)
	if m["a"] != 1.0 || m["b"] != 99.0 || m["c"] != 3.0 {
		t.Errorf("got %v", m)
	}
}

func TestMerge_ObjectRecurse(t *testing.T) {
	base := `{"tls":{"enabled":false,"version":12}}`
	over := `{"tls":{"enabled":true}}`
	out, _ := Merge([]byte(base), []byte(over))
	var m map[string]interface{}
	json.Unmarshal(out, &m)
	tls := m["tls"].(map[string]interface{})
	if tls["enabled"] != true {
		t.Error("enabled should be true")
	}
	if tls["version"] != 12.0 {
		t.Error("version should be preserved from base")
	}
}

func TestMerge_ArrayReplaced(t *testing.T) {
	base := `{"inbounds":[{"tag":"old"}]}`
	over := `{"inbounds":[{"tag":"new"},{"tag":"new2"}]}`
	out, _ := Merge([]byte(base), []byte(over))
	var m map[string]interface{}
	json.Unmarshal(out, &m)
	arr := m["inbounds"].([]interface{})
	if len(arr) != 2 {
		t.Errorf("array should be replaced, len=%d", len(arr))
	}
}

func TestMerge_NullRemovesKey(t *testing.T) {
	base := `{"a":1,"b":2}`
	over := `{"b":null}`
	out, _ := Merge([]byte(base), []byte(over))
	var m map[string]interface{}
	json.Unmarshal(out, &m)
	if _, ok := m["b"]; ok {
		t.Error("b should be removed by null override")
	}
	if m["a"] != 1.0 {
		t.Error("a should be preserved")
	}
}

func TestMerge_BaseOnlyKey(t *testing.T) {
	base := `{"log":{"level":"error"},"inbounds":[{"tag":"socks"}]}`
	over := `{"outbounds":[{"protocol":"vless"}]}`
	out, _ := Merge([]byte(base), []byte(over))
	var m map[string]interface{}
	json.Unmarshal(out, &m)
	if m["log"] == nil {
		t.Error("log from base should be kept")
	}
	if m["inbounds"] == nil {
		t.Error("inbounds from base should be kept")
	}
	if m["outbounds"] == nil {
		t.Error("outbounds from override should be added")
	}
}
