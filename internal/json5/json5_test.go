package json5

import (
	"encoding/json"
	"testing"
)

func mustValid(t *testing.T, src, label string) {
	t.Helper()
	out, err := Strip([]byte(src))
	if err != nil {
		t.Fatalf("%s: Strip error: %v", label, err)
	}
	if !json.Valid(out) {
		t.Errorf("%s: output not valid JSON:\n%s", label, out)
	}
}

func TestStrip_LineCommentSlash(t *testing.T) {
	mustValid(t, `{
		"a": 1, // comment
		"b": 2
	}`, "// comment")
}

func TestStrip_LineCommentHash(t *testing.T) {
	mustValid(t, `{
		"a": 1, # comment
		"b": 2
	}`, "# comment")
}

func TestStrip_BlockComment(t *testing.T) {
	mustValid(t, `{
		"a": /* inline block */ 1,
		"b": 2
	}`, "block comment")
}

func TestStrip_TrailingCommaObject(t *testing.T) {
	mustValid(t, `{"a":1,"b":2,}`, "trailing comma object")
}

func TestStrip_TrailingCommaArray(t *testing.T) {
	mustValid(t, `[1,2,3,]`, "trailing comma array")
}

func TestStrip_TrailingCommaWithWhitespace(t *testing.T) {
	mustValid(t, `{
		"a": 1,
		"b": 2,
	}`, "trailing comma + whitespace")
}

func TestStrip_CommentInsideStringIgnored(t *testing.T) {
	src := `{"url":"https://example.com/path//foo","b":1}`
	out, _ := Strip([]byte(src))
	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal: %v\noutput: %s", err, out)
	}
	if m["url"] != "https://example.com/path//foo" {
		t.Errorf("URL mangled: %v", m["url"])
	}
}

func TestStrip_HashInsideStringIgnored(t *testing.T) {
	src := `{"color":"#ff0000"}`
	out, _ := Strip([]byte(src))
	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal: %v\noutput: %s", err, out)
	}
	if m["color"] != "#ff0000" {
		t.Errorf("color mangled: %v", m["color"])
	}
}

func TestStrip_Combined(t *testing.T) {
	src := `{
		// server configuration
		"listen_port": 8388, # UDP port
		"method": "chacha20-ietf-poly1305",
		/* the password */
		"password": "secret",
	}`
	mustValid(t, src, "combined")
}
