# SMS OTP Format Research

## Scope

This report asks what Bluepost can safely infer from an incoming SMS when it
decides if a notification click copies a one-time code. It uses only
first-party documentation and public specifications.

The evidence does not support an exhaustive provider-template table. Several
large delivery platforms let their customers customize and localize the whole
message. A detector must parse strong structures first and use a
small heuristic second.

## Findings

### There is no universal OTP shape

- Six numeric digits are common, but they are not universal. Twilio Verify
  generates 4–10 digits and defaults to 6. Its English default places the code
  after `verification code is:`. Twilio also offers localized, pre-approved,
  and custom templates. [Twilio Verify best practices](https://www.twilio.com/docs/verify/developer-best-practices),
  [Twilio verification templates](https://www.twilio.com/docs/verify/verification-templates)
- Google Play's SMS User Consent filter accepts a 4–10-character alphanumeric
  token that contains at least one digit. Google's SMS Retriever example uses
  `123ABC78`, so a numeric-only detector has known false negatives.
  [Google API comparison](https://developers.google.com/identity/sms-retriever/choose-an-api),
  [Google server guide](https://developers.google.com/identity/sms-retriever/verify)
- Auth0's current SMS login and MFA screens ask for a six-digit numeric code,
  but Auth0 also lets a tenant customize and localize the SMS template.
  [Auth0 Universal Login templates](https://auth0.com/docs/customize/login-pages/universal-login/customize-templates),
  [Auth0 MFA message customization](https://auth0.com/docs/customize/customize-sms-or-voice-messages)
- Amazon Cognito requires the `{####}` token in a custom verification or MFA
  SMS and permits the surrounding message to be customized. The placeholder's
  spelling is not evidence that every generated code is exactly four digits.
  [Amazon Cognito message customization](https://docs.aws.amazon.com/cognito/latest/developerguide/cognito-user-pool-settings-message-customizations.html)

### Stripe is covered, but its public format contract is incomplete

Stripe's first-party support material documents at least two distinct flows:

- The Link support article identifies messages beginning with the phrase
  `Your Stripe verification code is`, but does not publish a code length or
  punctuation contract.
  [Stripe Link support](https://support.stripe.com/questions/received-your-stripe-verification-code-is-text-message-from-stripe)
- Stripe Express explicitly documents a six-digit code sent by SMS or email,
  but does not publish the complete SMS body.
  [Stripe Express support](https://support.stripe.com/express/questions/how-do-i-login-to-my-stripe-express-account)

These sources justify recognizing the Stripe phrase and a nearby six-digit
candidate. They do not justify hard-coding one exact Stripe message or claiming
that Link and Express always use identical formatting. In particular, the
surveyed first-party sources do not specify whether six digits are displayed
contiguously or in hyphenated groups.

### Structured domain-bound messages are the strongest signal

The origin-bound SMS specification puts a host and code on the final line:

```text
@example.com #747723
```

The final line starts with `@`, contains exactly one space before `#code`, and
can contain an embedded host after the code. The specification deliberately
allows arbitrary human-readable text before this line and notes that heuristic
extraction without a standard format is unreliable.
[WICG origin-bound SMS specification](https://wicg.github.io/sms-one-time-codes/)

Apple documents the same top-level `@domain #code` form. Its documented iframe
variant appends `%iframe-domain`, while the current WICG draft represents an
embedded host with `@embedded-domain`. Bluepost can cheaply accept both suffix
forms after it has parsed the top-level host and code.
[Apple domain-bound SMS AutoFill](https://developer.apple.com/documentation/security/enabling-autofill-for-domain-bound-sms-codes)

The explanatory text often repeats the code. The final structured line must win
instead of producing two candidates.

### Message placement and extra numbers vary

- Twilio's default English form puts the code after a colon. Its domain-bound
  example puts the code both before the prose and after `#` on the final line.
  [Twilio verification templates](https://www.twilio.com/docs/verify/verification-templates)
- Auth0's documented templates put `{{code}}` before the explanatory phrase in
  some languages and support locale-dependent text.
  [Auth0 MFA message customization](https://auth0.com/docs/customize/customize-sms-or-voice-messages)
- Google SMS Retriever messages also contain an 11-character application hash.
  That hash is not the OTP and must not become a candidate.
  [Google server guide](https://developers.google.com/identity/sms-retriever/verify)
- Twilio PSD2 verification accepts both an amount and a payee. Transactional
  messages can therefore contain a decimal amount as well as the code.
  [Twilio PSD2 verification](https://www.twilio.com/docs/verify/verifying-transactions-psd2)
- Microsoft documents a real example in which `69525` is the SMS sender and
  `585112` is the code. Sender metadata must remain separate from body parsing.
  Microsoft also documents genuine messages that contain links and partial
  account addresses.
  [Microsoft SMS security guidance](https://support.microsoft.com/en-US/accounts-billing/security/why-is-microsoft-texting-me)

GitHub documents that it sends an SMS security code, but the public GitHub
documentation surveyed here does not define the SMS body, character set, or
length. Bluepost must not invent a GitHub-specific template.
[GitHub 2FA configuration](https://docs.github.com/en/authentication/securing-your-account-with-two-factor-authentication-2fa/changing-your-two-factor-authentication-method)

### Localization prevents a complete keyword list

Twilio automatically selects among many locales. Auth0 exposes the locale to
Liquid templates and shows different word order in English, French, and
Spanish. Cognito permits arbitrary customer text. Provider names and English
keywords alone cannot provide global coverage.

For this personal, local tool, English and Norwegian lexical rules plus the
language-independent structured formats are a defensible first version. Real
false negatives can be added as small fixtures. Building a speculative global
phrase dictionary is larger and still incomplete.

## Recommended First Detector

Apply the rules in this order and stop on the first unambiguous match.

1. **Parse a domain-bound final line.** Normalize newlines, inspect the final
   non-empty line, and parse `@host #code`. Permit an optional `@host` or
   `%host` suffix. Copy the token after `#` exactly.
2. **Recognize the Google SMS Retriever structure.** If the final line is an
   11-character application hash and the preceding text has exactly one
   strongly bound 4–10-character candidate, ignore the hash and use the
   candidate.
3. **Extract bounded candidates.** Accept either 4–10 ASCII digits or a
   4–10-character ASCII alphanumeric token containing both a digit and a
   letter. Do not match inside a longer word or number.
4. **Require nearby authentication context.** Accept English and Norwegian
   phrases such as `verification code`, `security code`, `authentication code`,
   `one-time code`, `OTP`, `2FA`, `bekreftelseskode`, `sikkerhetskode`,
   `engangskode`, and `innloggingskode`. Support both `code ... candidate` and
   `candidate ... code` order.
5. **Treat generic `code` or `kode` as weak context.** Accept a numeric
   candidate only when grammar binds it directly, such as `code is: 123456`.
   Do not accept an alphanumeric promotion such as `use code SAVE20`.
6. **Fail closed on ambiguity.** If two candidates have equally strong context,
   attach no copy action. The notification still shows the complete SMS.

Before step 4, exclude candidates that are part of a URL, email address,
decimal amount, date, time, phone number, or a longer identifier. Do not use a
provider or sender allowlist: short codes, alphanumeric sender IDs, and routing
vary by country.

An optional compatibility rule can recognize `ddd-ddd` next to a strong OTP
phrase and copy the six digits without the hyphen. This is reasonable input
tolerance, not a documented Stripe contract. Keep it behind a test made from a
message actually received by the user.

## Test Corpus

The examples below are short enough to keep as unit-test fixtures. “Adapted”
means placeholders or names were replaced; it does not claim an exact current
production message.

| Expected | Fixture | Basis |
| --- | --- | --- |
| `123456` | `Your ExampleApp verification code is: 123456` | Twilio's official English example. [Source](https://www.twilio.com/docs/verify/developer-best-practices) |
| `747723` | `747723 is your ExampleCo authentication code.\n\n@example.com #747723` | WICG's structured example. [Source](https://wicg.github.io/sms-one-time-codes/) |
| `123456` | `Your Example code is 123456.\n\n@example.com #123456 %iframe-auth.example.org` | Apple's iframe form. [Source](https://developer.apple.com/documentation/security/enabling-autofill-for-domain-bound-sms-codes) |
| `123ABC78` | `Your ExampleApp code is: 123ABC78\n\nFA+9qCX9VSu` | Google's SMS Retriever example. [Source](https://developers.google.com/identity/sms-retriever/verify) |
| `123456` | `123456 is your verification code to enroll with ExampleApp.` | Adapted substitution of Auth0's documented SMS template. [Source](https://auth0.com/docs/customize/customize-sms-or-voice-messages) |
| `482731` | `Your Stripe verification code is 482731` | Synthetic: combines Stripe's documented Link phrase with a six-digit candidate. It intentionally makes no punctuation claim. [Phrase](https://support.stripe.com/questions/received-your-stripe-verification-code-is-text-message-from-stripe), [length evidence](https://support.stripe.com/express/questions/how-do-i-login-to-my-stripe-express-account) |
| `482731` | `482731 er bekreftelseskoden din for ExampleApp.` | Bluepost's initial Norwegian rule. This is not a provider template. |
| none | `Your order 482731 has shipped.` | Order identifiers are not OTPs. |
| none | `Use code SAVE20 for 20% off.` | Promotion codes are not authentication codes. |
| none | `Pay €39.99 to Example; card ending 1234.` | Amounts and account suffixes are common false positives. |
| none | `Your verification numbers are 1234 and 5678.` | Ambiguous candidates must not create a copy action. |
| none | `Call 12345678 to confirm your appointment.` | A phone-like number without authentication context is not an OTP. |

## Limits and Follow-up

This detector intentionally prefers precision over recall because a normal SMS
still appears in full even when no copy action is attached. It will miss some
localized or unusually formatted OTPs. Add a new pattern only after a real
miss is reduced to a non-secret fixture.

Do not log extracted codes, candidate lists, or full messages while debugging.
The user's accepted notification-history behavior does not make extra logs
necessary.
