package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/godbus/dbus/v5"
	"github.com/spf13/cobra"

	"github.com/s0up4200/bluepost/internal/bus"
	"github.com/s0up4200/bluepost/internal/cli"
	"github.com/s0up4200/bluepost/internal/textsafe"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, func() (*dbus.Conn, error) {
		return dbus.ConnectSessionBus()
	}, newDaemonCommand(os.Stderr, os.Getenv, runDaemon)))
}

func run(
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	connect func() (*dbus.Conn, error),
	daemon *cobra.Command,
) int {
	client := bus.NewClient(nil)
	var connection *dbus.Conn
	command := cli.New(client, stdout, stderr)
	command.AddCommand(daemon)
	command.SetArgs(args)
	command.PersistentPreRunE = func(selected *cobra.Command, _ []string) error {
		if selected == daemon {
			return nil
		}
		var err error
		connection, err = connect()
		if err != nil {
			return errors.New("Could not connect to the user D-Bus")
		}
		client.SetConnection(connection)
		return nil
	}
	if err := command.Execute(); err != nil {
		fmt.Fprintln(stderr, textsafe.OneLine(err.Error()))
		if connection != nil {
			connection.Close()
		}
		return 1
	}
	if connection != nil {
		connection.Close()
	}
	return 0
}
