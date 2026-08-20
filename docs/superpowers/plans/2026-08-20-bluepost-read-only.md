# Bluepost Read-Only Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a read-only Go daemon and Cobra CLI for iPhone MAP messages and PBAP contacts on Omarchy.

**Architecture:** `bluepostd` owns BlueZ OBEX sessions, encrypted snapshots, and the Bluepost D-Bus service.
One worker serializes all MAP and PBAP operations. `bluepost` is a short-lived D-Bus client.

**Tech Stack:** Go 1.27, `github.com/godbus/dbus/v5` v5.2.2, `github.com/spf13/cobra` v1.10.2, BlueZ 5.86 or later, and `/usr/bin/secret-tool`.

**Spec:** `docs/superpowers/specs/2026-08-20-bluepost-read-only-design.md`

## Global Constraints

- Use the project name `Bluepost` and the code name `bluepost`.
- Write all source, tests, commands, help text, errors, logs, and documentation in English.
- Support only Arch Linux x86-64 with BlueZ 5.86 or later in this release.
- Keep message access read-only. Do not add send, reply, erase, or read-state operations.
- Do not add ANCS, a GUI, a TUI, SQLite, telemetry, update checks, plugins, or network listeners.
- Use only the Go standard library, godbus, Cobra, and Cobra's required transitive modules.
- Store private data only in AES-256-GCM snapshots. Store the key only through `secret-tool` and Secret Service.
- Fail closed before OBEX startup when the key or encrypted state is unavailable.
- Do not install packages, change system configuration, start installed services, commit, push, or publish.
- Commit commands in this plan require separate user authorization and must remain unexecuted in the current session.

---

## File Map

| Path | Responsibility |
| --- | --- |
| `go.mod`, `go.sum` | Pin the module and dependency graph. |
| `internal/config/config.go` | Parse the phone address and XDG paths. |
| `internal/model/model.go` | Define message, contact, and status records. |
| `internal/protocol/protocol.go` | Define Bluepost D-Bus names, limits, and wire methods. |
| `internal/textsafe/textsafe.go` | Remove terminal control characters. |
| `internal/mapmsg/bmessage.go` | Parse bounded MAP bMessage content. |
| `internal/pbap/vcard.go` | Parse bounded PBAP vCard 3.0 content. |
| `internal/storage/keyring.go` | Create and retrieve the storage key through `secret-tool`. |
| `internal/storage/snapshot.go` | Authenticate and atomically replace encrypted snapshots. |
| `internal/storage/repository.go` | Own bounded history and contact records. |
| `internal/obex/transport.go` | Wrap godbus calls and signals behind a small test seam. |
| `internal/obex/worker.go` | Serialize bounded OBEX operations. |
| `internal/obex/sessions.go` | Open, monitor, and reconnect MAP and PBAP sessions. |
| `internal/obex/transfer.go` | Wait for bounded OBEX file transfers. |
| `internal/obex/map.go` | Receive MAP events and query live folders. |
| `internal/obex/pbap.go` | Download and parse the main phonebook. |
| `internal/backend/backend.go` | Coordinate storage, contacts, OBEX, status, and revisions. |
| `internal/bus/server.go` | Export the read-only Messages1 and Events1 interfaces. |
| `internal/bus/client.go` | Call the Bluepost D-Bus service from the CLI. |
| `internal/cli/root.go` | Define Cobra commands and plain-text output. |
| `cmd/bluepost/main.go` | Start the CLI. |
| `cmd/bluepostd/main.go` | Start the daemon. |
| `README.md` | Document source builds, prerequisites, operation, and security boundaries. |

Tests stay beside their production packages as `*_test.go` files.

### Task 1: Module, Configuration, Models, and Text Safety

**Files:**
- Create: `go.mod`
- Create: `internal/config/config_test.go`
- Create: `internal/config/config.go`
- Create: `internal/model/model.go`
- Create: `internal/protocol/protocol.go`
- Create: `internal/textsafe/textsafe_test.go`
- Create: `internal/textsafe/textsafe.go`

**Interfaces:**
- Produces: `config.Load(args []string, getenv func(string) string) (config.Config, error)`
- Produces: `textsafe.OneLine(string) string`
- Produces: shared `model.Message`, `model.Contact`, and `model.Status` types

- [ ] **Step 1: Create the Go module metadata**

```go
module github.com/s0up4200/bluepost

go 1.27.0

require (
	github.com/godbus/dbus/v5 v5.2.2
	github.com/spf13/cobra v1.10.2
)
```

Run: `go mod download`

Expected: Go downloads the pinned module graph and creates `go.sum`.

- [ ] **Step 2: Write failing configuration and terminal-safety tests**

```go
func TestLoadRejectsUntrustedPhoneSyntax(t *testing.T) {
	_, err := Load([]string{"--phone", "AA:BB;rm -rf"}, func(string) string { return "" })
	if err == nil { t.Fatal("expected invalid phone error") }
}

func TestOneLineRemovesTerminalControls(t *testing.T) {
	got := OneLine("hello\x1b[31m\nworld")
	if got != "hello[31m ⏎ world" { t.Fatalf("got %q", got) }
}
```

Run: `go test ./internal/config ./internal/textsafe`

Expected: FAIL because `Load` and `OneLine` do not exist.

- [ ] **Step 3: Implement the minimum shared foundation**

`config.Load` must accept `--phone` before `BLUEPOST_PHONE`.
It must require the canonical uppercase MAC form after normalization.
It must reject missing `XDG_RUNTIME_DIR` and unsafe XDG roots.

`textsafe.OneLine` must remove ESC and all Unicode control characters.
It must replace each line break with ` ⏎ `.

The model file must define JSON-tagged records without behavior:

```go
type Message struct {
	Handle          string    `json:"handle"`
	SenderAddress   string    `json:"sender_address"`
	SenderPhoneNorm string    `json:"sender_phone_norm"`
	ContactName     string    `json:"contact_name,omitempty"`
	Body            string    `json:"body"`
	Timestamp       time.Time `json:"timestamp"`
	Read            bool      `json:"read"`
}

type Contact struct {
	Name   string   `json:"name"`
	Phones []string `json:"phones"`
	Emails []string `json:"emails"`
}

type Status struct {
	State        string `json:"state"`
	Detail       string `json:"detail"`
	MAP          bool   `json:"map"`
	PBAP         bool   `json:"pbap"`
	Storage      string `json:"storage"`
	Phone        string `json:"phone"`
	HistoryCount int    `json:"history_count"`
	ContactCount int    `json:"contact_count"`
}
```

The protocol file must contain the exact names and limits from the design spec.

- [ ] **Step 4: Make sure that the tests pass**

Run: `go test ./internal/config ./internal/textsafe`

Expected: PASS.

- [ ] **Step 5: Commit after separate authorization**

```bash
git add -- go.mod go.sum internal/config internal/model internal/protocol internal/textsafe
git commit -m "feat: add Bluepost project foundation"
```

### Task 2: MAP and PBAP Parsers

**Files:**
- Create: `internal/mapmsg/bmessage_test.go`
- Create: `internal/mapmsg/bmessage.go`
- Create: `internal/pbap/vcard_test.go`
- Create: `internal/pbap/vcard.go`

**Interfaces:**
- Produces: `mapmsg.Parse(io.Reader, int64) (mapmsg.Message, error)`
- Produces: `pbap.Parse(io.Reader, pbap.Limits) ([]model.Contact, error)`
- Produces: `pbap.DefaultLimits() pbap.Limits`
- Produces: `pbap.NormalizeAddress(string) (string, pbap.AddressKind)`

- [ ] **Step 1: Write a failing bMessage parser test**

Use a literal fixture with `BEGIN:BMSG`, an originator vCard, `BBODY`, and `BEGIN:MSG`.
The fixture must include `TEL:+47 123 45 678` and a byte-stuffed ` END:MSG` body line.

```go
got, err := Parse(strings.NewReader(fixture), 1<<20)
if err != nil { t.Fatal(err) }
if got.Sender != "+47 123 45 678" { t.Fatalf("sender %q", got.Sender) }
if got.Body != "hello\nEND:MSG" { t.Fatalf("body %q", got.Body) }
```

Run: `go test ./internal/mapmsg`

Expected: FAIL because `Parse` does not exist.

- [ ] **Step 2: Implement the bounded bMessage parser**

Use `io.LimitReader` with one extra byte to detect an oversized input.
Track the structural sections explicitly.
Reject a missing `BMSG`, `BBODY`, or `MSG` terminator.
Prefer `TEL` to `EMAIL` when both occur in the originator vCard.

- [ ] **Step 3: Make sure that MAP parser tests pass**

Run: `go test ./internal/mapmsg`

Expected: PASS for valid, malformed, missing-terminator, and oversized fixtures.

- [ ] **Step 4: Write failing vCard parser tests**

```go
const cards = "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Jane\\, Doe\r\n" +
	"TEL;TYPE=CELL:+47 123 45 678\r\nEMAIL:jane@EXAMPLE.COM\r\nEND:VCARD\r\n"

got, err := Parse(strings.NewReader(cards), DefaultLimits())
if err != nil { t.Fatal(err) }
want := model.Contact{Name: "Jane, Doe", Phones: []string{"4712345678"}, Emails: []string{"jane@example.com"}}
if !slices.Equal(got[0].Phones, want.Phones) { t.Fatalf("phones %q", got[0].Phones) }
if !slices.Equal(got[0].Emails, want.Emails) { t.Fatalf("emails %q", got[0].Emails) }
if got[0].Name != want.Name { t.Fatalf("name %q", got[0].Name) }
```

Add fixtures for folded lines, ambiguous names, oversized cards, and invalid email addresses.

Run: `go test ./internal/pbap`

Expected: FAIL because the PBAP parser does not exist.

- [ ] **Step 5: Implement the bounded vCard parser**

Parse `FN`, `TEL`, and `EMAIL` only.
Unfold lines that start with a space or tab.
Decode `\\n`, `\\,`, `\\;`, and `\\\\` escapes.
Keep the first occurrence of each normalized address.

- [ ] **Step 6: Make sure that parser tests pass**

Run: `go test ./internal/mapmsg ./internal/pbap`

Expected: PASS.

- [ ] **Step 7: Commit after separate authorization**

```bash
git add -- internal/mapmsg internal/pbap
git commit -m "feat: parse bounded MAP and PBAP data"
```

### Task 3: Keyring and Encrypted Snapshots

**Files:**
- Create: `internal/storage/keyring_test.go`
- Create: `internal/storage/keyring.go`
- Create: `internal/storage/snapshot_test.go`
- Create: `internal/storage/snapshot.go`

**Interfaces:**
- Produces: `storage.Keyring.Key(ctx context.Context, stateExists bool) ([32]byte, error)`
- Produces: `storage.Snapshot.Load(purpose string) ([]byte, error)`
- Produces: `storage.Snapshot.Save(purpose string, plaintext []byte) error`

- [ ] **Step 1: Write failing keyring behavior tests**

Use a test command runner that records arguments and supplies controlled standard output.
Assert behavior through the returned key and error, not through mock call counts.

Cover these cases:

- A valid stored Base64 key returns exactly 32 bytes.
- A missing key and existing state returns `ErrLocked`.
- A missing key and new state generates, stores, and reads back a key.
- Empty, malformed, or late command output returns `ErrLocked`.
- The generated secret never appears in command arguments or environment values.

Run: `go test ./internal/storage -run Keyring`

Expected: FAIL because `Keyring.Key` does not exist.

- [ ] **Step 2: Implement the fixed-path Secret Service adapter**

Use `/usr/bin/secret-tool lookup application bluepost purpose storage-v1`.
Use `secret-tool store --label=Bluepost storage key application bluepost purpose storage-v1` for creation.
Pass the Base64 key through standard input and apply a 15-second context deadline.

- [ ] **Step 3: Make sure that keyring tests pass**

Run: `go test ./internal/storage -run Keyring`

Expected: PASS.

- [ ] **Step 4: Write failing authenticated-snapshot tests**

```go
if err := store.Save("history-v1", []byte(`{"schema":1}`)); err != nil { t.Fatal(err) }
got, err := store.Load("history-v1")
if err != nil { t.Fatal(err) }
if string(got) != `{"schema":1}` { t.Fatalf("got %q", got) }
```

Add independent tests that flip one ciphertext bit, exchange file purposes, and use the wrong key.
Each mutation must return `ErrLocked` and no plaintext.
Add a test that leaves an old snapshot intact when the replacement write fails before rename.
Add tests that reject a symbolic-link state path, a symbolic-link snapshot, and group-readable snapshot modes.

Run: `go test ./internal/storage -run Snapshot`

Expected: FAIL because `Snapshot.Load` and `Snapshot.Save` do not exist.

- [ ] **Step 5: Implement the AES-256-GCM snapshot format**

Use this header:

```go
var magic = [8]byte{'B', 'L', 'U', 'E', 'P', 'O', 'S', 'T'}
const formatVersion byte = 1
```

Use a fresh 12-byte nonce for every save.
Use `bluepost:<purpose>:v1` as additional authenticated data.
Create temporary files with `os.O_CREATE|os.O_EXCL|os.O_WRONLY` and mode `0600`.
Synchronize the file and parent directory around the rename.
Reject symbolic links, unsafe modes, wrong owners, oversized files, and authentication errors.

- [ ] **Step 6: Make sure that storage tests pass**

Run: `go test ./internal/storage`

Expected: PASS.

- [ ] **Step 7: Commit after separate authorization**

```bash
git add -- internal/storage
git commit -m "feat: add fail-closed encrypted storage"
```

### Task 4: Bounded History and Contact Repository

**Files:**
- Create: `internal/storage/repository_test.go`
- Create: `internal/storage/repository.go`

**Interfaces:**
- Produces: `storage.Repository.Open() error`
- Produces: `storage.Repository.AppendMessage(model.Message) error`
- Produces: `storage.Repository.Messages(limit int) []model.Message`
- Produces: `storage.Repository.ReplaceContacts([]model.Contact) error`
- Produces: `storage.Repository.FindContacts(string, int) []model.Contact`

- [ ] **Step 1: Write failing repository tests**

Use a real temporary directory and the real snapshot implementation.
Use a fixed literal key.

Cover these user-visible behaviors:

- A reopened repository returns the same messages and contacts.
- The 2,001st message removes the oldest message.
- The 64 MiB plaintext bound removes old messages before save.
- A failed contact replacement preserves the prior contact snapshot.
- Phone and case-insensitive email queries return the correct contact.
- One ambiguous name returns each matching contact without choosing one.

Run: `go test ./internal/storage -run Repository`

Expected: FAIL because `Repository` does not exist.

- [ ] **Step 2: Implement the in-memory repository**

Keep one mutex around the two bounded slices.
Marshal versioned JSON and apply limits before each snapshot save.
Copy slices on public reads.
Do a linear contact scan because the contact set is bounded.

Add this ceiling comment at the scan:

```go
// ponytail: Linear search is bounded by the PBAP card limit; add an index only after measured latency requires it.
```

- [ ] **Step 3: Make sure that repository tests pass**

Run: `go test ./internal/storage`

Expected: PASS.

- [ ] **Step 4: Commit after separate authorization**

```bash
git add -- internal/storage/repository.go internal/storage/repository_test.go
git commit -m "feat: store bounded private records"
```

### Task 5: D-Bus Transport, OBEX Worker, and Sessions

**Files:**
- Create: `internal/obex/transport.go`
- Create: `internal/obex/worker_test.go`
- Create: `internal/obex/worker.go`
- Create: `internal/obex/sessions_test.go`
- Create: `internal/obex/sessions.go`

**Interfaces:**
- Produces: `obex.Transport`, with bounded calls and D-Bus signal subscriptions
- Produces: `obex.DBusTransport`, which implements `obex.Transport` with godbus
- Produces: `obex.Worker.Submit(context.Context, func(context.Context) error) error`
- Produces: `obex.Sessions.Open(context.Context, string) error`
- Produces: `obex.Sessions.MapPath() (dbus.ObjectPath, bool)` and `PBAPPath()`

```go
type Transport interface {
	Call(context.Context, string, dbus.ObjectPath, string, ...any) ([]any, error)
	Subscribe(context.Context, ...dbus.MatchOption) (<-chan *dbus.Signal, func(), error)
}
```

- [ ] **Step 1: Write failing worker tests**

Submit two operations that block on test channels.
Make sure that the second operation does not start before the first operation completes.
Add a test that rejects operation 257 without blocking.

Run: `go test ./internal/obex -run Worker`

Expected: FAIL because `Worker` does not exist.

- [ ] **Step 2: Implement the bounded single-owner worker**

Use one goroutine and one buffered channel with capacity 256.
Return the caller context error when it expires.
Stop the worker without accepting new work.

- [ ] **Step 3: Write failing session call-contract tests**

Use a recording transport that returns complete BlueZ response bodies.
Make sure that MAP uses `Target="MAP"` and PBAP uses `Target="PBAP"`.
Make sure that the target phone address is present in each `CreateSession` call.
Make sure that a partial open removes only the created live session.
Make sure that a disappeared session is discarded without `RemoveSession`.
Return `Paired=false` or `Trusted=false` from the system-bus fixture and assert that no OBEX session opens.

Run: `go test ./internal/obex -run Sessions`

Expected: FAIL because `Sessions` does not exist.

- [ ] **Step 4: Implement the godbus adapter and session owner**

Connect to the system bus for `org.bluez` device validation.
Connect to the session bus for `org.bluez.obex`.
Call `org.bluez.obex.Client1.CreateSession` with the exact target options.
Watch `InterfacesRemoved` and `NameOwnerChanged` for loss.

- [ ] **Step 5: Make sure that worker and session tests pass**

Run: `go test ./internal/obex`

Expected: PASS.

- [ ] **Step 6: Commit after separate authorization**

```bash
git add -- internal/obex/transport.go internal/obex/worker.go internal/obex/worker_test.go internal/obex/sessions.go internal/obex/sessions_test.go
git commit -m "feat: own serialized OBEX sessions"
```

### Task 6: MAP Events, Live Queries, and PBAP Synchronization

**Files:**
- Create: `internal/obex/map_test.go`
- Create: `internal/obex/map.go`
- Create: `internal/obex/pbap_test.go`
- Create: `internal/obex/pbap.go`

**Interfaces:**
- Produces: `obex.MAP.HandleAdded(context.Context, dbus.ObjectPath, map[string]map[string]dbus.Variant) (model.Message, error)`
- Produces: `obex.MAP.Watch(context.Context, func(model.Message) error) error`
- Produces: `obex.MAP.ListRecent(context.Context, string, uint32) ([]model.Message, error)`
- Produces: `obex.PBAP.Sync(context.Context) ([]model.Contact, error)`

- [ ] **Step 1: Write failing MAP path and transfer tests**

Use a MAP session path of `/org/bluez/obex/client/session1`.
Reject a `Message1` path below any other session.

For an accepted path, make the fake transport write a literal bMessage to the requested runtime file.
Assert the returned sender, body, timestamp, read state, and opaque handle.
Assert that the runtime file no longer exists after success and after a parse error.
Send one complete `InterfacesAdded` signal to `MAP.Watch` and assert the saved message result.

Run: `go test ./internal/obex -run MAP`

Expected: FAIL because `MAP` does not exist.

- [ ] **Step 2: Implement MAP event retrieval and live queries**

Call `Message1.Get(path, false)` for event bodies.
Wait no more than 120 seconds for a transfer.
Allow a two-second file-visibility grace period after transfer disappearance.

For live queries, reset the folder with `SetFolder("/")`.
Then set `telecom`, `msg`, and `inbox` or `sent` one segment at a time.
Call `ListMessages("", {"MaxListCount": uint16(limit)})`.

- [ ] **Step 3: Write failing PBAP transfer tests**

Make the fake transport require this exact option map:

```go
map[string]dbus.Variant{
	"Format":   dbus.MakeVariant("vcard30"),
	"MaxCount": dbus.MakeVariant(uint16(65535)),
}
```

Assert that a complete transfer replaces contacts.
Assert that an explicit transfer error returns no new contacts.
Assert that the temporary phonebook file is removed in both cases.

Run: `go test ./internal/obex -run PBAP`

Expected: FAIL because `PBAP` does not exist.

- [ ] **Step 4: Implement PBAP synchronization**

Call `PhonebookAccess1.Select("int", "pb")`.
Call `PullAll` with the exact tested option names.
Reject files larger than 64 MiB before parser allocation.

- [ ] **Step 5: Make sure that profile operation tests pass**

Run: `go test ./internal/obex`

Expected: PASS.

- [ ] **Step 6: Commit after separate authorization**

```bash
git add -- internal/obex/map.go internal/obex/map_test.go internal/obex/pbap.go internal/obex/pbap_test.go
git commit -m "feat: receive MAP messages and PBAP contacts"
```

### Task 7: Backend and Read-Only D-Bus API

**Files:**
- Create: `internal/backend/backend_test.go`
- Create: `internal/backend/backend.go`
- Create: `internal/bus/server_test.go`
- Create: `internal/bus/server.go`
- Create: `internal/bus/client_test.go`
- Create: `internal/bus/client.go`

**Interfaces:**
- Produces: `backend.Backend.Status() model.Status`
- Produces: `backend.Backend.Healthy() bool`
- Produces: `backend.Backend.ListEvents([]string, uint32) ([]model.Message, error)`
- Produces: `backend.Backend.ListRecent(context.Context, string, uint32) ([]model.Message, error)`
- Produces: `backend.Backend.FindContacts(string) ([]model.Contact, error)`
- Produces: `backend.Backend.ListContacts(uint32, uint32) ([]model.Contact, error)`
- Produces: `backend.Backend.SyncContacts(context.Context) (uint32, error)`
- Produces: `bus.Serve(context.Context, *dbus.Conn, *backend.Backend) error`
- Produces: `bus.API` for the Cobra CLI
- Produces: `bus.Client`, which implements `bus.API`

```go
type API interface {
	Status(context.Context) (model.Status, error)
	Healthy(context.Context) (bool, error)
	Events(context.Context, []string, uint32) ([]model.Message, error)
	Recent(context.Context, string, uint32) ([]model.Message, error)
	FindContacts(context.Context, string) ([]model.Contact, error)
	Contacts(context.Context, uint32, uint32) ([]model.Contact, error)
	SyncContacts(context.Context) (uint32, error)
}
```

- [ ] **Step 1: Write a failing fail-closed startup test**

Use a key source that returns `storage.ErrLocked`.
Assert that the backend status is `locked`.
Assert that the fake OBEX opener receives no call.

Add a ready-path test that sends one MAP event through the backend.
Assert that the repository contains the resolved contact name and message body.
Add tests for the 5-second initial retry and the 15-second reconnect retry.
Assert that status masks the first four MAC octets and contains no private message data.

Run: `go test ./internal/backend`

Expected: FAIL because `Backend` does not exist.

- [ ] **Step 2: Implement backend coordination and reconnect states**

Start storage before Bluetooth.
Submit every profile operation to the worker.
Use 5-second retries before first success and 15-second retries after connection loss.
Increment one local revision after each saved message or complete contact replacement.

- [ ] **Step 3: Write failing D-Bus contract and caller tests**

Use a private test bus when available.
Otherwise test the exported object through a transport seam.

Assert these exact method signatures:

```text
GetStatus() -> s
IsHealthy() -> b
ListEvents(as, u) -> s
ListRecent(s, u) -> s
FindContacts(s) -> s
ListContacts(u, u) -> s
SyncContacts() -> u
```

Return a caller user ID that differs from `os.Getuid()`.
Assert a named `io.github.s0up4200.Bluepost.Error.AccessDenied` error.
Assert that no API method exposes send, erase, policy, or unlock operations.
Make a response exceed 8 MiB and assert a named `ResponseTooLarge` error without private content.

Run: `go test ./internal/bus`

Expected: FAIL because the server and client do not exist.

- [ ] **Step 4: Implement the D-Bus server and client**

Claim `io.github.s0up4200.Bluepost` without queueing.
Export the Messages1 object at `/io/github/s0up4200/Bluepost`.
Resolve each `dbus.Sender` with `GetConnectionUnixUser` before backend access.
Serialize JSON with an 8 MiB hard limit.
Emit only `HistoryChanged` revision data and empty `StatusChanged` signals.

- [ ] **Step 5: Make sure that backend and D-Bus tests pass**

Run: `go test ./internal/backend ./internal/bus`

Expected: PASS.

- [ ] **Step 6: Commit after separate authorization**

```bash
git add -- internal/backend internal/bus
git commit -m "feat: expose the read-only Bluepost service"
```

### Task 8: Cobra CLI and Executables

**Files:**
- Create: `internal/cli/root_test.go`
- Create: `internal/cli/root.go`
- Create: `cmd/bluepost/main.go`
- Create: `cmd/bluepostd/main.go`

**Interfaces:**
- Produces: `cli.New(client bus.API, stdout, stderr io.Writer) *cobra.Command`
- Produces: the `bluepost` and `bluepostd` binaries

- [ ] **Step 1: Write failing command behavior tests**

Invoke real Cobra commands with in-memory output and a small fake D-Bus API.
Assert these behaviors:

- `status` shows locked and ready states without private details.
- `messages --limit 0` returns a usage error before a D-Bus call.
- `messages --iphone` calls the live inbox method.
- `contacts jane` shows matching names and normalized addresses.
- `contacts sync` shows the returned contact count.
- A sender containing `\x1b[31m` cannot change terminal state.

Run: `go test ./internal/cli`

Expected: FAIL because `cli.New` does not exist.

- [ ] **Step 2: Implement the Cobra tree**

Use this command surface only:

```text
bluepost status
bluepost messages [--limit N] [--iphone]
bluepost contacts [query]
bluepost contacts sync
```

Set `SilenceUsage=true` after argument validation.
Write plain text through the supplied writers.
Do not add Viper, color, prompts, completion generation, or JSON output.

- [ ] **Step 3: Implement the two small entry points**

`cmd/bluepost/main.go` connects to the session bus and invokes `cli.New`.
`cmd/bluepostd/main.go` loads daemon configuration and handles SIGINT and SIGTERM.
Both entry points must map errors to nonzero exit codes without printing private content.

- [ ] **Step 4: Make sure that CLI and build checks pass**

Run: `go test ./internal/cli`

Expected: PASS.

Run: `go build ./cmd/bluepost ./cmd/bluepostd`

Expected: PASS with no output.

- [ ] **Step 5: Commit after separate authorization**

```bash
git add -- internal/cli cmd/bluepost cmd/bluepostd
git commit -m "feat: add the Bluepost daemon and CLI"
```

### Task 9: Documentation, Audit, and Complete Verification

**Files:**
- Create: `README.md`
- Modify: any source or test file only when a verification command exposes a defect

**Interfaces:**
- Produces: source-build and run instructions for the complete MVP

- [ ] **Step 1: Write the English README**

Document these facts:

- Bluepost cannot distinguish SMS from iMessage on MAP.
- The phone must already be paired, trusted, and configured for message and contact synchronization.
- `bluez-obex`, GNOME Keyring, and BlueZ 5.86 or later are runtime prerequisites.
- The current machine does not have `bluez-obex` installed.
- Source operation uses `BLUEPOST_PHONE=AA:BB:CC:DD:EE:FF go run ./cmd/bluepostd`.
- CLI operation uses `go run ./cmd/bluepost status` and the other approved commands.
- A same-user process can call the session D-Bus API and erase local files.
- AES-GCM detects unauthenticated file changes but does not prevent deletion or rollback.
- Bluepost makes no Internet connection and contains no telemetry.

- [ ] **Step 2: Audit the dependency graph and forbidden features**

Run: `go list -m all`

Expected direct modules: godbus and Cobra.
Expected Cobra transitive modules: pflag and mousetrap only when required by the pinned release.

Run: `go mod verify`

Expected: `all modules verified`.

Run: `rg -n "net/http|Listen\(|Send\(|PushMessage|ANCS|sqlite|telemetry|update check" --glob '*.go' .`

Expected: no production implementation of a forbidden feature.
Review all matches from tests or documentation manually.

- [ ] **Step 3: Run the complete automated verification**

Run: `gofmt -w cmd internal`

Run: `go test -race ./...`

Expected: PASS.

Run: `go vet ./...`

Expected: PASS with no output.

Run: `go build ./cmd/bluepost ./cmd/bluepostd`

Expected: PASS with no output.

Run: `git diff --check`

Expected: PASS with no output.

- [ ] **Step 4: Inspect the current hardware gate**

Run: `test -x /usr/lib/bluetooth/obexd || test -x /usr/libexec/bluetooth/obexd || command -v obexd`

If this command fails, report that the live MAP/PBAP check cannot run without `bluez-obex`.
Do not install the package without explicit user authorization.

If `obexd` is available, run the daemon from source against the configured paired phone.
Make sure that `status`, one live message query, and one contact synchronization work.

- [ ] **Step 5: Review the goal requirement by requirement**

Match each goal statement to source, tests, command output, or live runtime evidence.
Do not claim live Bluetooth support from fake transport tests alone.
Keep the goal active if the required hardware evidence is unavailable.

- [ ] **Step 6: Commit after separate authorization**

```bash
git add -- README.md
git commit -m "docs: document Bluepost security and operation"
```
