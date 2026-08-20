package obex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/s0up4200/bluepost/internal/model"
)

const incomingBMessage = "BEGIN:BMSG\r\nSTATUS:UNREAD\r\nTYPE:SMS_GSM\r\n" +
	"FOLDER:telecom/msg/inbox\r\nBEGIN:VCARD\r\nFN:Jane\r\n" +
	"TEL:+47 123 45 678\r\nEND:VCARD\r\nBEGIN:BENV\r\nBEGIN:BBODY\r\n" +
	"BEGIN:MSG\r\nhello from phone\r\nEND:MSG\r\nEND:BBODY\r\nEND:BENV\r\nEND:BMSG\r\n"

func TestMAPHandleAddedFetchesOnlyItsSessionMessage(t *testing.T) {
	t.Parallel()

	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	sessions := &Sessions{mapPath: "/org/bluez/obex/client/session1"}
	transport := &fakeTransport{}
	transport.handler = func(call transportCall) ([]any, error) {
		if call.method != messageGet {
			return nil, errors.New("unexpected call: " + call.method)
		}
		path := call.args[0].(string)
		if err := os.WriteFile(path, []byte(incomingBMessage), 0o600); err != nil {
			return nil, err
		}
		return []any{
			dbus.ObjectPath("/org/bluez/obex/client/session1/transfer1"),
			map[string]dbus.Variant{"Status": dbus.MakeVariant("complete")},
		}, nil
	}
	client := NewMAP(transport, sessions, nil, runtimeDir, func(address string) string {
		if address == "+47 123 45 678" {
			return "Jane"
		}
		return ""
	})
	properties := map[string]map[string]dbus.Variant{
		messageInterface: {
			"Timestamp": dbus.MakeVariant("20260820T120102+0200"),
			"Read":      dbus.MakeVariant(false),
		},
	}
	got, err := client.HandleAdded(
		context.Background(),
		"/org/bluez/obex/client/session1/message42",
		properties,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Handle != "message42" || got.SenderAddress != "+47 123 45 678" ||
		got.SenderPhoneNorm != "4712345678" || got.ContactName != "Jane" ||
		got.Body != "hello from phone" || got.Read {
		t.Fatalf("message %#v", got)
	}
	wantTime := time.Date(2026, 8, 20, 12, 1, 2, 0, time.FixedZone("", 2*60*60))
	if !got.Timestamp.Equal(wantTime) {
		t.Fatalf("timestamp %v", got.Timestamp)
	}
	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary files remain: %v", entries)
	}

	if _, err := client.HandleAdded(
		context.Background(),
		"/org/bluez/obex/client/session2/message1",
		properties,
	); !errors.Is(err, ErrForeignMessage) {
		t.Fatalf("foreign path error %v", err)
	}
}

func TestMAPHandleAddedRemovesTemporaryFileAfterParseError(t *testing.T) {
	t.Parallel()

	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	sessions := &Sessions{mapPath: "/org/bluez/obex/client/session1"}
	transport := &fakeTransport{}
	transport.handler = func(call transportCall) ([]any, error) {
		if err := os.WriteFile(call.args[0].(string), []byte("not a bMessage"), 0o600); err != nil {
			return nil, err
		}
		return []any{dbus.ObjectPath("/transfer1"), map[string]dbus.Variant{"Status": dbus.MakeVariant("complete")}}, nil
	}
	client := NewMAP(transport, sessions, nil, runtimeDir, nil)
	_, err := client.HandleAdded(
		context.Background(),
		"/org/bluez/obex/client/session1/message1",
		map[string]map[string]dbus.Variant{messageInterface: {}},
	)
	if err == nil {
		t.Fatal("expected parse error")
	}
	entries, readErr := os.ReadDir(runtimeDir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary files remain: %v", entries)
	}
}

func TestMAPListRecentUsesBoundedInboxProperties(t *testing.T) {
	t.Parallel()

	sessions := &Sessions{mapPath: "/org/bluez/obex/client/session1"}
	transport := &fakeTransport{}
	transport.handler = func(call transportCall) ([]any, error) {
		switch call.method {
		case messageAccessSetFolder:
			return nil, nil
		case messageAccessListMessages:
			return []any{map[dbus.ObjectPath]map[string]dbus.Variant{
				"/org/bluez/obex/client/session1/message7": {
					"Sender":    dbus.MakeVariant("+4712345678"),
					"Subject":   dbus.MakeVariant("hello"),
					"Timestamp": dbus.MakeVariant("20260820T120102"),
					"Read":      dbus.MakeVariant(true),
				},
			}}, nil
		default:
			return nil, errors.New("unexpected call: " + call.method)
		}
	}
	client := NewMAP(transport, sessions, nil, filepath.Join(t.TempDir(), "runtime"), nil)
	got, err := client.ListRecent(context.Background(), "telecom/msg/inbox", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Handle != "message7" || got[0].Body != "hello" || !got[0].Read {
		t.Fatalf("messages %#v", got)
	}
	var folders []string
	for _, call := range transport.calls {
		if call.method == messageAccessSetFolder {
			folders = append(folders, call.args[0].(string))
		}
	}
	if strings.Join(folders, "/") != "//telecom/msg/inbox" {
		t.Fatalf("folder calls %q", folders)
	}
	if _, err := client.ListRecent(context.Background(), "../../private", 20); err == nil {
		t.Fatal("expected folder validation error")
	}
}

func TestMAPWatchDispatchesCompleteInterfacesAddedSignal(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	sessions := &Sessions{mapPath: "/org/bluez/obex/client/session1"}
	signals := make(chan *dbus.Signal, 1)
	transport := &fakeTransport{signals: signals}
	transport.handler = func(call transportCall) ([]any, error) {
		if err := os.WriteFile(call.args[0].(string), []byte(incomingBMessage), 0o600); err != nil {
			return nil, err
		}
		return []any{dbus.ObjectPath("/transfer1"), map[string]dbus.Variant{"Status": dbus.MakeVariant("complete")}}, nil
	}
	worker := NewWorker()
	defer worker.Stop()
	client := NewMAP(transport, sessions, worker, runtimeDir, nil)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan model.Message, 1)
	done := make(chan error, 1)
	go func() {
		done <- client.Watch(ctx, func(message model.Message) error {
			result <- message
			cancel()
			return nil
		})
	}()
	signals <- &dbus.Signal{
		Name: objectManagerInterface + ".InterfacesAdded",
		Body: []any{
			dbus.ObjectPath("/org/bluez/obex/client/session1/message9"),
			map[string]map[string]dbus.Variant{messageInterface: {}},
		},
	}
	select {
	case got := <-result:
		if got.Handle != "message9" {
			t.Fatalf("message %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("MAP watcher did not dispatch message")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("watch error %v", err)
	}
}
