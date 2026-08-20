package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/s0up4200/bluepost/internal/model"
	"github.com/s0up4200/bluepost/internal/storage"
)

type keySourceFunc func(context.Context, bool) ([32]byte, error)

func (function keySourceFunc) Key(ctx context.Context, stateExists bool) ([32]byte, error) {
	return function(ctx, stateExists)
}

type fakeProfiles struct {
	mu sync.Mutex

	openCalls  int
	openErrors []error
	watch      func(context.Context, func(model.Message) error) error
	refreshMAP func(context.Context) error
	mapReady   bool
	pbapReady  bool
	recent     []model.Message
	contacts   []model.Contact
}

func (profiles *fakeProfiles) Open(context.Context, string) error {
	profiles.mu.Lock()
	defer profiles.mu.Unlock()
	profiles.openCalls++
	if len(profiles.openErrors) == 0 {
		return nil
	}
	err := profiles.openErrors[0]
	profiles.openErrors = profiles.openErrors[1:]
	return err
}

func (profiles *fakeProfiles) Watch(ctx context.Context, callback func(model.Message) error) error {
	if profiles.watch != nil {
		return profiles.watch(ctx, callback)
	}
	<-ctx.Done()
	return ctx.Err()
}

func (profiles *fakeProfiles) ListRecent(context.Context, string, uint32) ([]model.Message, error) {
	return append([]model.Message(nil), profiles.recent...), nil
}

func (profiles *fakeProfiles) RefreshMAP(ctx context.Context) error {
	if profiles.refreshMAP != nil {
		return profiles.refreshMAP(ctx)
	}
	return nil
}

func (profiles *fakeProfiles) SyncContacts(context.Context) ([]model.Contact, error) {
	return append([]model.Contact(nil), profiles.contacts...), nil
}

func (profiles *fakeProfiles) Health() (bool, bool) {
	return profiles.mapReady, profiles.pbapReady
}

func (profiles *fakeProfiles) Close(context.Context, bool) error { return nil }

func TestInitializeLocksBeforeProfilesWhenKeyringFails(t *testing.T) {
	t.Parallel()

	profiles := &fakeProfiles{}
	service := New(Config{
		Phone:    "AA:BB:CC:DD:EE:FF",
		StateDir: filepath.Join(t.TempDir(), "state"),
		Keys: keySourceFunc(func(context.Context, bool) ([32]byte, error) {
			return [32]byte{}, storage.ErrLocked
		}),
		Profiles: profiles,
	})
	if err := service.Initialize(context.Background()); !errors.Is(err, storage.ErrLocked) {
		t.Fatalf("error %v", err)
	}
	status := service.Status()
	if status.State != "locked" || status.Storage != "locked" {
		t.Fatalf("status %#v", status)
	}
	profiles.mu.Lock()
	defer profiles.mu.Unlock()
	if profiles.openCalls != 0 {
		t.Fatalf("profile open calls %d", profiles.openCalls)
	}
}

func TestInitializeChecksStateDirectoryBeforeKeyring(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	keyCalls := 0
	service := New(Config{
		Phone:    "AA:BB:CC:DD:EE:FF",
		StateDir: stateDir,
		Keys: keySourceFunc(func(context.Context, bool) ([32]byte, error) {
			keyCalls++
			return [32]byte{}, nil
		}),
		Profiles: &fakeProfiles{},
	})
	if err := service.Initialize(context.Background()); !errors.Is(err, storage.ErrLocked) {
		t.Fatalf("error %v", err)
	}
	if keyCalls != 0 {
		t.Fatalf("keyring calls %d", keyCalls)
	}
}

func TestRunStoresReceivedMessageAndMasksStatus(t *testing.T) {
	t.Parallel()

	delivered := make(chan struct{})
	profiles := &fakeProfiles{mapReady: true, pbapReady: true}
	profiles.watch = func(ctx context.Context, callback func(model.Message) error) error {
		if err := callback(model.Message{
			Handle:        "message1",
			SenderAddress: "+4712345678",
			ContactName:   "Jane",
			Body:          "private body",
		}); err != nil {
			return err
		}
		close(delivered)
		<-ctx.Done()
		return ctx.Err()
	}
	service := New(Config{
		Phone:    "AA:BB:CC:DD:EE:FF",
		StateDir: filepath.Join(t.TempDir(), "state"),
		Keys:     fixedKeySource(0x31),
		Profiles: profiles,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	select {
	case <-delivered:
	case <-time.After(time.Second):
		t.Fatal("backend did not store the profile message")
	}
	messages, err := service.ListEvents([]string{"sms_received"}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Body != "private body" || messages[0].Kind != "sms_received" {
		t.Fatalf("messages %#v", messages)
	}
	status := service.Status()
	if status.Phone != "XX:XX:XX:XX:EE:FF" || status.Detail == "private body" {
		t.Fatalf("status %#v", status)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("run error %v", err)
	}
}

func TestRunUsesInitialAndReconnectRetryIntervals(t *testing.T) {
	t.Parallel()

	stop := errors.New("stop retry test")
	initialProfiles := &fakeProfiles{openErrors: []error{errors.New("not ready")}}
	initial := New(Config{
		Phone:    "AA:BB:CC:DD:EE:FF",
		StateDir: filepath.Join(t.TempDir(), "initial"),
		Keys:     fixedKeySource(0x32),
		Profiles: initialProfiles,
	})
	initial.wait = func(_ context.Context, duration time.Duration) error {
		if duration != 5*time.Second {
			t.Fatalf("initial retry %v", duration)
		}
		return stop
	}
	if err := initial.Run(context.Background()); !errors.Is(err, stop) {
		t.Fatalf("initial run error %v", err)
	}

	reconnectProfiles := &fakeProfiles{mapReady: true, pbapReady: true}
	reconnectProfiles.watch = func(context.Context, func(model.Message) error) error {
		return ErrConnectionLost
	}
	reconnect := New(Config{
		Phone:    "AA:BB:CC:DD:EE:FF",
		StateDir: filepath.Join(t.TempDir(), "reconnect"),
		Keys:     fixedKeySource(0x33),
		Profiles: reconnectProfiles,
	})
	reconnect.wait = func(_ context.Context, duration time.Duration) error {
		if duration != 15*time.Second {
			t.Fatalf("reconnect retry %v", duration)
		}
		return stop
	}
	if err := reconnect.Run(context.Background()); !errors.Is(err, stop) {
		t.Fatalf("reconnect run error %v", err)
	}
}

func TestRunPollsAndRefreshesMAPAfterMissedPush(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 20, 22, 33, 0, 0, time.Local)
	refreshed := make(chan struct{}, 1)
	notified := make(chan model.Message, 1)
	profiles := &fakeProfiles{
		mapReady:  true,
		pbapReady: true,
		recent: []model.Message{{
			Handle:    "message42",
			Body:      "Your verification code is 123456",
			Timestamp: now,
		}},
		refreshMAP: func(context.Context) error {
			refreshed <- struct{}{}
			return nil
		},
	}
	service := New(Config{
		Phone:    "AA:BB:CC:DD:EE:FF",
		StateDir: filepath.Join(t.TempDir(), "state"),
		Keys:     fixedKeySource(0x36),
		Profiles: profiles,
		Notify: func(_ context.Context, message model.Message) {
			notified <- message
		},
	})
	service.now = func() time.Time { return now }
	waits := 0
	service.wait = func(ctx context.Context, duration time.Duration) error {
		if duration != 15*time.Second {
			return errors.New("unexpected wait duration")
		}
		waits++
		if waits == 1 {
			return nil
		}
		<-ctx.Done()
		return ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	select {
	case <-refreshed:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("backend did not refresh MAP after polling found a missed message")
	}
	select {
	case message := <-notified:
		if message.Handle != "message42" {
			t.Fatalf("notification %#v", message)
		}
	default:
		t.Fatal("polled message was not notified")
	}
	messages, err := service.ListEvents([]string{"sms_received"}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Handle != "message42" {
		t.Fatalf("messages %#v", messages)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("run error %v", err)
	}
}

func TestPollMessagesRetriesFailedMAPRefresh(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	refreshCalls := 0
	profiles := &fakeProfiles{
		recent: []model.Message{{Handle: "message42", Body: "hello"}},
		refreshMAP: func(context.Context) error {
			refreshCalls++
			if refreshCalls == 1 {
				return errors.New("temporary MAP refresh error")
			}
			cancel()
			return nil
		},
	}
	service := notificationBackend(t, time.Now(), nil)
	service.config.Profiles = profiles
	service.wait = func(ctx context.Context, _ time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	if err := service.pollMessages(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("poll error %v", err)
	}
	if refreshCalls != 2 {
		t.Fatalf("MAP refresh calls %d", refreshCalls)
	}
}

func TestSyncContactsUpdatesSenderResolution(t *testing.T) {
	t.Parallel()

	profiles := &fakeProfiles{
		contacts: []model.Contact{{Name: "Jane", Phones: []string{"4712345678"}}},
	}
	service := New(Config{
		Phone:    "AA:BB:CC:DD:EE:FF",
		StateDir: filepath.Join(t.TempDir(), "state"),
		Keys:     fixedKeySource(0x34),
		Profiles: profiles,
	})
	if err := service.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SyncContacts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := service.ResolveContact("+47 123 45 678"); got != "Jane" {
		t.Fatalf("contact name %q", got)
	}
}

func TestAcceptMessageStoresBeforeNotification(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)
	stored := false
	var service *Backend
	service = notificationBackend(t, now, func(context.Context, model.Message) {
		messages, err := service.ListEvents([]string{"sms_received"}, 20)
		stored = err == nil && len(messages) == 1 && messages[0].Handle == "message1"
	})
	if err := service.acceptMessage(context.Background(), model.Message{
		Handle: "message1", Body: "hello", Timestamp: now,
	}); err != nil {
		t.Fatal(err)
	}
	if !stored {
		t.Fatal("notification started before storage completed")
	}
}

func TestAcceptMessageDoesNotNotifyReplayedHandle(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)
	count := 0
	service := notificationBackend(t, now, func(context.Context, model.Message) { count++ })
	message := model.Message{Handle: "message1", Body: "hello", Timestamp: now}
	if err := service.acceptMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if err := service.acceptMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("notification count %d", count)
	}

}

func TestPollMessagesKeepsFullBodyForKnownHandle(t *testing.T) {
	t.Parallel()

	stop := errors.New("stop polling")
	profiles := &fakeProfiles{recent: []model.Message{{Handle: "message1", Body: "short subject"}}}
	service := notificationBackend(t, time.Now(), nil)
	service.config.Profiles = profiles
	if err := service.acceptMessage(context.Background(), model.Message{
		Handle: "message1", Body: "complete message body",
	}); err != nil {
		t.Fatal(err)
	}
	waits := 0
	service.wait = func(context.Context, time.Duration) error {
		waits++
		if waits == 1 {
			return nil
		}
		return stop
	}
	if err := service.pollMessages(context.Background()); !errors.Is(err, stop) {
		t.Fatalf("poll error %v", err)
	}
	messages, err := service.ListEvents([]string{"sms_received"}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Body != "complete message body" {
		t.Fatalf("messages %#v", messages)
	}
}

func TestPollMessagesStoresBackfillOldestFirst(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 8, 20, 18, 0, 0, 0, time.Local)
	stop := errors.New("stop polling")
	profiles := &fakeProfiles{recent: []model.Message{
		{Handle: "newest", Timestamp: base.Add(2 * time.Minute)},
		{Handle: "oldest", Timestamp: base},
		{Handle: "middle", Timestamp: base.Add(time.Minute)},
	}}
	service := notificationBackend(t, base.Add(2*time.Minute), nil)
	service.config.Profiles = profiles
	waits := 0
	service.wait = func(context.Context, time.Duration) error {
		waits++
		if waits == 1 {
			return nil
		}
		return stop
	}
	if err := service.pollMessages(context.Background()); !errors.Is(err, stop) {
		t.Fatalf("poll error %v", err)
	}
	messages, err := service.ListEvents([]string{"sms_received"}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 || messages[0].Handle != "oldest" ||
		messages[1].Handle != "middle" || messages[2].Handle != "newest" {
		t.Fatalf("messages %#v", messages)
	}
}

func TestAcceptMessageDoesNotNotifyStaleMessage(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)
	count := 0
	service := notificationBackend(t, now, func(context.Context, model.Message) { count++ })
	err := service.acceptMessage(context.Background(), model.Message{
		Handle: "old", Body: "hello", Timestamp: now.Add(-5*time.Minute - time.Nanosecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("notification count %d", count)
	}
}

func TestAcceptMessageAcceptsTimestampBoundariesAndMissingTimestamp(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)
	count := 0
	service := notificationBackend(t, now, func(context.Context, model.Message) { count++ })
	for _, message := range []model.Message{
		{Handle: "old-boundary", Timestamp: now.Add(-5 * time.Minute)},
		{Handle: "future", Timestamp: now.Add(time.Minute)},
		{Handle: "missing"},
	} {
		if err := service.acceptMessage(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	if count != 3 {
		t.Fatalf("notification count %d", count)
	}
}

func TestAcceptMessageDoesNotNotifyExcessiveFutureTimestamp(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)
	count := 0
	service := notificationBackend(t, now, func(context.Context, model.Message) { count++ })
	err := service.acceptMessage(context.Background(), model.Message{
		Handle: "future", Timestamp: now.Add(time.Minute + time.Nanosecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("notification count %d", count)
	}
}

func notificationBackend(
	t *testing.T,
	now time.Time,
	notify func(context.Context, model.Message),
) *Backend {
	t.Helper()
	service := New(Config{
		StateDir: filepath.Join(t.TempDir(), "state"),
		Keys:     fixedKeySource(0x35),
		Profiles: &fakeProfiles{},
		Notify:   notify,
	})
	service.now = func() time.Time { return now }
	if err := service.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return service
}

func fixedKeySource(value byte) keySourceFunc {
	return func(context.Context, bool) ([32]byte, error) {
		var key [32]byte
		for index := range key {
			key[index] = value
		}
		return key, nil
	}
}
