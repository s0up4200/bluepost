package pbap

import (
	"slices"
	"strings"
	"testing"

	"github.com/s0up4200/bluepost/internal/model"
)

func TestParseReadsAndNormalizesContact(t *testing.T) {
	t.Parallel()

	const cards = "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Jane\\, Doe\r\n" +
		"TEL;TYPE=CELL:+47 123 45 678\r\nEMAIL:jane@EXAMPLE.COM\r\nEND:VCARD\r\n"
	got, err := Parse(strings.NewReader(cards), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	want := model.Contact{
		Name:   "Jane, Doe",
		Phones: []string{"4712345678"},
		Emails: []string{"jane@example.com"},
	}
	if len(got) != 1 {
		t.Fatalf("contact count %d", len(got))
	}
	if got[0].Name != want.Name {
		t.Fatalf("name %q", got[0].Name)
	}
	if !slices.Equal(got[0].Phones, want.Phones) {
		t.Fatalf("phones %q", got[0].Phones)
	}
	if !slices.Equal(got[0].Emails, want.Emails) {
		t.Fatalf("emails %q", got[0].Emails)
	}
}

func TestParseUnfoldsLinesAndDeduplicatesAddresses(t *testing.T) {
	t.Parallel()

	const cards = "BEGIN:VCARD\nFN:Jane\n\tDoe\nTEL:+47 123 45 678\nTEL:+4712345678\nEND:VCARD\n"
	got, err := Parse(strings.NewReader(cards), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Name != "JaneDoe" {
		t.Fatalf("name %q", got[0].Name)
	}
	if !slices.Equal(got[0].Phones, []string{"4712345678"}) {
		t.Fatalf("phones %q", got[0].Phones)
	}
}

func TestParseKeepsAmbiguousNamesAsSeparateContacts(t *testing.T) {
	t.Parallel()

	const cards = "BEGIN:VCARD\nFN:Alex\nTEL:+4711111111\nEND:VCARD\n" +
		"BEGIN:VCARD\nFN:Alex\nTEL:+4722222222\nEND:VCARD\n"
	got, err := Parse(strings.NewReader(cards), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("contact count %d", len(got))
	}
}

func TestParseRejectsOversizedCard(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	limits.MaxCardBytes = 32
	fixture := "BEGIN:VCARD\nFN:" + strings.Repeat("a", 40) + "\nEND:VCARD\n"
	if _, err := Parse(strings.NewReader(fixture), limits); err == nil {
		t.Fatal("expected oversized card error")
	}
}

func TestParseCountsFoldMarkersTowardCardLimit(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	limits.MaxCardBytes = 40
	fixture := "BEGIN:VCARD\nFN:A\n" + strings.Repeat(" \n", 40) + "END:VCARD\n"
	if _, err := Parse(strings.NewReader(fixture), limits); err == nil {
		t.Fatal("expected folded card size error")
	}
}

func TestParseDiscardsInvalidAddresses(t *testing.T) {
	t.Parallel()

	const cards = "BEGIN:VCARD\nFN:Unsafe\nTEL:call-me\nEMAIL:Jane Doe <jane@example.com>\nEND:VCARD\n"
	got, err := Parse(strings.NewReader(cards), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Phones) != 0 || len(got[0].Emails) != 0 {
		t.Fatalf("contacts %#v", got)
	}
}

func TestParseDoesNotChangeAddressPropertyTypes(t *testing.T) {
	t.Parallel()

	const cards = "BEGIN:VCARD\nFN:Unsafe\nTEL:jane@example.com\nEMAIL:+4712345678\nEND:VCARD\n"
	got, err := Parse(strings.NewReader(cards), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Phones) != 0 || len(got[0].Emails) != 0 {
		t.Fatalf("contacts %#v", got)
	}
}
