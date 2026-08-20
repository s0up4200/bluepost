package obex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareRuntimeDirRequiresOwnerOnlyDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	private := filepath.Join(root, "private")
	if err := PrepareRuntimeDir(private); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(private)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("runtime mode %o", info.Mode().Perm())
	}

	unsafe := filepath.Join(root, "unsafe")
	if err := os.Mkdir(unsafe, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := PrepareRuntimeDir(unsafe); err == nil {
		t.Fatal("expected unsafe runtime directory error")
	}
}
