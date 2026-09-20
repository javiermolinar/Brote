package backend

type obj = map[string]any

func asObj(v any) obj { o, _ := v.(map[string]any); return o }

func asList(v any) []any { a, _ := v.([]any); return a }

func str(v any) string { s, _ := v.(string); return s }

func num(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func truth(v any) bool { b, _ := v.(bool); return b }
