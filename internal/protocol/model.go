// Package protocol holds Anthropic Messages wire helpers for the hot path:
// a byte-level scan for the top-level "model" field (REQ-PASS-03) and an
// incremental SSE scanner that reads usage without holding the stream
// (REQ-STREAM-01, §10.1).
package protocol

import (
	"encoding/json"
	"errors"
	"strings"
)

var errNotObject = errors.New("request body is not a JSON object")

// FindTopLevelString locates the string value of key in the top-level JSON
// object body, without building a tree. It returns the byte range of the
// value's contents (inside the quotes). Nested keys of the same name, such as
// a tool schema property called "model", are skipped.
func FindTopLevelString(body []byte, key string) (start, end int, found bool, err error) {
	i := skipWS(body, 0)
	if i >= len(body) || body[i] != '{' {
		return 0, 0, false, errNotObject
	}
	i++
	for {
		i = skipWS(body, i)
		if i >= len(body) {
			return 0, 0, false, errNotObject
		}
		if body[i] == '}' {
			return 0, 0, false, nil
		}
		if body[i] == ',' {
			i++
			continue
		}
		if body[i] != '"' {
			return 0, 0, false, errNotObject
		}
		ks := i + 1
		ke, ok := stringEnd(body, i)
		if !ok {
			return 0, 0, false, errNotObject
		}
		i = skipWS(body, ke+1)
		if i >= len(body) || body[i] != ':' {
			return 0, 0, false, errNotObject
		}
		i = skipWS(body, i+1)
		if i >= len(body) {
			return 0, 0, false, errNotObject
		}
		if string(body[ks:ke]) == key && body[i] == '"' {
			ve, ok := stringEnd(body, i)
			if !ok {
				return 0, 0, false, errNotObject
			}
			return i + 1, ve, true, nil
		}
		if i, ok = skipValue(body, i); !ok {
			return 0, 0, false, errNotObject
		}
	}
}

// ModelName returns the top-level model as a Go string.
func ModelName(body []byte) (name string, start, end int, err error) {
	s, e, found, err := FindTopLevelString(body, "model")
	if err != nil {
		return "", 0, 0, err
	}
	if !found {
		return "", 0, 0, errors.New(`request body has no "model" string`)
	}
	raw := body[s:e]
	if !strings.ContainsRune(string(raw), '\\') {
		return string(raw), s, e, nil
	}
	if err := json.Unmarshal(body[s-1:e+1], &name); err != nil {
		return "", 0, 0, err
	}
	return name, s, e, nil
}

// ReplaceRange returns body with [start, end) replaced by the JSON-escaped
// contents of value. Every other byte is unchanged (REQ-PASS-03).
func ReplaceRange(body []byte, start, end int, value string) []byte {
	q, _ := json.Marshal(value)
	q = q[1 : len(q)-1]
	out := make([]byte, 0, len(body)-(end-start)+len(q))
	out = append(out, body[:start]...)
	out = append(out, q...)
	return append(out, body[end:]...)
}

func skipWS(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// stringEnd returns the index of the closing quote of the string at b[i].
func stringEnd(b []byte, i int) (int, bool) {
	for j := i + 1; j < len(b); j++ {
		switch b[j] {
		case '\\':
			j++
		case '"':
			return j, true
		}
	}
	return 0, false
}

// skipValue returns the index just past the JSON value starting at b[i].
func skipValue(b []byte, i int) (int, bool) {
	switch b[i] {
	case '"':
		e, ok := stringEnd(b, i)
		return e + 1, ok
	case '{', '[':
		depth := 0
		for j := i; j < len(b); j++ {
			switch b[j] {
			case '"':
				e, ok := stringEnd(b, j)
				if !ok {
					return 0, false
				}
				j = e
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return j + 1, true
				}
			}
		}
		return 0, false
	default: // number, true, false, null
		j := i
		for j < len(b) && b[j] != ',' && b[j] != '}' && b[j] != ']' && b[j] != ' ' && b[j] != '\n' && b[j] != '\t' && b[j] != '\r' {
			j++
		}
		return j, j > i
	}
}
