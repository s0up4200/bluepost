package storage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotRoundTrip(t *testing.T) {
	t.Parallel()

	store := testSnapshot(t, testKey(0x11))
	want := []byte(`{"schema":1,"records":["private"]}`)
	if err := store.Save(HistoryPurpose, want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(HistoryPurpose)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("plaintext %q", got)
	}
}

func TestSnapshotRejectsCiphertextMutation(t *testing.T) {
	t.Parallel()

	store := testSnapshot(t, testKey(0x22))
	if err := store.Save(HistoryPurpose, []byte("private")); err != nil {
		t.Fatal(err)
	}
	path, _ := store.path(HistoryPurpose)
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	blob[len(blob)-1] ^= 0x01
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(HistoryPurpose)
	if !errors.Is(err, ErrLocked) || got != nil {
		t.Fatalf("plaintext %q, error %v", got, err)
	}
}

func TestSnapshotRejectsPurposeSwap(t *testing.T) {
	t.Parallel()

	store := testSnapshot(t, testKey(0x33))
	if err := store.Save(HistoryPurpose, []byte("private")); err != nil {
		t.Fatal(err)
	}
	historyPath, _ := store.path(HistoryPurpose)
	contactsPath, _ := store.path(ContactsPurpose)
	blob, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(contactsPath, blob, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(ContactsPurpose); !errors.Is(err, ErrLocked) {
		t.Fatalf("error %v", err)
	}
}

func TestSnapshotRejectsWrongKey(t *testing.T) {
	t.Parallel()

	store := testSnapshot(t, testKey(0x44))
	if err := store.Save(HistoryPurpose, []byte("private")); err != nil {
		t.Fatal(err)
	}
	wrong := Snapshot{Dir: store.Dir, Key: testKey(0x45), Random: bytes.NewReader(bytes.Repeat([]byte{1}, 12))}
	if _, err := wrong.Load(HistoryPurpose); !errors.Is(err, ErrLocked) {
		t.Fatalf("error %v", err)
	}
}

func TestSnapshotKeepsOldDataWhenReplacementCannotStart(t *testing.T) {
	store := testSnapshot(t, testKey(0x55))
	if err := store.Save(HistoryPurpose, []byte("old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(store.Dir, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(HistoryPurpose, []byte("new")); err == nil {
		t.Fatal("expected replacement error")
	}
	if err := os.Chmod(store.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(HistoryPurpose)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatalf("plaintext %q", got)
	}
}

func TestSnapshotRejectsSymlinkStateDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "state")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatal(err)
	}
	store := Snapshot{Dir: linkDir, Key: testKey(0x66)}
	if err := store.Save(HistoryPurpose, []byte("private")); !errors.Is(err, ErrLocked) {
		t.Fatalf("error %v", err)
	}
}

func TestSnapshotRejectsSymlinkAndUnsafeSnapshotModes(t *testing.T) {
	t.Parallel()

	store := testSnapshot(t, testKey(0x77))
	if err := os.MkdirAll(store.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path, _ := store.path(HistoryPurpose)
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(HistoryPurpose); !errors.Is(err, ErrLocked) {
		t.Fatalf("symlink error %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(HistoryPurpose, []byte("private")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(HistoryPurpose); !errors.Is(err, ErrLocked) {
		t.Fatalf("mode error %v", err)
	}
}

func TestSnapshotSaveRejectsUnsafeExistingTarget(t *testing.T) {
	t.Parallel()

	store := testSnapshot(t, testKey(0x79))
	if err := os.MkdirAll(store.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path, _ := store.path(HistoryPurpose)
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(HistoryPurpose, []byte("private")); !errors.Is(err, ErrLocked) {
		t.Fatalf("symlink error %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "outside" {
		t.Fatalf("target content %q", got)
	}
}

func testSnapshot(t *testing.T, key [32]byte) Snapshot {
	t.Helper()
	return Snapshot{
		Dir:    filepath.Join(t.TempDir(), "state"),
		Key:    key,
		Random: bytes.NewReader(bytes.Repeat([]byte{0x88}, 12*32)),
	}
}

func testKey(value byte) [32]byte {
	var key [32]byte
	for index := range key {
		key[index] = value
	}
	return key
}
