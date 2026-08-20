# Bluepost Omarchy widget and automatic service design

**Date:** 2026-08-20

**Status:** Approved in chat

## Goal

Bluepost must connect to the configured iPhone without a manual Bluetooth step.
Omarchy must show Bluepost in a separate bar widget.

The widget must show connection health and the five most recent SMS messages.
The stock `omarchy.bluetooth` widget must not change.

## User experience

The user completes Bluetooth pairing once. A systemd user service then starts
`bluepost daemon` at login. The daemon reconnects when the iPhone returns to
Bluetooth range.

The bar icon shows one of these states:

- `Connected`
- `Reconnecting`
- `Daemon stopped`

The popup shows the MAP, PBAP, and storage states. It also shows the five most
recent SMS messages, newest first. Each message shows its sender, time, and
body.

A click on a message copies text to the clipboard. If Bluepost detects an OTP,
it copies only the code. Otherwise, it copies the complete message body. This
fallback keeps unknown OTP formats useful.

The popup shows `Reconnect now` only when Bluepost is not ready. This action
restarts the systemd user service. The widget has no disconnect action because
automatic connection is the primary behavior.

## Scope

This change includes:

- a systemd user service for `bluepost daemon`
- a persistent `bluepost widget` command in the existing binary
- a separate Omarchy plugin with the ID `io.github.s0up4200.bluepost`
- immediate widget updates from existing Bluepost D-Bus signals
- smart clipboard text for each message
- local installation on this Omarchy machine

This change does not include:

- changes to the stock Bluetooth widget
- message send, reply, erase, or read-state actions
- Bluetooth pairing in the widget
- a full message client
- code copied from the GPL-2.0-only BlueFerry widget
- a second application daemon

The plugin source stays in `contrib/omarchy-bluepost` for now. A separate
plugin repository is not necessary for the local installation.

## Components

### Bluepost daemon service

The service starts `%h/.local/bin/bluepost daemon`. It reads
`BLUEPOST_PHONE` from `%h/.config/bluepost/environment`. The service starts
from the user default target and restarts only after a process error.

The daemon already retries MAP and PBAP connections. The service does not add
a second retry mechanism.

The repository contains `contrib/systemd/bluepost.service` without a Bluetooth
address. The local environment file contains the configured address and has
mode `0600`.

### Widget stream

The new `bluepost widget` Cobra command connects to the existing session D-Bus
service. It fetches the current status and five stored SMS messages.

The command writes one JSON object per line. It writes an initial object and a
new object after `StatusChanged` or `HistoryChanged`.

The command monitors the owner of the Bluepost D-Bus name. It exits if the
daemon is unavailable or loses the name. The Omarchy plugin retries the
command after five seconds. This timer does not delay updates while the daemon
is available.

Each message object contains these fields:

- the message handle
- the display sender
- the message timestamp
- the message body
- the clipboard text
- the clipboard kind, `code` or `message`

Bluepost uses the existing OTP detector to select the clipboard text. The QML
plugin does not implement a second detector.

### Omarchy plugin

The plugin is installed at
`~/.config/omarchy/plugins/io.github.s0up4200.bluepost`. It uses Omarchy panel
components and does not modify files in `/usr/share/omarchy`.

The plugin starts `bluepost widget` as one long-running child process. A split
parser reads each JSON line and replaces the current view model.

The plugin writes clipboard text to `wl-copy` through standard input. Message
content never appears in a process argument. The plugin uses
`--sensitive --trim-newline` for all copied message data.

The plugin calls `systemctl --user restart bluepost.service` for the reconnect
action. It does not use a shell command string.

## Data flow

1. The systemd user service starts `bluepost daemon`.
2. The daemon opens MAP and PBAP sessions through BlueZ OBEX.
3. The daemon stores a received SMS and emits `HistoryChanged`.
4. `bluepost widget` receives the signal and fetches status and messages.
5. The command writes a JSON line to the Omarchy plugin.
6. The plugin updates the popup.
7. A message click writes the selected clipboard text to `wl-copy`.

Status changes use the same flow through `StatusChanged`.

## Error behavior

The plugin shows `Daemon stopped` when the widget command cannot contact the
daemon. The plugin retries after five seconds.

Malformed JSON does not replace the last valid view model. The popup shows a
short local error until a valid update arrives.

An empty history shows `No messages yet`. A clipboard error shows `Copy
failed`. These errors do not create desktop notifications.

The widget never shows an unmasked Bluetooth address.

## Notification latency

The reported 2FA notification delay is a separate backend problem. The widget
stream adds no polling delay after Bluepost receives a history event.

Before a transport change, a live trace must compare these times:

- SMS arrival on the iPhone
- MAP notification arrival in BlueZ OBEX
- message acceptance in the Bluepost daemon
- the `notify-send` call

This trace will show whether the delay occurs in iOS, BlueZ OBEX, or Bluepost.
No inbox polling change belongs in this widget work without that evidence.

## Verification

Go tests must cover the initial JSON object, signal updates, OTP clipboard
selection, normal message fallback, and daemon-unavailable behavior.

`qmllint` must accept the plugin files. The plugin must load without an
Omarchy shell error.

The live acceptance test must show these results:

1. The daemon starts after a user-service restart.
2. The daemon connects MAP and PBAP without a widget click.
3. The widget changes to `Connected`.
4. A new SMS appears in the widget without a five-second polling delay.
5. An OTP row copies only its code.
6. A normal SMS row copies its complete body.
7. `Reconnect now` restores a stopped or degraded connection.
8. The stock Bluetooth widget remains unchanged.
