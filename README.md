# Bluepost

Bluepost is an experimental, read-only iPhone message client for Omarchy.
It uses Bluetooth MAP for messages and PBAP for contacts.
It does not need a Mac, Apple account, relay server, or cloud service.

Bluepost cannot tell whether the Messages app used SMS, RCS, or iMessage.
The iPhone reports these messages through the same MAP message type.
Do not use Bluepost as the only way to receive an important message.

## Current scope

Bluepost provides one binary.
`bluepost daemon` receives messages and synchronizes contacts.
It stores messages and contacts in encrypted files.
The other `bluepost` commands read daemon status, messages, and contacts.

The daemon receives MAP notifications and checks the 20 newest inbox entries every 15 seconds.
If this check finds a missed notification, the daemon opens a new MAP session.
During a live inbox query, Bluepost reads recent MAP entries from the iPhone.
Polled and live entries can contain only the short subject text that the iPhone provides.

`bluepost daemon` shows the full text of each new SMS in a desktop notification.
If an SMS contains one unambiguous authentication code, left-click its notification to copy the code.
Bluepost does not add a notification action for other SMS messages.

Bluepost cannot send, reply to, delete, or mark a message as read.
It does not support attachments, reactions, groups, calls, or ANCS notifications.
The optional Omarchy widget shows connection status and the five newest SMS messages.
Click a widget message to copy its code or complete body.

## Requirements

The first release supports Arch Linux on x86-64 with:

- Go 1.27 or later
- BlueZ 5.86 or later
- `bluez-obex`, including its `obexd` D-Bus service
- GNOME Keyring and `/usr/bin/secret-tool`
- `notify-send` from libnotify
- `wl-copy` from wl-clipboard
- A user D-Bus session

This repository does not install or configure these requirements.
A live test on August 20, 2026, used an iPhone 16 Pro Max.
During this test, Bluepost received SMS messages, queried the MAP inbox, and synchronized 75 PBAP contacts.

## Prepare the iPhone

Before you pair the iPhone, activate `obexd`:

```bash
busctl --user introspect org.bluez.obex /org/bluez/obex org.bluez.obex.Client1 >/dev/null
```

Before you pair the iPhone, set the adapter device class to A/V Hands-Free:

```bash
sudo /usr/bin/btmgmt --index 0 class 4 8
```

If the Bluetooth controller uses a different index, replace `0`.
This setting makes the message permission available on the tested iPhone.

Keep the iPhone unlocked with **Settings → Bluetooth** open.
Start the pairing from Linux.
Make sure that both devices show the same numeric code.
Then mark the iPhone as trusted in BlueZ.

On the iPhone, open **Settings → Bluetooth**.
Select the Linux computer.
Enable **Show Message Notifications**.
If **Sync Contacts** is available, enable it.

On the tested iPhone, PBAP contact synchronization worked before the **Sync Contacts** option appeared.

Keep Bluetooth enabled on both devices.
Only one computer can use the iPhone MAP connection at one time.

## Build from source

From the repository root, run:

```bash
go mod verify
go test ./...
go build -o bluepost ./cmd/bluepost
```

The build uses the versions and checksums in `go.mod` and `go.sum`.
Cobra is the only CLI framework.
The other direct dependency is godbus for local D-Bus communication.
Place the resulting `bluepost` binary on `PATH` to use it from any directory.

## Run

Replace the example address with the iPhone Bluetooth address.
Then start the daemon in one terminal:

```bash
./bluepost daemon --phone AA:BB:CC:DD:EE:FF
```

If you omit `--phone`, Bluepost uses `BLUEPOST_PHONE`.
If you set both, the `--phone` option takes precedence.

Use the CLI in another terminal:

```bash
./bluepost status
./bluepost messages
./bluepost messages --iphone --limit 20
./bluepost contacts sync
./bluepost contacts
./bluepost contacts jane
```

The daemon runs in the foreground unless a service manager starts it.

## Automatic start on Omarchy

Run these commands to build and install the binary:

```bash
go build -trimpath -o bluepost ./cmd/bluepost
install -Dm755 bluepost ~/.local/bin/bluepost
```

Install the systemd user service:

```bash
install -Dm644 contrib/systemd/bluepost.service ~/.config/systemd/user/bluepost.service
install -d -m700 ~/.config/bluepost
printf 'BLUEPOST_PHONE=%s\n' 'AA:BB:CC:DD:EE:FF' >~/.config/bluepost/environment
chmod 0600 ~/.config/bluepost/environment
systemctl --user daemon-reload
systemctl --user enable --now bluepost.service
```

Replace the example address with the iPhone Bluetooth address.
The private environment file is `~/.config/bluepost/environment`.
The service starts `~/.local/bin/bluepost` at login.
It restarts the binary after an error.

Run these commands to view the service status and Bluepost health:

```bash
systemctl --user status bluepost.service
bluepost status
```

## Omarchy widget

Run these commands to validate and install the separate plugin:

```bash
omarchy plugin validate contrib/omarchy-bluepost
install -d ~/.config/omarchy/plugins/io.github.s0up4200.bluepost
install -m644 contrib/omarchy-bluepost/manifest.json ~/.config/omarchy/plugins/io.github.s0up4200.bluepost/
install -m644 contrib/omarchy-bluepost/Panel.qml ~/.config/omarchy/plugins/io.github.s0up4200.bluepost/
omarchy-shell shell rescanPlugins
omarchy plugin enable io.github.s0up4200.bluepost --section right --before omarchy.bluetooth
```

The plugin directory must exist before the rescan.
This plugin does not modify the default `omarchy.bluetooth` widget.

## Storage and security

Bluepost stores `history.enc` and `contacts.enc` below `$XDG_STATE_HOME/bluepost`.
It encrypts each snapshot with AES-256-GCM from the Go standard library.
It stores the random 32-byte key in GNOME Keyring through `secret-tool`.
If the key is missing, locked, or invalid, the daemon does not open a phone connection.

The daemon accepts D-Bus method calls only from the same UNIX user ID.
It limits downloaded files, parsed records, queued work, and D-Bus responses.
It removes terminal control characters before the CLI prints phone data.
It does not open a network port, send telemetry, or check for updates.

Encryption protects stored content after you lock the user session or turn off the machine.
Encryption does not protect content from root or another process that runs as the same user.
Encryption cannot detect deletion of an encrypted file or replacement with an older authentic copy.
Decrypted content exists in process memory while the daemon runs.

Omarchy stores its ten most recent notifications as plain JSON.
These files can contain the sender, the full message body, and an authentication code.
Bluepost uses `wl-copy --sensitive`, so Omarchy does not add copied codes or widget messages to clipboard history.
Copied text remains in the active clipboard until another copy replaces it.
Restored notifications do not keep the copy action in this version.

## Development checks

Run these local checks:

```bash
go test -race ./...
go vet ./...
go build ./cmd/...
```

Run the D-Bus integration test in a private user session:

```bash
dbus-run-session -- env BLUEPOST_TEST_PRIVATE_BUS=1 go test -tags=integration ./internal/...
```
