package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/spf13/cobra"

	appconfig "github.com/s0up4200/bluepost/internal/config"
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
		&cobra.Command{Use: "daemon"},
	)
	if exitCode != 0 {
		t.Fatalf("exit code %d, output %q", exitCode, output.String())
	}
	if connectCalls != 0 {
		t.Fatalf("D-Bus connection attempts %d", connectCalls)
	}
}

func TestDaemonDoesNotConnectToClientDBus(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	connectCalls := 0
	daemonRuns := 0
	exitCode := run(
		[]string{"daemon"},
		&output,
		&output,
		func() (*dbus.Conn, error) {
			connectCalls++
			return nil, errors.New("D-Bus is unavailable")
		},
		&cobra.Command{
			Use:  "daemon",
			Args: cobra.NoArgs,
			Run:  func(*cobra.Command, []string) { daemonRuns++ },
		},
	)
	if exitCode != 0 || daemonRuns != 1 {
		t.Fatalf("exit code %d, daemon runs %d, output %q", exitCode, daemonRuns, output.String())
	}
	if connectCalls != 0 {
		t.Fatalf("D-Bus client connection attempts %d", connectCalls)
	}
}

func TestDaemonCommandUsesCobraPhoneFlag(t *testing.T) {
	t.Parallel()

	stateRoot := t.TempDir()
	runtimeRoot := t.TempDir()
	values := map[string]string{
		"BLUEPOST_PHONE":  "11:22:33:44:55:66",
		"XDG_STATE_HOME":  stateRoot,
		"XDG_RUNTIME_DIR": runtimeRoot,
	}
	var got appconfig.Config
	command := newDaemonCommand(
		io.Discard,
		func(key string) string { return values[key] },
		func(_ context.Context, configuration appconfig.Config, _ io.Writer) error {
			got = configuration
			return nil
		},
	)
	command.SetArgs([]string{"--phone", "aa:bb:cc:dd:ee:ff"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got.Phone != "AA:BB:CC:DD:EE:FF" ||
		got.StateDir != filepath.Join(stateRoot, "bluepost") ||
		got.RuntimeDir != filepath.Join(runtimeRoot, "bluepost") {
		t.Fatalf("configuration %#v", got)
	}
}
