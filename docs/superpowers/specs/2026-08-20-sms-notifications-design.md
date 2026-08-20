# SMS notification design

## Goal

Bluepost shows each new SMS as an Omarchy desktop notification.
The notification shows the sender and the complete message body.

If Bluepost finds one authentication code, a left click copies only that code.
If Bluepost finds no clear code, a left click does not copy message text.

## Scope

This change adds notification support to `bluepost daemon`.
It does not add a graphical application, message replies, or notification settings.

The first version supports current Omarchy installations.
It uses the installed `notify-send` and `wl-copy` programs.
It adds no Go dependency.

The copy action works while the original notification is active.
A notification that Omarchy restores from history has no copy action.

## Accepted privacy behavior

Omarchy stores the ten most recent notifications as plain JSON files.
These files can contain the sender, the SMS body, and the code.
The user accepted this behavior for the first version.

Bluepost continues to store its own message history in encrypted files.
Bluepost does not write notification payloads or extracted codes to logs.

## Architecture

The backend stores an incoming message before it requests a notification.
The repository reports whether the message handle is new or already stored.
Bluepost does not show a second notification for an existing handle.

The backend sends each new and recent SMS to a desktop notifier.
The notifier extracts a possible authentication code and calls `notify-send`.

For a clear code, the notifier adds the standard `default` notification action.
Omarchy invokes this action when the user left-clicks the notification.
The waiting `notify-send` process returns the action name to Bluepost.

Bluepost sends the code to `wl-copy` through standard input.
It uses `--sensitive` and `--trim-newline`.
The code never appears in the `wl-copy` argument list.

Bluepost does not use `--paste-once`.
Omarchy runs a clipboard watcher that can consume the first paste request.
The `--sensitive` flag prevents Omarchy from writing the code to clipboard history.

## Notification content

The notification title uses the contact name when it is available.
Otherwise, it uses the sender address.
If both values are empty, it uses `New message`.

The body contains the complete SMS text.
Bluepost converts invalid UTF-8 and removes unsafe control characters.
It escapes notification markup before it calls `notify-send`.
It puts `--` before the title and body so that message text cannot become a `notify-send` option.

For a message without a clear code, Bluepost does not add a default action.
Omarchy dismisses the notification after a left click and copies nothing.

## Authentication code detector

The detector uses the Go standard library only.
It uses small capture expressions and normal Go validation.
It does not use one large provider expression or an external parser.

The detector applies these rules in order:

1. Normalize line endings and remove empty lines at the end.
2. Parse a domain-bound final line in the form `@host #code`.
3. Accept the Apple and WICG embedded-host suffix forms.
4. Recognize the Google SMS Retriever application hash on the final line.
5. Find bounded candidates near English or Norwegian authentication phrases.
6. Return a code only when one candidate has the strongest match.

The domain-bound rule accepts 4 to 10 ASCII letters or digits.
The code must contain at least one digit.
The detector copies the token after `#` exactly.

The general rule accepts 4 to 10 ASCII digits.
It also accepts an alphanumeric token after a strong authentication phrase.
An alphanumeric token must contain a letter and a digit.

Strong English phrases include these terms:

- `verification code`
- `security code`
- `authentication code`
- `one-time code`
- `one-time password`
- `passcode`
- `OTP`
- `2FA code`

Strong Norwegian phrases include these terms:

- `bekreftelseskode`
- `sikkerhetskode`
- `engangskode`
- `innloggingskode`

The detector supports a phrase before or after the candidate.
It accepts inflected Norwegian forms when the grammar binds the phrase to the candidate.

The words `code` and `kode` are weak phrases.
They accept only a numeric candidate with direct grammar, such as `code is: 123456`.
They do not accept promotion codes such as `Use code SAVE20`.

The detector excludes candidates inside these values:

- URLs and email addresses
- decimal amounts
- dates and times
- phone numbers
- longer words, numbers, or identifiers

If two candidates have equal confidence, the detector returns no code.
The notification still shows the complete SMS.

The first version does not accept grouped codes such as `123-456`.
Bluepost can add this form after a real received message supplies a safe test case.

The source research and test corpus are in
`docs/research/2026-08-20-sms-otp-formats.md`.

## New-message rules

Bluepost notifies only after it saves a message successfully.
It notifies only when the message handle was not already in encrypted history.

BlueZ can create message objects during an explicit inbox query.
Bluepost suppresses stale objects with the message timestamp.
A timestamp is recent when it is not more than five minutes old.
A timestamp can be up to one minute in the future for clock differences.

If a live message has no timestamp, Bluepost treats it as recent.
This choice prevents a missing optional property from hiding a real SMS.

## Error handling

A notification error does not stop Bluetooth reception or encrypted storage.
Bluepost writes a generic error to standard error.
The error does not contain the sender, body, candidate, or code.

If `notify-send` returns no `default` action, Bluepost does not call `wl-copy`.
This behavior covers expiry, dismissal, right click, and Do Not Disturb.

If `wl-copy` fails, Bluepost writes a generic copy error to standard error.
It does not retry because a late retry can replace newer clipboard content.

## Test strategy

Table-driven unit tests use the cited positive and negative message corpus.
The tests cover structured formats, Stripe wording, English, Norwegian, ambiguity, and false positives.

Notifier tests record command names, arguments, standard input, and returned actions.
They prove that only a detected code reaches `wl-copy`.
They also prove that `--sensitive` is present and `--paste-once` is absent.

Backend tests prove these behaviors:

- Storage occurs before notification.
- A duplicate handle does not create another notification.
- A stale message does not create a notification.
- A notification error cannot stop message storage.

The existing unit, race, vet, build, and private D-Bus integration checks remain required.

## Runtime requirements

The README adds these Omarchy requirements:

- `notify-send` from libnotify
- `wl-copy` from wl-clipboard

The repository does not install or configure these programs.
