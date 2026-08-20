package obex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestPBAPSyncUsesExactOptionsAndParsesContacts(t *testing.T) {
	t.Parallel()

	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	sessions := &Sessions{pbapPath: "/org/bluez/obex/client/session2"}
	transport := &fakeTransport{}
	transport.handler = func(call transportCall) ([]any, error) {
		switch call.method {
		case phonebookSelect:
			if call.args[0] != "int" || call.args[1] != "pb" {
				return nil, errors.New("wrong phonebook selection")
			}
			return nil, nil
		case phonebookPullAll:
			options := call.args[1].(map[string]dbus.Variant)
			if options["Format"].Value() != "vcard30" || options["MaxCount"].Value() != uint16(65535) {
				return nil, errors.New("wrong PullAll options")
			}
			cards := "BEGIN:VCARD\r\nFN:Jane\r\nTEL:+4712345678\r\nEND:VCARD\r\n"
			if err := os.WriteFile(call.args[0].(string), []byte(cards), 0o600); err != nil {
				return nil, err
			}
			return []any{dbus.ObjectPath("/transfer2"), map[string]dbus.Variant{"Status": dbus.MakeVariant("complete")}}, nil
		default:
			return nil, errors.New("unexpected call: " + call.method)
		}
	}
	client := NewPBAP(transport, sessions, runtimeDir)
	got, err := client.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "Jane" || got[0].Phones[0] != "4712345678" {
		t.Fatalf("contacts %#v", got)
	}
	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary files remain: %v", entries)
	}
}

func TestPBAPSyncRemovesTemporaryFileAfterTransferError(t *testing.T) {
	t.Parallel()

	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	sessions := &Sessions{pbapPath: "/org/bluez/obex/client/session2"}
	transport := &fakeTransport{}
	transport.handler = func(call transportCall) ([]any, error) {
		switch call.method {
		case phonebookSelect:
			return nil, nil
		case phonebookPullAll:
			return []any{dbus.ObjectPath("/transfer2"), map[string]dbus.Variant{"Status": dbus.MakeVariant("error")}}, nil
		default:
			return nil, errors.New("unexpected call: " + call.method)
		}
	}
	client := NewPBAP(transport, sessions, runtimeDir)
	if _, err := client.Sync(context.Background()); err == nil {
		t.Fatal("expected transfer error")
	}
	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary files remain: %v", entries)
	}
}
