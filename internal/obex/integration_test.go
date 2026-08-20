//go:build integration

package obex

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"
)

const integrationPhone = "AA:BB:CC:DD:EE:FF"

var (
	integrationMAPPath     = dbus.ObjectPath("/org/bluez/obex/client/session_map")
	integrationPBAPPath    = dbus.ObjectPath("/org/bluez/obex/client/session_pbap")
	integrationMessagePath = dbus.ObjectPath("/org/bluez/obex/client/session_map/message1")
)

type integrationObjectManager struct {
	objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
}

func (manager *integrationObjectManager) GetManagedObjects() (
	map[dbus.ObjectPath]map[string]map[string]dbus.Variant,
	*dbus.Error,
) {
	return manager.objects, nil
}

type integrationOBEXClient struct {
	mu      sync.Mutex
	created []string
	removed []dbus.ObjectPath
	phone   string
}

func (client *integrationOBEXClient) CreateSession(
	phone string,
	options map[string]dbus.Variant,
) (dbus.ObjectPath, *dbus.Error) {
	target, _ := options["Target"].Value().(string)
	client.mu.Lock()
	client.phone = phone
	client.created = append(client.created, target)
	client.mu.Unlock()
	switch target {
	case "MAP":
		return integrationMAPPath, nil
	case "PBAP":
		return integrationPBAPPath, nil
	default:
		return "", dbus.NewError("org.bluez.obex.Error.InvalidArguments", []any{"invalid target"})
	}
}

func (client *integrationOBEXClient) RemoveSession(path dbus.ObjectPath) *dbus.Error {
	client.mu.Lock()
	client.removed = append(client.removed, path)
	client.mu.Unlock()
	return nil
}

type integrationMAP struct {
	mu      sync.Mutex
	folders []string
	limit   uint16
}

func (profile *integrationMAP) SetFolder(folder string) *dbus.Error {
	profile.mu.Lock()
	profile.folders = append(profile.folders, folder)
	profile.mu.Unlock()
	return nil
}

func (profile *integrationMAP) ListMessages(
	name string,
	options map[string]dbus.Variant,
) (map[dbus.ObjectPath]map[string]dbus.Variant, *dbus.Error) {
	if name != "" {
		return nil, dbus.NewError("org.bluez.obex.Error.InvalidArguments", []any{"invalid name"})
	}
	limit, _ := options["MaxListCount"].Value().(uint16)
	profile.mu.Lock()
	profile.limit = limit
	profile.mu.Unlock()
	return map[dbus.ObjectPath]map[string]dbus.Variant{
		integrationMessagePath: {
			"Sender":    dbus.MakeVariant("+4712345678"),
			"Subject":   dbus.MakeVariant("hello"),
			"Timestamp": dbus.MakeVariant("20260820T120102+0200"),
			"Read":      dbus.MakeVariant(true),
		},
	}, nil
}

type integrationPBAP struct {
	mu        sync.Mutex
	location  string
	phonebook string
	format    string
	maxCount  uint16
}

func (profile *integrationPBAP) Select(location, phonebook string) *dbus.Error {
	profile.mu.Lock()
	profile.location = location
	profile.phonebook = phonebook
	profile.mu.Unlock()
	return nil
}

func (profile *integrationPBAP) PullAll(
	target string,
	options map[string]dbus.Variant,
) (dbus.ObjectPath, map[string]dbus.Variant, *dbus.Error) {
	if err := os.WriteFile(
		target,
		[]byte("BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Jane\r\nTEL:+4712345678\r\nEND:VCARD\r\n"),
		0o600,
	); err != nil {
		return "", nil, dbus.MakeFailedError(err)
	}
	format, _ := options["Format"].Value().(string)
	maxCount, _ := options["MaxCount"].Value().(uint16)
	profile.mu.Lock()
	profile.format = format
	profile.maxCount = maxCount
	profile.mu.Unlock()
	return "/org/bluez/obex/client/session_pbap/transfer1", map[string]dbus.Variant{
		"Status": dbus.MakeVariant("complete"),
	}, nil
}

func TestBlueZProfilesRoundTripOnPrivateBus(t *testing.T) {
	if os.Getenv("BLUEPOST_TEST_PRIVATE_BUS") != "1" {
		t.Skip("requires a private dbus-run-session")
	}

	bluezConn := integrationBusConnection(t)
	defer bluezConn.Close()
	obexConn := integrationBusConnection(t)
	defer obexConn.Close()
	clientConn := integrationBusConnection(t)
	defer clientConn.Close()
	integrationOwnName(t, bluezConn, bluezDestination)
	integrationOwnName(t, obexConn, obexDestination)

	bluezManager := &integrationObjectManager{objects: map[dbus.ObjectPath]map[string]map[string]dbus.Variant{
		"/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF": {
			bluezDeviceInterface: {
				"Address": dbus.MakeVariant(integrationPhone),
				"Paired":  dbus.MakeVariant(true),
				"Trusted": dbus.MakeVariant(true),
			},
		},
	}}
	obexManager := &integrationObjectManager{objects: map[dbus.ObjectPath]map[string]map[string]dbus.Variant{}}
	obexClient := &integrationOBEXClient{}
	mapProfile := &integrationMAP{}
	pbapProfile := &integrationPBAP{}
	integrationExport(t, bluezConn, bluezManager, bluezRoot, objectManagerInterface)
	integrationExport(t, obexConn, obexManager, bluezRoot, objectManagerInterface)
	integrationExport(t, obexConn, obexClient, obexRoot, "org.bluez.obex.Client1")
	integrationExport(t, obexConn, mapProfile, integrationMAPPath, messageAccessInterface)
	integrationExport(t, obexConn, pbapProfile, integrationPBAPPath, phonebookInterface)

	transport := DBusTransport{Conn: clientConn}
	sessions := NewSessions(transport, transport)
	if err := sessions.Open(context.Background(), integrationPhone); err != nil {
		t.Fatal(err)
	}
	messages, err := NewMAP(transport, sessions, nil, filepath.Join(t.TempDir(), "runtime"), nil).
		ListRecent(context.Background(), "telecom/msg/inbox", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].SenderAddress != "+4712345678" || messages[0].Body != "hello" {
		t.Fatalf("messages %#v", messages)
	}
	contacts, err := NewPBAP(transport, sessions, filepath.Join(t.TempDir(), "runtime")).Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts) != 1 || contacts[0].Name != "Jane" || !slices.Equal(contacts[0].Phones, []string{"4712345678"}) {
		t.Fatalf("contacts %#v", contacts)
	}
	if err := sessions.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	obexClient.mu.Lock()
	if obexClient.phone != integrationPhone || !slices.Equal(obexClient.created, []string{"MAP", "PBAP"}) ||
		!slices.Equal(obexClient.removed, []dbus.ObjectPath{integrationMAPPath, integrationPBAPPath}) {
		t.Fatalf("phone %q, created %q, removed %q", obexClient.phone, obexClient.created, obexClient.removed)
	}
	obexClient.mu.Unlock()
	mapProfile.mu.Lock()
	if !slices.Equal(mapProfile.folders, []string{"/", "telecom", "msg", "inbox"}) || mapProfile.limit != 20 {
		t.Fatalf("folders %q, limit %d", mapProfile.folders, mapProfile.limit)
	}
	mapProfile.mu.Unlock()
	pbapProfile.mu.Lock()
	if pbapProfile.location != "int" || pbapProfile.phonebook != "pb" ||
		pbapProfile.format != "vcard30" || pbapProfile.maxCount != 65535 {
		t.Fatalf(
			"PBAP location %q, phonebook %q, format %q, max count %d",
			pbapProfile.location,
			pbapProfile.phonebook,
			pbapProfile.format,
			pbapProfile.maxCount,
		)
	}
	pbapProfile.mu.Unlock()
}

func integrationBusConnection(t *testing.T) *dbus.Conn {
	t.Helper()
	connection, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	return connection
}

func integrationOwnName(t *testing.T, connection *dbus.Conn, name string) {
	t.Helper()
	reply, err := connection.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own %s: reply %d, error %v", name, reply, err)
	}
}

func integrationExport(t *testing.T, connection *dbus.Conn, value any, path dbus.ObjectPath, iface string) {
	t.Helper()
	if err := connection.Export(value, path, iface); err != nil {
		t.Fatal(err)
	}
}
