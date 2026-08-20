package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/s0up4200/bluepost/internal/model"
	"github.com/s0up4200/bluepost/internal/protocol"
	"github.com/s0up4200/bluepost/internal/widget"
)

type widgetSource struct {
	status   model.Status
	messages []model.Message
}

func (source widgetSource) Status(context.Context) (model.Status, error) {
	return source.status, nil
}

func (source widgetSource) Events(context.Context, []string, uint32) ([]model.Message, error) {
	return append([]model.Message(nil), source.messages...), nil
}

func TestWidgetCommandStreamsSnapshots(t *testing.T) {
	t.Parallel()

	signals := make(chan *dbus.Signal, 1)
	signals <- daemonOwnerLossSignal()
	var output bytes.Buffer
	command := newWidgetCommand(
		widgetSource{status: model.Status{State: "ready", MAP: true}},
		func(context.Context) (<-chan *dbus.Signal, func(), error) {
			return signals, func() {}, nil
		},
	)
	command.SetOut(&output)

	err := command.ExecuteContext(context.Background())
	if !errors.Is(err, widget.ErrDaemonUnavailable) {
		t.Fatalf("error %v", err)
	}
	var snapshot widget.Snapshot
	if err := json.NewDecoder(&output).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Status.State != "ready" || !snapshot.Status.MAP {
		t.Fatalf("snapshot %#v", snapshot)
	}
}

func TestWidgetCommandReportsSubscriptionFailure(t *testing.T) {
	t.Parallel()

	command := newWidgetCommand(widgetSource{}, func(context.Context) (<-chan *dbus.Signal, func(), error) {
		return nil, nil, errors.New("private D-Bus detail")
	})
	err := command.ExecuteContext(context.Background())
	if err == nil || err.Error() != "Could not subscribe to Bluepost D-Bus events" {
		t.Fatalf("error %v", err)
	}
}

func TestWidgetCommandCancelsSubscription(t *testing.T) {
	t.Parallel()

	cancelCalls := 0
	signals := make(chan *dbus.Signal, 1)
	signals <- daemonOwnerLossSignal()
	command := newWidgetCommand(widgetSource{}, func(context.Context) (<-chan *dbus.Signal, func(), error) {
		return signals, func() { cancelCalls++ }, nil
	})
	_ = command.ExecuteContext(context.Background())
	if cancelCalls != 1 {
		t.Fatalf("cancel calls %d", cancelCalls)
	}
}

func TestSubscribeWidgetSignalsRejectsMissingConnection(t *testing.T) {
	t.Parallel()

	_, _, err := subscribeWidgetSignals(context.Background(), nil)
	if err == nil || err.Error() != "D-Bus connection is not available" {
		t.Fatalf("error %v", err)
	}
}

func daemonOwnerLossSignal() *dbus.Signal {
	return &dbus.Signal{
		Name: "org.freedesktop.DBus.NameOwnerChanged",
		Body: []any{protocol.BusName, ":1.42", ""},
	}
}
