package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/godbus/dbus/v5"
	"github.com/spf13/cobra"

	"github.com/s0up4200/bluepost/internal/backend"
	"github.com/s0up4200/bluepost/internal/bus"
	appconfig "github.com/s0up4200/bluepost/internal/config"
	"github.com/s0up4200/bluepost/internal/desktop"
	"github.com/s0up4200/bluepost/internal/obex"
	"github.com/s0up4200/bluepost/internal/storage"
)

func newDaemonCommand(
	errors io.Writer,
	getenv func(string) string,
	run func(context.Context, appconfig.Config, io.Writer) error,
) *cobra.Command {
	var phone string
	command := &cobra.Command{
		Use:   "daemon",
		Short: "Receive messages and synchronize contacts",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			command.Root().SilenceUsage = true
			configuration, err := appconfig.Load(phone, getenv)
			if err != nil {
				return err
			}
			return run(command.Context(), configuration, errors)
		},
	}
	command.Flags().StringVar(&phone, "phone", "", "paired and trusted iPhone MAC address")
	return command
}

func runDaemon(ctx context.Context, configuration appconfig.Config, errorsOut io.Writer) error {
	if err := obex.PrepareRuntimeDir(configuration.RuntimeDir); err != nil {
		return err
	}
	systemBus, err := dbus.ConnectSystemBus()
	if err != nil {
		return errors.New("Could not connect to the system D-Bus")
	}
	defer systemBus.Close()
	sessionBus, err := dbus.ConnectSessionBus()
	if err != nil {
		return errors.New("Could not connect to the user D-Bus")
	}
	defer sessionBus.Close()

	worker := obex.NewWorker()
	defer worker.Stop()
	sessions := obex.NewSessions(
		obex.DBusTransport{Conn: systemBus},
		obex.DBusTransport{Conn: sessionBus},
	)
	var application *backend.Backend
	mapAPI := obex.NewMAP(
		obex.DBusTransport{Conn: sessionBus},
		sessions,
		worker,
		configuration.RuntimeDir,
		func(address string) string {
			if application == nil {
				return ""
			}
			return application.ResolveContact(address)
		},
	)
	pbapAPI := obex.NewPBAP(
		obex.DBusTransport{Conn: sessionBus},
		sessions,
		configuration.RuntimeDir,
	)
	profiles := obex.NewProfiles(sessions, mapAPI, pbapAPI, worker)
	notifier := desktop.NewNotifier(errorsOut)
	application = backend.New(backend.Config{
		Phone:    configuration.Phone,
		StateDir: configuration.StateDir,
		Keys:     storage.Keyring{},
		Profiles: profiles,
		Notify:   notifier.Notify,
	})

	parent, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	runContext, cancel := context.WithCancel(parent)
	defer cancel()
	serverResult := make(chan error, 1)
	backendResult := make(chan error, 1)
	go func() { serverResult <- bus.Serve(runContext, sessionBus, application) }()
	go func() { backendResult <- application.Run(runContext) }()

	serverDone := false
	backendDone := false
	var result error
	select {
	case err := <-serverResult:
		serverDone = true
		if !errors.Is(err, context.Canceled) {
			result = err
		}
	case err := <-backendResult:
		backendDone = true
		if !errors.Is(err, context.Canceled) {
			result = errors.New("The Bluepost backend stopped")
		}
	case <-parent.Done():
	}
	cancel()
	if !serverDone {
		<-serverResult
	}
	if !backendDone {
		<-backendResult
	}
	return result
}
