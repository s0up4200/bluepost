package pbap

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/s0up4200/bluepost/internal/model"
	"github.com/s0up4200/bluepost/internal/protocol"
)

type AddressKind uint8

const (
	InvalidAddress AddressKind = iota
	PhoneAddress
	EmailAddress
)

type Limits struct {
	MaxBytes         int64
	MaxCardBytes     int
	MaxCards         int
	MaxNameChars     int
	MaxAddressChars  int
	MaxAddressesCard int
}

func DefaultLimits() Limits {
	return Limits{
		MaxBytes:         protocol.MaxPhonebookBytes,
		MaxCardBytes:     protocol.MaxVCardBytes,
		MaxCards:         protocol.MaxPhonebookContacts,
		MaxNameChars:     protocol.MaxContactNameChars,
		MaxAddressChars:  protocol.MaxContactAddressChars,
		MaxAddressesCard: protocol.MaxAddressesPerCard,
	}
}

var (
	phoneShape = regexp.MustCompile(`^[+0-9(][0-9 ()\-.+]*$`)
	emailShape = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+/=?^_` + "`" + `{|}~.-]+@[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?$`)
)

func NormalizeAddress(value string) (string, AddressKind) {
	value = strings.TrimSpace(value)
	if phoneShape.MatchString(value) {
		var digits strings.Builder
		for _, char := range value {
			if char >= '0' && char <= '9' {
				digits.WriteRune(char)
			}
		}
		if digits.Len() >= 7 {
			return digits.String(), PhoneAddress
		}
	}
	if emailShape.MatchString(value) {
		return strings.ToLower(value), EmailAddress
	}
	return "", InvalidAddress
}

func Parse(reader io.Reader, limits Limits) ([]model.Contact, error) {
	if err := limits.valid(); err != nil {
		return nil, err
	}
	blob, err := io.ReadAll(io.LimitReader(reader, limits.MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read phonebook: %w", err)
	}
	if int64(len(blob)) > limits.MaxBytes {
		return nil, errors.New("phonebook exceeds the byte limit")
	}
	if err := checkCardSizes(string(blob), limits.MaxCardBytes); err != nil {
		return nil, err
	}

	lines := unfoldLines(string(blob))
	contacts := make([]model.Contact, 0)
	card := make([]string, 0)
	cardBytes := 0
	inCard := false

	for _, line := range lines {
		marker := strings.ToUpper(strings.TrimSpace(line))
		switch marker {
		case "BEGIN:VCARD":
			if inCard {
				return nil, errors.New("phonebook contains a nested vCard")
			}
			inCard = true
			card = card[:0]
			cardBytes = len(line) + 1
			continue
		case "END:VCARD":
			if !inCard {
				return nil, errors.New("phonebook contains an unmatched vCard terminator")
			}
			cardBytes += len(line) + 1
			if cardBytes > limits.MaxCardBytes {
				return nil, errors.New("vCard exceeds the byte limit")
			}
			contact := parseCard(card, limits)
			if contact.Name != "" || len(contact.Phones) != 0 || len(contact.Emails) != 0 {
				contacts = append(contacts, contact)
				if len(contacts) > limits.MaxCards {
					return nil, errors.New("phonebook exceeds the card limit")
				}
			}
			inCard = false
			continue
		}
		if inCard {
			cardBytes += len(line) + 1
			if cardBytes > limits.MaxCardBytes {
				return nil, errors.New("vCard exceeds the byte limit")
			}
			card = append(card, line)
		}
	}
	if inCard {
		return nil, errors.New("phonebook contains an unterminated vCard")
	}
	return contacts, nil
}

func checkCardSizes(blob string, maximum int) error {
	inCard := false
	size := 0
	for line := range strings.SplitSeq(blob, "\n") {
		marker := strings.ToUpper(strings.TrimSpace(line))
		if marker == "BEGIN:VCARD" && !inCard {
			inCard = true
			size = 0
		}
		if inCard {
			size += len(line) + 1
			if size > maximum {
				return errors.New("vCard exceeds the byte limit")
			}
		}
		if marker == "END:VCARD" {
			inCard = false
		}
	}
	return nil
}

func (limits Limits) valid() error {
	if limits.MaxBytes <= 0 || limits.MaxCardBytes <= 0 || limits.MaxCards <= 0 ||
		limits.MaxNameChars <= 0 || limits.MaxAddressChars <= 0 || limits.MaxAddressesCard <= 0 {
		return errors.New("all vCard limits must be positive")
	}
	return nil
}

func unfoldLines(blob string) []string {
	blob = strings.ReplaceAll(blob, "\r\n", "\n")
	blob = strings.ReplaceAll(blob, "\r", "\n")
	raw := strings.Split(blob, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') && len(lines) != 0 {
			lines[len(lines)-1] += line[1:]
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func parseCard(lines []string, limits Limits) model.Contact {
	var contact model.Contact
	phones := make(map[string]struct{})
	emails := make(map[string]struct{})

	for _, line := range lines {
		left, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		property, _, _ := strings.Cut(left, ";")
		if dot := strings.LastIndexByte(property, '.'); dot >= 0 {
			property = property[dot+1:]
		}
		switch strings.ToUpper(strings.TrimSpace(property)) {
		case "FN":
			contact.Name = truncateRunes(strings.TrimSpace(unescape(value)), limits.MaxNameChars)
		case "TEL":
			if utf8.RuneCountInString(value) > limits.MaxAddressChars ||
				len(contact.Phones)+len(contact.Emails) >= limits.MaxAddressesCard {
				continue
			}
			normalized, kind := NormalizeAddress(unescape(value))
			if kind == PhoneAddress {
				if _, exists := phones[normalized]; !exists {
					phones[normalized] = struct{}{}
					contact.Phones = append(contact.Phones, normalized)
				}
			}
		case "EMAIL":
			if utf8.RuneCountInString(value) > limits.MaxAddressChars ||
				len(contact.Phones)+len(contact.Emails) >= limits.MaxAddressesCard {
				continue
			}
			normalized, kind := NormalizeAddress(unescape(value))
			if kind == EmailAddress {
				if _, exists := emails[normalized]; !exists {
					emails[normalized] = struct{}{}
					contact.Emails = append(contact.Emails, normalized)
				}
			}
		}
	}
	return contact
}

func unescape(value string) string {
	var output strings.Builder
	output.Grow(len(value))
	escaped := false
	for _, char := range value {
		if !escaped {
			if char == '\\' {
				escaped = true
				continue
			}
			output.WriteRune(char)
			continue
		}
		switch char {
		case 'n', 'N':
			output.WriteByte('\n')
		default:
			output.WriteRune(char)
		}
		escaped = false
	}
	if escaped {
		output.WriteByte('\\')
	}
	return output.String()
}

func truncateRunes(value string, maximum int) string {
	if utf8.RuneCountInString(value) <= maximum {
		return value
	}
	runes := []rune(value)
	return string(runes[:maximum])
}
