# SMS Notifications Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show every new SMS as an Omarchy notification and copy one clear authentication code after a left click.

**Architecture:** The backend stores and deduplicates each MAP message before it calls the desktop notifier. The notifier uses a standard-library detector, `notify-send`, and `wl-copy --sensitive`.

**Tech Stack:** Go 1.27, Go `regexp`, `os/exec`, libnotify `notify-send` 0.8.8, wl-clipboard `wl-copy`, and the existing Bluepost packages.

**Spec:** `docs/superpowers/specs/2026-08-20-sms-notifications-design.md`

## Global Constraints

- Write all source, tests, commands, errors, and documentation in English.
- Show the contact name or sender address and the complete SMS body.
- Copy only one unambiguous authentication code.
- Use English and Norwegian authentication phrases in the first version.
- Parse domain-bound SMS and Google SMS Retriever structures before heuristic patterns.
- Do not add a provider allowlist, an NLP library, or a Go dependency.
- Do not pass the extracted code in a process argument.
- Use `wl-copy --sensitive --trim-newline`. Do not use `--paste-once`.
- Do not log a sender, body, candidate, or extracted code.
- Store a message successfully before a notification can start.
- Do not notify for a duplicate handle or a message more than five minutes old.
- Accept a timestamp up to one minute in the future.
- Treat a missing timestamp as recent.
- Keep the daemon read-only. Do not add send, reply, erase, or read-state behavior.
- Do not install packages, change Omarchy files, push, or publish.
- The user authorized local unsigned commits for this work.

---

## File map

| Path | Responsibility |
| --- | --- |
| `internal/otp/otp.go` | Extract one unambiguous authentication code from an SMS body. |
| `internal/otp/otp_test.go` | Cover structured formats, provider examples, localization, and false positives. |
| `internal/storage/repository.go` | Report whether an appended message handle is new. |
| `internal/storage/repository_test.go` | Prove new, replayed, empty-handle, and failed-save results. |
| `internal/backend/backend.go` | Notify only after storage and only for a new, recent message. |
| `internal/backend/backend_test.go` | Prove storage order, duplicate suppression, and timestamp limits. |
| `internal/desktop/notifier.go` | Show notifications and copy a clicked authentication code. |
| `internal/desktop/notifier_test.go` | Record process calls and inspect arguments, input, actions, and generic errors. |
| `cmd/bluepostd/main.go` | Connect the notifier to the backend. |
| `README.md` | Document notification behavior, privacy, and runtime programs. |

### Task 1: Authentication code detector

**Files:**
- Create: `internal/otp/otp_test.go`
- Create: `internal/otp/otp.go`

**Interfaces:**
- Produces: `otp.Extract(body string) (code string, ok bool)`
- Consumes: no Bluepost package and no external dependency

- [ ] **Step 1: Write the failing detector test**

Create a table-driven test with literal expected values:

```go
func TestExtract(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want string
	}{
		{"twilio", "Your ExampleApp verification code is: 123456", "123456"},
		{"stripe", "Your Stripe verification code is 482731", "482731"},
		{"norwegian after", "Bekreftelseskoden din er 482731.", "482731"},
		{"norwegian before", "482731 er bekreftelseskoden din.", "482731"},
		{"domain bound", "747723 is your authentication code.\n\n@example.com #747723", "747723"},
		{"apple iframe", "Your code is 123456.\n\n@example.com #123456 %iframe.example.org", "123456"},
		{"wicg embedded", "Your code is A1B2C3.\n\n@example.com #A1B2C3 @auth.example.org", "A1B2C3"},
		{"google hash", "Your ExampleApp code is: 123ABC78\nFA+9qCX9VSu", "123ABC78"},
		{"strong alphanumeric", "Your authentication code is A1B2C3", "A1B2C3"},
		{"weak numeric", "Your code is 654321", "654321"},
		{"otp", "OTP: 193847", "193847"},
		{"promotion", "Use code SAVE20 for 20% off.", ""},
		{"order", "Your order 482731 has shipped.", ""},
		{"amount", "Pay EUR 39.99; card ending 1234.", ""},
		{"url", "Your verification code is at https://example.com/123456", ""},
		{"email", "Your verification code is user1234@example.com", ""},
		{"date", "Your verification code expires on 20260820", ""},
		{"phone", "If your verification code fails, call 12345678.", ""},
		{"ambiguous", "Verification code 1234 or 5678", ""},
		{"too short", "Your verification code is 123", ""},
		{"too long", "Your verification code is 12345678901", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := Extract(test.body)
			if got != test.want || ok != (test.want != "") {
				t.Fatalf("Extract() = %q, %v; want %q", got, ok, test.want)
			}
		})
	}
}
```

Production change caught: a missing structure rule, context rule, length boundary, or ambiguity guard changes a literal result.

- [ ] **Step 2: Run the detector test and observe the expected error**

Run: `go test ./internal/otp`

Expected: FAIL because the `otp` package and `Extract` do not exist.

- [ ] **Step 3: Implement the minimum staged detector**

Use package-level compiled expressions and this public function:

```go
func Extract(body string) (string, bool) {
	text := normalize(body)
	if code, ok := domainBound(text); ok {
		return code, true
	}
	hasGoogleHash := googleHashLine.MatchString(lastLine(text))
	candidates := contextualCandidates(text, hasGoogleHash)
	if len(candidates) != 1 {
		return "", false
	}
	return candidates[0], true
}
```

Implement these exact rules:

- `domainBound` inspects only the final non-empty line.
- It accepts `@host #code`, with one optional `%host` or `@host` suffix.
- A candidate contains 4 to 10 ASCII letters or digits and at least one digit.
- A plain candidate cannot be part of a longer ASCII word or number.
- Strong phrases accept numeric or mixed alphanumeric candidates on either side.
- Weak `code` and `kode` grammar accepts numeric candidates only.
- The Google 11-character hash enables a mixed candidate after weak `code` grammar.
- Repeated occurrences of the same code count as one candidate.
- Two different candidates with equal confidence return no code.
- Do not implement grouped `123-456` codes.

- [ ] **Step 4: Run the detector tests and make sure that they pass**

Run: `go test ./internal/otp`

Expected: PASS.

- [ ] **Step 5: Commit the detector**

```bash
git add internal/otp
git commit --no-gpg-sign -m "feat: detect SMS authentication codes"
```

### Task 2: Storage result and notification eligibility

**Files:**
- Modify: `internal/storage/repository_test.go`
- Modify: `internal/storage/repository.go:102`
- Modify: `internal/backend/backend_test.go`
- Modify: `internal/backend/backend.go:32`

**Interfaces:**
- Produces: `(*storage.Repository).AppendMessage(model.Message) (created bool, err error)`
- Produces: `backend.Config.Notify func(context.Context, model.Message)`
- Consumes: the existing `ProfileClient.Watch` callback and encrypted repository

- [ ] **Step 1: Write failing repository result assertions**

Extend `TestRepositoryReplacesReplayedMessageHandle`:

```go
created, err := repository.AppendMessage(model.Message{Handle: "message7", Body: "old"})
if err != nil || !created {
	t.Fatalf("first append = %v, %v", created, err)
}
created, err = repository.AppendMessage(model.Message{Handle: "message7", Body: "new"})
if err != nil || created {
	t.Fatalf("replayed append = %v, %v", created, err)
}
```

Extend `TestRepositoryKeepsMessagesWithEmptyHandlesDistinct` and require `created == true` for both appends.
Extend the failed replacement test and require `created == false` when the encrypted save fails.

Production change caught: a replay incorrectly reports a new message, or a failed save reports success.

- [ ] **Step 2: Write failing backend eligibility tests**

Add this helper:

```go
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
```

Use `now := time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)` in each test.

Add these separate tests:

```go
func TestAcceptMessageStoresBeforeNotification(t *testing.T) {
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

func TestAcceptMessageDoesNotNotifyStaleMessage(t *testing.T) {
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

func TestAcceptMessageAcceptsClockSkewAndMissingTimestamp(t *testing.T) {
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
```

Call `service.acceptMessage(context.Background(), message)` directly.
Use literal handles and timestamps.
Do not mock the repository or encrypted snapshot.

Production change caught: notification starts before storage, or a timestamp boundary uses the wrong comparison.

- [ ] **Step 3: Run the focused tests and observe the expected compile errors**

Run: `go test ./internal/storage ./internal/backend`

Expected: FAIL because `AppendMessage` returns one value, `Config.Notify` does not exist, and `acceptMessage` has no context.

- [ ] **Step 4: Implement the repository result**

Change the signature to:

```go
func (repository *Repository) AppendMessage(message model.Message) (bool, error)
```

Start with `created := true`.
Set `created = false` when a non-empty handle matches an existing record.
Return `false, err` for every validation, encoding, size, or snapshot error.
Return `created, nil` only after the snapshot save and memory update succeed.

Update every existing caller and test to receive both return values.

- [ ] **Step 5: Implement notification eligibility in the backend**

Add these members:

```go
type Config struct {
	Phone    string
	StateDir string
	Keys     KeySource
	Profiles ProfileClient
	Notify   func(context.Context, model.Message)
}

type Backend struct {
	// existing fields
	now func() time.Time
}
```

Set `now: time.Now` in `New`.
Wrap the watch callback so that it passes the run context to `acceptMessage`.

Use these bounds:

```go
const (
	notificationMaxAge     = 5 * time.Minute
	notificationFutureSkew = time.Minute
)
```

Change the method to `acceptMessage(ctx context.Context, message model.Message) error`.
After a successful append, emit the existing history signal.
Call `Notify` only when `created` is true and the timestamp is recent.
A zero timestamp is recent.

- [ ] **Step 6: Run all Go tests and make sure that they pass**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 7: Commit storage and eligibility**

```bash
git add internal/storage internal/backend
git commit --no-gpg-sign -m "feat: select new messages for notifications"
```

### Task 3: Omarchy desktop notifier

**Files:**
- Create: `internal/desktop/notifier_test.go`
- Create: `internal/desktop/notifier.go`

**Interfaces:**
- Produces: `desktop.NewNotifier(errors io.Writer) *desktop.Notifier`
- Produces: `(*desktop.Notifier).Notify(context.Context, model.Message)`
- Consumes: `otp.Extract`, `notify-send`, and `wl-copy`

- [ ] **Step 1: Write failing process-boundary tests**

Define a test runner that records this structure:

```go
type commandCall struct {
	name  string
	args  []string
	input string
}
```

Construct `Notifier` with an unexported runner field from the same package test.
Call the synchronous unexported `notify` method in tests.

Add these tests:

- `TestNotifyCopiesDetectedCodeAfterDefaultAction`
- `TestNotifyDoesNotAddActionForOrdinarySMS`
- `TestNotifyDoesNotCopyAfterDismissal`
- `TestNotifyEscapesUntrustedMarkup`
- `TestNotifySanitizesInvalidTextAndControls`
- `TestNotifyReportsGenericCommandErrors`

The detected-code test must assert these exact effects:

```go
wantNotifyAction := "--action=default=Copy code"
wantCopyArgs := []string{"--sensitive", "--trim-newline"}
wantCopyInput := "482731"
```

Require that the first call is `notify-send`.
Require that its title prefers `ContactName` over `SenderAddress`.
Require that the body argument converts `<b>hello</b>` to escaped markup.
Return `default` from the fake `notify-send` call.
Require that the second call is `wl-copy` with the code on standard input.
Require that no argument equals `--paste-once` or contains `482731`.

The text-safety test uses this body:

```go
"hello\x00\x1bworld\xff\nnext\tfield"
```

Require that the body argument removes NUL and ESC.
Require that it converts `\xff` to `U+FFFD`.
Require that it preserves the line break and tab.

The error test makes each command fail in turn.
Require only the exact generic error line for that command.
Require that the error output does not contain the sender, body, or code.

Production change caught: ordinary message text reaches the clipboard, markup remains active, or the code leaks into an argument.

- [ ] **Step 2: Run the notifier tests and observe the expected error**

Run: `go test ./internal/desktop`

Expected: FAIL because the `desktop` package and notifier do not exist.

- [ ] **Step 3: Implement the command seam and safe notification content**

Use these internal types:

```go
type commandRunner func(context.Context, string, []string, string) (string, error)

type Notifier struct {
	run    commandRunner
	errors io.Writer
}
```

`NewNotifier` uses a runner based on `exec.CommandContext`.
The runner sends `input` through `cmd.Stdin` and returns trimmed standard output.

Convert invalid UTF-8 to `U+FFFD`.
Preserve line breaks and tabs.
Remove other Unicode control characters.
Then use `html.EscapeString` on the title and body.

Build notification arguments in this order:

```text
--app-name=Bluepost
--icon=mail-unread-symbolic
[--action=default=Copy code]
<title>
<body>
```

Use `New message` when both sender fields are empty.

- [ ] **Step 4: Implement click handling and generic errors**

`Notify` starts one goroutine and returns.
The goroutine calls the synchronous `notify` method.

If `notify-send` fails while the context is active, write exactly:

```text
Could not show message notification
```

If the output is not `default`, return without a clipboard call.
For `default`, call `wl-copy` with `--sensitive` and `--trim-newline`.
Send only the extracted code through standard input.

If `wl-copy` fails while the context is active, write exactly:

```text
Could not copy authentication code
```

Do not retry either process.

- [ ] **Step 5: Add the asynchronous return test**

Use a runner that blocks on a channel.
Call `Notify` and require it to return before the runner channel closes.
Then close the channel so that the test leaves no goroutine behind.

Production change caught: `Notify` blocks MAP reception while `notify-send` waits for a click.

- [ ] **Step 6: Run the notifier and full test suites**

Run: `go test ./internal/desktop ./internal/otp`

Expected: PASS.

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 7: Commit the notifier**

```bash
git add internal/desktop
git commit --no-gpg-sign -m "feat: notify and copy SMS codes"
```

### Task 4: Daemon wiring and documentation

**Files:**
- Modify: `cmd/bluepostd/main.go:13`
- Modify: `README.md:11`

**Interfaces:**
- Consumes: `desktop.NewNotifier(os.Stderr).Notify`
- Produces: a configured `backend.Config.Notify` callback in `bluepostd`

- [ ] **Step 1: Wire the notifier into the daemon**

Import `internal/desktop`.
Create one notifier before `backend.New`:

```go
notifier := desktop.NewNotifier(os.Stderr)
```

Set this configuration member:

```go
Notify: notifier.Notify,
```

Do not add a command-line flag or configuration value.

- [ ] **Step 2: Update the README**

Add `notify-send` from libnotify and `wl-copy` from wl-clipboard to the requirements.
State that `bluepostd` shows full incoming SMS notifications.
State that a left click copies one clear authentication code.
State that ordinary SMS text is never copied.

In the security section, document these facts:

- Omarchy stores its ten most recent notifications as plain JSON.
- `--sensitive` keeps authentication codes out of Omarchy clipboard history.
- A code remains in the active clipboard until another copy replaces it.
- Copy actions do not survive notification-history restoration in this version.

Link both SMS notification design documents from the final README section.

- [ ] **Step 3: Format and run the focused checks**

Run: `gofmt -w cmd/bluepostd/main.go internal/backend/backend.go internal/backend/backend_test.go internal/storage/repository.go internal/storage/repository_test.go internal/otp/otp.go internal/otp/otp_test.go internal/desktop/notifier.go internal/desktop/notifier_test.go`

Expected: no output.

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 4: Run the release checks**

Run: `go test -race ./...`

Expected: PASS.

Run: `go vet ./...`

Expected: PASS.

Run: `go build ./cmd/...`

Expected: PASS.

Run: `dbus-run-session -- env BLUEPOST_TEST_PRIVATE_BUS=1 go test -tags=integration ./internal/...`

Expected: PASS.

Run: `git diff --check`

Expected: no output.

- [ ] **Step 5: Make a local synthetic Omarchy notification check**

Make sure that `notify-send` and `wl-copy` are available:

```bash
command -v notify-send
command -v wl-copy
```

Use a synthetic code, not a real account code:

```bash
notify-send --app-name=Bluepost --action=default='Copy code' 'Bluepost test' 'Your verification code is 482731'
```

Expected: Omarchy shows the complete body. A left click returns `default` to the terminal.

- [ ] **Step 6: Commit daemon wiring and documentation**

```bash
git add cmd/bluepostd/main.go README.md
git commit --no-gpg-sign -m "feat: wire SMS notifications"
```

- [ ] **Step 7: Inspect the final branch**

Run: `git status --short`

Expected: no output.

Run: `git log --oneline -6`

Expected: the design commit and the four implementation commits appear above `b0678db`.
