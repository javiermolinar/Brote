package broker

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
)

// Delve's Eval only inspects values. Reject function calls and channel receives
// here too, keeping this endpoint separate from debugger calls and assignment.
func validateExpression(expression string) error {
	if len(expression) == 0 || len(expression) > 4096 {
		return fmt.Errorf("expression must contain 1–4096 bytes")
	}
	parsed, e := parser.ParseExpr(expression)
	if e != nil {
		return e
	}
	var bad error
	ast.Inspect(parsed, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			id, ok := n.Fun.(*ast.Ident)
			if !ok || (id.Name != "len" && id.Name != "cap" && id.Name != "real" && id.Name != "imag" && id.Name != "complex") {
				bad = fmt.Errorf("only read-only expressions and len/cap/real/imag/complex are supported")
				return false
			}
		case *ast.UnaryExpr:
			if n.Op == token.ARROW {
				bad = fmt.Errorf("channel receives are not supported")
				return false
			}
		case *ast.FuncLit:
			bad = fmt.Errorf("function literals are not supported")
			return false
		}
		return bad == nil
	})
	return bad
}

func (b *broker) evaluate(expression string, gid, frame, depth, count int, s obj) (obj, error) {
	if e := validateExpression(expression); e != nil {
		return nil, e
	}
	if depth < 0 || depth > 6 || count < 1 || count > 128 || frame < 0 {
		return nil, fmt.Errorf("depth must be 0–6, count 1–128, and frame nonnegative")
	}
	if gid == 0 {
		gid = num(asObj(s["currentGoroutine"])["id"])
	}
	if gid == 0 {
		gid = -1
	}
	cfg := obj{"FollowPointers": true, "MaxVariableRecurse": depth, "MaxStringLen": 4096, "MaxArrayValues": count, "MaxStructFields": count}
	v, e := b.rpc("Eval", obj{"Scope": obj{"GoroutineID": gid, "Frame": frame}, "Expr": expression, "Cfg": cfg})
	if e != nil {
		return nil, e
	}
	values := compactVariableDepth([]any{v["Variable"]}, 0, depth+1)
	return obj{"expression": expression, "value": values[0], "goroutine": gid, "frame": frame, "generation": b.generation}, nil
}
