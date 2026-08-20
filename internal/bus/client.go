package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/godbus/dbus/v5"

	"github.com/s0up4200/bluepost/internal/model"
	"github.com/s0up4200/bluepost/internal/protocol"
)

const (
	methodGetStatus    = protocol.MessagesIface + ".GetStatus"
	methodIsHealthy    = protocol.MessagesIface + ".IsHealthy"
	methodListEvents   = protocol.MessagesIface + ".ListEvents"
	methodListRecent   = protocol.MessagesIface + ".ListRecent"
	methodFindContacts = protocol.MessagesIface + ".FindContacts"
	methodListContacts = protocol.MessagesIface + ".ListContacts"
	methodSyncContacts = protocol.MessagesIface + ".SyncContacts"
)

type API interface {
	Status(context.Context) (model.Status, error)
	Healthy(context.Context) (bool, error)
	Events(context.Context, []string, uint32) ([]model.Message, error)
	Recent(context.Context, string, uint32) ([]model.Message, error)
	FindContacts(context.Context, string) ([]model.Contact, error)
	Contacts(context.Context, uint32, uint32) ([]model.Contact, error)
	SyncContacts(context.Context) (uint32, error)
}

type callFunc func(context.Context, string, ...any) ([]any, error)

type Client struct {
	call callFunc
}

func NewClient(connection *dbus.Conn) *Client {
	client := &Client{}
	client.SetConnection(connection)
	return client
}

func (client *Client) SetConnection(connection *dbus.Conn) {
	if connection == nil {
		client.call = nil
		return
	}
	object := connection.Object(protocol.BusName, dbus.ObjectPath(protocol.ObjectPath))
	client.call = func(ctx context.Context, method string, args ...any) ([]any, error) {
		call := object.CallWithContext(ctx, method, 0, args...)
		if call.Err != nil {
			return nil, call.Err
		}
		return call.Body, nil
	}
}

func newClient(call callFunc) *Client {
	return &Client{call: call}
}

func (client *Client) Status(ctx context.Context) (model.Status, error) {
	var status model.Status
	err := client.callJSON(ctx, methodGetStatus, &status)
	return status, err
}

func (client *Client) Healthy(ctx context.Context) (bool, error) {
	body, err := client.invoke(ctx, methodIsHealthy)
	if err != nil {
		return false, err
	}
	value, ok := body[0].(bool)
	if !ok {
		return false, errors.New("D-Bus health response has an invalid type")
	}
	return value, nil
}

func (client *Client) Events(
	ctx context.Context,
	kinds []string,
	limit uint32,
) ([]model.Message, error) {
	var messages []model.Message
	err := client.callJSON(ctx, methodListEvents, &messages, kinds, limit)
	return messages, err
}

func (client *Client) Recent(
	ctx context.Context,
	folder string,
	limit uint32,
) ([]model.Message, error) {
	var messages []model.Message
	err := client.callJSON(ctx, methodListRecent, &messages, folder, limit)
	return messages, err
}

func (client *Client) FindContacts(ctx context.Context, query string) ([]model.Contact, error) {
	var contacts []model.Contact
	err := client.callJSON(ctx, methodFindContacts, &contacts, query)
	return contacts, err
}

func (client *Client) Contacts(
	ctx context.Context,
	offset uint32,
	limit uint32,
) ([]model.Contact, error) {
	var contacts []model.Contact
	err := client.callJSON(ctx, methodListContacts, &contacts, offset, limit)
	return contacts, err
}

func (client *Client) SyncContacts(ctx context.Context) (uint32, error) {
	body, err := client.invoke(ctx, methodSyncContacts)
	if err != nil {
		return 0, err
	}
	value, ok := body[0].(uint32)
	if !ok {
		return 0, errors.New("D-Bus contact count has an invalid type")
	}
	return value, nil
}

func (client *Client) callJSON(
	ctx context.Context,
	method string,
	target any,
	args ...any,
) error {
	body, err := client.invoke(ctx, method, args...)
	if err != nil {
		return err
	}
	encoded, ok := body[0].(string)
	if !ok {
		return errors.New("D-Bus JSON response has an invalid type")
	}
	if len(encoded) > protocol.MaxDBusJSONBytes {
		return errors.New("D-Bus JSON response exceeds the byte limit")
	}
	if err := json.Unmarshal([]byte(encoded), target); err != nil {
		return errors.New("D-Bus JSON response is malformed")
	}
	return nil
}

func (client *Client) invoke(ctx context.Context, method string, args ...any) ([]any, error) {
	if client == nil || client.call == nil {
		return nil, errors.New("D-Bus client is not connected")
	}
	body, err := client.call(ctx, method, args...)
	if err != nil {
		return nil, fmt.Errorf("Bluepost D-Bus call failed: %w", err)
	}
	if len(body) != 1 {
		return nil, errors.New("Bluepost D-Bus call returned an invalid response")
	}
	return body, nil
}
