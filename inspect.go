package main

import (
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"debug/macho"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

type Fingerprint struct {
	BinarySHA       string            `json:"binarySHA256"`
	BinarySize      int64             `json:"binarySize"`
	BinaryMtime     int64             `json:"binaryMtime"`
	BuildRevision   string            `json:"buildRevision,omitempty"`
	ProjectRevision string            `json:"projectRevision,omitempty"`
	Match           string            `json:"match"`
	Sources         map[string]string `json:"sources"`
}

func fileHash(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func fingerprint(binary, project string) *Fingerprint {
	p := &Fingerprint{Match: "unverified", Sources: map[string]string{}}
	p.BinarySHA, _ = fileHash(binary)
	if st, e := os.Stat(binary); e == nil {
		p.BinarySize = st.Size()
		p.BinaryMtime = st.ModTime().UnixNano()
	}
	modified := false
	if info, e := buildinfo.ReadFile(binary); e == nil {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				p.BuildRevision = s.Value
			}
			if s.Key == "vcs.modified" && s.Value == "true" {
				modified = true
			}
		}
	}
	cmd := exec.Command("git", "-C", project, "rev-parse", "HEAD")
	if out, e := cmd.Output(); e == nil {
		p.ProjectRevision = strings.TrimSpace(string(out))
	}
	if p.BuildRevision != "" && p.ProjectRevision != "" {
		p.Match = "mismatch"
		if p.BuildRevision == p.ProjectRevision && !modified && exec.Command("git", "-C", project, "diff", "--quiet", "HEAD", "--").Run() == nil {
			p.Match = "matching-vcs"
		}
	}
	return p
}
func (b *Broker) captureSources() {
	p := b.s.Fingerprint
	if p == nil {
		return
	}
	sources, e := b.rpc("ListSources", obj{"Filter": "^" + regexp.QuoteMeta(b.s.Project+string(os.PathSeparator))})
	if e != nil {
		return
	}
	for i, v := range asList(sources["Sources"]) {
		if i >= 10000 {
			break
		}
		path := str(v)
		if data, e := readSource(path); e == nil {
			h := sha256.Sum256(data)
			p.Sources[path] = hex.EncodeToString(h[:])
		}
	}
}
func (b *Broker) sourceIdentity(file string, data []byte) obj {
	p := b.s.Fingerprint
	if p == nil {
		return obj{"match": "unverified"}
	}
	v := obj{"match": p.Match, "buildRevision": p.BuildRevision, "projectRevision": p.ProjectRevision}
	if st, e := os.Stat(b.s.Binary); e != nil || st.Size() != p.BinarySize || st.ModTime().UnixNano() != p.BinaryMtime {
		v["binaryChanged"] = true
	}
	if expected := p.Sources[file]; expected != "" {
		h := sha256.Sum256(data)
		v["changedSinceStart"] = hex.EncodeToString(h[:]) != expected
	}
	return v
}

// Delve's Eval only inspects values. Reject function calls and channel receives
// here too, keeping this endpoint separate from debugger calls and assignment.
func validateExpression(expression string) error {
	if len(expression) == 0 || len(expression) > 4096 {
		return fail("expression must contain 1–4096 bytes")
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
				bad = fail("only read-only expressions and len/cap/real/imag/complex are supported")
				return false
			}
		case *ast.UnaryExpr:
			if n.Op == token.ARROW {
				bad = fail("channel receives are not supported")
				return false
			}
		case *ast.FuncLit:
			bad = fail("function literals are not supported")
			return false
		}
		return bad == nil
	})
	return bad
}
func (b *Broker) evaluate(expression string, gid, frame, depth, count int, s obj) (obj, error) {
	if e := validateExpression(expression); e != nil {
		return nil, e
	}
	if depth < 0 || depth > 6 || count < 1 || count > 128 || frame < 0 {
		return nil, fail("depth must be 0–6, count 1–128, and frame nonnegative")
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
func hasDebugInfo(path string) bool {
	if f, e := elf.Open(path); e == nil {
		defer f.Close()
		_, e = f.DWARF()
		return e == nil
	}
	if f, e := macho.Open(path); e == nil {
		defer f.Close()
		_, e = f.DWARF()
		return e == nil
	}
	return false
}
