package broker

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"regexp"
)

func (b *broker) captureSources() {
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

func (b *broker) sourceIdentity(file string, data []byte) obj {
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
