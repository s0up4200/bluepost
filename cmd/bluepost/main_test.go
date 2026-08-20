package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestHelpDoesNotConnectToDBus(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	connectCalls := 0
	exitCode := run(
		[]string{"--help"},
		&output,
		&output,
		func() (*dbus.Conn, error) {
			connectCalls++
			return nil, errors.New("D-Bus is unavailable")
		},
	)
	if exitCode != 0 {
		t.Fatalf("exit code %d, output %q", exitCode, output.String())
	}
	if connectCalls != 0 {
		t.Fatalf("D-Bus connection attempts %d", connectCalls)
	}
}
