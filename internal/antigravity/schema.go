package antigravity

import (
	"strconv"
	"strings"
)

// unsupported are JSON Schema keywords Gemini's schema proto has no field
// for; one occurrence anywhere rejects the whole request with "Unknown name
// …: Cannot find field" (9router's UNSUPPORTED_SCHEMA_CONSTRAINTS).
var unsupported = func() map[string]bool {
	m := map[string]bool{}
	for _, k := range []string{
		"minLength", "maxLength", "exclusiveMinimum", "exclusiveMaximum", "minItems", "maxItems",
		"format", "multipleOf", "uniqueItems", "contains", "unevaluatedProperties", "unevaluatedItems",
		"contentSchema", "prefixItems", "additionalItems", "default", "examples",
		"$schema", "$defs", "definitions", "const", "$ref", "$comment", "$id",
		"deprecated", "readOnly", "writeOnly",
		"additionalProperties", "propertyNames", "patternProperties", "enumDescriptions",
		"anyOf", "oneOf", "allOf", "not", "dependencies", "dependentSchemas", "dependentRequired",
		"title", "optional", "if", "then", "else", "contentMediaType", "contentEncoding",
		"errorMessage", "errorMessages", "markdownDescription", "doNotSuggest", "suggestSortText",
		"minProperties", "maxProperties",
	} {
		m[k] = true
	}
	return m
}()

// placeholder stands in for an object schema with no properties, which
// Antigravity rejects.
func placeholder(s map[string]any) {
	s["type"] = "object"
	s["properties"] = map[string]any{"reason": map[string]any{
		"type": "string", "description": "Brief explanation of why you are calling this tool",
	}}
	s["required"] = []any{"reason"}
}

// cleanSchema rewrites a tool's input_schema into the subset Gemini accepts
// (9router's cleanJSONSchemaForAntigravity): const and enums as strings,
// allOf merged, anyOf/oneOf and type lists narrowed to one choice, missing
// types inferred, every unsupported keyword dropped.
func cleanSchema(v any) map[string]any {
	s, _ := v.(map[string]any)
	if s == nil {
		s = map[string]any{}
	}
	cleanNode(s)
	if len(s) == 0 || s["type"] == "object" && len(asMap(s["properties"])) == 0 {
		placeholder(s)
	}
	return s
}

func cleanNode(s map[string]any) {
	if c, ok := s["const"]; ok {
		if _, has := s["enum"]; !has {
			s["enum"] = []any{c}
		}
	}
	mergeAllOf(s)
	if pi, ok := s["prefixItems"].([]any); ok && s["items"] == nil {
		if alts := nonNull(pi); len(alts) == 1 {
			s["items"] = alts[0]
		} else if len(alts) > 1 {
			s["items"] = alts[best(alts)]
		}
	}
	// A chosen alternative may itself be an anyOf: narrow until none is left.
	for range 8 {
		changed := false
		for _, k := range []string{"anyOf", "oneOf"} {
			alts := nonNull(asSlice(s[k]))
			delete(s, k)
			if len(alts) == 0 {
				continue
			}
			for key, val := range asMap(alts[best(alts)]) {
				s[key] = val
			}
			changed = true
		}
		if !changed {
			break
		}
	}
	if ts, ok := s["type"].([]any); ok {
		s["type"] = "string"
		for _, t := range ts {
			if t != "null" {
				s["type"] = t
				break
			}
		}
	}
	if e, ok := s["enum"].([]any); ok {
		out := make([]any, 0, len(e))
		for _, x := range e {
			out = append(out, enumString(x))
		}
		s["enum"] = out
		if s["type"] == nil {
			s["type"] = "string"
		}
	}
	if s["properties"] != nil && s["type"] == nil {
		s["type"] = "object"
	}
	if s["type"] == "array" && s["items"] == nil {
		s["items"] = map[string]any{"type": "string"}
	}
	for k := range s {
		if unsupported[k] || strings.HasPrefix(k, "x-") {
			delete(s, k)
		}
	}

	// properties maps names to schemas: names are never keywords.
	props := asMap(s["properties"])
	for name, p := range props {
		ps, ok := p.(map[string]any)
		if !ok {
			ps = map[string]any{}
			props[name] = ps
		}
		cleanNode(ps)
		if len(ps) == 0 {
			ps["type"] = "string" // "any value": the nearest Gemini can say
		} else if ps["type"] == "object" && len(asMap(ps["properties"])) == 0 {
			placeholder(ps)
		}
	}
	switch it := s["items"].(type) {
	case map[string]any:
		cleanNode(it)
		if len(it) == 0 {
			it["type"] = "string"
		} else if it["type"] == "object" && len(asMap(it["properties"])) == 0 {
			placeholder(it)
		}
	case []any:
		if alts := nonNull(it); len(alts) > 0 {
			m := asMap(alts[0])
			cleanNode(m)
			s["items"] = m
		} else {
			s["items"] = map[string]any{"type": "string"}
		}
	}
	if req := asSlice(s["required"]); req != nil {
		var keep []any
		for _, r := range req {
			if name, ok := r.(string); ok && props[name] != nil {
				keep = append(keep, name)
			}
		}
		if len(keep) == 0 {
			delete(s, "required")
		} else {
			s["required"] = keep
		}
	}
}

// mergeAllOf folds allOf members' properties and required into s.
func mergeAllOf(s map[string]any) {
	all := asSlice(s["allOf"])
	if all == nil {
		return
	}
	delete(s, "allOf")
	for _, a := range all {
		m := asMap(a)
		if p := asMap(m["properties"]); p != nil {
			props := asMap(s["properties"])
			if props == nil {
				props = map[string]any{}
				s["properties"] = props
			}
			for k, v := range p {
				props[k] = v
			}
		}
		if r := asSlice(m["required"]); r != nil {
			s["required"] = append(asSlice(s["required"]), r...)
		}
		if s["type"] == nil && m["type"] != nil {
			s["type"] = m["type"]
		}
	}
}

// best picks the most structured alternative: object, then array, then any
// typed schema (9router's selectBest).
func best(alts []any) int {
	bi, bs := 0, -1
	for i, a := range alts {
		m := asMap(a)
		score := 0
		switch {
		case m["type"] == "object" || m["properties"] != nil:
			score = 3
		case m["type"] == "array" || m["items"] != nil:
			score = 2
		case m["type"] != nil:
			score = 1
		}
		if score > bs {
			bi, bs = i, score
		}
	}
	return bi
}

func nonNull(xs []any) []any {
	var out []any
	for _, x := range xs {
		if m, ok := x.(map[string]any); ok && m["type"] != "null" {
			out = append(out, m)
		}
	}
	return out
}

func enumString(x any) string {
	switch v := x.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case nil:
		return "null"
	}
	return ""
}

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func asSlice(v any) []any        { s, _ := v.([]any); return s }
