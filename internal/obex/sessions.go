package obex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"
)

const (
	bluezDestination               = "org.bluez"
	bluezDeviceInterface           = "org.bluez.Device1"
	obexDestination                = "org.bluez.obex"
	objectManagerInterface         = "org.freedesktop.DBus.ObjectManager"
	objectManagerGetManagedObjects = objectManagerInterface + ".GetManagedObjects"
	clientCreateSession            = "org.bluez.obex.Client1.CreateSession"
	clientRemoveSession            = "org.bluez.obex.Client1.RemoveSession"
	obexSessionInterface           = "org.bluez.obex.Session1"
	dbusInterface                  = "org.freedesktop.DBus"
)

var (
	bluezRoot = dbus.ObjectPath("/")
	obexRoot  = dbus.ObjectPath("/org/bluez/obex")
)

type managedObjects map[dbus.ObjectPath]map[string]map[string]dbus.Variant

type Sessions struct {
	mu sync.RWMutex

	system   Transport
	obex     Transport
	phone    string
	mapPath  dbus.ObjectPath
	pbapPath dbus.ObjectPath
}

func NewSessions(system, obex Transport) *Sessions {
	return &Sessions{system: system, obex: obex}
}

func (sessions *Sessions) Open(ctx context.Context, phone string) error {
	if sessions.system == nil || sessions.obex == nil {
		return errors.New("system and OBEX transports are required")
	}
	if err := sessions.validateDevice(ctx, phone); err != nil {
		return err
	}
	sessions.mu.RLock()
	ready := sessions.mapPath != "" && sessions.pbapPath != ""
	sessions.mu.RUnlock()
	if ready {
		return nil
	}
	if err := sessions.close(ctx, true); err != nil {
		return err
	}
	if err := sessions.removeStale(ctx, phone, map[string]bool{"MAP": true, "PBAP": true}); err != nil {
		return err
	}

	mapPath, err := sessions.create(ctx, phone, "MAP", true)
	if err != nil {
		return err
	}
	pbapPath, err := sessions.create(ctx, phone, "PBAP", true)
	if err != nil {
		_ = sessions.remove(ctx, mapPath)
		return err
	}
	sessions.mu.Lock()
	sessions.phone = strings.ToUpper(phone)
	sessions.mapPath = mapPath
	sessions.pbapPath = pbapPath
	sessions.mu.Unlock()
	return nil
}

func (sessions *Sessions) Close(ctx context.Context) error {
	return sessions.close(ctx, true)
}

func (sessions *Sessions) RefreshMAP(ctx context.Context) error {
	sessions.mu.Lock()
	path := sessions.mapPath
	phone := sessions.phone
	if phone == "" {
		sessions.mu.Unlock()
		return errors.New("MAP session is not available")
	}
	sessions.mapPath = ""
	sessions.mu.Unlock()

	if path != "" {
		if err := sessions.remove(ctx, path); err != nil {
			return err
		}
	}
	path, err := sessions.create(ctx, phone, "MAP", true)
	if err != nil {
		return err
	}
	sessions.mu.Lock()
	sessions.mapPath = path
	sessions.mu.Unlock()
	return nil
}

func (sessions *Sessions) close(ctx context.Context, removeRemote bool) error {
	sessions.mu.Lock()
	paths := []dbus.ObjectPath{sessions.mapPath, sessions.pbapPath}
	sessions.mapPath = ""
	sessions.pbapPath = ""
	sessions.mu.Unlock()
	if !removeRemote {
		return nil
	}
	var result error
	for _, path := range paths {
		if path == "" {
			continue
		}
		if err := sessions.remove(ctx, path); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (sessions *Sessions) MarkLost(path dbus.ObjectPath) bool {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	lost := false
	if sessions.mapPath == path {
		sessions.mapPath = ""
		lost = true
	}
	if sessions.pbapPath == path {
		sessions.pbapPath = ""
		lost = true
	}
	return lost
}

func (sessions *Sessions) MarkServiceLost() bool {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	lost := sessions.mapPath != "" || sessions.pbapPath != ""
	sessions.mapPath = ""
	sessions.pbapPath = ""
	return lost
}

func (sessions *Sessions) MapPath() (dbus.ObjectPath, bool) {
	sessions.mu.RLock()
	defer sessions.mu.RUnlock()
	return sessions.mapPath, sessions.mapPath != ""
}

func (sessions *Sessions) PBAPPath() (dbus.ObjectPath, bool) {
	sessions.mu.RLock()
	defer sessions.mu.RUnlock()
	return sessions.pbapPath, sessions.pbapPath != ""
}

func (sessions *Sessions) Monitor(ctx context.Context, onLost func(string)) error {
	removed, cancelRemoved, err := sessions.obex.Subscribe(
		ctx,
		dbus.WithMatchInterface(objectManagerInterface),
		dbus.WithMatchMember("InterfacesRemoved"),
		dbus.WithMatchSender(obexDestination),
	)
	if err != nil {
		return err
	}
	defer cancelRemoved()
	owners, cancelOwners, err := sessions.obex.Subscribe(
		ctx,
		dbus.WithMatchInterface(dbusInterface),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, obexDestination),
	)
	if err != nil {
		return err
	}
	defer cancelOwners()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case signal, open := <-removed:
			if !open {
				return errors.New("OBEX removed-session subscription closed")
			}
			if signal == nil || signal.Name != objectManagerInterface+".InterfacesRemoved" || len(signal.Body) < 1 {
				continue
			}
			path, ok := signal.Body[0].(dbus.ObjectPath)
			if ok && sessions.MarkLost(path) && onLost != nil {
				onLost("OBEX session disappeared")
			}
		case signal, open := <-owners:
			if !open {
				return errors.New("OBEX owner subscription closed")
			}
			if signal == nil || signal.Name != dbusInterface+".NameOwnerChanged" || len(signal.Body) != 3 {
				continue
			}
			name, _ := signal.Body[0].(string)
			oldOwner, _ := signal.Body[1].(string)
			newOwner, _ := signal.Body[2].(string)
			if name == obexDestination && oldOwner != "" && newOwner == "" && sessions.MarkServiceLost() && onLost != nil {
				onLost("obexd exited")
			}
		}
	}
}

func (sessions *Sessions) validateDevice(ctx context.Context, phone string) error {
	objects, err := managed(ctx, sessions.system, bluezDestination, bluezRoot)
	if err != nil {
		return fmt.Errorf("read BlueZ devices: %w", err)
	}
	phone = strings.ToUpper(phone)
	for _, interfaces := range objects {
		properties, ok := interfaces[bluezDeviceInterface]
		if !ok || strings.ToUpper(variantString(properties["Address"])) != phone {
			continue
		}
		paired, _ := properties["Paired"].Value().(bool)
		trusted, _ := properties["Trusted"].Value().(bool)
		if !paired || !trusted {
			return errors.New("configured iPhone is not paired and trusted")
		}
		return nil
	}
	return errors.New("configured iPhone is not available in BlueZ")
}

func (sessions *Sessions) removeStale(ctx context.Context, phone string, targets map[string]bool) error {
	objects, err := managed(ctx, sessions.obex, obexDestination, bluezRoot)
	if err != nil {
		return fmt.Errorf("inspect existing OBEX sessions: %w", err)
	}
	for path, interfaces := range objects {
		properties, ok := interfaces[obexSessionInterface]
		if !ok || strings.ToUpper(variantString(properties["Destination"])) != strings.ToUpper(phone) {
			continue
		}
		target := strings.ToUpper(variantString(properties["Target"]))
		if targets[target] {
			if err := sessions.remove(ctx, path); err != nil {
				return err
			}
		}
	}
	return nil
}

func (sessions *Sessions) create(
	ctx context.Context,
	phone string,
	target string,
	retryForbidden bool,
) (dbus.ObjectPath, error) {
	options := map[string]dbus.Variant{"Target": dbus.MakeVariant(target)}
	body, err := sessions.obex.Call(
		ctx,
		obexDestination,
		obexRoot,
		clientCreateSession,
		phone,
		options,
	)
	if err != nil && retryForbidden &&
		(strings.Contains(err.Error(), "Forbidden") || strings.Contains(err.Error(), "0x43")) {
		if cleanErr := sessions.removeStale(ctx, phone, map[string]bool{target: true}); cleanErr != nil {
			return "", cleanErr
		}
		return sessions.create(ctx, phone, target, false)
	}
	if err != nil {
		return "", fmt.Errorf("create %s session: %w", target, err)
	}
	if len(body) != 1 {
		return "", fmt.Errorf("create %s session returned an invalid response", target)
	}
	path, ok := body[0].(dbus.ObjectPath)
	if !ok || !path.IsValid() {
		return "", fmt.Errorf("create %s session returned an invalid path", target)
	}
	return path, nil
}

func (sessions *Sessions) remove(ctx context.Context, path dbus.ObjectPath) error {
	_, err := sessions.obex.Call(
		ctx,
		obexDestination,
		obexRoot,
		clientRemoveSession,
		path,
	)
	if err != nil {
		return fmt.Errorf("remove OBEX session: %w", err)
	}
	return nil
}

func managed(
	ctx context.Context,
	transport Transport,
	destination string,
	path dbus.ObjectPath,
) (managedObjects, error) {
	body, err := transport.Call(ctx, destination, path, objectManagerGetManagedObjects)
	if err != nil {
		return nil, err
	}
	if len(body) != 1 {
		return nil, errors.New("object manager returned an invalid response")
	}
	objects, ok := body[0].(managedObjects)
	if ok {
		return objects, nil
	}
	plain, ok := body[0].(map[dbus.ObjectPath]map[string]map[string]dbus.Variant)
	if !ok {
		return nil, errors.New("object manager returned an invalid object map")
	}
	return managedObjects(plain), nil
}

func variantString(variant dbus.Variant) string {
	value, _ := variant.Value().(string)
	return value
}
