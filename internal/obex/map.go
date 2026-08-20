package obex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/godbus/dbus/v5"

	"github.com/s0up4200/bluepost/internal/mapmsg"
	"github.com/s0up4200/bluepost/internal/model"
	"github.com/s0up4200/bluepost/internal/pbap"
	"github.com/s0up4200/bluepost/internal/protocol"
)

const (
	messageInterface          = "org.bluez.obex.Message1"
	messageGet                = messageInterface + ".Get"
	messageAccessInterface    = "org.bluez.obex.MessageAccess1"
	messageAccessSetFolder    = messageAccessInterface + ".SetFolder"
	messageAccessListMessages = messageAccessInterface + ".ListMessages"
	propertiesGetAll          = "org.freedesktop.DBus.Properties.GetAll"
)

var ErrForeignMessage = errors.New("MAP message belongs to another session")

type MAP struct {
	transport  Transport
	sessions   *Sessions
	worker     *Worker
	runtimeDir string
	resolve    func(string) string
}

func NewMAP(
	transport Transport,
	sessions *Sessions,
	worker *Worker,
	runtimeDir string,
	resolve func(string) string,
) *MAP {
	return &MAP{
		transport:  transport,
		sessions:   sessions,
		worker:     worker,
		runtimeDir: runtimeDir,
		resolve:    resolve,
	}
}

func (client *MAP) HandleAdded(
	ctx context.Context,
	path dbus.ObjectPath,
	interfaces map[string]map[string]dbus.Variant,
) (model.Message, error) {
	mapPath, ok := client.sessions.MapPath()
	if !ok || !strings.HasPrefix(string(path), string(mapPath)+"/") {
		return model.Message{}, ErrForeignMessage
	}
	properties, ok := interfaces[messageInterface]
	if !ok {
		return model.Message{}, errors.New("object does not provide the MAP message interface")
	}
	temporary, err := privateTemp(client.runtimeDir, "message-*.bmsg")
	if err != nil {
		return model.Message{}, err
	}
	defer os.Remove(temporary)

	body, err := client.transport.Call(
		ctx,
		obexDestination,
		path,
		messageGet,
		temporary,
		false,
	)
	if err != nil {
		return model.Message{}, fmt.Errorf("fetch MAP message: %w", err)
	}
	transferPath, status, err := transferResult(body)
	if err != nil {
		return model.Message{}, err
	}
	if err := waitTransfer(ctx, client.transport, transferPath, status, temporary, protocol.MaxBMessageBytes); err != nil {
		return model.Message{}, err
	}
	if err := waitForFile(ctx, temporary, protocol.MaxBMessageBytes); err != nil {
		return model.Message{}, err
	}
	file, err := os.Open(temporary)
	if err != nil {
		return model.Message{}, err
	}
	parsed, parseErr := mapmsg.Parse(file, protocol.MaxBMessageBytes)
	closeErr := file.Close()
	if parseErr != nil {
		return model.Message{}, parseErr
	}
	if closeErr != nil {
		return model.Message{}, closeErr
	}

	normalized, kind := pbap.NormalizeAddress(parsed.Sender)
	if kind != pbap.PhoneAddress {
		normalized = ""
	}
	contactName := ""
	if client.resolve != nil {
		contactName = client.resolve(parsed.Sender)
	}
	read := strings.EqualFold(parsed.Status, "READ")
	if value, exists := properties["Read"]; exists {
		if propertyRead, valid := value.Value().(bool); valid {
			read = propertyRead
		}
	}
	return model.Message{
		Kind:            "sms_received",
		Handle:          boundedText(filepath.Base(string(path)), protocol.MaxContactAddressChars),
		SenderAddress:   boundedText(parsed.Sender, protocol.MaxContactAddressChars),
		SenderPhoneNorm: normalized,
		ContactName:     boundedText(contactName, protocol.MaxContactNameChars),
		Body:            parsed.Body,
		Timestamp:       parseMAPTime(variantString(properties["Timestamp"])),
		Read:            read,
	}, nil
}

func (client *MAP) Watch(ctx context.Context, onMessage func(model.Message) error) error {
	signals, cancel, err := client.transport.Subscribe(
		ctx,
		dbus.WithMatchInterface(objectManagerInterface),
		dbus.WithMatchMember("InterfacesAdded"),
		dbus.WithMatchSender(obexDestination),
	)
	if err != nil {
		return err
	}
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case signal, open := <-signals:
			if !open {
				return errors.New("MAP signal subscription closed")
			}
			if signal == nil || signal.Name != objectManagerInterface+".InterfacesAdded" || len(signal.Body) != 2 {
				continue
			}
			path, pathOK := signal.Body[0].(dbus.ObjectPath)
			interfaces, interfacesOK := signal.Body[1].(map[string]map[string]dbus.Variant)
			if !pathOK || !interfacesOK {
				continue
			}
			operationCtx, stop := context.WithTimeout(ctx, 120*time.Second)
			var message model.Message
			operation := func(callCtx context.Context) error {
				var callErr error
				message, callErr = client.HandleAdded(callCtx, path, interfaces)
				return callErr
			}
			if client.worker != nil {
				err = client.worker.Submit(operationCtx, operation)
			} else {
				err = operation(operationCtx)
			}
			stop()
			if err != nil {
				continue
			}
			if onMessage != nil {
				if err := onMessage(message); err != nil {
					return err
				}
			}
		}
	}
}

func (client *MAP) ListRecent(
	ctx context.Context,
	folder string,
	limit uint32,
) ([]model.Message, error) {
	folder = strings.ToLower(strings.TrimSpace(folder))
	if folder != "telecom/msg/inbox" && folder != "telecom/msg/sent" {
		return nil, errors.New("MAP folder is not allowed")
	}
	if limit == 0 || limit > protocol.MaxRecentRecords {
		return nil, errors.New("live message limit is outside the allowed range")
	}
	mapPath, ok := client.sessions.MapPath()
	if !ok {
		return nil, errors.New("MAP session is not available")
	}
	_, _ = client.transport.Call(ctx, obexDestination, mapPath, messageAccessSetFolder, "/")
	for _, segment := range strings.Split(folder, "/") {
		if _, err := client.transport.Call(ctx, obexDestination, mapPath, messageAccessSetFolder, segment); err != nil {
			return nil, fmt.Errorf("select MAP folder: %w", err)
		}
	}
	body, err := client.transport.Call(
		ctx,
		obexDestination,
		mapPath,
		messageAccessListMessages,
		"",
		map[string]dbus.Variant{"MaxListCount": dbus.MakeVariant(uint16(limit))},
	)
	if err != nil {
		return nil, fmt.Errorf("list MAP messages: %w", err)
	}
	if len(body) == 0 {
		return nil, errors.New("MAP list returned an invalid response")
	}
	paths, ok := body[0].([]dbus.ObjectPath)
	if !ok {
		return nil, errors.New("MAP list returned invalid paths")
	}
	if len(paths) > int(limit) {
		paths = paths[:limit]
	}
	messages := make([]model.Message, 0, len(paths))
	for _, path := range paths {
		propertiesBody, err := client.transport.Call(
			ctx,
			obexDestination,
			path,
			propertiesGetAll,
			messageInterface,
		)
		if err != nil || len(propertiesBody) != 1 {
			continue
		}
		properties, ok := propertiesBody[0].(map[string]dbus.Variant)
		if !ok {
			continue
		}
		sender := variantString(properties["Sender"])
		if sender == "" {
			sender = variantString(properties["SenderAddress"])
		}
		normalized, kind := pbap.NormalizeAddress(sender)
		if kind != pbap.PhoneAddress {
			normalized = ""
		}
		contactName := ""
		if client.resolve != nil {
			contactName = client.resolve(sender)
		}
		read, _ := properties["Read"].Value().(bool)
		messages = append(messages, model.Message{
			Kind:            "sms_received",
			Handle:          boundedText(filepath.Base(string(path)), protocol.MaxContactAddressChars),
			SenderAddress:   boundedText(sender, protocol.MaxContactAddressChars),
			SenderPhoneNorm: normalized,
			ContactName:     boundedText(contactName, protocol.MaxContactNameChars),
			Body:            boundedText(variantString(properties["Subject"]), protocol.MaxPublicBodyChars),
			Timestamp:       parseMAPTime(variantString(properties["Timestamp"])),
			Read:            read,
		})
	}
	return messages, nil
}

func parseMAPTime(value string) time.Time {
	for _, format := range []string{"20060102T150405-0700", "20060102T150405Z0700"} {
		if parsed, err := time.Parse(format, value); err == nil {
			return parsed
		}
	}
	if parsed, err := time.ParseInLocation("20060102T150405", value, time.Local); err == nil {
		return parsed
	}
	return time.Time{}
}

func boundedText(value string, maximum int) string {
	if utf8.RuneCountInString(value) <= maximum {
		return value
	}
	return string([]rune(value)[:maximum])
}
