# End-to-end encrypted rooms

**Status: not supported yet.** The bridge refuses encrypted rooms instead of
half-working with them.

## Why it matters

Element encrypts private rooms and direct messages by default, so many real
rooms people want to bridge are encrypted. The bridge has to be a participant
that can decrypt what members send and encrypt what Mattermost sends. That also
means encryption here ends at the bridge: the bridge, Mattermost and its
database see the plain text, as with every bridge that supports encrypted rooms.

## What happens today

- `/matrix map` and `/matrix join` refuse an encrypted room and say why.
- If a mapped room later turns encryption on, syncing stops in both directions:
  messages from Mattermost are **not** sent in the clear, and ciphertext from
  Matrix is not turned into posts. `/matrix status` and the bridge panel show a
  lock and say so, and the log has a warning.
- A mapped room that is not encrypted keeps working as before.

## Plan

Support is planned on top of [mautrix-go](https://github.com/mautrix/go)'s
crypto package, which the bridge can build with `CGO_ENABLED=0` using its pure-Go
Olm implementation (`-tags goolm`). The design follows mautrix's end-to-bridge
encryption: one crypto device for the bridge bot, used for every account the
bridge sends as, with the homeserver delivering device and key data to the
appservice (MSC3202) and the bridge managing its own device (MSC4190).

The bridge's crypto state (account, Olm and Megolm sessions, device lists) will
live in Mattermost's key-value store through an implementation of mautrix's
`crypto.Store`, since a plugin has no database of its own.

Tracked in the encryption issue (#36).
