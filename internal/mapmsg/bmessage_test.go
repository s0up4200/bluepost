package mapmsg

import (
	"strings"
	"testing"
)

const validBMessage = "BEGIN:BMSG\r\n" +
	"VERSION:1.0\r\n" +
	"STATUS:UNREAD\r\n" +
	"TYPE:SMS_GSM\r\n" +
	"FOLDER:telecom/msg/inbox\r\n" +
	"BEGIN:VCARD\r\n" +
	"VERSION:3.0\r\n" +
	"FN:Jane Doe\r\n" +
	"TEL:+47 123 45 678\r\n" +
	"EMAIL:jane@example.com\r\n" +
	"END:VCARD\r\n" +
	"BEGIN:BENV\r\n" +
	"BEGIN:BBODY\r\n" +
	"CHARSET:UTF-8\r\n" +
	"BEGIN:MSG\r\n" +
	"hello\r\n" +
	" END:MSG\r\n" +
	"END:MSG\r\n" +
	"END:BBODY\r\n" +
	"END:BENV\r\n" +
	"END:BMSG\r\n"

func TestParseReadsOriginatorAndUnstuffsBody(t *testing.T) {
	t.Parallel()

	got, err := Parse(strings.NewReader(validBMessage), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sender != "+47 123 45 678" {
		t.Fatalf("sender %q", got.Sender)
	}
	if got.SenderName != "Jane Doe" {
		t.Fatalf("sender name %q", got.SenderName)
	}
	if got.Body != "hello\nEND:MSG" {
		t.Fatalf("body %q", got.Body)
	}
	if got.Status != "UNREAD" || got.Type != "SMS_GSM" || got.Folder != "telecom/msg/inbox" {
		t.Fatalf("metadata %#v", got)
	}
}

func TestParseIgnoresRecipientAndBodyVCards(t *testing.T) {
	t.Parallel()

	fixture := "BEGIN:BMSG\nBEGIN:BENV\nBEGIN:VCARD\nTEL:+4799999999\nEND:VCARD\n" +
		"BEGIN:BBODY\nBEGIN:MSG\nBEGIN:VCARD\nTEL:+4788888888\nEND:VCARD\nEND:MSG\n" +
		"END:BBODY\nEND:BENV\nEND:BMSG\n"
	got, err := Parse(strings.NewReader(fixture), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sender != "" {
		t.Fatalf("accepted non-originator sender %q", got.Sender)
	}
}

func TestParsePrefersOriginatorPhoneWhenEmailComesFirst(t *testing.T) {
	t.Parallel()

	fixture := strings.Replace(
		validBMessage,
		"TEL:+47 123 45 678\r\nEMAIL:jane@example.com",
		"EMAIL:jane@example.com\r\nTEL:+47 123 45 678",
		1,
	)
	got, err := Parse(strings.NewReader(fixture), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sender != "+47 123 45 678" {
		t.Fatalf("sender %q", got.Sender)
	}
}

func TestParseRejectsMissingMessageTerminator(t *testing.T) {
	t.Parallel()

	fixture := "BEGIN:BMSG\nBEGIN:BENV\nBEGIN:BBODY\nBEGIN:MSG\nhello\nEND:BBODY\nEND:BENV\nEND:BMSG\n"
	if _, err := Parse(strings.NewReader(fixture), 1<<20); err == nil {
		t.Fatal("expected missing END:MSG error")
	}
}

func TestParseRejectsMessageTerminatorOutsideBody(t *testing.T) {
	t.Parallel()

	fixture := "BEGIN:BMSG\nBEGIN:BENV\nBEGIN:BBODY\nBEGIN:MSG\nhello\nEND:BBODY\nEND:MSG\nEND:BENV\nEND:BMSG\n"
	if _, err := Parse(strings.NewReader(fixture), 1<<20); err == nil {
		t.Fatal("expected out-of-order END:MSG error")
	}
}

func TestParseRejectsOversizedInput(t *testing.T) {
	t.Parallel()

	if _, err := Parse(strings.NewReader(validBMessage), 32); err == nil {
		t.Fatal("expected size limit error")
	}
}
