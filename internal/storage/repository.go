package storage

import (
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/s0up4200/bluepost/internal/model"
	"github.com/s0up4200/bluepost/internal/pbap"
	"github.com/s0up4200/bluepost/internal/protocol"
)

const snapshotSchema = 1

type historyEnvelope struct {
	Schema  int             `json:"schema"`
	Records []model.Message `json:"records"`
}

type contactsEnvelope struct {
	Schema  int             `json:"schema"`
	Records []model.Contact `json:"records"`
}

type Repository struct {
	mu sync.RWMutex

	snapshot          Snapshot
	messages          []model.Message
	messageSizes      []int
	contacts          []model.Contact
	maxHistoryRecords int
	maxHistoryBytes   int
}

func NewRepository(snapshot Snapshot) *Repository {
	return &Repository{
		snapshot:          snapshot,
		maxHistoryRecords: protocol.MaxHistoryRecords,
		maxHistoryBytes:   protocol.MaxHistoryBytes,
	}
}

func (repository *Repository) Open() error {
	historyBlob, err := repository.snapshot.Load(HistoryPurpose)
	if err != nil {
		return err
	}
	contactsBlob, err := repository.snapshot.Load(ContactsPurpose)
	if err != nil {
		return err
	}

	var history historyEnvelope
	if len(historyBlob) != 0 {
		if err := json.Unmarshal(historyBlob, &history); err != nil || history.Schema != snapshotSchema {
			return lockedError(errors.New("encrypted history structure is invalid"))
		}
	}
	var contacts contactsEnvelope
	if len(contactsBlob) != 0 {
		if err := json.Unmarshal(contactsBlob, &contacts); err != nil || contacts.Schema != snapshotSchema {
			return lockedError(errors.New("encrypted contact structure is invalid"))
		}
	}

	messageSizes := make([]int, len(history.Records))
	for index, message := range history.Records {
		if err := validateMessage(message); err != nil {
			return lockedError(err)
		}
		encoded, err := json.Marshal(message)
		if err != nil {
			return lockedError(errors.New("encrypted history record is invalid"))
		}
		messageSizes[index] = len(encoded)
	}
	if len(history.Records) > repository.maxHistoryRecords ||
		historyJSONSize(messageSizes) > repository.maxHistoryBytes {
		return lockedError(errors.New("encrypted history exceeds retention limits"))
	}
	if len(contacts.Records) > protocol.MaxPhonebookContacts {
		return lockedError(errors.New("encrypted contacts exceed the card limit"))
	}
	for _, contact := range contacts.Records {
		if err := validateContact(contact); err != nil {
			return lockedError(err)
		}
	}

	repository.mu.Lock()
	repository.messages = append([]model.Message(nil), history.Records...)
	repository.messageSizes = messageSizes
	repository.contacts = copyContacts(contacts.Records)
	repository.mu.Unlock()
	return nil
}

func (repository *Repository) AppendMessage(message model.Message) (bool, error) {
	if err := validateMessage(message); err != nil {
		return false, err
	}
	encodedMessage, err := json.Marshal(message)
	if err != nil {
		return false, err
	}

	repository.mu.Lock()
	defer repository.mu.Unlock()
	candidate := make([]model.Message, 0, len(repository.messages)+1)
	sizes := make([]int, 0, len(repository.messageSizes)+1)
	created := true
	// ponytail: History is bounded; add a handle index only if replay latency becomes measurable.
	for index, existing := range repository.messages {
		if message.Handle != "" && existing.Handle == message.Handle {
			created = false
			continue
		}
		candidate = append(candidate, existing)
		sizes = append(sizes, repository.messageSizes[index])
	}
	candidate = append(candidate, message)
	sizes = append(sizes, len(encodedMessage))
	for len(candidate) > repository.maxHistoryRecords || historyJSONSize(sizes) > repository.maxHistoryBytes {
		if len(candidate) == 1 {
			return false, errors.New("message cannot fit in the encrypted history")
		}
		candidate = candidate[1:]
		sizes = sizes[1:]
	}
	blob, err := json.Marshal(historyEnvelope{Schema: snapshotSchema, Records: candidate})
	if err != nil {
		return false, err
	}
	if len(blob) > repository.maxHistoryBytes {
		return false, errors.New("history encoding exceeds the byte limit")
	}
	if err := repository.snapshot.Save(HistoryPurpose, blob); err != nil {
		return false, err
	}
	repository.messages = candidate
	repository.messageSizes = sizes
	return created, nil
}

func (repository *Repository) Messages(limit int) []model.Message {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	if limit <= 0 || len(repository.messages) == 0 {
		return nil
	}
	start := len(repository.messages) - min(limit, len(repository.messages))
	return append([]model.Message(nil), repository.messages[start:]...)
}

func (repository *Repository) ReplaceContacts(records []model.Contact) error {
	if len(records) > protocol.MaxPhonebookContacts {
		return errors.New("contact list exceeds the card limit")
	}
	candidate := copyContacts(records)
	for _, contact := range candidate {
		if err := validateContact(contact); err != nil {
			return err
		}
	}
	sort.SliceStable(candidate, func(left, right int) bool {
		return strings.ToLower(candidate[left].Name) < strings.ToLower(candidate[right].Name)
	})
	blob, err := json.Marshal(contactsEnvelope{Schema: snapshotSchema, Records: candidate})
	if err != nil {
		return err
	}
	if len(blob) > protocol.MaxHistoryBytes {
		return errors.New("contact snapshot exceeds the byte limit")
	}

	repository.mu.Lock()
	defer repository.mu.Unlock()
	if err := repository.snapshot.Save(ContactsPurpose, blob); err != nil {
		return err
	}
	repository.contacts = candidate
	return nil
}

func (repository *Repository) Contacts(offset, limit int) []model.Contact {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	if offset < 0 || limit <= 0 || offset >= len(repository.contacts) {
		return nil
	}
	end := min(offset+limit, len(repository.contacts))
	return copyContacts(repository.contacts[offset:end])
}

func (repository *Repository) FindContacts(query string, limit int) []model.Contact {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	if limit <= 0 {
		return nil
	}
	query = strings.ToLower(strings.TrimSpace(query))
	normalized, kind := pbap.NormalizeAddress(query)
	results := make([]model.Contact, 0, min(limit, len(repository.contacts)))
	// ponytail: Linear search is bounded by the PBAP card limit; add an index only after measured latency requires it.
	for _, contact := range repository.contacts {
		matched := query == "" || strings.Contains(strings.ToLower(contact.Name), query)
		if !matched {
			for _, address := range append(append([]string(nil), contact.Phones...), contact.Emails...) {
				matched = strings.Contains(strings.ToLower(address), query) ||
					(kind != pbap.InvalidAddress && address == normalized)
				if matched {
					break
				}
			}
		}
		if matched {
			results = append(results, copyContact(contact))
			if len(results) == limit {
				break
			}
		}
	}
	return results
}

func (repository *Repository) ResolveContact(address string) string {
	normalized, kind := pbap.NormalizeAddress(address)
	if kind == pbap.InvalidAddress {
		return ""
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	name := ""
	matches := 0
	for _, contact := range repository.contacts {
		addresses := contact.Phones
		if kind == pbap.EmailAddress {
			addresses = contact.Emails
		}
		if slices.Contains(addresses, normalized) {
			name = contact.Name
			matches++
		}
	}
	if matches != 1 {
		return ""
	}
	return name
}

func (repository *Repository) Counts() (int, int) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	return len(repository.messages), len(repository.contacts)
}

func historyJSONSize(recordSizes []int) int {
	size := len(`{"schema":1,"records":[]}`)
	for index, recordSize := range recordSizes {
		size += recordSize
		if index != 0 {
			size++
		}
	}
	return size
}

func validateMessage(message model.Message) error {
	if len(message.Body) > protocol.MaxBMessageBytes {
		return errors.New("message body exceeds the byte limit")
	}
	for _, value := range []string{message.Handle, message.SenderAddress, message.SenderPhoneNorm} {
		if utf8.RuneCountInString(value) > protocol.MaxContactAddressChars {
			return errors.New("message address field exceeds the character limit")
		}
	}
	if utf8.RuneCountInString(message.ContactName) > protocol.MaxContactNameChars {
		return errors.New("message contact name exceeds the character limit")
	}
	return nil
}

func validateContact(contact model.Contact) error {
	if utf8.RuneCountInString(contact.Name) > protocol.MaxContactNameChars {
		return errors.New("contact name exceeds the character limit")
	}
	if len(contact.Phones)+len(contact.Emails) > protocol.MaxAddressesPerCard {
		return errors.New("contact exceeds the address limit")
	}
	for _, address := range append(append([]string(nil), contact.Phones...), contact.Emails...) {
		if utf8.RuneCountInString(address) > protocol.MaxContactAddressChars {
			return errors.New("contact address exceeds the character limit")
		}
	}
	return nil
}

func copyContacts(records []model.Contact) []model.Contact {
	result := make([]model.Contact, len(records))
	for index, contact := range records {
		result[index] = copyContact(contact)
	}
	return result
}

func copyContact(contact model.Contact) model.Contact {
	contact.Phones = append([]string(nil), contact.Phones...)
	contact.Emails = append([]string(nil), contact.Emails...)
	return contact
}
