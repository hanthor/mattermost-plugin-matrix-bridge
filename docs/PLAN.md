# Plan: seamless for everyone, and a way out of Mattermost

Two goals drive the roadmap:

1. **Seamless day-to-day use.** A person on either network should not notice
   the bridge: one identity, full conversation fidelity, presence, and the
   bridge visible inside each client rather than only through slash commands.
2. **A migration path.** The bridge should let a Mattermost deployment move to
   Matrix without losing identity, structure or history, run both side by side
   for as long as needed, then cut over.

The test-enforced status of every capability is [CAPABILITIES.md](CAPABILITIES.md);
`TestCapabilityGaps` in `e2e/` fails when a documented gap starts working, so
promoting a row is part of each change below.

> GitHub Issues are disabled on this repository, so the issues are drafted
> here. Each `###` heading below is one issue; the `Epic` headings are the
> parent issues. File them once Issues are enabled.

## Epic 1 — One identity on both sides

A Mattermost user and a Matrix user who are the same person should be one
identity: their Mattermost posts appear from their real Matrix account and
their Matrix posts from their real Mattermost account.

### Bridge-bot commands in Matrix DMs ✅ (shipped)
`!mattermost help | whoami | link <code> | login <token> | logout | unlink`.
The bot accepts invites. Rows: `integration.bot_commands.in` (partial: room
management from Matrix still missing).

### Self-service account linking ✅ (shipped)
`/matrix link` issues a one-time code; redeeming it with the bot binds the
accounts and inbound events from the Matrix account post as the real
Mattermost user, whose profile is left alone. Row: `identity.link`.

### Double puppeting ✅ (shipped)
`!mattermost login <access_token>` (verified with `/account/whoami`) makes the
bridge send messages, edits and reactions as the linked Matrix account. Row:
`users.puppeting`.

### Automatic linking through shared SSO
When Mattermost (OIDC/SAML) and the homeserver (Matrix Authentication Service)
share an identity provider, match accounts by verified email and link without
any user action. Obtain puppet tokens through MAS/OIDC token exchange instead
of asking for access tokens. Row: `identity.sso`.

### Linked identities everywhere
Use the linked account (not the ghost) for membership joins/leaves, typing,
presence and profile; stop creating `matrix:<name>` remote accounts for linked
people; migrate existing remote accounts' posts when a link is created.

## Epic 2 — Conversation fidelity parity

### `@channel` / `@here` ↔ `@room`
Rows `messages.channel_mentions`, `messages.channel_mentions.in`.

### Pinned messages ↔ `m.room.pinned_events`
Rows `messages.pins`, `messages.pins.in`.

### Custom emoji ↔ image packs and image reactions
MSC2545 room/space image packs for Mattermost custom emoji; MSC4027 image
reactions both ways. Rows `reactions.custom_emoji`, `reactions.custom_emoji.in`.

### Stickers, locations and polls inbound
Rows `files.stickers.in`, `files.location.in`, `polls.in`.

### Reply fallbacks
Send `m.in_reply_to` alongside `m.thread` so non-thread Matrix clients keep
context; render Matrix replies with quoted context in Mattermost.

### Notification parity
Mention and keyword highlights map to the other side's notification model.

### Moderation
Kicks, bans and message reports in both directions.

## Epic 3 — Mattermost → Matrix ephemeral and channel state (websocket client)

Mattermost has no plugin hooks for typing, status, read state, channel updates
or archiving. A supervised websocket client in the plugin (bot token,
single consumer per cluster) translates:

### Typing, presence, custom status
Rows `ephemeral.typing`, `ephemeral.presence`, `ephemeral.custom_status`.

### Read receipts from Mattermost
`channel_viewed` → `m.read` from the ghost. Row `ephemeral.read_receipts`.
(Matrix → Mattermost receipts need a new Mattermost plugin API; propose upstream.)

### Channel rename, header and archive
Rows `rooms.name_topic`, `rooms.archive`, `rooms.archive.in`.

### Deactivated users leave rooms
Row `users.deactivation`.

## Epic 4 — Native integration UI

### Room widget
`im.vector.modular.widgets` pointing at a plugin-served page with the linked
channel, members and a deep link. Row `integration.widget`.

### Channel header button and right-hand sidebar in Mattermost
Linked room, alias, Matrix members, "Open in Element", unmap.
Row `integration.channel_header`.

### System Console page
All mappings, bridge health, registration download.

### Message actions
"Open in Matrix" (matrix.to) and "Copy Matrix event ID".

### Power levels ↔ channel roles; bot-created channels; space membership
Rows `membership.roles`, `membership.roles.in`, `rooms.create.in`, `spaces.team`.

### Bridge-bot room management from Matrix
`!mattermost rooms | link-room <team>/<channel> | unlink-room | status`.

## Epic 5 — Migration toolkit

Mirror mode is the migration vehicle; the scenarios it has to handle (account
claiming through shared login, collisions, dual use, cut-over, encryption)
and the design for bridging calls are worked out in
[CALLS_AND_MIRROR_MODE.md](CALLS_AND_MIRROR_MODE.md), which also orders the
work: mirror-mode hygiene first, shared login second, then calls.

### History import with original timestamps
Replay post history as the real users with `origin_server_ts` (edits,
reactions, threads, files); resumable per channel; progress in
`/matrix status`; bulk mode from a Mattermost export. Row `integration.backfill`.

### Structure import completeness
Teams → spaces, channel metadata, admins → power levels, pinned posts, custom
emoji packs, DMs and group DMs (extends `/matrix backfill`).

### Cut-over tooling
`/matrix migrate-team`: read-only channels, pinned message with room link and
widget, mention redirection, later archive; verification report comparing
counts and members per channel/room.

### Account redirection
Mattermost login banner and email with the new Matrix ID; import of
`m.direct` and room tags so DMs and favourites land in the right place.

## Epic 6 — Operations

### Cluster-safe transaction deduplication (KV instead of per-node memory)
### Prometheus-style metrics endpoint
### Rate-limit auto-tuning from 429 responses
### Single-consumer election for the websocket client
### Encryption stance: refuse encrypted rooms clearly until MSC3202 is implemented

## Order of work

1. Epic 1 (identity) — in progress; shared SSO next.
2. Epic 3 (websocket client) — unblocks six rows at once.
3. Epic 2 — small, independent items; good first issues.
4. Epic 5 — history import first, since it is also the hardest to retrofit.
5. Epic 4 and 6 — in parallel as capacity allows.
