package session

import (
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"debug/macho"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
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

func FileHash(path string) (string, error) {
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

func CaptureFingerprint(binary, project string) *Fingerprint {
	p := &Fingerprint{Match: "unverified", Sources: map[string]string{}}
	p.BinarySHA, _ = FileHash(binary)
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

func HasDebugInfo(path string) bool {
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
