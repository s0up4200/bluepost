package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/godbus/dbus/v5"

	"github.com/s0up4200/bluepost/internal/backend"
	"github.com/s0up4200/bluepost/internal/bus"
	appconfig "github.com/s0up4200/bluepost/internal/config"
	"github.com/s0up4200/bluepost/internal/desktop"
	"github.com/s0up4200/bluepost/internal/obex"
	"github.com/s0up4200/bluepost/internal/storage"
	"github.com/s0up4200/bluepost/internal/textsafe"
)

func main() {
	os.Exit(run())
}

func run() int {
	configuration, err := appconfig.Load(os.Args[1:], os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, textsafe.OneLine(err.Error()))
		return 2
	}
	if err := obex.PrepareRuntimeDir(configuration.RuntimeDir); err != nil {
		fmt.Fprintln(os.Stderr, textsafe.OneLine(err.Error()))
		return 1
	}
	systemBus, err := dbus.ConnectSystemBus()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not connect to the system D-Bus")
		return 1
	}
	defer systemBus.Close()
	sessionBus, err := dbus.ConnectSessionBus()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not connect to the user D-Bus")
		return 1
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
	notifier := desktop.NewNotifier(os.Stderr)
	application = backend.New(backend.Config{
		Phone:    configuration.Phone,
		StateDir: configuration.StateDir,
		Keys:     storage.Keyring{},
		Profiles: profiles,
		Notify:   notifier.Notify,
	})

	parent, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	serverResult := make(chan error, 1)
	backendResult := make(chan error, 1)
	go func() { serverResult <- bus.Serve(ctx, sessionBus, application) }()
	go func() { backendResult <- application.Run(ctx) }()

	serverDone := false
	backendDone := false
	exitCode := 0
	select {
	case err := <-serverResult:
		serverDone = true
		if !errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, textsafe.OneLine(err.Error()))
			exitCode = 1
		}
	case err := <-backendResult:
		backendDone = true
		if !errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "The Bluepost backend stopped")
			exitCode = 1
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
	return exitCode
}
