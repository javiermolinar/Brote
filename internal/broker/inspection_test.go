package broker

import (
	"os"
	"path/filepath"
	"testing"

	"debug-handover/internal/session"
)

func TestReadOnlyExpressions(t *testing.T) {
	for _, expression := range []string{"x", "x.Field[3]", "xs[128:256]", "len(xs)", "cap(xs)", "a+b*2", "*ptr", "len([]int{1,2})"} {
		if e := validateExpression(expression); e != nil {
			t.Fatalf("%s: %v", expression, e)
		}
	}
	for _, expression := range []string{"", "x=2", "<-ch", "mutate()", "pkg.Mutate()", "append(xs, 1)", "func() int {return 2}()", "call f()"} {
		if e := validateExpression(expression); e == nil {
			t.Fatalf("accepted %s", expression)
		}
	}
}

func TestSourceFingerprintDetectsChanges(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	binary := filepath.Join(dir, "demo")
	os.WriteFile(file, []byte("package main"), 0600)
	os.WriteFile(binary, []byte("binary"), 0700)
	p := session.CaptureFingerprint(binary, dir)
	h, _ := session.FileHash(file)
	p.Sources[file] = h
	b := &broker{s: session.Descriptor{Binary: binary, Fingerprint: p}}
	if truth(b.sourceIdentity(file, []byte("package main"))["changedSinceStart"]) {
		t.Fatal("unchanged source marked changed")
	}
	if !truth(b.sourceIdentity(file, []byte("package changed"))["changedSinceStart"]) {
		t.Fatal("source change missed")
	}
	os.WriteFile(binary, []byte("changed binary"), 0700)
	if !truth(b.sourceIdentity(file, nil)["binaryChanged"]) {
		t.Fatal("binary change missed")
	}
}
