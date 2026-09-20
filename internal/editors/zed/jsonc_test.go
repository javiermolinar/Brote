package zed

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestAppendZedPreservesJSONC(t *testing.T) {
	cases := []string{
		"[\n // existing task\n {\"label\":\"mine\",\"url\":\"http://localhost\"}, // keep me\n]\n",
		"[ {\"label\":\"mine\",\"nested\": {\"a\":1,},} /* keep */ ]",
		"[]",
		"[ // empty\n]",
	}
	for _, original := range cases {
		t.Run(original, func(t *testing.T) {
			p := obj{"label": "test", "adapter": "Delve"}
			out, e := appendZedProfile([]byte(original), p)
			if e != nil {
				t.Fatal(e)
			}
			end := strings.LastIndex(original, "]")
			if !bytes.HasPrefix(out, []byte(original[:end])) {
				t.Fatalf("original content changed: %s", out)
			}
			// Re-appending validates the complete JSONC document and is idempotent.
			again, e := appendZedProfile(out, p)
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(out, again) {
				t.Fatal("duplicate profile")
			}
		})
	}
}

func TestInvalidZedConfigRejected(t *testing.T) {
	for _, s := range []string{"{}", "[broken]", "[/*unclosed]"} {
		if _, e := appendZedProfile([]byte(s), obj{}); e == nil {
			t.Fatalf("accepted %s", s)
		}
	}
}

func TestJSONSourceStringsRemainIntact(t *testing.T) {
	data := []byte(`[{"label":"x,]/*\\\"//","a":[1,2,],}]`)
	out, e := appendZedProfile(data, obj{"label": "another"})
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(out, []byte(`x,]/*\\\"//`)) {
		t.Fatal("string modified")
	}
	_ = json.Valid(out)
}

func TestProfileUpdateAndCleanupPreserveOtherEntries(t *testing.T) {
	for _, data := range []string{
		`[/* keep */ {"label":"mine","nested":{"x":1,},}, {"label":"handover","adapter":"Delve","tcp_connection":{"port":1}}, // keep too
]`,
		`[{"label":"handover"}, /* between */ {"label":"mine"}]`,
		`[{"label":"handover"}]`,
		`[{"label":"mine"}, {"label":"handover"},]`,
	} {
		updated, e := appendZedProfile([]byte(data), obj{"label": "handover", "adapter": "Delve", "tcp_connection": obj{"port": 2}})
		if e != nil {
			t.Fatal(e)
		}
		cleaned, e := removeZedProfile(updated, "handover")
		if e != nil {
			t.Fatal(e)
		}
		clean, _ := stripComments(cleaned)
		var entries []obj
		if e = json.Unmarshal(stripTrailingCommas(clean), &entries); e != nil {
			t.Fatalf("invalid cleanup %s: %v", cleaned, e)
		}
		for _, entry := range entries {
			if str(entry["label"]) != "mine" {
				t.Fatalf("wrong entry: %v", entry)
			}
		}
		if strings.Contains(data, "/* keep */") && !strings.Contains(string(cleaned), "/* keep */") {
			t.Fatal("lost unrelated comment")
		}
		again, e := removeZedProfile(cleaned, "handover")
		if e != nil || string(again) != string(cleaned) {
			t.Fatal("cleanup not idempotent")
		}
	}
}
