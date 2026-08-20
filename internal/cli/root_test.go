package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/s0up4200/bluepost/internal/model"
)

type fakeAPI struct {
	status        model.Status
	messages      []model.Message
	contacts      []model.Contact
	synced        uint32
	eventCalls    int
	recentCalls   int
	contactQuery  string
	syncCalls     int
	recentFolder  string
	recentLimit   uint32
	contactsLimit uint32
}

func (api *fakeAPI) Status(context.Context) (model.Status, error) { return api.status, nil }
func (api *fakeAPI) Healthy(context.Context) (bool, error)        { return api.status.MAP, nil }

func (api *fakeAPI) Events(context.Context, []string, uint32) ([]model.Message, error) {
	api.eventCalls++
	return append([]model.Message(nil), api.messages...), nil
}

func (api *fakeAPI) Recent(_ context.Context, folder string, limit uint32) ([]model.Message, error) {
	api.recentCalls++
	api.recentFolder = folder
	api.recentLimit = limit
	return append([]model.Message(nil), api.messages...), nil
}

func (api *fakeAPI) FindContacts(_ context.Context, query string) ([]model.Contact, error) {
	api.contactQuery = query
	return append([]model.Contact(nil), api.contacts...), nil
}

func (api *fakeAPI) Contacts(_ context.Context, _ uint32, limit uint32) ([]model.Contact, error) {
	api.contactsLimit = limit
	return append([]model.Contact(nil), api.contacts...), nil
}

func (api *fakeAPI) SyncContacts(context.Context) (uint32, error) {
	api.syncCalls++
	return api.synced, nil
}

func TestStatusShowsLockedState(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{status: model.Status{State: "locked", Storage: "locked", Detail: "Encrypted storage is unavailable"}}
	output, err := execute(t, api, "status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "State: locked") || !strings.Contains(output, "Storage: locked") {
		t.Fatalf("output %q", output)
	}
}

func TestMessagesRejectsInvalidLimitBeforeDBusCall(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{}
	if _, err := execute(t, api, "messages", "--limit", "0"); err == nil {
		t.Fatal("expected limit error")
	}
	if api.eventCalls != 0 || api.recentCalls != 0 {
		t.Fatalf("D-Bus calls events=%d recent=%d", api.eventCalls, api.recentCalls)
	}
}

func TestMessagesIPhoneUsesLiveInbox(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{messages: []model.Message{{SenderAddress: "+4712345678", Body: "hello"}}}
	output, err := execute(t, api, "messages", "--iphone", "--limit", "5")
	if err != nil {
		t.Fatal(err)
	}
	if api.recentCalls != 1 || api.recentFolder != "telecom/msg/inbox" || api.recentLimit != 5 {
		t.Fatalf("recent call count=%d folder=%q limit=%d", api.recentCalls, api.recentFolder, api.recentLimit)
	}
	if !strings.Contains(output, "+4712345678") || !strings.Contains(output, "hello") {
		t.Fatalf("output %q", output)
	}
}

func TestContactsSearchAndSync(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{
		contacts: []model.Contact{{Name: "Jane", Phones: []string{"4712345678"}}},
		synced:   42,
	}
	output, err := execute(t, api, "contacts", "jane")
	if err != nil {
		t.Fatal(err)
	}
	if api.contactQuery != "jane" || !strings.Contains(output, "Jane") || !strings.Contains(output, "4712345678") {
		t.Fatalf("query %q output %q", api.contactQuery, output)
	}
	output, err = execute(t, api, "contacts", "sync")
	if err != nil {
		t.Fatal(err)
	}
	if api.syncCalls != 1 || !strings.Contains(output, "42 contacts synchronized") {
		t.Fatalf("sync calls=%d output=%q", api.syncCalls, output)
	}
}

func TestMessagesRemoveTerminalControlCharacters(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{messages: []model.Message{{
		SenderAddress: "Jane\x1b[31m",
		Body:          "hello\nworld",
	}}}
	output, err := execute(t, api, "messages")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(output, '\x1b') || !strings.Contains(output, "hello ⏎ world") {
		t.Fatalf("output %q", output)
	}
}

func TestMessagesPrintNewestTimestampFirst(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{messages: []model.Message{
		{Body: "new", Timestamp: time.Date(2026, 8, 20, 18, 48, 0, 0, time.Local)},
		{Body: "old", Timestamp: time.Date(2026, 8, 19, 9, 0, 0, 0, time.Local)},
	}}
	output, err := execute(t, api, "messages")
	if err != nil {
		t.Fatal(err)
	}
	newIndex := strings.Index(output, "new")
	oldIndex := strings.Index(output, "old")
	if newIndex < 0 || oldIndex < 0 || newIndex > oldIndex {
		t.Fatalf("output %q", output)
	}
}

func execute(t *testing.T, api *fakeAPI, args ...string) (string, error) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command := New(api, &stdout, &stderr)
	command.SetArgs(args)
	err := command.ExecuteContext(context.Background())
	return stdout.String() + stderr.String(), err
}
