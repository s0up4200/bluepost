//go:build integration

package bus

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/s0up4200/bluepost/internal/model"
	"github.com/s0up4200/bluepost/internal/protocol"
)

func TestServiceAndClientRoundTripOnPrivateBus(t *testing.T) {
	if os.Getenv("BLUEPOST_TEST_PRIVATE_BUS") != "1" {
		t.Skip("requires a private dbus-run-session")
	}

	serverConn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer serverConn.Close()
	clientConn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()

	backend := &fakeBackend{
		status:   model.Status{State: "ready", Storage: "unlocked", MAP: true, PBAP: true},
		messages: []model.Message{{Kind: "sms_received", Handle: "message-1", Body: "hello"}},
		contacts: []model.Contact{{Name: "Jane", Phones: []string{"4712345678"}}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, serverConn, backend) }()
	waitForBusName(t, clientConn, done)

	client := NewClient(clientConn)
	status, err := client.Status(context.Background())
	if err != nil || status.State != "ready" || !status.MAP || !status.PBAP {
		t.Fatalf("status %#v, error %v", status, err)
	}
	healthy, err := client.Healthy(context.Background())
	if err != nil || !healthy {
		t.Fatalf("healthy %v, error %v", healthy, err)
	}
	events, err := client.Events(context.Background(), []string{"sms_received"}, 20)
	if err != nil || len(events) != 1 || events[0].Handle != "message-1" {
		t.Fatalf("events %#v, error %v", events, err)
	}
	recent, err := client.Recent(context.Background(), "telecom/msg/inbox", 20)
	if err != nil || len(recent) != 1 || recent[0].Body != "hello" {
		t.Fatalf("recent %#v, error %v", recent, err)
	}
	found, err := client.FindContacts(context.Background(), "jane")
	if err != nil || len(found) != 1 || found[0].Name != "Jane" {
		t.Fatalf("found contacts %#v, error %v", found, err)
	}
	contacts, err := client.Contacts(context.Background(), 0, 20)
	if err != nil || len(contacts) != 1 || contacts[0].Phones[0] != "4712345678" {
		t.Fatalf("contacts %#v, error %v", contacts, err)
	}
	count, err := client.SyncContacts(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("contact count %d, error %v", count, err)
	}

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("server error %v", err)
	}
}

func waitForBusName(t *testing.T, connection *dbus.Conn, serverDone <-chan error) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var owned bool
		if err := connection.BusObject().Call(
			"org.freedesktop.DBus.NameHasOwner",
			0,
			protocol.BusName,
		).Store(&owned); err != nil {
			t.Fatal(err)
		}
		if owned {
			return
		}
		select {
		case err := <-serverDone:
			t.Fatalf("server stopped before owning its name: %v", err)
		case <-deadline.C:
			t.Fatal("server did not own its D-Bus name")
		case <-ticker.C:
		}
	}
}
