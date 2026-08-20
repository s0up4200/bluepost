package widget

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/s0up4200/bluepost/internal/model"
	"github.com/s0up4200/bluepost/internal/protocol"
)

type fakeSource struct {
	status   model.Status
	messages []model.Message
	kinds    []string
	limit    uint32
	err      error
}

func (source *fakeSource) Status(context.Context) (model.Status, error) {
	return source.status, source.err
}

func (source *fakeSource) Events(
	_ context.Context,
	kinds []string,
	limit uint32,
) ([]model.Message, error) {
	source.kinds = append([]string(nil), kinds...)
	source.limit = limit
	return append([]model.Message(nil), source.messages...), source.err
}

func TestBuildSortsFiveMessagesAndSelectsClipboardText(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.August, 20, 20, 0, 0, 0, time.UTC)
	source := &fakeSource{
		status: model.Status{
			State: "ready",
			MAP:   true,
			PBAP:  true,
			Phone: "XX:XX:XX:XX:A3:BB",
		},
		messages: []model.Message{
			{Handle: "old", SenderAddress: "+4700000000", Body: "Old", Timestamp: base.Add(-time.Hour)},
			{Handle: "otp", ContactName: "Stripe", SenderAddress: "+4711111111", Body: "Your Stripe verification code is 482731.", Timestamp: base.Add(5 * time.Minute)},
			{Handle: "normal", SenderAddress: "+4722222222", Body: "Dinner is at seven.", Timestamp: base.Add(4 * time.Minute)},
			{Handle: "third", SenderAddress: "+4733333333", Body: "Third", Timestamp: base.Add(3 * time.Minute)},
			{Handle: "second", SenderAddress: "+4744444444", Body: "Second", Timestamp: base.Add(2 * time.Minute)},
			{Handle: "first", SenderAddress: "+4755555555", Body: "First", Timestamp: base.Add(time.Minute)},
		},
	}

	got, err := Build(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status.Phone != "XX:XX:XX:XX:A3:BB" {
		t.Fatalf("phone %q", got.Status.Phone)
	}
	if !slices.Equal(source.kinds, []string{"sms_received"}) || source.limit != 5 {
		t.Fatalf("query kinds %q, limit %d", source.kinds, source.limit)
	}
	if len(got.Messages) != 5 {
		t.Fatalf("message count %d", len(got.Messages))
	}
	if got.Messages[0].Handle != "otp" || got.Messages[0].Sender != "Stripe" ||
		got.Messages[0].CopyText != "482731" || got.Messages[0].CopyKind != "code" {
		t.Fatalf("OTP message %#v", got.Messages[0])
	}
	if got.Messages[1].Handle != "normal" || got.Messages[1].Sender != "+4722222222" ||
		got.Messages[1].CopyText != "Dinner is at seven." || got.Messages[1].CopyKind != "message" {
		t.Fatalf("normal message %#v", got.Messages[1])
	}
	if got.Messages[4].Handle != "first" {
		t.Fatalf("last message %#v", got.Messages[4])
	}
}

func TestRunWritesInitialSnapshotAndHistoryUpdate(t *testing.T) {
	t.Parallel()

	source := &fakeSource{
		status:   model.Status{State: "ready", MAP: true},
		messages: []model.Message{{Handle: "one", Body: "Hello"}},
	}
	signals := make(chan *dbus.Signal, 1)
	signals <- &dbus.Signal{Name: protocol.EventsIface + ".HistoryChanged"}
	close(signals)
	var output bytes.Buffer

	err := Run(context.Background(), source, signals, &output)
	if err == nil {
		t.Fatal("expected closed signal channel error")
	}
	snapshots := decodeSnapshots(t, output.Bytes())
	if len(snapshots) != 2 || len(snapshots[0].Messages) != 1 || len(snapshots[1].Messages) != 1 {
		t.Fatalf("snapshots %#v", snapshots)
	}
}

func TestRunWritesStatusUpdate(t *testing.T) {
	t.Parallel()

	source := &fakeSource{status: model.Status{State: "connecting"}}
	signals := make(chan *dbus.Signal, 1)
	signals <- &dbus.Signal{Name: protocol.EventsIface + ".StatusChanged"}
	close(signals)
	var output bytes.Buffer

	if err := Run(context.Background(), source, signals, &output); err == nil {
		t.Fatal("expected closed signal channel error")
	}
	if snapshots := decodeSnapshots(t, output.Bytes()); len(snapshots) != 2 {
		t.Fatalf("snapshot count %d", len(snapshots))
	}
}

func TestRunStopsWhenDaemonLosesName(t *testing.T) {
	t.Parallel()

	signals := make(chan *dbus.Signal, 1)
	signals <- &dbus.Signal{
		Name: "org.freedesktop.DBus.NameOwnerChanged",
		Body: []any{protocol.BusName, ":1.42", ""},
	}
	var output bytes.Buffer

	err := Run(context.Background(), &fakeSource{}, signals, &output)
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Fatalf("error %v", err)
	}
	if snapshots := decodeSnapshots(t, output.Bytes()); len(snapshots) != 1 {
		t.Fatalf("snapshot count %d", len(snapshots))
	}
}

func TestRunReturnsSourceErrorWithoutPrivateOutput(t *testing.T) {
	t.Parallel()

	private := "private message body"
	var output bytes.Buffer
	err := Run(
		context.Background(),
		&fakeSource{err: errors.New(private)},
		make(chan *dbus.Signal),
		&output,
	)
	if err == nil || err.Error() != "Could not get Bluepost status" {
		t.Fatalf("error %v", err)
	}
	if strings.Contains(output.String(), private) {
		t.Fatal("private source error reached output")
	}
}

func decodeSnapshots(t *testing.T, data []byte) []Snapshot {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	var snapshots []Snapshot
	for {
		var snapshot Snapshot
		if err := decoder.Decode(&snapshot); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatal(err)
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots
}
