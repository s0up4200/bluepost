# Bluepost Omarchy Widget Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Start Bluepost at login and add a separate Omarchy widget with live connection status, five recent SMS messages, and smart clipboard actions.

**Architecture:** The existing daemon remains the only application daemon. A new `bluepost widget` command converts the existing D-Bus status, history, and signals into a JSON-lines stream for one user-owned Omarchy plugin. A systemd user service starts and restarts `bluepost daemon`.

**Tech Stack:** Go 1.27.0, Cobra, godbus, systemd user services, Quickshell QML, Omarchy plugin APIs, and `wl-copy`.

**Spec:** `docs/superpowers/specs/2026-08-20-bluepost-omarchy-widget-design.md`

## Global constraints

- Keep one `bluepost` binary. Do not add another Go executable or daemon.
- Keep the application read-only. Do not add send, reply, erase, pairing, or read-state actions.
- Keep `omarchy.bluetooth` unchanged.
- Implement the widget as `io.github.s0up4200.bluepost`.
- Keep all code, UI text, documentation, and commit messages in English.
- Do not copy GPL-2.0-only source from BlueFerry or `omarchy-blueferry`.
- Send message content to `wl-copy` through standard input. Never place message content in process arguments.
- Use the existing OTP detector. Do not add a detector in QML.
- Keep the configured Bluetooth address out of tracked files and widget output.
- Preserve the unrelated untracked `.gitignore` file.
- Use plain `git commit --no-gpg-sign`. Do not override the Git author identity.

## File structure

- Create `internal/widget/stream.go` for snapshot construction and the JSON-lines event loop.
- Create `internal/widget/stream_test.go` for snapshot, signal, ordering, and clipboard-selection tests.
- Create `cmd/bluepost/widget.go` for the Cobra command and D-Bus signal subscription.
- Create `cmd/bluepost/widget_test.go` for command and subscription-error behavior.
- Modify `cmd/bluepost/main.go` to register the widget command with the existing client connection.
- Modify `cmd/bluepost/main_test.go` to preserve the help and daemon connection rules.
- Create `contrib/omarchy-bluepost/manifest.json` for the separate plugin manifest.
- Create `contrib/omarchy-bluepost/Panel.qml` for the bar icon, popup, stream reader, reconnect action, and clipboard process.
- Create `contrib/systemd/bluepost.service` for login start and process restart.
- Modify `README.md` with user-service and Omarchy plugin instructions.

---

### Task 1: Build the widget snapshot and event stream

**Files:**

- Create: `internal/widget/stream.go`
- Create: `internal/widget/stream_test.go`

**Interfaces:**

- Consumes: `model.Status`, `model.Message`, `otp.Extract`, `protocol.EventsIface`, and a narrow D-Bus source.
- Produces: `widget.Source`, `widget.Snapshot`, `widget.Message`, `widget.Run`, and `widget.ErrDaemonUnavailable`.

- [ ] **Step 1: Write the failing snapshot test**

Create `internal/widget/stream_test.go`. Use a fake source that implements only these methods:

```go
type fakeSource struct {
	status   model.Status
	messages []model.Message
	err      error
}

func (source *fakeSource) Status(context.Context) (model.Status, error) {
	return source.status, source.err
}

func (source *fakeSource) Events(context.Context, []string, uint32) ([]model.Message, error) {
	return append([]model.Message(nil), source.messages...), source.err
}
```

Add `TestBuildSortsFiveMessagesAndSelectsClipboardText`. Supply six unsorted messages. Include these two bodies:

```go
"Your Stripe verification code is 482731."
"Dinner is at seven."
```

Assert these results:

- The output contains five messages.
- The newest message is first.
- A contact name takes precedence over the sender address.
- The Stripe item has `copy_text` equal to `482731` and `copy_kind` equal to `code`.
- The normal item has its complete body in `copy_text` and `copy_kind` equal to `message`.
- The masked phone value from `model.Status` remains unchanged.

- [ ] **Step 2: Run the snapshot test and see the expected failure**

Run:

```bash
go test ./internal/widget -run TestBuildSortsFiveMessagesAndSelectsClipboardText -count=1
```

Expected result: FAIL because `Build` and the widget types do not exist.

- [ ] **Step 3: Implement the minimal snapshot builder**

Create `internal/widget/stream.go` with these public types and signatures:

```go
package widget

type Source interface {
	Status(context.Context) (model.Status, error)
	Events(context.Context, []string, uint32) ([]model.Message, error)
}

type Message struct {
	Handle    string    `json:"handle"`
	Sender    string    `json:"sender"`
	Timestamp time.Time `json:"timestamp"`
	Body      string    `json:"body"`
	CopyText  string    `json:"copy_text"`
	CopyKind  string    `json:"copy_kind"`
}

type Snapshot struct {
	Status   model.Status `json:"status"`
	Messages []Message    `json:"messages"`
}

func Build(ctx context.Context, source Source) (Snapshot, error)
```

`Build` must request `[]string{"sms_received"}` with a limit of `5`. Copy the returned slice before sorting it newest first. Use `ContactName`, then `SenderAddress`, then `Unknown sender` for the display sender. Use `otp.Extract` once per message.

Return `Could not get Bluepost status` for a status error. Return `Could not get recent messages` for a history error. Do not wrap either source error because a remote error can contain private text.

- [ ] **Step 4: Run the snapshot test and see it pass**

Run:

```bash
go test ./internal/widget -run TestBuildSortsFiveMessagesAndSelectsClipboardText -count=1
```

Expected result: PASS.

- [ ] **Step 5: Write failing stream tests**

Add these tests to `internal/widget/stream_test.go`:

```go
func TestRunWritesInitialSnapshotAndHistoryUpdate(t *testing.T)
func TestRunWritesStatusUpdate(t *testing.T)
func TestRunStopsWhenDaemonLosesName(t *testing.T)
func TestRunReturnsSourceErrorWithoutPrivateOutput(t *testing.T)
```

Use a buffered `chan *dbus.Signal`. Use these signal names:

```go
protocol.EventsIface + ".HistoryChanged"
protocol.EventsIface + ".StatusChanged"
"org.freedesktop.DBus.NameOwnerChanged"
```

For the owner-loss signal, use this body:

```go
[]any{protocol.BusName, ":1.42", ""}
```

Close the signal channel after the expected update. Decode each output line with `json.Decoder`. Assert that `Run` writes the initial snapshot before it waits for a signal. Assert that an owner loss returns `ErrDaemonUnavailable`.

- [ ] **Step 6: Run the stream tests and see the expected failure**

Run:

```bash
go test ./internal/widget -run 'TestRun' -count=1
```

Expected result: FAIL because `Run` and `ErrDaemonUnavailable` do not exist.

- [ ] **Step 7: Implement the JSON-lines loop**

Add these declarations:

```go
var ErrDaemonUnavailable = errors.New("Bluepost daemon is unavailable")

func Run(
	ctx context.Context,
	source Source,
	signals <-chan *dbus.Signal,
	output io.Writer,
) error
```

`Run` must do these operations:

1. Build and encode the initial snapshot with `json.NewEncoder(output)`.
2. Wait for context cancellation or a D-Bus signal.
3. Build and encode a new snapshot for `HistoryChanged` and `StatusChanged`.
4. Return `ErrDaemonUnavailable` when `NameOwnerChanged` reports an empty new owner for `protocol.BusName`.
5. Return a short error if the signal channel closes.
6. Return source and encoding errors without message content.

Wrap each `Build` call in a ten-second child context. Do not buffer or batch signals until a measured event storm requires it.

- [ ] **Step 8: Run the widget package tests**

Run:

```bash
go test ./internal/widget -count=1
go test -race ./internal/widget -count=1
```

Expected result: PASS.

- [ ] **Step 9: Commit the stream**

```bash
git add -- internal/widget/stream.go internal/widget/stream_test.go
git commit --no-gpg-sign -m "feat: add live widget stream"
```

---

### Task 2: Add the Cobra widget command and D-Bus subscription

**Files:**

- Create: `cmd/bluepost/widget.go`
- Create: `cmd/bluepost/widget_test.go`
- Modify: `cmd/bluepost/main.go`
- Modify: `cmd/bluepost/main_test.go`

**Interfaces:**

- Consumes: `widget.Source`, `widget.Run`, the session `*dbus.Conn`, and the existing `bus.Client`.
- Produces: `bluepost widget`, `newWidgetCommand`, and `subscribeWidgetSignals`.

- [ ] **Step 1: Write the failing Cobra command tests**

Create `cmd/bluepost/widget_test.go` with these cases:

```go
func TestWidgetCommandStreamsSnapshots(t *testing.T)
func TestWidgetCommandReportsSubscriptionFailure(t *testing.T)
func TestWidgetCommandCancelsSubscription(t *testing.T)
```

Define this test seam in the expected API:

```go
type subscribeFunc func(context.Context) (<-chan *dbus.Signal, func(), error)

func newWidgetCommand(source widget.Source, subscribe subscribeFunc) *cobra.Command
```

For the success case, return a signal channel that contains one owner-loss signal. Assert that the command writes one valid JSON line before it returns. For the cleanup case, assert that the returned cancel function runs once.

- [ ] **Step 2: Run the command tests and see the expected failure**

Run:

```bash
go test ./cmd/bluepost -run TestWidgetCommand -count=1
```

Expected result: FAIL because `newWidgetCommand` does not exist.

- [ ] **Step 3: Implement the Cobra command**

Create `cmd/bluepost/widget.go`. The command must have this shape:

```go
func newWidgetCommand(source widget.Source, subscribe subscribeFunc) *cobra.Command {
	return &cobra.Command{
		Use:   "widget",
		Short: "Stream status and recent messages for desktop widgets",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			command.Root().SilenceUsage = true
			signals, cancel, err := subscribe(command.Context())
			if err != nil {
				return errors.New("Could not subscribe to Bluepost D-Bus events")
			}
			defer cancel()
			return widget.Run(command.Context(), source, signals, command.OutOrStdout())
		},
	}
}
```

Keep internal errors out of the user-facing subscription error.

- [ ] **Step 4: Implement the production signal subscription**

Add this signature:

```go
func subscribeWidgetSignals(
	ctx context.Context,
	connection *dbus.Conn,
) (<-chan *dbus.Signal, func(), error)
```

Register one buffered signal channel on the connection. Add both match rules below to that channel:

```go
[]dbus.MatchOption{
	dbus.WithMatchObjectPath(dbus.ObjectPath(protocol.ObjectPath)),
	dbus.WithMatchInterface(protocol.EventsIface),
}

[]dbus.MatchOption{
	dbus.WithMatchObjectPath(dbus.ObjectPath("/org/freedesktop/DBus")),
	dbus.WithMatchInterface("org.freedesktop.DBus"),
	dbus.WithMatchMember("NameOwnerChanged"),
	dbus.WithMatchArg(0, protocol.BusName),
}
```

If the second match fails, remove the first match and the signal channel. Return an idempotent cancel function that removes both matches with a two-second cleanup context.

- [ ] **Step 5: Register the command with the shared client connection**

In `cmd/bluepost/main.go`, create the command before `Execute`:

```go
widgetCommand := newWidgetCommand(client, func(ctx context.Context) (<-chan *dbus.Signal, func(), error) {
	return subscribeWidgetSignals(ctx, connection)
})
command.AddCommand(daemon, widgetCommand)
```

Keep the existing persistent pre-run rule. The widget command uses the normal client D-Bus connection. Only the daemon skips that connection.

- [ ] **Step 6: Extend the root command tests**

Add `TestWidgetHelpDoesNotConnectToDBus` to `cmd/bluepost/main_test.go`. Run `bluepost widget --help` through `run` and assert zero connection attempts.

Keep `TestDaemonDoesNotConnectToClientDBus` unchanged. It guards the one-daemon architecture.

- [ ] **Step 7: Run the command tests**

Run:

```bash
go test ./cmd/bluepost -count=1
go test -race ./cmd/bluepost -count=1
```

Expected result: PASS.

- [ ] **Step 8: Run a private D-Bus integration check**

Build the binary and start the existing daemon in one terminal. Then run:

```bash
go build -o /tmp/bluepost-widget-test ./cmd/bluepost
timeout 3s /tmp/bluepost-widget-test widget
```

Expected result: at least one JSON line with `status` and `messages`. The timeout can exit with status `124` because the stream remains open.

- [ ] **Step 9: Commit the command**

```bash
git add -- cmd/bluepost/main.go cmd/bluepost/main_test.go cmd/bluepost/widget.go cmd/bluepost/widget_test.go
git commit --no-gpg-sign -m "feat: expose widget event stream"
```

---

### Task 3: Build the separate Omarchy plugin

**Files:**

- Create: `contrib/omarchy-bluepost/manifest.json`
- Create: `contrib/omarchy-bluepost/Panel.qml`

**Interfaces:**

- Consumes: the JSON lines from `bluepost widget`, `/usr/bin/wl-copy`, and `bluepost.service`.
- Produces: the `io.github.s0up4200.bluepost` bar widget.

- [ ] **Step 1: Create and validate the manifest first**

Create this exact `contrib/omarchy-bluepost/manifest.json`:

```json
{
  "schemaVersion": 1,
  "id": "io.github.s0up4200.bluepost",
  "name": "Bluepost",
  "version": "0.1.0",
  "author": "s0up4200",
  "description": "Show iPhone connection health and recent SMS messages",
  "kinds": ["bar-widget"],
  "entryPoints": {"barWidget": "Panel.qml"},
  "barWidget": {
    "displayName": "Bluepost",
    "description": "Shows iPhone connection health and recent SMS messages",
    "category": "Network",
    "allowMultiple": false,
    "defaultSection": "right"
  }
}
```

Run:

```bash
omarchy plugin validate contrib/omarchy-bluepost
```

Expected result: FAIL because `Panel.qml` does not exist. This is the plugin red test.

- [ ] **Step 2: Create the minimal panel state and stream reader**

Create `contrib/omarchy-bluepost/Panel.qml` with these imports and root properties:

```qml
pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

Panel {
  id: root
  moduleName: "io.github.s0up4200.bluepost"
  ipcTarget: "io.github.s0up4200.bluepost"
  manageIpc: false

  property var status: ({state: "stopped", map: false, pbap: false, storage: "locked"})
  property var messages: []
  property string feedback: ""
  property bool streamReady: false
  property bool cursorActive: false
  property int cursorIndex: 0

  readonly property string bluepostBinary: Quickshell.env("HOME") + "/.local/bin/bluepost"
  readonly property bool connected: streamReady && status.state === "ready" && status.map === true
  readonly property string summary: !streamReady ? "Daemon stopped"
    : connected ? "Connected" : "Reconnecting"
}
```

Add `acceptLine(line)`. Parse JSON in `try/catch`. Require an object-valued `status` and an array-valued `messages`. Replace the current values only after both checks pass. Set `streamReady` to true and clear old errors after a valid line.

Add this long-running process and restart timer:

```qml
Process {
  id: widgetStream
  running: true
  command: [root.bluepostBinary, "widget"]
  stdout: SplitParser {
    onRead: function(line) { root.acceptLine(line) }
  }
  onExited: {
    root.streamReady = false
    root.status = ({state: "stopped", map: false, pbap: false, storage: "locked"})
    streamRestart.restart()
  }
}

Timer {
  id: streamRestart
  interval: 5000
  repeat: false
  onTriggered: widgetStream.running = true
}
```

- [ ] **Step 3: Add safe clipboard and reconnect actions**

Add `copyMessage(message)` and one reusable clipboard process:

```qml
function copyMessage(message) {
  var text = String(message.copy_text || "")
  if (text === "") return
  clipboard.payload = text
  clipboard.kind = String(message.copy_kind || "message")
  clipboard.stdinEnabled = true
  clipboard.exec(["/usr/bin/wl-copy", "--sensitive", "--trim-newline"])
}

Process {
  id: clipboard
  property string payload: ""
  property string kind: "message"
  stdinEnabled: true
  onStarted: {
    write(payload)
    payload = ""
    stdinEnabled = false
  }
  onExited: function(exitCode) {
    root.feedback = exitCode === 0
      ? (kind === "code" ? "Code copied" : "Message copied")
      : "Copy failed"
    feedbackTimer.restart()
  }
}
```

Setting `stdinEnabled` to false closes the running process input. Set it to true before each new process starts.

Add the feedback timer:

```qml
Timer {
  id: feedbackTimer
  interval: 2500
  repeat: false
  onTriggered: root.feedback = ""
}
```

Add this reconnect function:

```qml
function reconnect() {
  feedback = "Reconnecting"
  Quickshell.execDetached(["/usr/bin/systemctl", "--user", "restart", "bluepost.service"])
}
```

Do not use `sh -c` in either action.

- [ ] **Step 4: Add the bar button and popup**

Use `BarIconButton`, `KeyboardPanel`, `PanelKeyCatcher`, `PanelHero`, `PanelSeparator`, and `CursorSurface`. Match the installed Omarchy panel patterns.

Add the panel IPC handler:

```qml
IpcHandler {
  target: root.ipcTarget
  function open(): void { root.open() }
  function close(): void { root.close() }
  function toggle(): void { root.toggle() }
  function status(): string { return root.summary }
}
```

The popup must contain these items in order:

1. A hero with title `iPhone` and meta text from `summary`.
2. Status rows for `Messages`, `Contacts`, and `Storage`.
3. A short error or feedback line when `feedback` is not empty.
4. A `RECENT SMS` section with at most five rows.
5. `No messages yet` when the list is empty.
6. A `Reconnect now` row only when `connected` is false.

Use this message-row behavior:

```qml
MouseArea {
  anchors.fill: parent
  hoverEnabled: true
  cursorShape: Qt.PointingHandCursor
  onEntered: {
    root.cursorActive = true
    root.cursorIndex = row.index
  }
  onClicked: root.copyMessage(row.message)
}
```

Render the body with `textFormat: Text.PlainText`, `wrapMode: Text.Wrap`, and `maximumLineCount: 2`. The keyboard Enter action must call the same `copyMessage` function. Put `Reconnect now` after the message rows and include it in keyboard navigation.

The bar button uses a phone glyph, dims when disconnected, and has this tooltip:

```qml
tooltipText: "Bluepost: " + root.summary
```

- [ ] **Step 5: Validate the plugin and QML**

Run:

```bash
omarchy plugin validate contrib/omarchy-bluepost
qmllint -I /usr/share/omarchy/shell -I /usr/lib/qt6/qml contrib/omarchy-bluepost/Panel.qml
```

Expected result: both commands exit `0`. Resolve QML errors. Do not suppress real errors.

- [ ] **Step 6: Commit the plugin**

```bash
git add -- contrib/omarchy-bluepost/manifest.json contrib/omarchy-bluepost/Panel.qml
git commit --no-gpg-sign -m "feat: add Omarchy Bluepost widget"
```

---

### Task 4: Add the user service and installation documentation

**Files:**

- Create: `contrib/systemd/bluepost.service`
- Modify: `README.md`

**Interfaces:**

- Consumes: `%h/.local/bin/bluepost` and `%h/.config/bluepost/environment`.
- Produces: the fixed `bluepost.service` unit that the widget restarts.

- [ ] **Step 1: Create the service unit**

Create this exact `contrib/systemd/bluepost.service`:

```ini
[Unit]
Description=Bluepost iPhone message bridge
After=bluetooth.target

[Service]
Type=simple
EnvironmentFile=%h/.config/bluepost/environment
ExecStart=%h/.local/bin/bluepost daemon
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=default.target
```

- [ ] **Step 2: Verify the service syntax**

Run:

```bash
systemd-analyze --user verify contrib/systemd/bluepost.service
```

Expected result: exit `0` with no unknown directive or invalid specifier error. A warning about the not-yet-installed executable is acceptable before deployment.

- [ ] **Step 3: Update the README**

Replace the statement that no user service exists. Add an `Automatic start on Omarchy` procedure with these facts:

- The binary path is `~/.local/bin/bluepost`.
- The service path is `~/.config/systemd/user/bluepost.service`.
- The private environment path is `~/.config/bluepost/environment`.
- The environment file contains `BLUEPOST_PHONE=AA:BB:CC:DD:EE:FF` and has mode `0600`.
- `systemctl --user enable --now bluepost.service` starts the daemon at login.
- `systemctl --user status bluepost.service` and `bluepost status` show health.

Add an `Omarchy widget` procedure with these commands:

```bash
omarchy plugin validate contrib/omarchy-bluepost
omarchy-shell shell rescanPlugins
omarchy plugin enable io.github.s0up4200.bluepost --section right --before omarchy.bluetooth
```

Explain that the local plugin folder must exist at
`~/.config/omarchy/plugins/io.github.s0up4200.bluepost` before the rescan.

- [ ] **Step 4: Check the documentation and unit diff**

Run:

```bash
git diff --check -- README.md contrib/systemd/bluepost.service
```

Expected result: no output and exit `0`.

- [ ] **Step 5: Commit the service and documentation**

```bash
git add -- README.md contrib/systemd/bluepost.service
git commit --no-gpg-sign -m "docs: add automatic Bluepost startup"
```

---

### Task 5: Install and exercise the feature on this Omarchy machine

**Files:**

- Install: `~/.local/bin/bluepost`
- Install: `~/.config/systemd/user/bluepost.service`
- Create locally: `~/.config/bluepost/environment`
- Install: `~/.config/omarchy/plugins/io.github.s0up4200.bluepost/manifest.json`
- Install: `~/.config/omarchy/plugins/io.github.s0up4200.bluepost/Panel.qml`

**Interfaces:**

- Consumes: all tracked artifacts from Tasks 1 through 4.
- Produces: an enabled daemon service and visible Omarchy widget on the current machine.

- [ ] **Step 1: Build the exact binary that will be installed**

Run:

```bash
go build -trimpath -o /tmp/bluepost-install ./cmd/bluepost
/tmp/bluepost-install --help
```

Expected result: help lists `daemon`, `widget`, `status`, `messages`, and `contacts`.

- [ ] **Step 2: Install the binary, service, and plugin files**

Use `install` with explicit source and destination paths:

```bash
install -Dm755 /tmp/bluepost-install /home/soup/.local/bin/bluepost
install -Dm644 contrib/systemd/bluepost.service /home/soup/.config/systemd/user/bluepost.service
install -Dm644 contrib/omarchy-bluepost/manifest.json /home/soup/.config/omarchy/plugins/io.github.s0up4200.bluepost/manifest.json
install -Dm644 contrib/omarchy-bluepost/Panel.qml /home/soup/.config/omarchy/plugins/io.github.s0up4200.bluepost/Panel.qml
```

These commands need approval because they write outside the repository.

- [ ] **Step 3: Create the private phone environment**

Read the address from the existing paired and trusted iPhone. Do not add the address to a tracked file or command output in the plan.

Create `/home/soup/.config/bluepost/environment` with one line in this form:

```text
BLUEPOST_PHONE=AA:BB:CC:DD:EE:FF
```

Use the actual paired address during execution. Set the file mode to `0600`. Inspect the destination after installation without printing its contents.

- [ ] **Step 4: Replace the temporary foreground daemon with the user service**

Stop the known foreground Bluepost test process cleanly. Do not use a broad `pkill` pattern.

Then run:

```bash
systemctl --user daemon-reload
systemctl --user enable --now bluepost.service
systemctl --user is-enabled bluepost.service
systemctl --user is-active bluepost.service
/home/soup/.local/bin/bluepost status
```

Expected result: the service is `enabled` and `active`. Bluepost reaches `ready` with MAP and PBAP connected when the iPhone is in range.

- [ ] **Step 5: Load and enable the separate Omarchy plugin**

Run:

```bash
omarchy plugin validate /home/soup/.config/omarchy/plugins/io.github.s0up4200.bluepost
omarchy-shell shell rescanPlugins
omarchy plugin enable io.github.s0up4200.bluepost --section right --before omarchy.bluetooth
```

Expected result: the Bluepost icon appears before the stock Bluetooth icon. The stock plugin folder and plugin ID remain unchanged.

- [ ] **Step 6: Exercise reconnect behavior**

Run:

```bash
systemctl --user restart bluepost.service
timeout 20s sh -c 'until /home/soup/.local/bin/bluepost status | grep -q "State: ready"; do sleep 1; done'
```

Expected result: the status reaches `ready` within 20 seconds when the iPhone is available. The widget changes from `Reconnecting` to `Connected` without a Bluetooth-widget click.

- [ ] **Step 7: Exercise both clipboard paths**

Open the Bluepost widget. Click one stored OTP message and one normal SMS.

After each click, run:

```bash
wl-paste --no-newline
```

Expected result: the OTP click returns only its code. The normal message click returns its complete body. Do not include clipboard contents in logs or the final report.

---

### Task 6: Run final verification, `go fix`, and commit any mechanical update

**Files:**

- Verify: all tracked Go, QML, service, and documentation files.
- Modify only if needed: files changed by `go fix ./...`.

**Interfaces:**

- Consumes: the completed implementation and live installation.
- Produces: a verified branch with no uncommitted implementation changes.

- [ ] **Step 1: Run the complete automated verification**

Run:

```bash
go mod verify
go test ./...
go test -race ./...
go vet ./...
go build -trimpath -o /tmp/bluepost-final ./cmd/bluepost
omarchy plugin validate contrib/omarchy-bluepost
qmllint -I /usr/share/omarchy/shell -I /usr/lib/qt6/qml contrib/omarchy-bluepost/Panel.qml
systemd-analyze --user verify contrib/systemd/bluepost.service
git diff --check
```

Expected result: every command exits `0`.

- [ ] **Step 2: Run `go fix` after all feature commits**

Run:

```bash
go fix ./...
git status --short
git diff --check
```

If `go fix` changes tracked Go files, rerun the full Go verification and commit only those exact files:

```bash
git add -u -- ':(glob)**/*.go'
git commit --no-gpg-sign -m "chore: apply Go 1.27 fixes"
```

If `go fix` changes nothing, do not create an empty commit.

- [ ] **Step 3: Confirm the final worktree scope**

Run:

```bash
git status --short --branch
git log -6 --oneline --decorate
```

Expected result: the only untracked item is the pre-existing `.gitignore`. No implementation file remains modified or staged.

## Separate notification-latency diagnosis

The delayed 2FA notification is not a widget implementation task. After this
plan passes, use the `diagnosing-bugs` skill for one controlled SMS trace.

The trace must timestamp these events without logging message content:

1. The user observes the SMS on the iPhone.
2. `org.bluez.obex` emits the MAP object event.
3. Bluepost emits `HistoryChanged`.
4. the notification service receives `Notify`.

If steps 2 through 4 are close together, the delay is before Bluepost. If a
gap occurs between these steps, fix only the component that owns that gap.
Do not add inbox polling before this trace identifies the delay.
