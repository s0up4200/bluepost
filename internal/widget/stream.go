package widget

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/s0up4200/bluepost/internal/model"
	"github.com/s0up4200/bluepost/internal/otp"
	"github.com/s0up4200/bluepost/internal/protocol"
	"github.com/s0up4200/bluepost/internal/textsafe"
)

const messageLimit = 5

var ErrDaemonUnavailable = errors.New("Bluepost daemon is unavailable")

type Source interface {
	Status(context.Context) (model.Status, error)
	Events(context.Context, []string, uint32) ([]model.Message, error)
}

type Message struct {
	Handle    string    `json:"handle"`
	Sender    string    `json:"sender"`
	Timestamp time.Time `json:"timestamp"`
	Body      string    `json:"body"`
	CopyText  string    `json:"copy_text"`
	CopyKind  string    `json:"copy_kind"`
}

type Snapshot struct {
	Status   model.Status `json:"status"`
	Messages []Message    `json:"messages"`
}

func Build(ctx context.Context, source Source) (Snapshot, error) {
	status, err := source.Status(ctx)
	if err != nil {
		return Snapshot{}, errors.New("Could not get Bluepost status")
	}
	messages, err := source.Events(ctx, []string{"sms_received"}, messageLimit)
	if err != nil {
		return Snapshot{}, errors.New("Could not get recent messages")
	}
	messages = append([]model.Message(nil), messages...)
	slices.SortStableFunc(messages, func(left, right model.Message) int {
		return right.Timestamp.Compare(left.Timestamp)
	})
	if len(messages) > messageLimit {
		messages = messages[:messageLimit]
	}

	result := Snapshot{Status: status, Messages: make([]Message, 0, len(messages))}
	for _, message := range messages {
		sender := textsafe.OneLine(message.ContactName)
		if sender == "" {
			sender = textsafe.OneLine(message.SenderAddress)
		}
		if sender == "" {
			sender = "Unknown sender"
		}
		copyText, copyKind := message.Body, "message"
		if code, ok := otp.Extract(message.Body); ok {
			copyText, copyKind = code, "code"
		}
		result.Messages = append(result.Messages, Message{
			Handle:    message.Handle,
			Sender:    sender,
			Timestamp: message.Timestamp,
			Body:      message.Body,
			CopyText:  copyText,
			CopyKind:  copyKind,
		})
	}
	return result, nil
}

func Run(
	ctx context.Context,
	source Source,
	signals <-chan *dbus.Signal,
	output io.Writer,
) error {
	encoder := json.NewEncoder(output)
	if err := writeSnapshot(ctx, source, encoder); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case signal, open := <-signals:
			if !open {
				return errors.New("Bluepost D-Bus signal stream closed")
			}
			if daemonLostName(signal) {
				return ErrDaemonUnavailable
			}
			if signal != nil && (signal.Name == protocol.EventsIface+".HistoryChanged" ||
				signal.Name == protocol.EventsIface+".StatusChanged") {
				if err := writeSnapshot(ctx, source, encoder); err != nil {
					return err
				}
			}
		}
	}
}

func writeSnapshot(ctx context.Context, source Source, encoder *json.Encoder) error {
	buildCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	snapshot, err := Build(buildCtx, source)
	cancel()
	if err != nil {
		return err
	}
	if err := encoder.Encode(snapshot); err != nil {
		return errors.New("Could not encode widget snapshot")
	}
	return nil
}

func daemonLostName(signal *dbus.Signal) bool {
	if signal == nil || signal.Name != "org.freedesktop.DBus.NameOwnerChanged" || len(signal.Body) != 3 {
		return false
	}
	name, nameOK := signal.Body[0].(string)
	newOwner, ownerOK := signal.Body[2].(string)
	return nameOK && ownerOK && name == protocol.BusName && newOwner == ""
}
