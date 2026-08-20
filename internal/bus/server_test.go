package bus

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"

	"github.com/s0up4200/bluepost/internal/model"
	"github.com/s0up4200/bluepost/internal/protocol"
)

type fakeBackend struct {
	status   model.Status
	messages []model.Message
	contacts []model.Contact
}

func (backend *fakeBackend) Status() model.Status { return backend.status }
func (backend *fakeBackend) Healthy() bool        { return backend.status.MAP }

func (backend *fakeBackend) ListEvents([]string, uint32) ([]model.Message, error) {
	return append([]model.Message(nil), backend.messages...), nil
}

func (backend *fakeBackend) ListRecent(context.Context, string, uint32) ([]model.Message, error) {
	return append([]model.Message(nil), backend.messages...), nil
}

func (backend *fakeBackend) FindContacts(string) ([]model.Contact, error) {
	return append([]model.Contact(nil), backend.contacts...), nil
}

func (backend *fakeBackend) ListContacts(uint32, uint32) ([]model.Contact, error) {
	return append([]model.Contact(nil), backend.contacts...), nil
}

func (backend *fakeBackend) SyncContacts(context.Context) (uint32, error) {
	return uint32(len(backend.contacts)), nil
}

func (backend *fakeBackend) SetSignals(func(uint64), func()) {}

func TestServiceExportsOnlyReadMethodsWithStableSignatures(t *testing.T) {
	t.Parallel()

	service := NewService(&fakeBackend{}, UIDResolver(sameUID))
	methods := introspect.Methods(service)
	names := make([]string, 0, len(methods))
	signatures := make(map[string]string)
	for _, method := range methods {
		names = append(names, method.Name)
		var signature strings.Builder
		for _, argument := range method.Args {
			signature.WriteString(argument.Direction)
			signature.WriteByte(':')
			signature.WriteString(argument.Type)
			signature.WriteByte(' ')
		}
		signatures[method.Name] = strings.TrimSpace(signature.String())
	}
	wantNames := []string{
		"FindContacts", "GetStatus", "IsHealthy", "ListContacts",
		"ListEvents", "ListRecent", "SyncContacts",
	}
	if !slices.Equal(names, wantNames) {
		t.Fatalf("methods %q", names)
	}
	wantSignatures := map[string]string{
		"GetStatus":    "out:s",
		"IsHealthy":    "out:b",
		"ListEvents":   "in:as in:u out:s",
		"ListRecent":   "in:s in:u out:s",
		"FindContacts": "in:s out:s",
		"ListContacts": "in:u in:u out:s",
		"SyncContacts": "out:u",
	}
	for name, want := range wantSignatures {
		if signatures[name] != want {
			t.Fatalf("%s signature %q", name, signatures[name])
		}
	}
}

func TestServiceRejectsDifferentUserID(t *testing.T) {
	t.Parallel()

	service := NewService(&fakeBackend{}, UIDResolver(func(context.Context, dbus.Sender) (uint32, error) {
		return uint32(os.Getuid() + 1), nil
	}))
	_, dbusErr := service.GetStatus(dbus.Sender(":1.99"))
	if dbusErr == nil || dbusErr.Name != protocol.ErrorPrefix+".AccessDenied" {
		t.Fatalf("D-Bus error %#v", dbusErr)
	}
}

func TestServiceRejectsOversizedResponseWithoutPrivateContent(t *testing.T) {
	t.Parallel()

	private := "private-value" + strings.Repeat("p", protocol.MaxDBusJSONBytes)
	service := NewService(&fakeBackend{messages: []model.Message{{Body: private}}}, UIDResolver(sameUID))
	_, dbusErr := service.ListEvents(dbus.Sender(":1.1"), nil, 1)
	if dbusErr == nil || dbusErr.Name != protocol.ErrorPrefix+".ResponseTooLarge" {
		t.Fatalf("D-Bus error %#v", dbusErr)
	}
	for _, value := range dbusErr.Body {
		if strings.Contains(value.(string), "private-value") {
			t.Fatal("private response content appeared in the error")
		}
	}
}

func sameUID(context.Context, dbus.Sender) (uint32, error) {
	return uint32(os.Getuid()), nil
}
