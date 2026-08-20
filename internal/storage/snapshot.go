package storage

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/s0up4200/bluepost/internal/protocol"
)

const (
	HistoryPurpose  = "history-v1"
	ContactsPurpose = "contacts-v1"
	formatVersion   = byte(1)
)

var magic = [8]byte{'B', 'L', 'U', 'E', 'P', 'O', 'S', 'T'}

type Snapshot struct {
	Dir    string
	Key    [32]byte
	Random io.Reader
}

func StateExists(dir string) (bool, error) {
	if err := ensurePrivateDir(dir); err != nil {
		return false, err
	}
	for _, name := range []string{"history.enc", "contacts.enc"} {
		_, err := os.Lstat(filepath.Join(dir, name))
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, os.ErrNotExist):
			continue
		default:
			return false, err
		}
	}
	return false, nil
}

func (snapshot Snapshot) Load(purpose string) ([]byte, error) {
	path, err := snapshot.path(purpose)
	if err != nil {
		return nil, err
	}
	if err := ensurePrivateDir(snapshot.Dir); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, lockedError(fmt.Errorf("inspect encrypted snapshot: %w", err))
	}
	if err := validatePrivateFile(info); err != nil {
		return nil, err
	}
	maximum, _ := purposeMaximum(purpose)
	if info.Size() > int64(len(magic)+1+12+aes.BlockSize)+maximum {
		return nil, lockedError(errors.New("encrypted snapshot exceeds the byte limit"))
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, lockedError(fmt.Errorf("read encrypted snapshot: %w", err))
	}
	return snapshot.open(purpose, blob)
}

func (snapshot Snapshot) Save(purpose string, plaintext []byte) error {
	path, err := snapshot.path(purpose)
	if err != nil {
		return err
	}
	maximum, _ := purposeMaximum(purpose)
	if int64(len(plaintext)) > maximum {
		return errors.New("snapshot plaintext exceeds the byte limit")
	}
	if err := ensurePrivateDir(snapshot.Dir); err != nil {
		return err
	}
	if err := validateExistingTarget(path); err != nil {
		return err
	}
	blob, err := snapshot.seal(purpose, plaintext)
	if err != nil {
		return err
	}

	temporary, err := os.CreateTemp(snapshot.Dir, ".bluepost-*.tmp")
	if err != nil {
		return lockedError(fmt.Errorf("create encrypted snapshot: %w", err))
	}
	temporaryPath := temporary.Name()
	keepTemporary := true
	defer func() {
		_ = temporary.Close()
		if keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return lockedError(fmt.Errorf("protect encrypted snapshot: %w", err))
	}
	if _, err := temporary.Write(blob); err != nil {
		return lockedError(fmt.Errorf("write encrypted snapshot: %w", err))
	}
	if err := temporary.Sync(); err != nil {
		return lockedError(fmt.Errorf("synchronize encrypted snapshot: %w", err))
	}
	if err := temporary.Close(); err != nil {
		return lockedError(fmt.Errorf("close encrypted snapshot: %w", err))
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return lockedError(fmt.Errorf("replace encrypted snapshot: %w", err))
	}
	keepTemporary = false
	directory, err := os.Open(snapshot.Dir)
	if err != nil {
		return lockedError(fmt.Errorf("open state directory: %w", err))
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return lockedError(fmt.Errorf("synchronize state directory: %w", err))
	}
	return nil
}

func validateExistingTarget(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return lockedError(fmt.Errorf("inspect encrypted snapshot: %w", err))
	}
	return validatePrivateFile(info)
}

func (snapshot Snapshot) path(purpose string) (string, error) {
	name := ""
	switch purpose {
	case HistoryPurpose:
		name = "history.enc"
	case ContactsPurpose:
		name = "contacts.enc"
	default:
		return "", errors.New("unknown snapshot purpose")
	}
	if snapshot.Dir == "" || !filepath.IsAbs(snapshot.Dir) {
		return "", errors.New("state directory must be absolute")
	}
	return filepath.Join(snapshot.Dir, name), nil
}

func purposeMaximum(purpose string) (int64, error) {
	switch purpose {
	case HistoryPurpose, ContactsPurpose:
		return protocol.MaxHistoryBytes, nil
	default:
		return 0, errors.New("unknown snapshot purpose")
	}
}

func (snapshot Snapshot) seal(purpose string, plaintext []byte) ([]byte, error) {
	aead, err := newGCM(snapshot.Key)
	if err != nil {
		return nil, err
	}
	random := snapshot.Random
	if random == nil {
		random = rand.Reader
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(random, nonce); err != nil {
		return nil, lockedError(fmt.Errorf("generate snapshot nonce: %w", err))
	}
	header := append(append([]byte(nil), magic[:]...), formatVersion)
	header = append(header, nonce...)
	ciphertext := aead.Seal(nil, nonce, plaintext, additionalData(purpose))
	return append(header, ciphertext...), nil
}

func (snapshot Snapshot) open(purpose string, blob []byte) ([]byte, error) {
	aead, err := newGCM(snapshot.Key)
	if err != nil {
		return nil, err
	}
	headerSize := len(magic) + 1 + aead.NonceSize()
	if len(blob) < headerSize+aead.Overhead() ||
		!bytes.Equal(blob[:len(magic)], magic[:]) || blob[len(magic)] != formatVersion {
		return nil, lockedError(errors.New("encrypted snapshot header is invalid"))
	}
	nonce := blob[len(magic)+1 : headerSize]
	plaintext, err := aead.Open(nil, nonce, blob[headerSize:], additionalData(purpose))
	if err != nil {
		return nil, lockedError(errors.New("encrypted snapshot authentication failed"))
	}
	return plaintext, nil
}

func newGCM(key [32]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, lockedError(fmt.Errorf("create storage cipher: %w", err))
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, lockedError(fmt.Errorf("create storage authentication: %w", err))
	}
	return aead, nil
}

func additionalData(purpose string) []byte {
	return []byte("bluepost:" + purpose + ":v1")
}

func ensurePrivateDir(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
		return lockedError(errors.New("state directory is unsafe"))
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return lockedError(fmt.Errorf("create state directory: %w", err))
		}
		if err := os.Chmod(path, 0o700); err != nil {
			return lockedError(fmt.Errorf("protect state directory: %w", err))
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return lockedError(fmt.Errorf("inspect state directory: %w", err))
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return lockedError(errors.New("state directory is not a private directory"))
	}
	if info.Mode().Perm() != 0o700 || !ownedByCurrentUser(info) {
		return lockedError(errors.New("state directory has unsafe ownership or permissions"))
	}
	return nil
}

func validatePrivateFile(info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return lockedError(errors.New("encrypted snapshot is not a regular file"))
	}
	if info.Mode().Perm() != 0o600 || !ownedByCurrentUser(info) {
		return lockedError(errors.New("encrypted snapshot has unsafe ownership or permissions"))
	}
	return nil
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}
