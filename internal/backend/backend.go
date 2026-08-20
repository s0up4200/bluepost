package backend

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/s0up4200/bluepost/internal/model"
	"github.com/s0up4200/bluepost/internal/protocol"
	"github.com/s0up4200/bluepost/internal/storage"
)

var ErrConnectionLost = errors.New("Bluetooth profile connection was lost")

type KeySource interface {
	Key(context.Context, bool) ([32]byte, error)
}

type ProfileClient interface {
	Open(context.Context, string) error
	Watch(context.Context, func(model.Message) error) error
	ListRecent(context.Context, string, uint32) ([]model.Message, error)
	RefreshMAP(context.Context) error
	SyncContacts(context.Context) ([]model.Contact, error)
	Health() (bool, bool)
	Close(context.Context, bool) error
}

type Config struct {
	Phone    string
	StateDir string
	Keys     KeySource
	Profiles ProfileClient
	Notify   func(context.Context, model.Message)
}

type Backend struct {
	mu sync.RWMutex

	config         Config
	repository     *storage.Repository
	initialized    bool
	status         model.Status
	revision       uint64
	historyChanged func(uint64)
	statusChanged  func()
	wait           func(context.Context, time.Duration) error
	now            func() time.Time
}

const (
	notificationMaxAge     = 5 * time.Minute
	notificationFutureSkew = time.Minute
	messagePollInterval    = 15 * time.Second
	messagePollLimit       = 20
)

func New(config Config) *Backend {
	return &Backend{
		config: config,
		status: model.Status{
			State:   "stopped",
			Detail:  "The daemon is not started",
			Storage: "locked",
			Phone:   maskPhone(config.Phone),
		},
		wait: waitContext,
		now:  time.Now,
	}
}

func (backend *Backend) Initialize(ctx context.Context) error {
	backend.mu.RLock()
	initialized := backend.initialized
	state := backend.status.State
	backend.mu.RUnlock()
	if initialized {
		if state == "locked" {
			return storage.ErrLocked
		}
		return nil
	}
	if backend.config.Keys == nil || backend.config.Profiles == nil {
		return backend.lock(errors.New("key source and Bluetooth profiles are required"))
	}
	stateExists, err := storage.StateExists(backend.config.StateDir)
	if err != nil {
		return backend.lock(err)
	}
	key, err := backend.config.Keys.Key(ctx, stateExists)
	if err != nil {
		return backend.lock(err)
	}
	repository := storage.NewRepository(storage.Snapshot{Dir: backend.config.StateDir, Key: key})
	if err := repository.Open(); err != nil {
		return backend.lock(err)
	}

	backend.mu.Lock()
	backend.repository = repository
	backend.initialized = true
	backend.status.State = "connecting"
	backend.status.Detail = "Opening MAP and PBAP sessions"
	backend.status.Storage = "unlocked"
	callback := backend.statusChanged
	backend.mu.Unlock()
	if callback != nil {
		callback()
	}
	return nil
}

func (backend *Backend) Run(ctx context.Context) error {
	if err := backend.Initialize(ctx); err != nil {
		if errors.Is(err, storage.ErrLocked) {
			<-ctx.Done()
			return ctx.Err()
		}
		return err
	}
	connected := false
	for {
		backend.setConnectionState("connecting", "Opening MAP and PBAP sessions", false, false)
		openCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		err := backend.config.Profiles.Open(openCtx, backend.config.Phone)
		cancel()
		if err != nil {
			backend.setConnectionState("degraded", connectionDetail(err), false, false)
			delay := 5 * time.Second
			if connected {
				delay = 15 * time.Second
			}
			if err := backend.wait(ctx, delay); err != nil {
				return err
			}
			continue
		}
		connected = true
		mapReady, pbapReady := backend.config.Profiles.Health()
		backend.setConnectionState("ready", "MAP and PBAP sessions are available", mapReady, pbapReady)
		err = backend.watchProfiles(ctx)
		if ctx.Err() != nil {
			backend.closeProfiles(true)
			backend.setConnectionState("stopped", "The daemon stopped", false, false)
			return ctx.Err()
		}
		if errors.Is(err, storage.ErrLocked) {
			backend.closeProfiles(true)
			backend.lock(err)
			<-ctx.Done()
			return ctx.Err()
		}
		backend.closeProfiles(false)
		backend.setConnectionState("degraded", "The Bluetooth profile connection was lost", false, false)
		if err := backend.wait(ctx, 15*time.Second); err != nil {
			return err
		}
	}
}

func (backend *Backend) watchProfiles(ctx context.Context) error {
	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, 2)
	go func() {
		results <- backend.config.Profiles.Watch(watchCtx, func(message model.Message) error {
			return backend.acceptMessage(watchCtx, message)
		})
	}()
	go func() { results <- backend.pollMessages(watchCtx) }()
	select {
	case err := <-results:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (backend *Backend) pollMessages(ctx context.Context) error {
	refreshPending := false
	for {
		if err := backend.wait(ctx, messagePollInterval); err != nil {
			return err
		}
		if refreshPending {
			if err := backend.refreshMAP(ctx); err != nil {
				continue
			}
			refreshPending = false
		}
		operationCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		messages, err := backend.config.Profiles.ListRecent(
			operationCtx,
			"telecom/msg/inbox",
			messagePollLimit,
		)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		slices.SortStableFunc(messages, func(left, right model.Message) int {
			return left.Timestamp.Compare(right.Timestamp)
		})
		missed := false
		for _, message := range messages {
			created, err := backend.storeMessage(ctx, message, true)
			if err != nil {
				return err
			}
			missed = missed || created
		}
		if !missed {
			continue
		}
		if err := backend.refreshMAP(ctx); err != nil {
			refreshPending = true
		}
	}
}

func (backend *Backend) refreshMAP(ctx context.Context) error {
	operationCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return backend.config.Profiles.RefreshMAP(operationCtx)
}

func (backend *Backend) Status() model.Status {
	backend.mu.RLock()
	status := backend.status
	repository := backend.repository
	backend.mu.RUnlock()
	if status.Storage == "unlocked" && backend.config.Profiles != nil {
		status.MAP, status.PBAP = backend.config.Profiles.Health()
	}
	if repository != nil {
		status.HistoryCount, status.ContactCount = repository.Counts()
	}
	return status
}

func (backend *Backend) Healthy() bool {
	status := backend.Status()
	return status.Storage == "unlocked" && status.MAP
}

func (backend *Backend) ListEvents(kinds []string, limit uint32) ([]model.Message, error) {
	repository, err := backend.unlockedRepository()
	if err != nil {
		return nil, err
	}
	if limit == 0 || limit > protocol.MaxHistoryRecords {
		return nil, errors.New("history limit is outside the allowed range")
	}
	allowed := make(map[string]bool, len(kinds))
	for _, kind := range kinds {
		if len(kind) > 64 {
			return nil, errors.New("history kind exceeds the character limit")
		}
		allowed[kind] = true
	}
	all := repository.Messages(protocol.MaxHistoryRecords)
	filtered := make([]model.Message, 0, min(int(limit), len(all)))
	for index := len(all) - 1; index >= 0 && len(filtered) < int(limit); index-- {
		message := all[index]
		if len(allowed) != 0 && !allowed[message.Kind] {
			continue
		}
		message.Body = truncatePublicBody(message.Body)
		filtered = append(filtered, message)
	}
	for left, right := 0, len(filtered)-1; left < right; left, right = left+1, right-1 {
		filtered[left], filtered[right] = filtered[right], filtered[left]
	}
	return filtered, nil
}

func (backend *Backend) ListRecent(
	ctx context.Context,
	folder string,
	limit uint32,
) ([]model.Message, error) {
	if _, err := backend.unlockedRepository(); err != nil {
		return nil, err
	}
	return backend.config.Profiles.ListRecent(ctx, folder, limit)
}

func (backend *Backend) FindContacts(query string) ([]model.Contact, error) {
	repository, err := backend.unlockedRepository()
	if err != nil {
		return nil, err
	}
	if utf8.RuneCountInString(query) > 256 {
		return nil, errors.New("contact query exceeds the character limit")
	}
	return repository.FindContacts(query, 100), nil
}

func (backend *Backend) ListContacts(offset, limit uint32) ([]model.Contact, error) {
	repository, err := backend.unlockedRepository()
	if err != nil {
		return nil, err
	}
	if limit == 0 || limit > protocol.MaxContactPage {
		return nil, errors.New("contact page limit is outside the allowed range")
	}
	return repository.Contacts(int(offset), int(limit)), nil
}

func (backend *Backend) SyncContacts(ctx context.Context) (uint32, error) {
	repository, err := backend.unlockedRepository()
	if err != nil {
		return 0, err
	}
	contacts, err := backend.config.Profiles.SyncContacts(ctx)
	if err != nil {
		return 0, err
	}
	if err := repository.ReplaceContacts(contacts); err != nil {
		return 0, err
	}
	backend.emitHistoryChanged()
	return uint32(len(contacts)), nil
}

func (backend *Backend) ResolveContact(address string) string {
	repository, err := backend.unlockedRepository()
	if err != nil {
		return ""
	}
	return repository.ResolveContact(address)
}

func (backend *Backend) SetSignals(historyChanged func(uint64), statusChanged func()) {
	backend.mu.Lock()
	backend.historyChanged = historyChanged
	backend.statusChanged = statusChanged
	backend.mu.Unlock()
}

func (backend *Backend) acceptMessage(ctx context.Context, message model.Message) error {
	_, err := backend.storeMessage(ctx, message, false)
	return err
}

func (backend *Backend) storeMessage(
	ctx context.Context,
	message model.Message,
	onlyIfMissing bool,
) (bool, error) {
	repository, err := backend.unlockedRepository()
	if err != nil {
		return false, err
	}
	if message.Kind == "" {
		message.Kind = "sms_received"
	}
	var created bool
	if onlyIfMissing {
		created, err = repository.AppendMessageIfMissing(message)
	} else {
		created, err = repository.AppendMessage(message)
	}
	if err != nil {
		if errors.Is(err, storage.ErrLocked) {
			backend.lock(err)
		}
		return false, err
	}
	if onlyIfMissing && !created {
		return false, nil
	}
	backend.emitHistoryChanged()
	if created && backend.config.Notify != nil && recentMessage(message.Timestamp, backend.now()) {
		backend.config.Notify(ctx, message)
	}
	return created, nil
}

func recentMessage(timestamp, now time.Time) bool {
	if timestamp.IsZero() {
		return true
	}
	age := now.Sub(timestamp)
	return age >= -notificationFutureSkew && age <= notificationMaxAge
}

func (backend *Backend) emitHistoryChanged() {
	backend.mu.Lock()
	backend.revision++
	revision := backend.revision
	callback := backend.historyChanged
	backend.mu.Unlock()
	if callback != nil {
		callback(revision)
	}
}

func (backend *Backend) setConnectionState(state, detail string, mapReady, pbapReady bool) {
	backend.mu.Lock()
	backend.status.State = state
	backend.status.Detail = detail
	backend.status.MAP = mapReady
	backend.status.PBAP = pbapReady
	callback := backend.statusChanged
	backend.mu.Unlock()
	if callback != nil {
		callback()
	}
}

func (backend *Backend) lock(cause error) error {
	backend.mu.Lock()
	backend.initialized = true
	backend.repository = nil
	backend.status.State = "locked"
	backend.status.Detail = "Encrypted storage is unavailable"
	backend.status.Storage = "locked"
	backend.status.MAP = false
	backend.status.PBAP = false
	callback := backend.statusChanged
	backend.mu.Unlock()
	if callback != nil {
		callback()
	}
	return fmt.Errorf("%w: %v", storage.ErrLocked, cause)
}

func (backend *Backend) unlockedRepository() (*storage.Repository, error) {
	backend.mu.RLock()
	repository := backend.repository
	unlocked := backend.status.Storage == "unlocked"
	backend.mu.RUnlock()
	if !unlocked || repository == nil {
		return nil, storage.ErrLocked
	}
	return repository, nil
}

func (backend *Backend) closeProfiles(removeRemote bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	_ = backend.config.Profiles.Close(ctx, removeRemote)
	cancel()
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func connectionDetail(err error) string {
	if strings.Contains(err.Error(), "Connection refused (111)") {
		return "The iPhone refused the MAP connection because another computer can own it"
	}
	return "MAP and PBAP sessions are not available"
}

func maskPhone(phone string) string {
	parts := strings.Split(strings.ToUpper(phone), ":")
	if len(parts) != 6 {
		return "XX:XX:XX:XX:XX:XX"
	}
	return "XX:XX:XX:XX:" + parts[4] + ":" + parts[5]
}

func truncatePublicBody(body string) string {
	if utf8.RuneCountInString(body) <= protocol.MaxPublicBodyChars {
		return body
	}
	runes := []rune(body)
	return string(runes[:protocol.MaxPublicBodyChars-1]) + "…"
}
