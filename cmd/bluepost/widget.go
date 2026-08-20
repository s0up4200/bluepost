package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/spf13/cobra"

	"github.com/s0up4200/bluepost/internal/protocol"
	"github.com/s0up4200/bluepost/internal/widget"
)

type subscribeFunc func(context.Context) (<-chan *dbus.Signal, func(), error)

func newWidgetCommand(source widget.Source, subscribe subscribeFunc) *cobra.Command {
	return &cobra.Command{
		Use:   "widget",
		Short: "Stream status and recent messages for desktop widgets",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			command.Root().SilenceUsage = true
			signals, cancel, err := subscribe(command.Context())
			if err != nil {
				return errors.New("Could not subscribe to Bluepost D-Bus events")
			}
			defer cancel()
			return widget.Run(command.Context(), source, signals, command.OutOrStdout())
		},
	}
}

func subscribeWidgetSignals(
	ctx context.Context,
	connection *dbus.Conn,
) (<-chan *dbus.Signal, func(), error) {
	if connection == nil {
		return nil, nil, errors.New("D-Bus connection is not available")
	}
	events := []dbus.MatchOption{
		dbus.WithMatchObjectPath(dbus.ObjectPath(protocol.ObjectPath)),
		dbus.WithMatchInterface(protocol.EventsIface),
	}
	owner := []dbus.MatchOption{
		dbus.WithMatchObjectPath(dbus.ObjectPath("/org/freedesktop/DBus")),
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, protocol.BusName),
	}
	signals := make(chan *dbus.Signal, 64)
	connection.Signal(signals)
	if err := connection.AddMatchSignalContext(ctx, events...); err != nil {
		connection.RemoveSignal(signals)
		return nil, nil, err
	}
	if err := connection.AddMatchSignalContext(ctx, owner...); err != nil {
		removeWidgetMatch(connection, events)
		connection.RemoveSignal(signals)
		return nil, nil, err
	}
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			removeWidgetMatch(connection, owner)
			removeWidgetMatch(connection, events)
			connection.RemoveSignal(signals)
		})
	}
	return signals, cancel, nil
}

func removeWidgetMatch(connection *dbus.Conn, options []dbus.MatchOption) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = connection.RemoveMatchSignalContext(ctx, options...)
	cancel()
}
