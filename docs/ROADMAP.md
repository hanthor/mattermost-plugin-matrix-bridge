# Roadmap: a bridge that is native on both sides

Most Matrix bridges connect to a closed network and can only do what that
network's API allows. This bridge connects two free-software networks, and it
runs *inside* Mattermost as a plugin while acting as a full Application
Service on the Matrix side. That lets us integrate deeply on both ends:
plugin UI, slash commands and hooks in Mattermost; widgets, bot commands,
state events and MSCs in Matrix.

The source of truth for what works today is
[CAPABILITIES.md](CAPABILITIES.md), generated from
`e2e/capabilities/capabilities.go`. The end-to-end suite probes every row:
`TestCapabilityGaps` runs each documented gap against real servers and fails
the build if a gap silently starts working (so the matrix is promoted) or a
supported capability breaks. Every item below therefore already has a failing
probe waiting for it.

## Principles

1. **Test first, in both directions.** A capability is added by promoting its
   row in `capabilities.go`, which turns the existing probe from "expected to
   fail" into "must pass".
2. **Users should not notice the bridge.** Mattermost users see ghosts with
   real names, avatars, presence and typing; Matrix users see remote users
   with the same.
3. **No licence requirements.** Everything must work on Team Edition through
   plugin hooks; Shared Channels is an optional accelerator.
4. **Spec first, MSC second, custom last.** Prefer stable spec features, then
   widely deployed MSCs (MSC2409 ephemeral, MSC3381 polls, MSC4027 image
   reactions, MSC3202 encryption), and only then bridge-specific events in the
   `com.mattermost.bridge.*` namespace.

## Phase 1 — Parity on the basics (close the ❌ rows in Messages/Reactions/Rooms)

| Gap | Approach |
|-----|----------|
| `@channel`/`@here` ↔ `@room` | Map in both Markdown→HTML and HTML→Markdown converters; set `m.mentions.room`. |
| Pinned messages ↔ `m.room.pinned_events` | Hook `MessageHasBeenUpdated` (IsPinned flips) → state event; state event → `PinPost`/`UnpinPost`. |
| Channel rename/header → room name/topic | Mattermost has no channel-updated hook. Subscribe to the server websocket from the plugin (bot token) for `channel_updated`, or poll mapped channels every minute. |
| Channel archive ↔ tombstone | `ChannelHasBeenDeleted` hook → `m.room.tombstone` and bot leaves; tombstone → `DeleteChannel`. |
| Inviting the bot creates a channel | Handle `m.room.member` invite for the bot in an unmapped room: join, create a channel named after the room in a configurable team, map it. The Matrix-side equivalent of `/matrix join`. |
| Deactivated users | `UserHasBeenDeactivated` hook → ghost leaves all rooms; reactivation re-joins. |
| Custom emoji reactions | Upload emoji image to the media repo once, react with `key: mxc://…` plus shortcode (MSC4027); inbound image reactions create a custom emoji. |
| Stickers, location, polls | Inbound only: sticker → image attachment, location → map link, poll → message attachment with the question and answers (votes bridged as replies). |

## Phase 2 — Presence and ephemeral both ways

| Gap | Approach |
|-----|----------|
| Typing Mattermost → Matrix | No plugin hook exists. Run a websocket client inside the plugin using a bot token; `typing` events for mapped channels become `PUT /rooms/{room}/typing/{ghost}`. |
| Presence Mattermost → Matrix | Same websocket (`status_change`) → `PUT /presence/{ghost}/status`; custom status → `status_msg`. |
| Read receipts Mattermost → Matrix | `channel_viewed` websocket event → `m.read` receipt from the ghost on the latest bridged event. |
| Read receipts Matrix → Mattermost | Blocked on a Mattermost plugin API to set another user's `LastViewedAt`; propose it upstream (`API.ViewChannelAsUser`). |

## Phase 3 — Deep integration

**Matrix side (Application Service):**

- **Bridge bot commands** in a DM with `@_mattermost_bridge`: `!mattermost link <team>/<channel>`, `unlink`, `status`, `whoami`. Same code path as the slash commands, with the same permission model mapped from room power levels.
- **Room widget**: the bot sets an `im.vector.modular.widgets` state event pointing at a page served by the plugin (`/plugins/<id>/widget/<room>`), showing the linked channel, its members and a "open in Mattermost" link. Element and other widget-capable clients render it in the room.
- **Power levels ↔ channel roles**: channel admins get PL 50, system admins PL 100 on their ghosts; PL changes in Matrix set `scheme_admin` on remote users.
- **Space membership**: team members' ghosts join the team space; Matrix users joining the space get a team membership.
- **Double puppeting**: let a Matrix user prove ownership of a Mattermost account (OAuth or a code pasted to the bot) so their own Matrix account is used instead of a ghost, and vice versa.

**Mattermost side (plugin):**

- **Channel header button + right-hand sidebar** showing the linked room, alias, Matrix members, and quick actions (unmap, open in Element).
- **System Console page** listing every mapping, bridge health (last appservice transaction, queue depth, Matrix API latency) and the generated registration.
- **Message action menu**: "Open in Matrix" (matrix.to link to the event) and "Copy Matrix event ID".
- **Backfill**: `/matrix backfill history [count]` using Mattermost post history and `MSC2716`-style batch sending or plain sequential sends with `origin_server_ts` (allowed for appservices).

## Phase 4 — Operations

- Cluster-safe transaction deduplication (KV store instead of an in-memory map).
- Prometheus-style metrics endpoint in addition to `/matrix metrics`.
- Rate-limit mode auto-tuning from `429` responses.
- Encrypted rooms via MSC3202 (appservice OTK and device-list delivery) once
  the Go crypto stack is settled; until then the bridge refuses to map
  encrypted rooms with a clear message.

## How to pick something up

1. Find the row in `e2e/capabilities/capabilities.go`.
2. Run its probe: `cd e2e && go test -run 'TestCapabilityGaps/<subtest>' ./...` (needs Docker and `COMPLEMENT_BASE_IMAGE`). It fails today.
3. Implement the feature; flip the row to `Supported`; run `make capabilities`.
4. The probe is now a regression test.
