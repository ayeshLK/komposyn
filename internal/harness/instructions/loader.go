// Package instructions reads bounded AGENTS.md files inside one selected root.
package instructions

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxFile = 64 << 10
const maxTotal = 256 << 10

type Source struct {
	Path     string    `json:"path"`
	Scope    string    `json:"scope"`
	SHA256   string    `json:"sha256"`
	Modified time.Time `json:"modified"`
	Bytes    int       `json:"bytes"`
}
type Result struct {
	Text    string   `json:"text"`
	Sources []Source `json:"sources"`
}

// Load returns root instructions followed by relevant nested instructions for a target path.
// It never scans parents outside root and treats instructions as context only, not permission.
func Load(root, target string) (Result, error) {
	abs, e := filepath.Abs(root)
	if e != nil {
		return Result{}, e
	}
	// Resolve both paths against the same lexical workspace root. Resolving
	// only the root through symlinks makes an otherwise valid target appear
	// outside the workspace on platforms that preserve the original path.
	if !filepath.IsAbs(target) {
		target = filepath.Join(abs, target)
	}
	target, e = filepath.Abs(target)
	if e != nil {
		return Result{}, e
	}
	rel, e := filepath.Rel(abs, target)
	if e != nil {
		return Result{}, e
	}
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Result{}, errors.New("instruction target escapes workspace")
	}
	rootFS, e := os.OpenRoot(abs)
	if e != nil {
		return Result{}, e
	}
	defer rootFS.Close()
	dirs := []string{"."}
	parent := filepath.Dir(rel)
	if parent != "." {
		cur := ""
		for _, part := range strings.Split(parent, string(filepath.Separator)) {
			cur = filepath.Join(cur, part)
			dirs = append(dirs, cur)
		}
	}
	out := Result{}
	total := 0
	for _, d := range dirs {
		p := "AGENTS.md"
		if d != "." {
			p = filepath.Join(d, p)
		}
		f, e := rootFS.Open(p)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return Result{}, fmt.Errorf("read scoped instructions: %w", e)
		}
		info, e := f.Stat()
		if e != nil {
			f.Close()
			return Result{}, e
		}
		data, e := io.ReadAll(io.LimitReader(f, maxFile+1))
		f.Close()
		if e != nil {
			return Result{}, e
		}
		if len(data) > maxFile {
			return Result{}, fmt.Errorf("%s exceeds 64 KiB", p)
		}
		total += len(data)
		if total > maxTotal {
			return Result{}, errors.New("combined AGENTS.md instructions exceed 256 KiB")
		}
		sum := sha256.Sum256(data)
		out.Sources = append(out.Sources, Source{Path: p, Scope: d, SHA256: hex.EncodeToString(sum[:]), Modified: info.ModTime().UTC(), Bytes: len(data)})
		if out.Text != "" {
			out.Text += "\n\n"
		}
		out.Text += string(data)
	}
	return out, nil
}
