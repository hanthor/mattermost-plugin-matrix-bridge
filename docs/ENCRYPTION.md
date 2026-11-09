# End-to-end encrypted rooms

**Status: supported, off by default.** Turn on **Bridge Encrypted Rooms** in
the plugin settings. With it off, the bridge refuses encrypted rooms instead of
half-working with them.

## What it means

Element encrypts private rooms and direct messages by default, so many real
rooms people want to bridge are encrypted. For the bridge to carry messages it
has to be a participant that can decrypt what members send and encrypt what
Mattermost sends.

> **Encryption ends at the bridge.** The bridge bot holds the room keys.
> Mattermost, its database and its administrators see the plain text, as with
> every bridge that supports encrypted rooms. The Matrix homeserver still only
> stores ciphertext.

Matrix clients show messages from Mattermost users as sent by the bridge's
device, which is not verified, so Element may show a grey shield on them. That
is accurate: the sender is the bridge, not the person's own device.

## Turning it on

1. **Registration file.** Download it again from the plugin settings; it now
   includes `org.matrix.msc3202: true` (the homeserver passes room keys and
   device changes to the bridge) and `io.element.msc4190: true` (the bridge bot
   may create its own device). Install it on the homeserver and restart the
   homeserver.
2. **Homeserver settings.** Synapse needs the experimental support for
   appservice encryption:

   ```yaml
   experimental_features:
     msc3202_transaction_extensions: true
     msc3202_device_masquerading: true
     msc3983_appservice_otk_claims: true
     msc3984_appservice_key_query: true
   ```

   (The Synapse image used by the Complement tests already sets these.)
3. **Plugin setting.** Enable **Bridge Encrypted Rooms**. The bridge bot logs in
   with its own device, uploads its keys, and `/matrix status` in a bridged
   encrypted room says the bridge encrypts and decrypts its messages.

## What works

- Messages in both directions (tested). Edits, replies, threads and reactions
  take the same encrypt/decrypt path but have no encrypted-room test of their own.
- Attachments (tested): files are encrypted before upload, so the media repository holds
  ciphertext; encrypted files from Matrix are decrypted before they are posted.
- Encrypted rooms can be mapped with `/matrix map` and joined with `/matrix join`.

## Limits

- **One Mattermost node.** The bot's keys live in Mattermost's key-value store
  and are held in memory by one node. In a high-availability cluster, two nodes
  would each hold a copy and diverge into two devices. Run the bridge on a
  single node when encryption is on.
- **Messages sent before the bridge joined** an encrypted room cannot be read:
  the bridge never had the keys, and it does not ask other devices for them.
- **Key backup and verification** are not implemented; the bridge device is not
  cross-signed. Losing the stored keys means starting a new device, after which
  older messages stay unreadable to the bridge.

## If something is wrong

- *"Could not start end-to-end encryption"* in the log: the homeserver refused
  the bot's login or key upload. Check that the registration file has
  `org.matrix.msc3202` and the homeserver restarted.
- Messages from Matrix do not arrive: the transaction did not carry the room
  key. Check the homeserver setting `msc3202_transaction_extensions`.
- To start over, remove the plugin's `e2ee_*` keys and turn the setting off and
  on; the bot gets a new device.

## How it works

The bridge uses [mautrix-go](https://github.com/mautrix/go)'s crypto package
with its pure-Go Olm implementation (build tag `goolm`), so the plugin still
builds with `CGO_ENABLED=0`. Under the hood:

- The bridge bot is one device ([`server/e2ee/manager.go`](../server/e2ee/manager.go)).
  Every account the bridge sends as uses that device's Megolm session for a room;
  the room key is shared with the devices of the real Matrix users in the room,
  never with ghosts.
- The homeserver delivers to-device messages (room keys), device list changes
  and one-time-key counts inside appservice transactions (MSC3202); the plugin
  hands them to the crypto machine before handling the transaction's events.
- State (account, Olm and Megolm sessions, device lists) is kept in memory and
  written as a compressed snapshot to the key-value store a couple of seconds
  after it changes ([`server/e2ee/store.go`](../server/e2ee/store.go)), split
  into pieces below the key-value size limit.
- Outgoing events pass through `RoomCrypto` in the Matrix client, which is the
  one place that decides whether a room's events are encrypted.

The mautrix library is licensed MPL-2.0 (file-level copyleft); this repository
is Apache-2.0. Linking the library is permitted; modifications to mautrix files
themselves would have to be published under the MPL.

## Tests

[`e2e/encryption_test.go`](../e2e/encryption_test.go) runs a real encrypting
Matrix client (its own device, using the same library) against Synapse and
Mattermost: messages and attachments both ways, a reused room key, and a check
that the homeserver only holds ciphertext.
