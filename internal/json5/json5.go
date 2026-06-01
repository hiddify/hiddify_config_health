// Package json5 strips JSON5 extensions to produce standard JSON.
//
// Supported extensions:
//   - // single-line comments
//   - # single-line comments (Python/shell style)
//   - /* … */ block comments
//   - Trailing commas before } or ]
//
// String contents are never touched — comments inside string literals are kept.
package json5

import (
	"bytes"
	"fmt"
)

// Strip removes JSON5 extensions and returns valid JSON bytes.
func Strip(src []byte) ([]byte, error) {
	out := make([]byte, 0, len(src))
	i := 0
	n := len(src)

	for i < n {
		c := src[i]

		// --- string literal ---
		if c == '"' || c == '\'' {
			quote := c
			out = append(out, '"') // normalise single-quote to double-quote
			i++
			for i < n {
				ch := src[i]
				if ch == '\\' {
					out = append(out, ch)
					i++
					if i < n {
						out = append(out, src[i])
						i++
					}
					continue
				}
				if ch == quote {
					out = append(out, '"')
					i++
					break
				}
				out = append(out, ch)
				i++
			}
			continue
		}

		// --- // line comment ---
		if c == '/' && i+1 < n && src[i+1] == '/' {
			i += 2
			for i < n && src[i] != '\n' {
				i++
			}
			continue
		}

		// --- /* block comment ---
		if c == '/' && i+1 < n && src[i+1] == '*' {
			i += 2
			for i < n {
				if src[i] == '*' && i+1 < n && src[i+1] == '/' {
					i += 2
					break
				}
				i++
			}
			continue
		}

		// --- # line comment ---
		if c == '#' {
			i++
			for i < n && src[i] != '\n' {
				i++
			}
			continue
		}

		out = append(out, c)
		i++
	}

	// Remove trailing commas before } or ]
	out = removeTrailingCommas(out)
	return out, nil
}

// removeTrailingCommas removes commas that appear immediately before } or ],
// ignoring whitespace between them.
func removeTrailingCommas(src []byte) []byte {
	// Walk backwards from every } or ] and remove the last comma + whitespace.
	out := bytes.NewBuffer(make([]byte, 0, len(src)))
	i := 0
	for i < len(src) {
		c := src[i]
		if c == '}' || c == ']' {
			// Scan back in out's buffer and remove trailing comma+whitespace.
			b := out.Bytes()
			j := len(b) - 1
			for j >= 0 && isSpace(b[j]) {
				j--
			}
			if j >= 0 && b[j] == ',' {
				// Trim the buffer back to j (removes comma + trailing spaces).
				truncated := make([]byte, j)
				copy(truncated, b[:j])
				// Preserve trailing whitespace after removal so formatting is intact.
				ws := b[j+1:]
				out.Reset()
				out.Write(truncated)
				out.Write(ws)
			}
		}
		out.WriteByte(c)
		i++
	}
	return out.Bytes()
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

// MustStrip panics on error — useful in tests.
func MustStrip(src []byte) []byte {
	out, err := Strip(src)
	if err != nil {
		panic(fmt.Sprintf("json5.Strip: %v", err))
	}
	return out
}
