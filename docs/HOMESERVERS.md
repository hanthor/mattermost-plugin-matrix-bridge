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

On the full suite 47 of 57 tests passed on the first run. One failure was a
bridge bug and is fixed: the bridge recognised "already in the room" by
Synapse's error text, so inviting someone who was already joined failed on
Tuwunel. It now reads the person's membership.

Known differences, skipped by `make e2e-tuwunel` where noted:

- **Structure backfill** (skipped): the room's initial name arrives after
  `/matrix backfill structure` and renames the channel back, so the test's
  ordering does not hold. The bridge's own rename succeeds.
- **Archive** (skipped): the bridge's leave call succeeds but the bot's
  membership does not show `leave` within the test's wait. The cause is not
  confirmed.
- **Typing, presence and the outage test**: failed in the first run and have not
  been classified; they still run.

This is a compatibility check, not a support statement: encryption in
particular is only tested against Synapse (it needs MSC3202 and related
options, see [ENCRYPTION.md](ENCRYPTION.md)).

## Not yet covered

- Mattermost Team Edition and a matrix of Mattermost versions.
- Dendrite and other homeservers.
