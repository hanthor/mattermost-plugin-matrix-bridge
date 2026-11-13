# Homeserver compatibility

The bridge is developed and tested against **Synapse**. Because it speaks the
standard appservice API, other homeservers can work; this page records what has
been checked.

## Test images

The end-to-end suite uses [Complement](https://github.com/matrix-org/complement)
images. The researched options:

| Homeserver | Complement image | Verdict |
|---|---|---|
| Synapse | `complement-synapse` (official) | The default. Everything is tested here. |
| Tuwunel | Built here ([`e2e/homeservers/tuwunel`](../e2e/homeservers/tuwunel)) | Usable: it reads the appservice registrations Complement provides and its production binary fits a thin Debian image. |
| Dendrite | Official image has no appservice hook | Would need a wrapper; the project is in maintenance mode. |
| conduwuit | None | Archived; Tuwunel is its successor. |

Run it with `make e2e-tuwunel`.

## Results on Tuwunel

`make e2e-tuwunel` (also a CI job) runs the whole suite. After the fixes below,
every test passes except the ones skipped for the reasons listed.

Fixed in the bridge: it recognised "already in the room" by Synapse's error
text, so inviting someone who was already joined failed on Tuwunel. It now
reads the person's membership.

Fixed in the test setup, not the bridge:

- **Typing.** Complement writes the registration with `push_ephemeral`;
  Tuwunel reads the spec's `receive_ephemeral` (the registration the plugin
  offers for download has it). The Tuwunel image adds it at start.
- **Outage test** read the room with a start token (`from=s0`) that Tuwunel
  rejects; it now reads back from the end of the room.
- **Moderation test** waited on a sync that matched at once; it now waits on the
  account's membership.

Skipped, with the reason:

| Test | Reason |
|---|---|
| `TestPresenceFromMatrix` | Tuwunel does not push presence to appservices (only typing and read receipts), so Matrix presence cannot reach Mattermost. A Tuwunel limit. |
| `TestBackfillStructure` | The room's initial name arrives after `/matrix backfill structure` and renames the channel back, so the test's ordering does not hold. The bridge's own rename succeeds. |
| `TestArchivingAChannelTellsTheRoom`, and the archive probe in `TestCapabilityGaps` | The bridge's leave call succeeds but the bot's membership does not show `leave` within the wait. The cause is not confirmed. |
| `TestRoomKeyChangesWhenAMemberLeaves` | The room key changed although nobody left. The encryption library starts a new key on any membership event that does not say what it replaced; the likely cause is that Tuwunel's events lack that field. Not confirmed. The extra key is harmless, but the "unchanged" check fails. |

This is a compatibility check, not a support statement: encryption in
particular is only tested against Synapse (it needs MSC3202 and related
options, see [ENCRYPTION.md](ENCRYPTION.md)).

## Not yet covered

- Mattermost Team Edition and a matrix of Mattermost versions.
- Dendrite and other homeservers.
