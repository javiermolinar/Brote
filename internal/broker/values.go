package broker

import "encoding/json"

type obj = map[string]any

func asObj(v any) obj { o, _ := v.(map[string]any); return o }

func asList(v any) []any { a, _ := v.([]any); return a }

func str(v any) string { s, _ := v.(string); return s }

func num(v any) int {
	switch n := v.(type) {
	case json.Number:
		value, _ := n.Int64()
		return int(value)
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func truth(v any) bool { b, _ := v.(bool); return b }

var loadConfig = obj{"FollowPointers": true, "MaxVariableRecurse": 1, "MaxStringLen": 256, "MaxArrayValues": 20, "MaxStructFields": 30}

func pick(v obj, keys ...string) obj {
	out := obj{}
	for _, k := range keys {
		if value, ok := v[k]; ok {
			out[k] = value
		}
	}
	return out
}

func compactVariables(input any, depth int) []any {
	return compactVariableDepth(input, depth, 2)
}

func compactVariableDepth(input any, depth, limit int) []any {
	out := []any{}
	for _, raw := range asList(input) {
		v := asObj(raw)
		item := pick(v, "name", "type", "value", "unreadable", "len", "cap", "kind", "error", "truncated")
		if depth < limit {
			if children := asList(v["children"]); len(children) > 0 {
				item["children"] = compactVariableDepth(children, depth+1, limit)
			}
		}
		out = append(out, item)
	}
	return out
}

func errorString(e error) string {
	if e != nil {
		return e.Error()
	}
	return ""
}
