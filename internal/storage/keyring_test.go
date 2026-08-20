package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

type runnerCall struct {
	stdin []byte
	args  []string
}

type runnerResult struct {
	output []byte
	err    error
}

type scriptedRunner struct {
	results []runnerResult
	calls   []runnerCall
}

func (runner *scriptedRunner) Run(_ context.Context, stdin []byte, args ...string) ([]byte, error) {
	runner.calls = append(runner.calls, runnerCall{
		stdin: append([]byte(nil), stdin...),
		args:  append([]string(nil), args...),
	})
	result := runner.results[0]
	runner.results = runner.results[1:]
	return result.output, result.err
}

func TestKeyringReturnsStoredKey(t *testing.T) {
	t.Parallel()

	want := bytes.Repeat([]byte{0x42}, 32)
	runner := &scriptedRunner{results: []runnerResult{{
		output: []byte(base64.StdEncoding.EncodeToString(want) + "\n"),
	}}}
	key, err := (Keyring{Runner: runner, Timeout: time.Second}).Key(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(key[:], want) {
		t.Fatalf("key %x", key)
	}
}

func TestKeyringDoesNotReplaceMissingKeyForExistingState(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{results: []runnerResult{{err: ErrSecretNotFound}}}
	_, err := (Keyring{Runner: runner, Timeout: time.Second}).Key(context.Background(), true)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("error %v", err)
	}
}

func TestKeyringCreatesAndReadsBackNewKey(t *testing.T) {
	t.Parallel()

	want := bytes.Repeat([]byte{0x24}, 32)
	encoded := base64.StdEncoding.EncodeToString(want)
	runner := &scriptedRunner{results: []runnerResult{
		{err: ErrSecretNotFound},
		{},
		{output: []byte(encoded + "\n")},
	}}
	keyring := Keyring{
		Runner:  runner,
		Random:  bytes.NewReader(want),
		Timeout: time.Second,
	}
	key, err := keyring.Key(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(key[:], want) {
		t.Fatalf("key %x", key)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("command count %d", len(runner.calls))
	}
	if string(runner.calls[1].stdin) != encoded+"\n" {
		t.Fatalf("stored input %q", runner.calls[1].stdin)
	}
	for _, argument := range runner.calls[1].args {
		if strings.Contains(argument, encoded) {
			t.Fatalf("secret appeared in argument %q", argument)
		}
	}
}

func TestKeyringRejectsMalformedStoredKey(t *testing.T) {
	t.Parallel()

	runner := &scriptedRunner{results: []runnerResult{{output: []byte("not-base64\n")}}}
	_, err := (Keyring{Runner: runner, Timeout: time.Second}).Key(context.Background(), false)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("error %v", err)
	}
}

type lateRunner struct{}

func (lateRunner) Run(ctx context.Context, _ []byte, _ ...string) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestKeyringLocksAfterDeadline(t *testing.T) {
	t.Parallel()

	_, err := (Keyring{Runner: lateRunner{}, Timeout: time.Millisecond}).Key(context.Background(), true)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("error %v", err)
	}
}
