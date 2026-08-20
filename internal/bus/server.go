package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"

	"github.com/s0up4200/bluepost/internal/model"
	"github.com/s0up4200/bluepost/internal/protocol"
	"github.com/s0up4200/bluepost/internal/storage"
)

type BackendAPI interface {
	Status() model.Status
	Healthy() bool
	ListEvents([]string, uint32) ([]model.Message, error)
	ListRecent(context.Context, string, uint32) ([]model.Message, error)
	FindContacts(string) ([]model.Contact, error)
	ListContacts(uint32, uint32) ([]model.Contact, error)
	SyncContacts(context.Context) (uint32, error)
	SetSignals(func(uint64), func())
}

type UIDResolver func(context.Context, dbus.Sender) (uint32, error)

type Service struct {
	backend BackendAPI
	uid     UIDResolver
}

func NewService(backend BackendAPI, resolver UIDResolver) *Service {
	return &Service{backend: backend, uid: resolver}
}

func (service *Service) GetStatus(sender dbus.Sender) (string, *dbus.Error) {
	if err := service.authorize(sender); err != nil {
		return "", err
	}
	return service.jsonResponse(service.backend.Status())
}

func (service *Service) IsHealthy(sender dbus.Sender) (bool, *dbus.Error) {
	if err := service.authorize(sender); err != nil {
		return false, err
	}
	return service.backend.Healthy(), nil
}

func (service *Service) ListEvents(
	sender dbus.Sender,
	kinds []string,
	limit uint32,
) (string, *dbus.Error) {
	if err := service.authorize(sender); err != nil {
		return "", err
	}
	records, err := service.backend.ListEvents(kinds, limit)
	if err != nil {
		return "", backendError(err)
	}
	return service.jsonResponse(records)
}

func (service *Service) ListRecent(
	sender dbus.Sender,
	folder string,
	limit uint32,
) (string, *dbus.Error) {
	if err := service.authorize(sender); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	records, err := service.backend.ListRecent(ctx, folder, limit)
	cancel()
	if err != nil {
		return "", backendError(err)
	}
	return service.jsonResponse(records)
}

func (service *Service) FindContacts(sender dbus.Sender, query string) (string, *dbus.Error) {
	if err := service.authorize(sender); err != nil {
		return "", err
	}
	records, err := service.backend.FindContacts(query)
	if err != nil {
		return "", backendError(err)
	}
	return service.jsonResponse(records)
}

func (service *Service) ListContacts(
	sender dbus.Sender,
	offset uint32,
	limit uint32,
) (string, *dbus.Error) {
	if err := service.authorize(sender); err != nil {
		return "", err
	}
	records, err := service.backend.ListContacts(offset, limit)
	if err != nil {
		return "", backendError(err)
	}
	return service.jsonResponse(records)
}

func (service *Service) SyncContacts(sender dbus.Sender) (uint32, *dbus.Error) {
	if err := service.authorize(sender); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	count, err := service.backend.SyncContacts(ctx)
	cancel()
	if err != nil {
		return 0, backendError(err)
	}
	return count, nil
}

func (service *Service) authorize(sender dbus.Sender) *dbus.Error {
	if service.backend == nil || service.uid == nil || sender == "" {
		return namedError("AccessDenied", "The caller is not authorized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	uid, err := service.uid(ctx, sender)
	cancel()
	if err != nil || uid != uint32(os.Getuid()) {
		return namedError("AccessDenied", "The caller is not authorized")
	}
	return nil
}

func (service *Service) jsonResponse(value any) (string, *dbus.Error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", namedError("Failed", "The daemon could not encode the response")
	}
	if len(encoded) > protocol.MaxDBusJSONBytes {
		return "", namedError("ResponseTooLarge", "The response exceeds the D-Bus size limit")
	}
	return string(encoded), nil
}

func backendError(err error) *dbus.Error {
	if errors.Is(err, storage.ErrLocked) {
		return namedError("Locked", "Encrypted storage is unavailable")
	}
	return namedError("Failed", "The requested operation failed")
}

func namedError(suffix, message string) *dbus.Error {
	return dbus.NewError(protocol.ErrorPrefix+"."+suffix, []any{message})
}

func Serve(ctx context.Context, connection *dbus.Conn, backend BackendAPI) error {
	if connection == nil || backend == nil {
		return errors.New("D-Bus connection and backend are required")
	}
	reply, err := connection.RequestName(protocol.BusName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return fmt.Errorf("claim Bluepost D-Bus name: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return errors.New("another Bluepost daemon owns the D-Bus name")
	}
	path := dbus.ObjectPath(protocol.ObjectPath)
	resolver := UIDResolver(func(callCtx context.Context, sender dbus.Sender) (uint32, error) {
		var uid uint32
		err := connection.BusObject().CallWithContext(
			callCtx,
			"org.freedesktop.DBus.GetConnectionUnixUser",
			0,
			string(sender),
		).Store(&uid)
		return uid, err
	})
	service := NewService(backend, resolver)
	if err := connection.Export(service, path, protocol.MessagesIface); err != nil {
		_, _ = connection.ReleaseName(protocol.BusName)
		return fmt.Errorf("export Bluepost D-Bus service: %w", err)
	}
	node := &introspect.Node{
		Name: protocol.ObjectPath,
		Interfaces: []introspect.Interface{
			{Name: protocol.MessagesIface, Methods: introspect.Methods(service)},
			{
				Name: protocol.EventsIface,
				Signals: []introspect.Signal{
					{Name: "HistoryChanged", Args: []introspect.Arg{{Name: "properties", Type: "a{sv}"}}},
					{Name: "StatusChanged"},
				},
			},
		},
	}
	if err := connection.Export(
		introspect.NewIntrospectable(node),
		path,
		"org.freedesktop.DBus.Introspectable",
	); err != nil {
		_ = connection.Export(nil, path, protocol.MessagesIface)
		_, _ = connection.ReleaseName(protocol.BusName)
		return fmt.Errorf("export Bluepost introspection: %w", err)
	}
	backend.SetSignals(
		func(revision uint64) {
			_ = connection.Emit(
				path,
				protocol.EventsIface+".HistoryChanged",
				map[string]dbus.Variant{"revision": dbus.MakeVariant(revision)},
			)
		},
		func() {
			_ = connection.Emit(path, protocol.EventsIface+".StatusChanged")
		},
	)
	<-ctx.Done()
	backend.SetSignals(nil, nil)
	_ = connection.Export(nil, path, protocol.MessagesIface)
	_ = connection.Export(nil, path, "org.freedesktop.DBus.Introspectable")
	_, _ = connection.ReleaseName(protocol.BusName)
	return ctx.Err()
}
