package backend

import "testing"

func TestPreRuntimeEntryHasNoSyntheticStack(t *testing.T) {
	// No DAP client is installed: requesting stackTrace for a missing goroutine
	// would panic. Valid entry inspection must return an empty stack without I/O.
	d := &Delve{state: obj{"stopReason": "entry", "currentGoroutine": obj{"id": 0}}}
	for _, id := range []int{0, -1} {
		result, err := d.Call("Stacktrace", obj{"Id": id, "Depth": 30})
		if err != nil || len(asList(result["Locations"])) != 0 {
			t.Fatalf("entry stack: %v %v", result, err)
		}
	}
}
