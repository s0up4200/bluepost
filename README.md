# Bluepost

Bluepost is an experimental, read-only iPhone message client for Omarchy.
It uses Bluetooth MAP for messages and PBAP for contacts.
It does not need a Mac, Apple account, relay server, or cloud service.

Bluepost cannot tell whether the Messages app used SMS, RCS, or iMessage.
The iPhone reports these messages through the same MAP message type.
Do not use Bluepost as the only way to receive an important message.

## Current scope

Bluepost provides two programs:

- `bluepostd` receives messages, synchronizes contacts, and owns encrypted storage.
- `bluepost` reads daemon status, messages, and contacts.

The daemon stores new messages that arrive while it is connected.
A live inbox query reads recent MAP entries from the iPhone.
The live entries can contain only the short subject text that the iPhone provides.

Bluepost cannot send, reply to, delete, or mark a message as read.
It does not support attachments, reactions, groups, calls, ANCS notifications, or a graphical interface.

## Requirements

The first release supports Arch Linux on x86-64 with:

- Go 1.27 or later
- BlueZ 5.86 or later
- `bluez-obex`, including its `obexd` D-Bus service
- GNOME Keyring and `/usr/bin/secret-tool`
- A user D-Bus session

This repository does not install or configure these requirements.
A live test used an iPhone 16 Pro Max on August 20, 2026.
The test confirmed SMS reception, MAP inbox queries, and PBAP synchronization with 75 contacts.

## Prepare the iPhone

Activate `obexd` before you pair the iPhone:

```bash
busctl --user introspect org.bluez.obex /org/bluez/obex org.bluez.obex.Client1 >/dev/null
```

Set the adapter device class to A/V Hands-Free before pairing:

```bash
sudo /usr/bin/btmgmt --index 0 class 4 8
```

Replace `0` if the Bluetooth controller uses a different index.
This setting makes the message permission available on the tested iPhone.

Keep the iPhone unlocked with **Settings → Bluetooth** open.
Start the pairing from Linux and confirm the same numeric code on both devices.
Then mark the iPhone as trusted in BlueZ.

On the iPhone, open **Settings → Bluetooth**, select the Linux computer, and enable:

- **Show Message Notifications**
- **Sync Contacts**, if this option is available

The tested iPhone accepted PBAP contact synchronization before it showed the **Sync Contacts** option.

Keep Bluetooth enabled on both devices.
Only one computer can own the iPhone MAP connection at one time.

## Build from source

From the repository root, run:

```bash
go mod verify
go test ./...
go build -o bluepost ./cmd/bluepost
go build -o bluepostd ./cmd/bluepostd
```

The build uses the versions and checksums in `go.mod` and `go.sum`.
Cobra is the only CLI framework.
The other direct dependency is godbus for local D-Bus communication.

## Run

Start the daemon in one terminal.
Replace the example address with the iPhone Bluetooth address:

```bash
./bluepostd --phone AA:BB:CC:DD:EE:FF
```

You can also set `BLUEPOST_PHONE`.
The `--phone` option takes precedence.

Use the CLI in another terminal:

```bash
./bluepost status
./bluepost messages
./bluepost messages --iphone --limit 20
./bluepost contacts sync
./bluepost contacts
./bluepost contacts jane
```

The daemon runs in the foreground.
This release does not include an installer or a systemd user service.

## Storage and security

Bluepost stores `history.enc` and `contacts.enc` below `$XDG_STATE_HOME/bluepost`.
It encrypts each snapshot with AES-256-GCM from the Go standard library.
It stores the random 32-byte key in GNOME Keyring through `secret-tool`.
If the key is missing, locked, or invalid, the daemon does not open a phone connection.

The daemon accepts D-Bus method calls only from the same UNIX user ID.
It limits downloaded files, parsed records, queued work, and D-Bus responses.
It removes terminal control characters before the CLI prints phone data.
It does not open a network port, send telemetry, or check for updates.

Encryption protects stored content after the user session is locked or the machine is off.
It does not protect content from root or another process that already runs as the same user.
It also cannot detect deletion of an encrypted file or replacement with an older authentic copy.
Decrypted content exists in process memory while the daemon runs.

## Development checks

Run the full local check with:

```bash
go test -race ./...
go vet ./...
go build ./cmd/...
```

Run the D-Bus integration test in a private user session:

```bash
dbus-run-session -- env BLUEPOST_TEST_PRIVATE_BUS=1 go test -tags=integration ./internal/...
```

See `docs/superpowers/specs/2026-08-20-bluepost-read-only-design.md` for the protocol and security design.
