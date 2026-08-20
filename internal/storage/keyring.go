package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

var (
	ErrLocked         = errors.New("storage is locked")
	ErrSecretNotFound = errors.New("storage key was not found")
)

type Runner interface {
	Run(context.Context, []byte, ...string) ([]byte, error)
}

type CommandRunner struct {
	Path string
}

func (runner CommandRunner) Run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	path := runner.Path
	if path == "" {
		path = "/usr/bin/secret-tool"
	}
	command := exec.CommandContext(ctx, path, args...)
	command.Stdin = bytes.NewReader(stdin)
	output, err := command.Output()
	if err == nil {
		if len(output) > 4096 {
			return nil, errors.New("secret-tool output exceeds the limit")
		}
		return output, nil
	}
	var exitError *exec.ExitError
	if len(args) != 0 && args[0] == "lookup" &&
		errors.As(err, &exitError) && exitError.ExitCode() == 1 {
		return nil, ErrSecretNotFound
	}
	return nil, err
}

type Keyring struct {
	Runner  Runner
	Random  io.Reader
	Timeout time.Duration
}

func (keyring Keyring) Key(ctx context.Context, stateExists bool) ([32]byte, error) {
	runner := keyring.Runner
	if runner == nil {
		runner = CommandRunner{}
	}
	timeout := keyring.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	key, err := lookupKey(ctx, runner, timeout)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, ErrSecretNotFound) || stateExists {
		return [32]byte{}, lockedError(err)
	}

	random := keyring.Random
	if random == nil {
		random = rand.Reader
	}
	var generated [32]byte
	if _, err := io.ReadFull(random, generated[:]); err != nil {
		return [32]byte{}, lockedError(fmt.Errorf("generate storage key: %w", err))
	}
	encoded := base64.StdEncoding.EncodeToString(generated[:]) + "\n"
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	_, err = runner.Run(
		commandCtx,
		[]byte(encoded),
		"store",
		"--label=Bluepost storage key",
		"application", "bluepost",
		"purpose", "storage-v1",
	)
	cancel()
	if err != nil {
		return [32]byte{}, lockedError(fmt.Errorf("store storage key: %w", err))
	}

	stored, err := lookupKey(ctx, runner, timeout)
	if err != nil {
		return [32]byte{}, lockedError(err)
	}
	if !bytes.Equal(stored[:], generated[:]) {
		return [32]byte{}, lockedError(errors.New("stored key does not match generated key"))
	}
	return stored, nil
}

func lookupKey(ctx context.Context, runner Runner, timeout time.Duration) ([32]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	output, err := runner.Run(
		commandCtx,
		nil,
		"lookup",
		"application", "bluepost",
		"purpose", "storage-v1",
	)
	cancel()
	if err != nil {
		return [32]byte{}, err
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(output)))
	if err != nil || len(decoded) != 32 {
		return [32]byte{}, errors.New("stored key is malformed")
	}
	var key [32]byte
	copy(key[:], decoded)
	return key, nil
}

func lockedError(cause error) error {
	return fmt.Errorf("%w: %v", ErrLocked, cause)
}
