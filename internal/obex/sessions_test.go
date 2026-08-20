package obex

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type transportCall struct {
	destination string
	path        dbus.ObjectPath
	method      string
	args        []any
}

type fakeTransport struct {
	mu        sync.Mutex
	handler   func(transportCall) ([]any, error)
	calls     []transportCall
	signals   <-chan *dbus.Signal
	subscribe error
}

func (transport *fakeTransport) Call(
	_ context.Context,
	destination string,
	path dbus.ObjectPath,
	method string,
	args ...any,
) ([]any, error) {
	call := transportCall{
		destination: destination,
		path:        path,
		method:      method,
		args:        append([]any(nil), args...),
	}
	transport.mu.Lock()
	transport.calls = append(transport.calls, call)
	transport.mu.Unlock()
	return transport.handler(call)
}

func (transport *fakeTransport) Subscribe(
	context.Context,
	...dbus.MatchOption,
) (<-chan *dbus.Signal, func(), error) {
	if transport.subscribe != nil {
		return nil, nil, transport.subscribe
	}
	if transport.signals == nil {
		return make(chan *dbus.Signal), func() {}, nil
	}
	return transport.signals, func() {}, nil
}

func TestSessionsUseExactMAPAndPBAPTargets(t *testing.T) {
	t.Parallel()

	phone := "AA:BB:CC:DD:EE:FF"
	system := trustedSystemTransport(phone, true, true)
	obex := &fakeTransport{}
	obex.handler = func(call transportCall) ([]any, error) {
		switch call.method {
		case objectManagerGetManagedObjects:
			return []any{managedObjects{}}, nil
		case clientCreateSession:
			options := call.args[1].(map[string]dbus.Variant)
			target := options["Target"].Value().(string)
			return []any{dbus.ObjectPath("/org/bluez/obex/client/session_" + strings.ToLower(target))}, nil
		default:
			return nil, errors.New("unexpected call: " + call.method)
		}
	}

	sessions := NewSessions(system, obex)
	if err := sessions.Open(context.Background(), phone); err != nil {
		t.Fatal(err)
	}
	mapPath, mapOK := sessions.MapPath()
	pbapPath, pbapOK := sessions.PBAPPath()
	if !mapOK || mapPath != "/org/bluez/obex/client/session_map" {
		t.Fatalf("MAP path %q, available %v", mapPath, mapOK)
	}
	if !pbapOK || pbapPath != "/org/bluez/obex/client/session_pbap" {
		t.Fatalf("PBAP path %q, available %v", pbapPath, pbapOK)
	}

	var targets []string
	for _, call := range obex.calls {
		if call.method != clientCreateSession {
			continue
		}
		if call.args[0] != phone {
			t.Fatalf("destination argument %#v", call.args[0])
		}
		options := call.args[1].(map[string]dbus.Variant)
		targets = append(targets, options["Target"].Value().(string))
	}
	if len(targets) != 2 || targets[0] != "MAP" || targets[1] != "PBAP" {
		t.Fatalf("targets %q", targets)
	}
}

func TestSessionsCleanUpOnlyLivePartialOpen(t *testing.T) {
	t.Parallel()

	phone := "AA:BB:CC:DD:EE:FF"
	system := trustedSystemTransport(phone, true, true)
	obex := &fakeTransport{}
	obex.handler = func(call transportCall) ([]any, error) {
		switch call.method {
		case objectManagerGetManagedObjects:
			return []any{managedObjects{}}, nil
		case clientCreateSession:
			options := call.args[1].(map[string]dbus.Variant)
			if options["Target"].Value() == "MAP" {
				return []any{dbus.ObjectPath("/org/bluez/obex/client/session_map")}, nil
			}
			return nil, errors.New("PBAP refused")
		case clientRemoveSession:
			return nil, nil
		default:
			return nil, errors.New("unexpected call: " + call.method)
		}
	}

	sessions := NewSessions(system, obex)
	if err := sessions.Open(context.Background(), phone); err == nil {
		t.Fatal("expected PBAP error")
	}
	removed := make([]dbus.ObjectPath, 0)
	for _, call := range obex.calls {
		if call.method == clientRemoveSession {
			removed = append(removed, call.args[0].(dbus.ObjectPath))
		}
	}
	if len(removed) != 1 || removed[0] != "/org/bluez/obex/client/session_map" {
		t.Fatalf("removed paths %q", removed)
	}
}

func TestSessionsDiscardDisappearedPathWithoutRemoteRemoval(t *testing.T) {
	t.Parallel()

	phone := "AA:BB:CC:DD:EE:FF"
	system := trustedSystemTransport(phone, true, true)
	obex := sessionTransport()
	sessions := NewSessions(system, obex)
	if err := sessions.Open(context.Background(), phone); err != nil {
		t.Fatal(err)
	}
	mapPath, _ := sessions.MapPath()
	sessions.MarkLost(mapPath)
	if _, ok := sessions.MapPath(); ok {
		t.Fatal("disappeared MAP path remained available")
	}
	for _, call := range obex.calls {
		if call.method == clientRemoveSession {
			t.Fatalf("removed disappeared path %#v", call.args)
		}
	}
}

func TestSessionsRejectUnpairedOrUntrustedPhone(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		paired  bool
		trusted bool
	}{
		{name: "unpaired", trusted: true},
		{name: "untrusted", paired: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			phone := "AA:BB:CC:DD:EE:FF"
			system := trustedSystemTransport(phone, test.paired, test.trusted)
			obex := sessionTransport()
			if err := NewSessions(system, obex).Open(context.Background(), phone); err == nil {
				t.Fatal("expected device trust error")
			}
			for _, call := range obex.calls {
				if call.method == clientCreateSession {
					t.Fatal("opened OBEX session for unsafe device")
				}
			}
		})
	}
}

func TestMonitorStopsWhenSignalConnectionCloses(t *testing.T) {
	t.Parallel()

	signals := make(chan *dbus.Signal)
	close(signals)
	transport := &fakeTransport{signals: signals}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := NewSessions(transport, transport).Monitor(ctx, nil)
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("monitor spun on a closed signal channel")
	}
	if err == nil {
		t.Fatal("expected closed subscription error")
	}
}

func trustedSystemTransport(phone string, paired, trusted bool) *fakeTransport {
	transport := &fakeTransport{}
	transport.handler = func(call transportCall) ([]any, error) {
		if call.method != objectManagerGetManagedObjects {
			return nil, errors.New("unexpected system call: " + call.method)
		}
		return []any{managedObjects{
			"/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF": {
				bluezDeviceInterface: {
					"Address": dbus.MakeVariant(phone),
					"Paired":  dbus.MakeVariant(paired),
					"Trusted": dbus.MakeVariant(trusted),
				},
			},
		}}, nil
	}
	return transport
}

func sessionTransport() *fakeTransport {
	transport := &fakeTransport{}
	transport.handler = func(call transportCall) ([]any, error) {
		switch call.method {
		case objectManagerGetManagedObjects:
			return []any{managedObjects{}}, nil
		case clientCreateSession:
			options := call.args[1].(map[string]dbus.Variant)
			target := strings.ToLower(options["Target"].Value().(string))
			return []any{dbus.ObjectPath("/org/bluez/obex/client/session_" + target)}, nil
		case clientRemoveSession:
			return nil, nil
		default:
			return nil, errors.New("unexpected call: " + call.method)
		}
	}
	return transport
}
