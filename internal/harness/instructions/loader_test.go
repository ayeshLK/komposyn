package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScopedInstructions(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "sub"), 0700)
	os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("root rules"), 0600)
	os.WriteFile(filepath.Join(root, "sub", "AGENTS.md"), []byte("nested rules"), 0600)
	got, e := Load(root, filepath.Join(root, "sub", "x.txt"))
	if e != nil {
		t.Fatal(e)
	}
	if len(got.Sources) != 2 || !strings.Contains(got.Text, "root rules") || !strings.Contains(got.Text, "nested rules") {
		t.Fatalf("result=%+v", got)
	}
	if _, e = Load(root, filepath.Join(root, "..", "outside")); e == nil {
		t.Fatal("escaped target accepted")
	}
}
