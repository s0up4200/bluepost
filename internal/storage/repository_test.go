package storage

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/s0up4200/bluepost/internal/model"
)

func TestRepositoryReopensMessagesAndContacts(t *testing.T) {
	t.Parallel()

	snapshot := testSnapshot(t, testKey(0x91))
	repository := NewRepository(snapshot)
	if err := repository.Open(); err != nil {
		t.Fatal(err)
	}
	message := model.Message{
		Handle:        "0001",
		SenderAddress: "+4712345678",
		Body:          "private",
		Timestamp:     time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC),
	}
	contacts := []model.Contact{{Name: "Jane", Phones: []string{"4712345678"}}}
	if _, err := repository.AppendMessage(message); err != nil {
		t.Fatal(err)
	}
	if err := repository.ReplaceContacts(contacts); err != nil {
		t.Fatal(err)
	}

	reopened := NewRepository(snapshot)
	if err := reopened.Open(); err != nil {
		t.Fatal(err)
	}
	if got := reopened.Messages(20); len(got) != 1 || got[0].Body != "private" {
		t.Fatalf("messages %#v", got)
	}
	if got := reopened.Contacts(0, 20); len(got) != 1 || got[0].Name != "Jane" {
		t.Fatalf("contacts %#v", got)
	}
}

func TestRepositoryRemovesOldestMessageAtRecordLimit(t *testing.T) {
	t.Parallel()

	repository := NewRepository(testSnapshot(t, testKey(0x92)))
	repository.maxHistoryRecords = 2
	if err := repository.Open(); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"one", "two", "three"} {
		if _, err := repository.AppendMessage(model.Message{Handle: body, Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	got := repository.Messages(20)
	if len(got) != 2 || got[0].Body != "two" || got[1].Body != "three" {
		t.Fatalf("messages %#v", got)
	}
}

func TestRepositoryReplacesReplayedMessageHandle(t *testing.T) {
	t.Parallel()

	repository := NewRepository(testSnapshot(t, testKey(0x96)))
	if err := repository.Open(); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.AppendMessage(model.Message{Handle: "before", Body: "before"}); err != nil {
		t.Fatal(err)
	}
	created, err := repository.AppendMessage(model.Message{Handle: "message7", Body: "old"})
	if err != nil || !created {
		t.Fatalf("first append = %v, %v", created, err)
	}
	if _, err := repository.AppendMessage(model.Message{Handle: "after", Body: "after"}); err != nil {
		t.Fatal(err)
	}
	created, err = repository.AppendMessage(model.Message{Handle: "message7", Body: "new"})
	if err != nil || created {
		t.Fatalf("replayed append = %v, %v", created, err)
	}
	got := repository.Messages(20)
	if len(got) != 3 || got[0].Handle != "before" || got[1].Body != "new" || got[2].Handle != "after" {
		t.Fatalf("messages %#v", got)
	}
}

func TestRepositoryKeepsMessagesWithEmptyHandlesDistinct(t *testing.T) {
	t.Parallel()

	repository := NewRepository(testSnapshot(t, testKey(0x97)))
	if err := repository.Open(); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"one", "two"} {
		created, err := repository.AppendMessage(model.Message{Body: body})
		if err != nil || !created {
			t.Fatalf("append = %v, %v", created, err)
		}
	}
	if got := repository.Messages(20); len(got) != 2 {
		t.Fatalf("messages %#v", got)
	}
}

func TestRepositoryKeepsReplayedMessageAfterFailedReplacement(t *testing.T) {
	snapshot := testSnapshot(t, testKey(0x98))
	repository := NewRepository(snapshot)
	if err := repository.Open(); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.AppendMessage(model.Message{Handle: "message7", Body: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(snapshot.Dir, 0o500); err != nil {
		t.Fatal(err)
	}
	created, err := repository.AppendMessage(model.Message{Handle: "message7", Body: "new"})
	if err == nil {
		t.Fatal("expected replacement error")
	}
	if created {
		t.Fatal("failed replacement reported a new message")
	}
	if err := os.Chmod(snapshot.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := repository.Messages(20); len(got) != 1 || got[0].Body != "old" {
		t.Fatalf("messages %#v", got)
	}
	reopened := NewRepository(snapshot)
	if err := reopened.Open(); err != nil {
		t.Fatal(err)
	}
	if got := reopened.Messages(20); len(got) != 1 || got[0].Body != "old" {
		t.Fatalf("reopened messages %#v", got)
	}
}

func TestRepositoryKeepsOldestMessageWhenLargerReplacementExceedsLimit(t *testing.T) {
	t.Parallel()

	snapshot := testSnapshot(t, testKey(0x99))
	repository := NewRepository(snapshot)
	if err := repository.Open(); err != nil {
		t.Fatal(err)
	}
	for _, message := range []model.Message{
		{Handle: "oldest", Body: "old"},
		{Handle: "newer", Body: "new"},
	} {
		if _, err := repository.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	repository.maxHistoryBytes = historyJSONSize(repository.messageSizes)
	created, err := repository.AppendMessage(model.Message{
		Handle: "oldest",
		Body:   strings.Repeat("larger", 20),
	})
	if err == nil || created {
		t.Fatalf("replacement = %v, %v", created, err)
	}
	if got := repository.Messages(20); len(got) != 2 || got[0].Body != "old" || got[1].Body != "new" {
		t.Fatalf("messages %#v", got)
	}
	reopened := NewRepository(snapshot)
	if err := reopened.Open(); err != nil {
		t.Fatal(err)
	}
	if got := reopened.Messages(20); len(got) != 2 || got[0].Body != "old" || got[1].Body != "new" {
		t.Fatalf("reopened messages %#v", got)
	}
}

func TestRepositoryRemovesOldestMessageAtByteLimit(t *testing.T) {
	t.Parallel()

	repository := NewRepository(testSnapshot(t, testKey(0x93)))
	repository.maxHistoryBytes = 360
	if err := repository.Open(); err != nil {
		t.Fatal(err)
	}
	for _, handle := range []string{"one", "two", "three"} {
		if _, err := repository.AppendMessage(model.Message{
			Handle: handle,
			Body:   strings.Repeat(handle, 20),
		}); err != nil {
			t.Fatal(err)
		}
	}
	got := repository.Messages(20)
	if len(got) == 0 || got[len(got)-1].Handle != "three" {
		t.Fatalf("messages %#v", got)
	}
	if got[0].Handle == "one" {
		t.Fatalf("oldest message was not removed: %#v", got)
	}
}

func TestRepositoryKeepsContactsAfterFailedReplacement(t *testing.T) {
	snapshot := testSnapshot(t, testKey(0x94))
	repository := NewRepository(snapshot)
	if err := repository.Open(); err != nil {
		t.Fatal(err)
	}
	old := []model.Contact{{Name: "Old", Phones: []string{"4711111111"}}}
	if err := repository.ReplaceContacts(old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(snapshot.Dir, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := repository.ReplaceContacts([]model.Contact{{Name: "New"}}); err == nil {
		t.Fatal("expected replacement error")
	}
	if err := os.Chmod(snapshot.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	got := repository.Contacts(0, 20)
	if len(got) != 1 || got[0].Name != "Old" {
		t.Fatalf("contacts %#v", got)
	}
}

func TestRepositoryFindsAddressesAndKeepsAmbiguousNames(t *testing.T) {
	t.Parallel()

	repository := NewRepository(testSnapshot(t, testKey(0x95)))
	if err := repository.Open(); err != nil {
		t.Fatal(err)
	}
	contacts := []model.Contact{
		{Name: "Alex", Phones: []string{"4711111111"}},
		{Name: "Alex", Emails: []string{"alex@example.com"}},
		{Name: "Jane", Emails: []string{"jane@example.com"}},
	}
	if err := repository.ReplaceContacts(contacts); err != nil {
		t.Fatal(err)
	}
	if got := repository.FindContacts("alex", 10); len(got) != 2 {
		t.Fatalf("ambiguous contacts %#v", got)
	}
	got := repository.FindContacts("JANE@EXAMPLE.COM", 10)
	if len(got) != 1 || !slices.Equal(got[0].Emails, []string{"jane@example.com"}) {
		t.Fatalf("email contacts %#v", got)
	}
}
