package mcpworkspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtocolReadOnlyToolsAndTraversal(t *testing.T) {
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello"), 0600); e != nil {
		t.Fatal(e)
	}
	w, e := New(root)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	c, e := Connect(context.Background(), w)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	tools, e := c.ListTools(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if len(tools) != 2 {
		t.Fatalf("tools=%v", tools)
	}
	got, e := c.Call(context.Background(), "read_file", "note.txt")
	if e != nil || got != "hello" {
		t.Fatalf("read=%q err=%v", got, e)
	}
	got, e = c.Call(context.Background(), "list_files", ".")
	if e == nil || !strings.Contains(e.Error(), "workspace-relative") {
		t.Fatalf("dot path err=%v", e)
	}
	if _, e = c.Call(context.Background(), "write_file", "note.txt"); e == nil {
		t.Fatal("write tool unexpectedly available")
	}
	if _, e = c.Call(context.Background(), "read_file", "../outside"); e == nil {
		t.Fatal("traversal unexpectedly succeeded")
	}
}
func TestReadFileBound(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "big")
	if e := os.WriteFile(p, make([]byte, maxFileBytes+1), 0600); e != nil {
		t.Fatal(e)
	}
	w, e := New(root)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	_, _, e = w.read(context.Background(), nil, readArgs{Path: "big"})
	if e == nil {
		t.Fatal("oversized file accepted")
	}
}
