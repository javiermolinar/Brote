package backend

import (
	"context"
	"strings"
	"testing"
)

func TestValueBudgetBoundsWideValues(t *testing.T) {
	d := &Delve{}
	budget := newValueBudget()
	for i := 0; i < 1000; i++ {
		value, err := d.variable(context.Background(), obj{"name": "wide", "value": strings.Repeat("x", 9000)}, 0, 128, budget)
		if err != nil {
			t.Fatal(err)
		}
		if len(str(value["value"])) > 4096 {
			t.Fatal("string unbounded")
		}
	}
	if budget.bytes < 0 || budget.nodes < 0 || !budget.truncated {
		t.Fatalf("budget %#v", budget)
	}
}
func TestValueDepthZeroMarksUnexpanded(t *testing.T) {
	value, err := (&Delve{}).variable(context.Background(), obj{"variablesReference": 1, "namedVariables": 3}, 0, 128, newValueBudget())
	if err != nil || !truth(value["truncated"]) {
		t.Fatalf("value: %v %v", value, err)
	}
}
func TestInspectionDeadlineStopsExpansion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&Delve{}).variable(ctx, obj{"variablesReference": 1}, 4, 128, newValueBudget()); err == nil {
		t.Fatal("cancelled read proceeded")
	}
}
