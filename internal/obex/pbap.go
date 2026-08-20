package obex

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/godbus/dbus/v5"

	"github.com/s0up4200/bluepost/internal/model"
	contactparser "github.com/s0up4200/bluepost/internal/pbap"
	"github.com/s0up4200/bluepost/internal/protocol"
)

const (
	phonebookInterface = "org.bluez.obex.PhonebookAccess1"
	phonebookSelect    = phonebookInterface + ".Select"
	phonebookPullAll   = phonebookInterface + ".PullAll"
)

type PBAP struct {
	transport  Transport
	sessions   *Sessions
	runtimeDir string
}

func NewPBAP(transport Transport, sessions *Sessions, runtimeDir string) *PBAP {
	return &PBAP{transport: transport, sessions: sessions, runtimeDir: runtimeDir}
}

func (client *PBAP) Sync(ctx context.Context) ([]model.Contact, error) {
	pbapPath, ok := client.sessions.PBAPPath()
	if !ok {
		return nil, errors.New("PBAP session is not available")
	}
	if _, err := client.transport.Call(
		ctx,
		obexDestination,
		pbapPath,
		phonebookSelect,
		"int",
		"pb",
	); err != nil {
		return nil, fmt.Errorf("select PBAP phonebook: %w", err)
	}
	temporary, err := privateTemp(client.runtimeDir, "phonebook-*.vcf")
	if err != nil {
		return nil, err
	}
	defer os.Remove(temporary)
	body, err := client.transport.Call(
		ctx,
		obexDestination,
		pbapPath,
		phonebookPullAll,
		temporary,
		map[string]dbus.Variant{
			"Format":   dbus.MakeVariant("vcard30"),
			"MaxCount": dbus.MakeVariant(uint16(protocol.MaxPhonebookContacts)),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("pull PBAP phonebook: %w", err)
	}
	transferPath, status, err := transferResult(body)
	if err != nil {
		return nil, err
	}
	if err := waitTransfer(ctx, client.transport, transferPath, status, temporary, protocol.MaxPhonebookBytes); err != nil {
		return nil, err
	}
	if err := waitForFile(ctx, temporary, protocol.MaxPhonebookBytes); err != nil {
		return nil, err
	}
	file, err := os.Open(temporary)
	if err != nil {
		return nil, err
	}
	contacts, parseErr := contactparser.Parse(file, contactparser.DefaultLimits())
	closeErr := file.Close()
	if parseErr != nil {
		return nil, parseErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return contacts, nil
}
