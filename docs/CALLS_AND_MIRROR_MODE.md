# Design: calls, and the scenarios mirror mode has to handle

Two questions came up once the bridge did the everyday things: what happens
when someone starts a **call** on either side, and what exactly **mirror
mode** promises when a community uses it to move from Mattermost to Matrix.
This document settles the design for both so the work in
[PLAN.md](PLAN.md) can proceed in order.

**Status.** Implemented: mirror mode hygiene (credential-less accounts, the
account resolver, collisions, pinned mode, `/matrix claim` with an admin
token or through the first message after shared login), call presence in both
directions (Tier 1) and `/matrix call` with a configurable Element Call URL
(Tier 2). Retiring a mirror when a native account is linked later (scenario 4),
deactivation handling (scenario 5) and encrypted rooms (scenario 7, see
[ENCRYPTION.md](ENCRYPTION.md)) are implemented too. Not implemented:
recordings and transcripts as thread attachments and power levels from channel
roles (scenario 10). The ghost-to-mirror migration
(`/matrix migrate ghosts-to-mirror`) and a cut-over mode that freezes accounts
and rooms are implemented; cut-over is deliberately smaller than the
read-only relay sketched in scenario 6. The sections below describe the design; the
capability matrix is the authoritative list of what works.

## Part 1: Calls

### The two call systems are not bridgeable at the media level

| | Mattermost | Matrix |
| --- | --- | --- |
| Product | Calls plugin (`com.mattermost.calls`) | Element Call and other MatrixRTC clients; legacy 1:1 VoIP |
| Signalling | Plugin websocket events; a call is a post of type `custom_calls` whose props carry `start_at`, `end_at`, `participants`, `title`, recordings | MatrixRTC: per-participant room state (`org.matrix.msc3401.call.member`, standardised as `m.rtc.member`, MSC4143); legacy DMs use `m.call.invite` / `m.call.hangup` |
| Media | WebRTC through the plugin or `rtcd` | LiveKit SFU selected through `.well-known` (`org.matrix.msc4143.rtc_foci`, MSC4195) |
| Join from outside | `https://<site>/<team>/channels/<channel>?join_call=true` (needs a Mattermost account) | A Matrix account on any homeserver; Element Call also supports guest join when its homeserver allows it |

Both sides run their own SFU and their own signalling. A bot that joins both
and relays media would have to be a full WebRTC endpoint on each side, with
no shared room model, no shared mute or raise-hand state, and double
transcoding. That is a product in itself and is a **non-goal** for this
bridge.

What can be bridged well is everything *around* the call: that it started,
who is in it, how to join, and that it ended. And one call can be made
joinable from both sides by picking a backend both sides can reach.

### Tier 1: call presence, both directions (do first)

Mattermost → Matrix

- A `custom_calls` post with `start_at` is a call start. Instead of relaying
  it as an empty message (what happens today), the bridge sends an
  `m.notice` from the starter's ghost: "📞 Ada started a call in Mattermost.
  Join: <link>", where the link is the channel's `?join_call=true` URL, and
  sets a room state event `com.mattermost.bridge.call` with `{post_id,
  start_at, participants}` so widgets and future clients can render it.
- Participant changes update the state event; the notice is not repeated.
- When the post gains `end_at`, the notice is edited to "Call ended · 12 min
  · 3 participants" and the state event is cleared. Recordings and
  transcriptions, when present, are relayed as file attachments in the
  call's thread, which the Calls plugin already uses for them.

Matrix → Mattermost

- The transaction handler watches `m.rtc.member` and
  `org.matrix.msc3401.call.member` state. The first non-empty membership in
  a room starts a call; when all memberships are empty or expired the call
  ended. Legacy `m.call.invite` in a DM room is treated the same way.
- The bridge creates a Mattermost post of a new custom type
  `custom_matrix_call` as the starter's remote user. The webapp renders it as
  a card: title, participant avatars, duration, and a **Join in Element**
  button whose link is `https://matrix.to/#/<room>` or, when configured, the
  deployment's Element URL. On end the same post is updated, exactly as the
  Calls plugin updates its own post.
- Post props carry `matrix_room_id` and the call's start, so edits are
  idempotent and a bridge restart does not create a second card.

Both directions become rows in the capability matrix: `calls.presence`
(supported both ways once shipped) and `calls.media` (documented gap).
Complement tests: send an `m.rtc.member` state event as Alice and expect
the card post; create a `custom_calls` post and expect the notice and state
event; end both and expect the updates. The walkthrough gets a "Calls" pair
of screenshots.

### Tier 2: one call both sides can join

The shared backend is **Element Call (MatrixRTC)**, for three reasons: Matrix
users join natively from the room; Element Call runs in any browser so a
Mattermost user needs no install; and the identity work in Epic 1 gives
Mattermost users a real Matrix identity to join with. Mattermost Calls is
the wrong shared backend because it requires a Mattermost account on the
joining side, and provisioning Mattermost accounts for Matrix users is the
inverse of the migration direction this project cares about.

Flow

1. `/matrix call [title]` in a bridged channel, or the **Start call** item
   added to the channel header menu. The bridge adds an Element Call widget
   to the room (`im.vector.modular.widgets` state, which the bot can set as
   room creator) so Element shows its native "Join call" banner, and posts
   the `custom_matrix_call` card in Mattermost with a **Join** button.
2. The Join button opens Element Call in the browser for that room:
   - a user with a **linked** Matrix account (Epic 1) is sent to the
     deployment's Element with the room, where they are already logged in;
   - a **mirror-mode** user (Part 2) has their own account and joins the same
     way once they have claimed it;
   - anyone else uses Element Call's guest join when the homeserver allows
     it, otherwise the button explains that linking an account is needed.
3. Participant state flows through Tier 1, so Mattermost shows who is in the
   call even though the media never touches Mattermost.

A configuration setting `calls_mode` selects `off`, `presence` (Tier 1) or
`element_call` (Tier 1 + 2), with `element_call_url` for self-hosted Element
Call. Mattermost Calls keeps working as before for Mattermost-only
conversations; the bridge only mirrors it.

### Things to get right

- **Rooms the bot does not own.** Setting widget state needs power level 50
  by default. For rooms mapped with `/matrix map`, the bot asks for that
  level once and otherwise falls back to presence only, with a clear notice.
- **Direct messages.** A call in a bridged DM arrives as legacy `m.call.*`
  from most clients; Tier 1 handles it as presence. Tier 2 is for channels.
- **Threads.** The Calls plugin anchors recordings and transcripts in the
  call post's thread; the bridge keeps that thread mapping so they land
  under the same notice in Matrix.
- **Restarts and replays.** Everything is keyed on the Mattermost post ID
  and on the Matrix room plus call start timestamp, never on "the last
  message", so replayed transactions and restarts cannot duplicate cards.

## Part 2: Mirror mode

### What mirror mode is

With `enable_mirror_mode` on, the bridge stops creating `@_mattermost_<id>`
ghosts and instead provisions a **real-looking account per Mattermost user,
`@<username>:<server>`**, with rooms and aliases that carry no
`_mattermost_` prefix. The registration file claims the whole user
namespace (`@.*`), so the homeserver is dedicated to mirroring one
Mattermost. The intent is migration: one day the people simply log into
Matrix and find their own account, their rooms and their history waiting.

### Where the current implementation falls short

Reading the code with that intent in mind turns up five problems to fix
before mirror mode can be recommended:

1. **Nobody can log into a mirrored account.** Accounts are registered with
   the configured mirror password or, when none is set, a random string that
   is generated per call and never stored. The shared-password option is
   worse: one password for every account. Mirrored accounts must be created
   without a password (`inhibit_login`, appservice registration) and handed
   over through an explicit claim step.
2. **Collisions are not handled.** If `@alice` already exists natively, or
   two Mattermost users would map to the same localpart after
   normalisation, registration fails and the bridge logs "may already
   exist" and carries on using an account it does not control.
3. **Identity links are ignored.** A Mattermost user who already linked a
   native Matrix account (Epic 1) should never get a mirror account; the
   link *is* their account.
4. **Mode switching corrupts state.** Turning mirror mode on after ghosts
   exist leaves every user with two Matrix identities and two sets of room
   memberships. The setting must be locked once any mapping exists, with a
   migration command to move ghosts to mirrors.
5. **Mirrored accounts are tracked separately** (`mirror_user_<id>`) from
   ghosts and identity links, so code paths that resolve "the Matrix side of
   this user" disagree. One resolver, with precedence, is needed.

### The account resolver

Every place that needs "the Matrix account for Mattermost user X" asks one
function, which answers in this order:

1. an **identity link** (the user's own native account, puppeted with their
   token when they gave one);
2. in mirror mode, the **mirrored account** `@<localpart>:<server>`, created
   on first use with the collision rules below;
3. otherwise the **ghost** `@_mattermost_<id>:<server>`.

Localparts are derived from the Mattermost username (already restricted to
`a-z0-9._-`, all valid in Matrix) and recorded by Mattermost user ID, so a
later username change in Mattermost only updates the display name; Matrix
localparts are immutable. On `M_USER_IN_USE` the bridge checks whether the
existing account is one it already owns (recorded in its own KV); if not,
it falls back to `<username>_mm`, records the collision, and tells the
administrator through `/matrix status`.

### Scenarios

**1. Dedicated homeserver, nobody has migrated yet.** The common case today.
Mirrored accounts post, react and join as the person. Matrix-native people
from other homeservers interact with them over federation and see normal
users. The homeserver accepts no other registrations because the namespace
is exclusive. This already works apart from the five problems above.

**2. A person claims their account.** `/matrix claim` in Mattermost. The
bridge must hand over credentials it does not have, and an appservice cannot
set passwords. Two supported routes:

- **Shared login (recommended).** The homeserver authenticates through
  Matrix Authentication Service (MAS) with an **OpenID Connect provider that
  Mattermost also signs people in with** (Mattermost is an OAuth 2.0 provider,
  not an OIDC one, so it cannot be the provider itself). MAS refuses to create
  users that Synapse already has, so the bridge creates each mirror account in
  MAS too (admin API, `skip_homeserver_check`) and links it to the person's
  provider subject, which Mattermost records when they sign in with OpenID
  Connect. Signing in to Element with the company login then lands on the
  account the bridge made, with no password and no matching on names. Setup:
  [MAS_SETUP.md](MAS_SETUP.md).
- **Admin-token password reset (fallback).** An optional
  `matrix_admin_token` lets the bridge set a one-time password through the
  Synapse admin API and show it once to the user, who must change it on
  first login. Only for deployments that cannot run OIDC.

After a claim the account is marked `claimed` in KV. The bridge keeps
puppeting it through the appservice (the namespace still allows that), which
is exactly double puppeting without a stored token.

**3. The person now uses both.** Events in a bridged room from a claimed
account can come from the person's own client or from the bridge puppeting
them. Sender identity cannot tell them apart. The bridge therefore ignores
only the events **it sent**, recognised by the `com.mattermost.bridge`
metadata it puts in every event and by its transaction IDs, and relays
everything else as that Mattermost user, which the identity-link path
already does. Read receipts, typing and presence from the person's Matrix
client reach Mattermost through the existing ephemeral path; the reverse
needs the websocket client in Epic 3.

**4. Some people already have native Matrix accounts.** Their link takes
precedence (resolver step 1). If they ran `/matrix link` after a mirrored
account had been created for them, the bridge retires the mirror: it leaves
every room as the mirrored account, points the mapping at the linked
account and invites it where needed. The retired localpart stays reserved
so nobody else is handed it later.

**5. Leaving, deactivation and deletion.** A deactivated Mattermost user's
unclaimed mirror leaves every bridged room and, with an admin token, is
deactivated on the homeserver. A **claimed** account is the person's: the
bridge only stops puppeting it and leaves their memberships alone.

**6. Cut-over.** When Mattermost is retired, the bridge switches to
read-only relay: no new accounts, no new rooms, Mattermost posts still
mirrored until the server is turned off. Rooms keep their names and aliases
because they never carried the `_mattermost_` prefix. History already lives
in Matrix with original timestamps once the backfill work in Epic 5 lands.
At that point the appservice registration is removed, the exclusive
namespace with it, and the homeserver is an ordinary one.

**7. Encryption.** A claimed account's clients may try to enable end-to-end
encryption in bridged rooms. The bridge cannot read those rooms yet, so it
keeps `m.room.encryption` behind a power level only the bot holds and says
so in the room topic prefix. Crypto support for the appservice is listed in
the roadmap; until then this is a documented limit.

**8. Calls.** Claimed and linked users join Element Call natively (Part 1,
Tier 2). Unclaimed mirrored users see the Mattermost call card like
everyone else.

**9. Switching mirror mode on an existing deployment.** The setting is
locked once mappings exist. `/matrix migrate ghosts-to-mirror` (admin,
dry-run by default) creates the mirrored account for each ghost, copies the
profile, joins the same rooms, leaves as the ghost and rewrites the
mappings, in batches with progress in the channel. The reverse direction is
not supported.

**10. Power levels.** Mirrored rooms should look like the channel: system
administrators at power level 100 alongside the bot, channel administrators
at 50, everyone else at 0, and `m.room.encryption` reserved to the bot.
This is the "power levels ↔ channel roles" item in Epic 4 and matters more
in mirror mode because the rooms are meant to outlive Mattermost.

### Order of work

1. Mirror mode hygiene: passwordless registration, the account resolver
   with precedence and collision handling, setting lock, status reporting.
2. Shared login through OIDC with Mattermost as identity provider; claim
   flow; admin-token fallback.
3. Calls Tier 1 with capability rows, Complement tests and walkthrough
   screenshots.
4. Calls Tier 2 (Element Call widget and join cards).
5. Ghost-to-mirror migration command and cut-over mode.
