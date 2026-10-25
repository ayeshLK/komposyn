package store

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.Load(); err != nil || got != nil {
		t.Fatalf("initial state=%q err=%v", got, err)
	}
	if err := s.Save([]byte(`{"version":1}`)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || string(got) != `{"version":1}` {
		t.Fatalf("state=%q err=%v", got, err)
	}
}

func TestRejectsPermissiveDataDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not authoritative on Windows")
	}
	dir := filepath.Join(t.TempDir(), "open")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(dir); err == nil {
		t.Fatal("expected permissive directory to be rejected")
	}
}
