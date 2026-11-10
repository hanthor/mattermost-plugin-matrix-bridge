# Privacy: what the bridge copies, and who controls it

Bridging copies people's messages into a second system with its own rules,
administrators and, when federation is on, other organisations. This page says
what leaves Mattermost, what comes back, who can read it, and what the bridge
does not yet do about it. Facts here follow the code; gaps are marked **Not
done**.

## What is copied

| From Mattermost to Matrix | Notes |
|---|---|
| Posts in a mapped channel: text, edits, deletes, reactions, files, replies and threads | Everything a channel member can see in that channel, sent as the author's ghost or mirror account (or as their own account with double puppeting) |
| Integration posts | Their text, including attachment content |
| Channel membership | Who joins and leaves a mapped channel |
| Display name and avatar | Sent when **Sync user profiles** is on; refreshed on the person's next activity and by a 15-minute sweep |
| In mirror mode, the Mattermost username | It becomes the Matrix username (`@ada:server`) |
| Direct and group messages | **Only** when every participant agreed with `/matrix dm`; the consent text says homeserver administrators can read the room |
| Message history | Only when an administrator runs `/matrix history` for a channel |

| From Matrix to Mattermost | Notes |
|---|---|
| Messages, edits, deletes, reactions, files, replies, threads, typing, presence | From any member of the mapped room, including people on other homeservers when federation is on |
| Matrix display names and avatars | Shown on the person's Mattermost account that represents them |

**Not copied:** Mattermost read state, drafts, direct messages that were not
agreed to, channels that are not mapped, private channels nobody mapped.

## Who can read the copies

- **Matrix room members**, on any homeserver the room federates to. A room made
  by the bridge is public or invite-only according to the channel's type;
  check the preset before bridging a private channel.
- **Homeserver administrators** can read every unencrypted room on their server.
  Bridged direct-message rooms are readable by them; end-to-end encryption for
  those rooms is a follow-up (#67).
- **The bridge** reads plain text of every bridged message, including in
  encrypted rooms: the bridge bot holds the room keys (see ENCRYPTION.md).
- **Mattermost administrators** can read personal Matrix access tokens only in
  sealed form (OPERATIONS.md, "Secrets and what protects them").

## Choices people and administrators have

- **Who bridges a channel.** System administrators and channel managers
  (`/matrix create`, `/matrix map`). Members are not asked first.
  **Not done:** a per-person "do not bridge my messages" for channels, and an
  automatic notice in the channel when it becomes bridged. Until then, tell
  people in the channel yourself; the channel header is a good place.
- **Direct messages** are opt-in per person (`/matrix dm`).
- **Their own account on Matrix.** `/matrix link` and `!mattermost login` let a
  person post as their own Matrix account instead of a ghost.
- **Mirror accounts** keep a person's Matrix identity after they leave
  Mattermost; see below.

## Retention and erasure

- **Mattermost data retention** deletes old posts in the database without
  telling the plugin, so their Matrix copies stay. **Not done:** a job that
  redacts Matrix events whose post no longer exists.
- **Matrix room retention** (`m.room.retention`) removes events on the
  homeserver without telling the bridge; the Mattermost copies stay.
  **Not done:** mirroring the channel's retention policy at mapping time.
- **Deleting a post** in either system is carried to the other (a redaction in
  Matrix).
- **Deactivating a Mattermost user** deactivates their mirror account or ghost
  (users.deactivation). **Not done:** erasing their bridged events when they are
  anonymised or deleted for a data-protection request. Today an administrator
  redacts them in Matrix by hand or with the homeserver's admin tools.
- **Federation.** Once an event reaches another homeserver, redaction asks that
  server to remove it; it cannot be forced.

## What the bridge stores itself

In the Mattermost key-value store: channel ↔ room and post ↔ event mappings,
ghost and mirror account IDs, identity links, consent and cut-over flags,
transaction IDs, the delivery journal and outbox (message content while it is
waiting to be delivered, removed afterwards), and, with encryption on, the
bot's keys. Personal Matrix access tokens and the encryption key protecting the
bot's state are sealed. Metrics contain counts only, never message content.

## Checklist before bridging a channel

1. Everyone in the channel knows it is bridged and who can read the room.
2. The Matrix room's join rule and history visibility match what the channel
   promised (a bridged private channel must not become a public room).
3. Federation is off, or you accept that copies leave your organisation.
4. You know who administers the homeserver, and that they can read the room.
5. For direct messages: everyone ran `/matrix dm` after reading its text.
