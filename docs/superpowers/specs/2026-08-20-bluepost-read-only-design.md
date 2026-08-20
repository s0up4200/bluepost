# Bluepost Read-Only Design

## Status

This design records the decisions from the Bluepost design discussion.
Bluepost is a new Go project based on the public BlueZ D-Bus API.

## Goal

Bluepost receives Messages app messages from a paired iPhone on an Omarchy system.
It uses the Bluetooth Message Access Profile (MAP) through BlueZ.
It also reads contacts through the Phone Book Access Profile (PBAP).

The first release provides a read-only daemon and command-line interface.
It stores private data only in authenticated, encrypted files.

## Platform

- Arch Linux on x86-64
- Omarchy user session
- BlueZ 5.86 or later
- `bluez-obex` and its `obexd` user service
- GNOME Keyring through `org.freedesktop.secrets`
- Go 1.27 or later for the initial build

The first release does not support other distributions or architectures.

## Names

| Item | Value |
| --- | --- |
| Project and CLI | `bluepost` |
| Daemon | `bluepostd` |
| D-Bus bus name | `io.github.s0up4200.Bluepost` |
| D-Bus object path | `/io/github/s0up4200/Bluepost` |
| Read interface | `io.github.s0up4200.Bluepost.Messages1` |
| Event interface | `io.github.s0up4200.Bluepost.Events1` |
| State directory | `$XDG_STATE_HOME/bluepost` |
| Runtime directory | `$XDG_RUNTIME_DIR/bluepost` |
| Secret service label | `Bluepost storage key` |

All source code, documentation, commands, help text, errors, and logs use English.

## Scope

The first release includes these functions:

- Maintain one MAP session and one PBAP session for one configured iPhone.
- Receive new MAP message events while the daemon runs.
- Read recent messages directly from the iPhone.
- Store a bounded local message history.
- Synchronize and search the iPhone contact list.
- Resolve message senders from phone numbers or email addresses.
- Show status, messages, and contacts through a Cobra CLI.

The first release excludes these functions:

- Message sending and replies
- Message deletion or read-state changes
- Apple Notification Center Service (ANCS)
- Group reconstruction and reactions
- A graphical interface or terminal interface
- Pairing and discovery workflows
- SQLite or another database
- Telemetry, update checks, network listeners, and plugins
- Installation or system configuration changes

The user must pair and trust the iPhone before the daemon starts.
The user must enable message and contact synchronization on the iPhone.

## Protocol Limits

MAP reports both SMS and iMessage as `sms-gsm` on the tested iPhone.
Bluepost must not claim that it can identify the transport for a message.

MAP does not provide a stable Messages conversation identifier.
The first release shows each received record without group reconstruction.

Only one computer can hold the iPhone MAP session at one time.
Bluepost reports `Connection refused (111)` as a distinct connection state.

## Process Architecture

```text
iPhone
  | Bluetooth MAP and PBAP
  v
BlueZ and obexd
  | local D-Bus
  v
bluepostd ---- encrypted snapshots
  |                   |
  | session D-Bus     `-- key in GNOME Keyring
  v
bluepost CLI
```

`bluepostd` owns every Bluetooth and storage operation.
The daemon keeps the OBEX sessions open while it runs.

`bluepost` is a short-lived D-Bus client.
The CLI never reads encrypted files or contacts `obexd` directly.

## Dependencies

The project has two direct Go dependencies:

- `github.com/godbus/dbus/v5` for local D-Bus communication
- `github.com/spf13/cobra` for the CLI

Cobra adds `github.com/spf13/pflag` and `github.com/inconshreveable/mousetrap` as transitive dependencies.
Godbus adds `golang.org/x/sys` as a transitive dependency.
The project does not use Viper or a logging framework.

The Go module files pin all module versions and checksums.
The daemon calls the existing `/usr/bin/secret-tool` executable.

## Configuration

`bluepostd` requires one iPhone MAC address.
The daemon reads the address from `--phone` or `BLUEPOST_PHONE`.
The command-line value takes precedence.

The daemon validates the canonical `XX:XX:XX:XX:XX:XX` format.
It then queries BlueZ for the matching device object.
The device must have both `Paired=true` and `Trusted=true`.

The daemon gets state and runtime paths from the XDG environment.
It does not read a project configuration file in the first release.

## Startup Sequence

1. The daemon validates its configuration and private runtime directory.
2. The daemon connects to the system and user D-Bus services.
3. The daemon starts the Bluepost D-Bus service and backend.
4. The backend validates the private state directory.
5. The backend gets the storage key from GNOME Keyring.
6. The backend authenticates and loads each encrypted snapshot.
7. The backend validates the configured BlueZ device.
8. The backend opens the MAP and PBAP sessions.
9. The backend starts the MAP event listener.

The daemon stays available in a locked state after a storage error.
The status methods remain available in this state.
The daemon does not open an OBEX session while storage is locked.

## Key Management

The storage key is 32 random bytes from `crypto/rand`.
The key uses these Secret Service attributes:

- `application=bluepost`
- `purpose=storage-v1`

The daemon calls `/usr/bin/secret-tool` with a fixed executable path.
It sends a new Base64 key through standard input.
It never puts a key in arguments, environment variables, files, or logs.

On a new profile, the daemon creates a key only when no snapshot exists.
It reads the stored key back before it creates the first snapshot.

If snapshots exist and the key is missing, the daemon stays locked.
It never replaces a missing key for existing data.

Each `secret-tool` operation has a 15-second deadline.
An unavailable, locked, or malformed Secret Service result locks storage.

## Encrypted Snapshots

The daemon stores `history.enc` and `contacts.enc` in the state directory.
It uses AES-256-GCM from the Go standard library.

Each file has this binary form:

```text
magic | format version | random GCM nonce | ciphertext and GCM tag
```

The additional authenticated data contains the application name, file purpose, and format version.
This value prevents an attacker from exchanging the history and contact files.

The plaintext is versioned JSON.
The history snapshot stores message records in arrival order.
The contact snapshot stores normalized contact records.

The state directory has mode `0700`.
Snapshot and temporary files have mode `0600`.

The daemon rejects a state directory that is a symbolic link.
It also rejects a state directory that another user owns.
The daemon rejects snapshot files that are symbolic links or have unsafe permissions.

For an update, the daemon writes a temporary file in the state directory.
It creates the file with an unpredictable name and exclusive creation.
It synchronizes the file, renames it over the old snapshot, and synchronizes the directory.
The old complete snapshot remains available if the write stops before the rename.

The daemon locks storage if GCM authentication fails.
It does not skip or replace an unauthenticated snapshot.

## Storage Bounds

The history contains no more than 2,000 records.
Its plaintext snapshot contains no more than 64 MiB.
The daemon removes the oldest records to satisfy both limits.

One downloaded bMessage contains no more than 1 MiB.
One body stored in public D-Bus output contains no more than 32 KiB.

One downloaded phonebook contains no more than 64 MiB.
One vCard contains no more than 1 MiB.
The contact list contains no more than 65,535 cards.

One contact name contains no more than 512 characters.
One contact address contains no more than 320 characters.
One card contains no more than 64 addresses.

One D-Bus JSON response contains no more than 8 MiB.
One live message query returns no more than 200 records.
One contact page returns no more than 150 records.

The daemon keeps decrypted snapshots in memory while it runs.
Go does not guarantee prompt removal of copied plaintext from memory.

## OBEX Ownership

One worker goroutine owns all blocking MAP and PBAP calls.
Its queue contains no more than 256 operations.

The daemon creates one long-lived session for each profile.
It removes only stale sessions for the configured phone and profile.

The daemon polls every 5 seconds before the first successful connection.
It polls every 15 seconds after a later connection loss.

The daemon treats these signals as connection loss:

- The MAP or PBAP session object disappears.
- The `org.bluez.obex` bus owner disappears.

After an observed transport loss, the daemon discards local object paths.
It does not call `RemoveSession` for a session that already disappeared.

## MAP Receive Flow

The daemon subscribes to `org.freedesktop.DBus.ObjectManager.InterfacesAdded`.
It accepts `org.bluez.obex.Message1` objects only below its MAP session path.

For each accepted object, the daemon does these operations:

1. It creates a private file in the runtime directory.
2. It calls `Message1.Get(path, false)`.
3. It waits for the associated `Transfer1` result.
4. It rejects a file that is empty or too large.
5. It parses the bMessage and removes MAP byte stuffing.
6. It normalizes the sender phone number or email address.
7. It resolves the sender against the in-memory contact list.
8. It adds the message to the encrypted history snapshot.
9. It emits `HistoryChanged` with only a local revision number.
10. It removes the runtime file.

The daemon removes the runtime file after success or an error.
It never writes the message body to a log.

## Live MAP Query

`ListRecent` uses `org.bluez.obex.MessageAccess1` on the MAP session.
The daemon selects the requested MAP folder and calls `ListMessages`.

The daemon accepts only these folders:

- `telecom/msg/inbox`
- `telecom/msg/sent`

The daemon converts BlueZ properties into bounded message records.
This query does not change the encrypted history.

## PBAP Contact Flow

`SyncContacts` serializes its work through the OBEX worker.
It selects the main phonebook with `Select("int", "pb")`.

The daemon calls `PullAll` with these options:

- `Format="vcard30"`
- `MaxCount=65535`

The daemon waits for the transfer and applies the file-visibility grace period.
It parses `FN`, `TEL`, and `EMAIL` properties from each vCard.

The parser unfolds continued vCard lines.
It decodes vCard escapes but does not interpret arbitrary character sets.
It normalizes phone numbers and lowercases email addresses.

The daemon replaces the encrypted contact snapshot after a complete transfer.
It keeps the prior snapshot after a transfer or parse error.

## D-Bus API

The D-Bus method names and signatures preserve the useful versioned read contract.
The bus name, object path, and interface names use the Bluepost namespace.

| Method | Input | Output | Purpose |
| --- | --- | --- | --- |
| `GetStatus` | none | `s` | Return bounded JSON status. |
| `IsHealthy` | none | `b` | Report an unlocked MAP connection. |
| `ListEvents` | `as`, `u` | `s` | Return bounded JSON history. |
| `ListRecent` | `s`, `u` | `s` | Query a MAP folder. |
| `FindContacts` | `s` | `s` | Search contact names and addresses. |
| `ListContacts` | `u`, `u` | `s` | Return one contact page. |
| `SyncContacts` | none | `u` | Replace the PBAP contact snapshot. |

The API does not expose send, erase, policy, or unlock methods.

`HistoryChanged` has signature `a{sv}`.
Its payload contains only a `revision` value of type `t`.

`StatusChanged` has no arguments.
Clients fetch private data after they receive an invalidation signal.

Before each method call, the daemon asks the bus for the caller UNIX user ID.
The caller user ID must equal the daemon user ID.
This rule does not protect data from another process that already runs as the same user.

## CLI

The Cobra CLI provides these commands:

```text
bluepost status
bluepost messages [--limit N] [--iphone]
bluepost contacts [query]
bluepost contacts sync
```

`bluepost messages` reads the encrypted local history through D-Bus.
The `--iphone` flag runs a live inbox query through the daemon.

The default message limit is 20.
The CLI rejects values outside the daemon limits before it makes a D-Bus call.

The CLI removes terminal control characters from remote text.
It replaces embedded line breaks with a visible separator in list output.

The CLI uses plain text and the Cobra help system.
It does not add color, completion scripts, JSON output, or interactive prompts.

## Status and Errors

The status JSON contains these fields:

- `state`: `locked`, `connecting`, `ready`, `degraded`, or `stopped`
- `detail`: a bounded message without private content
- `map`: a Boolean connection value
- `pbap`: a Boolean connection value
- `storage`: `locked` or `unlocked`
- `phone`: a MAC address masked as `XX:XX:XX:XX:EE:FF`
- `history_count`: a nonnegative count
- `contact_count`: a nonnegative count

The daemon maps internal errors to names below `io.github.s0up4200.Bluepost.Error`.
It does not put message content, contact content, file paths, or keys in D-Bus errors.

## Security Boundary

Bluepost protects private files at rest and authenticates their content.
It does not protect against a process that controls the same UNIX account.

A same-user process can call the session D-Bus API.
It can also erase or replace the encrypted files.
AES-GCM detects replacement with unauthenticated content, but it cannot prevent file deletion or rollback.

Bluepost trusts the configured and paired Bluetooth device.
It rejects discovered devices that are not the configured device.

Bluepost makes no Internet connection.
Its only external communication uses local D-Bus and Bluetooth through BlueZ.

## Test Strategy

Development follows a red-green-refactor cycle.
Tests use fake D-Bus and command boundaries where hardware is not available.

The automated suite covers these areas:

- bMessage parsing, byte stuffing, malformed structure, and size limits
- vCard folding, escaping, normalization, malformed cards, and size limits
- AES-GCM round trips, wrong keys, tampering, file-purpose swaps, and atomic replacement
- Key creation, retrieval, missing-key behavior, deadlines, and malformed output
- History retention by record count and plaintext size
- Contact search and ambiguous names
- Caller user-ID authorization
- MAP event filtering by session path
- OBEX serialization, queue limits, timeouts, and reconnect states
- D-Bus JSON signatures, response limits, and content-free signals
- CLI argument validation and terminal-text sanitization

The final local checks are:

```text
go test -race ./...
go vet ./...
go build ./cmd/bluepost ./cmd/bluepostd
```

A live hardware check runs the binaries from the source tree.
It requires an installed or separately available `obexd` and an already paired iPhone.
The check does not install Bluepost or change system configuration.

## Delivery Boundary

The first delivery contains source code, tests, and English documentation.
It does not install packages, start services, commit changes, push a branch, or create a remote repository.
