# Bridge capabilities

Generated from `e2e/capabilities/capabilities.go`; do not edit by hand. Run `make capabilities` after changing the Go file.

The end-to-end suite probes every row. A **supported** row that stops working fails CI; a **gap** that starts working also fails CI until it is promoted here, so this table always reflects what the bridge really does.

Legend: ✅ supported · 🟡 partial · ❌ gap · — not applicable

## Messages

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Text messages with formatting | Mattermost → Matrix | ✅ | Markdown → HTML, sent as the author's ghost. |
| Text messages with formatting | Matrix → Mattermost | ✅ | HTML → Markdown, posted as a remote user. |
| Edits | Mattermost → Matrix | ✅ | m.replace. |
| Edits | Matrix → Mattermost | ✅ |  |
| Deletions | Mattermost → Matrix | ✅ | Redaction of the message and its file events. |
| Redactions | Matrix → Mattermost | ✅ |  |
| Threads / replies | Mattermost → Matrix | ✅ | m.thread relation. |
| Threads / replies | Matrix → Mattermost | ✅ | m.thread and m.in_reply_to land in the post's thread. |
| Reply fallbacks keep context on both sides | Mattermost → Matrix | ✅ | Thread messages sent to Matrix also carry m.in_reply_to with is_falling_back, so clients without thread support show a reply to the thread's root. |
| Matrix replies keep the quoted message in Mattermost | Matrix → Mattermost | ✅ | A Matrix reply to a message that is not the thread's root appears in the thread with the replied-to message quoted (author and first line), since Mattermost threads have no reply-to. |
| User mentions | Mattermost → Matrix | ✅ | m.mentions and pills. |
| User mentions | Matrix → Mattermost | ✅ |  |
| @channel / @here ↔ @room | Mattermost → Matrix | ✅ | @channel, @all and @here in a Mattermost post set m.mentions.room on the Matrix message, so room-wide notifications fire. |
| @room ↔ @channel | Matrix → Mattermost | ✅ | A Matrix room mention (m.mentions.room, or the text @room) becomes @channel in the Mattermost post. |
| Pinned messages | Mattermost → Matrix | ✅ | Pinning or unpinning a post updates the room's m.room.pinned_events with the events of the pinned posts that were synced. In a room mapped with /matrix map the bot needs moderator rights (power level 50) to set it. |
| Pinned messages | Matrix → Mattermost | ✅ | Pinning an event in Matrix pins its post and unpinning unpins it; only events that have a post are affected. |
| Integration posts (webhooks, bots) keep their content | Mattermost → Matrix | 🟡 | The text of Slack-style attachments (pretext, title and link, text, fields, image link, footer) is sent as the message, and an overridden sender name is written as a bold prefix and also sent as a per-message profile (MSC4144) for clients that show it. /matrix history copies what bots posted when the Import bot and integration posts setting is on (unit-tested). An attachment's image stays a link: the bridge does not download addresses chosen by an integration (docs/ARCHITECTURE.md, Integration images stay links). Not yet: the integration's icon in Matrix. |
| Long Mattermost posts fit a Matrix event | Mattermost → Matrix | ✅ | A post whose text and HTML copy would exceed Matrix's event size loses the HTML copy first, then is cut and marked. Ordinary long posts (up to Mattermost's 16,383 characters) arrive whole. |
| Long Matrix messages are split into several posts | Matrix → Mattermost | ✅ | A Matrix message longer than Mattermost's 16,383 characters becomes consecutive posts, cut at paragraph breaks and marked as continued; an edit that is too long is cut with a marker instead of failing. Edits and reactions refer to the first post. |
| Mattermost permalinks and ~channel mentions work in Matrix | Mattermost → Matrix | ✅ | A permalink to a bridged post becomes a link to its Matrix event (Element Web when its address is set, otherwise matrix.to), and ~channel becomes a link to the channel's room. Links to posts and channels that are not bridged are left as they are. History import applies the same rewriting, so old permalinks keep working after a cut-over. |
| Matrix event and room links work in Mattermost | Matrix → Mattermost | ✅ | A matrix.to or Element link to a bridged event becomes a Mattermost permalink, and a link to a bridged room becomes ~channel. Links to rooms and events that are not bridged, and user links, are left as they are. |

## Reactions

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Add and remove reactions | Mattermost → Matrix | ✅ | 4,400+ emoji names mapped to Unicode. |
| Add and remove reactions | Matrix → Mattermost | ✅ |  |
| Custom emoji reactions | Mattermost → Matrix | ✅ | A reaction with a Mattermost custom emoji is sent as an image reaction (MSC4027): the key is the uploaded image (kept per emoji) and the shortcode names it. Clients without support show the shortcode. MSC2545 image packs are not published yet. |
| Image reactions (MSC4027) | Matrix → Mattermost | ✅ | An image reaction from Matrix becomes a Mattermost custom emoji with that shortcode, created once through a small bot account and reused; if it cannot be created the reaction falls back to its text. |

## Attachments

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Files, images, audio, video | Mattermost → Matrix | ✅ | One m.file/m.image/m.audio/m.video event per attachment. |
| Files, images, audio, video | Matrix → Mattermost | ✅ | Downloaded through authenticated media endpoints. |
| Stickers (m.sticker) | Matrix → Mattermost | ✅ | A sticker is posted as an image attachment. |
| Location (m.location) | Matrix → Mattermost | ✅ | A location is posted as a link to OpenStreetMap with the shared label. |
| Polls (MSC3381) | Matrix → Mattermost | 🟡 | A poll start is posted as its question and numbered options. Votes are not relayed. |

## Ephemeral

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Typing notifications | Mattermost → Matrix | ✅ | Needs Send Typing, Presence and Read Receipts to be on. A small bot (matrix-bridge-events) joins bridged channels, reads the server's typing events and shows the person typing in the room. In a cluster one server does it. |
| Typing notifications | Matrix → Mattermost | ✅ | Requires receive_ephemeral in the registration. |
| Presence / status | Mattermost → Matrix | ✅ | Needs the same setting. Online, away and do-not-disturb become online, unavailable and unavailable; checked every 30 seconds (adjustable). |
| Presence / status | Matrix → Mattermost | ✅ |  |
| Custom status ↔ presence status_msg | Mattermost → Matrix | ✅ | Needs the same setting. The custom status (emoji and text) becomes the presence status message; expired ones are cleared. |
| Read receipts | Mattermost → Matrix | ✅ | Needs the same setting. People who viewed a recently active channel after the latest bridged message get a read receipt on it, checked every 30 seconds. The other direction needs a Mattermost API that does not exist. |
| Read receipts | Matrix → Mattermost | ❌ | Mattermost has no plugin API to mark a channel viewed for another user. |

## Users

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Display name and avatar | Mattermost → Matrix | ✅ | Refreshed on the user's next activity or by a 15-minute sweep; Mattermost has no profile-change hook. |
| Display name and avatar | Matrix → Mattermost | ✅ |  |
| Deactivated users leave rooms | Mattermost → Matrix | ✅ | A deactivated person's ghost or unclaimed mirror account leaves every bridged room; with an admin token the mirror account is also deactivated on the homeserver. A claimed account is the person's own and is left alone. |
| Self-service account linking | Matrix → Mattermost | ✅ | /matrix link issues a code; "!mattermost link <code>" to the bridge bot binds the accounts. Linked Matrix users post as their real Mattermost account. |
| Double puppeting (use my own Matrix account) | Mattermost → Matrix | ✅ | "!mattermost login <access_token>" lets the bridge send as the linked Matrix account. |
| Automatic linking through shared SSO (OIDC / Matrix Authentication Service) | Mattermost → Matrix | ❌ | Match accounts by verified email from a shared identity provider so linking is zero-touch. |
| Bridge panel in Mattermost (channel status, your Matrix account, claim, link, call) | Mattermost → Matrix | ✅ | An icon in the Mattermost app bar opens a side panel backed by the plugin's /api/v1 routes; everything in it is also available as a slash command. |

## Mattermost clients

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Matrix activity reaches connected Mattermost clients in real time | Matrix → Mattermost | ✅ | Posts, edits, deletes, reactions, typing, header, membership and profile changes arrive as websocket events, with unread counts. |
| A mention from Matrix notifies the Mattermost user | Matrix → Mattermost | ✅ | The post event lists the mentioned person and the mention count rises. |

## Calls

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Call announced in the room (who started, who is in, join link, ended) | Mattermost → Matrix | ✅ | A Mattermost Calls post becomes a notice with a join link and a com.mattermost.bridge.call state event; the notice is edited when the call ends. |
| Call shown as a card in the channel (participants, duration, join button) | Matrix → Mattermost | ✅ | m.rtc.member and org.matrix.msc3401.call.member state become a custom_matrix_call post that is updated as people join and leave. |
| One call both sides can join | Mattermost → Matrix | 🟡 | /matrix call adds a widget to the room and posts a card with the configured Element Call URL. The bridge does not verify the call service itself; docs/CALLS_AND_MIRROR_MODE.md has a manual checklist for a real deployment (the test environment cannot run the media server). |
| Audio and video relayed between Mattermost Calls and MatrixRTC | Mattermost → Matrix | ❌ | Both sides run their own media servers and signalling; a relay would be a full WebRTC endpoint on each side. Not planned. |
| Call recordings and transcripts | Mattermost → Matrix | ❌ | Not claimed: no end-to-end test can verify it (the stack does not run the Mattermost Calls plugin). The bridge does carry a recording or transcript the Calls plugin posts (custom_calls_recording, custom_calls_transcription, file attached) to the room with a caption, in the thread of the call's announcement; that is unit-tested against the post format in the plugin's source, and becomes Supported once someone verifies it against a running Calls plugin. |

## Mirror mode

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Real @username accounts instead of ghosts | Mattermost → Matrix | ✅ | Created without credentials; the first account pins the mode so it cannot be switched under existing accounts. |
| Name collisions with existing accounts | Mattermost → Matrix | ✅ | A name taken by an account the bridge did not create gets a suffixed one (@name_mm) and is reported by /matrix status. |
| Claiming an account | Matrix → Mattermost | ✅ | /matrix claim with an admin token, or the first message from the person's own client after shared login. Their messages then post as them and the bridge keeps sending through the appservice. |
| Shared login with MAS and an OpenID Connect provider | Matrix → Mattermost | ✅ | The bridge creates each mirror account in MAS and links it to the person's OpenID Connect subject, so the company login lands on the account it made. Verified against real Synapse, MAS and Dex (docs/MAS_SETUP.md); the plugin driving a Mattermost signed in with OpenID Connect is not covered end to end. |
| Shared login with Mattermost as the identity provider | Matrix → Mattermost | ✅ | Mattermost is an OAuth 2.0 provider, not OpenID Connect, so MAS is configured without discovery and reads the person from /api/v4/users/me (docs/MAS_SETUP.md). Tested against a real Mattermost and MAS. |
| Ghosts to mirror accounts migration | Mattermost → Matrix | ✅ | /matrix migrate ghosts-to-mirror (dry run first, then confirm) creates each mirror account, moves the room memberships, retires the ghost and switches the mode; it can be repeated after a failure. Covered by unit tests against a fake homeserver. |
| Cut-over mode | Mattermost → Matrix | ✅ | Freezes accounts and rooms: nothing new is created, everything that exists keeps syncing. |
| Telling people their Matrix ID, bringing over direct messages and favourites | Mattermost → Matrix | ✅ | /matrix welcome (preview, then confirm) sends everyone with an unclaimed mirror account a direct message from the bridge with their Matrix ID and how to claim it, and brings their bridged direct-message rooms (m.direct) and favourite channels (m.favourite) into the Matrix account. It is safe to repeat and never messages a person twice; claimed accounts are left alone. With the Email people their Matrix ID setting on, the same news is also emailed (unit-tested; the e2e stack has no mail server). Not done: a login banner, which Mattermost has no per-person way to show. |
| Linking a native account retires the mirror account | Mattermost → Matrix | ✅ | When a person enables double puppeting with a native Matrix account, their mirror account leaves the bridged rooms, the native account is invited, and the old name stays reserved. A claimed mirror account is not made to leave. |
| Team cut-over: read-only channels, pinned link, report, archive | Mattermost → Matrix | ✅ | /matrix migrate-team report | readonly | writable | archive (each changing step needs confirm). Read-only channels still receive what arrives from Matrix; the report compares members and messages not yet in each room. |

## Identity

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Automatic linking by verified email | Matrix → Mattermost | ✅ | With "Link people by verified email" on and a Matrix admin token configured, a Matrix person who first appears in a bridged room (joining or writing) is linked to the Mattermost account with the same verified email instead of getting a separate remote account. Both sides must have verified the address, the Mattermost account must be active and not linked to someone else. When the homeserver signs people in through Matrix Authentication Service and the MAS admin client is set, the link also gives the bridge the person's own token, so no command is needed; otherwise puppeting needs !mattermost login. |
| Own-account token for people who sign in through MAS | Mattermost → Matrix | ✅ | When the homeserver delegates sign-in to Matrix Authentication Service, the bridge can obtain a personal session for a person's own Matrix account from MAS and use it as the access token for double puppeting, so nobody pastes a token. Needs the MAS admin client; only done after the person proved they own the account: by sending `!mattermost login` from it, or, with automatic linking by verified email, as soon as the link is made (the automatic part is unit-tested; the real-stack test covers the MAS call and the token). |
| Linking retires the ghost and any Matrix-side shadow user | Mattermost → Matrix | ✅ | When a person enables double puppeting, their ghost leaves the bridged rooms, their own Matrix account takes its place (also in mention pills), and the remote shadow user that stood for them in Mattermost is deactivated. Unlinking brings the ghost back. |

## Privacy

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Erase a person from the Matrix side | Mattermost → Matrix | ✅ | /matrix erase <username> (preview, then confirm; system administrators) redacts the messages the person's bridge account sent in bridged rooms, takes the account out of the rooms and, for a mirror account with a Matrix admin token, deactivates and erases it on the homeserver. Mattermost's own data is untouched. Messages sent as a person's own linked Matrix account cannot be redacted by the bridge. Not covered: messages deleted by Mattermost's data retention job, which fires no hook. |

## Rooms

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Join and leave | Mattermost → Matrix | ✅ |  |
| Join and leave | Matrix → Mattermost | ✅ |  |
| Channel admin ↔ power levels | Mattermost → Matrix | ✅ | Channel admins get power level 50 (moderator) in the room, and lose it when they stop being admins. Checked every 15 minutes, on /matrix repair, and every few seconds when real-time events are on. The bridge bot needs power level 100 in the room to change power levels, which rooms it creates give it. |
| Power levels ↔ channel admin | Matrix → Mattermost | ✅ | A Matrix person at power level 50 or more becomes a channel admin, and one below it an ordinary member. Applies to people on Matrix and linked accounts, not to accounts the bridge runs for Mattermost people. |
| New channel creates a room | Mattermost → Matrix | ✅ | Public and private channels; DMs on first message. |
| Inviting the bot creates a channel | Matrix → Mattermost | ✅ | With a team named in Create Channels for Invited Rooms, a moderator of a Matrix room who invites the bridge bot gets a new public channel in that team, bridged to the room; when their Matrix account is linked they become its admin. Off by default. |
| Channel name/header → room name/topic | Mattermost → Matrix | ✅ | A rename becomes the room name and a header change becomes the topic, as soon as Mattermost's system post announces it. A sweep every 15 minutes catches anything missed. |
| Room name/topic → channel name/header | Matrix → Mattermost | ✅ |  |
| Archive ↔ tombstone/leave | Mattermost → Matrix | ✅ | Archiving a channel posts a notice in the room and the bridge bot leaves; restoring the channel rejoins and says so. |
| Tombstone ↔ archive | Matrix → Mattermost | ✅ | A tombstoned room archives its channel. |
| Direct and group messages between Mattermost users | Mattermost → Matrix | ✅ | Opt-in: an administrator turns on Bridge Direct Messages and everyone in a conversation runs /matrix dm on; then new messages go to a private Matrix room. /matrix dm off unbridges. Earlier messages are not copied. |
| Direct messages | Matrix → Mattermost | ❌ | Mattermost refuses direct channels with remote users ("Cannot create a direct channel with a remote user"), so a DM started in Matrix cannot be created; needs a different account model for Matrix users. |
| Team ↔ space | Mattermost → Matrix | ✅ | Spaces are created by /matrix backfill and rooms are added to them; a person who joins or leaves the team has their account join or leave the space. |
| End-to-end encrypted rooms: reading | Matrix → Mattermost | ✅ | With "Bridge Encrypted Rooms" on, the bridge bot holds a device and the room keys, decrypts what Matrix people send, and posts it. Encryption ends at the bridge (docs/ENCRYPTION.md). Off by default; with it off, encrypted rooms are refused instead. |
| End-to-end encrypted rooms: writing | Mattermost → Matrix | ✅ | Mattermost posts are encrypted with the bot's room key before they leave, so the homeserver only holds ciphertext. Messages, reactions and attachments; Matrix clients show the sender as the bridge's device, not as verified. |
| A message the bridge cannot decrypt is announced | Matrix → Mattermost | ✅ | When an encrypted message cannot be read (the bridge was not given its key, for example because it was sent before the bridge joined), the channel gets a short notice from the sender instead of nothing, once per message. |
| The bridge's encryption device can be verified | Mattermost → Matrix | ✅ | /matrix encryption-verify confirm (system administrators) gives the bridge bot a cross-signing identity and signs its device, so clients stop warning about messages that come from Mattermost. The recovery key is shown once in the reply and is not stored by the bridge; losing it and the bridge's stored state means a new device. Not yet: server-side key backup. |

## Integration

| Capability | Direction | Status | Notes |
|---|---|---|---|
| System Console page: health, mappings and what is waiting | Mattermost → Matrix | ✅ | System Console → Plugins → Matrix bridge starts with a status page for system administrators: the doctor's checks with what to fix, every bridged channel with its Matrix room, the mode, mirror account and claim counts, and the number of changes that could not be delivered. The registration download is on the same page. |
| Matrix widget showing the Mattermost channel | Mattermost → Matrix | ✅ | A bridged room carries a widget (im.vector.modular.widgets) that shows the channel's name, team, purpose and member count with a button to open it in Mattermost. The page is served by the plugin and only loads with an address signed for that channel; it is removed when the channel is unmapped. Needs the bot to have power to set state in the room. |
| Bridge bot commands in Matrix (!mattermost …) | Matrix → Mattermost | ✅ | In a direct message with the bot: help, whoami, link, login, logout, unlink. In a bridged or unbridged room: rooms, status, link-room <team>/<channel> and unlink-room, run as the linked Mattermost account through the same slash commands (so Mattermost's permissions apply) and needing moderator rights in the room. |
| Channel shows the linked room in Mattermost | Mattermost → Matrix | ✅ | The bridge panel (app bar icon) shows the channel's room, who is in it and a link to open it. There is no button in the channel header itself. |
| Post menu: Open in Matrix and Copy Matrix event ID | Mattermost → Matrix | ✅ | Both actions appear on posts that were synced to Matrix. Open in Matrix uses Element Web when its address is configured and matrix.to otherwise. The server route is tested against a real homeserver; the menu itself is covered by webapp unit tests only. |
| Matrix clients show where a room is bridged to | Mattermost → Matrix | ✅ | A mapped room carries the bridge-information state event (m.bridge and Element's uk.half-shot.bridge) naming the Mattermost team, channel and link; it is emptied when the channel is unmapped. Needs the bot to be allowed to send state events, so rooms mapped with /matrix map where the bot is not a moderator keep no event (the doctor says so). Direct messages carry none. |
| Import a Matrix room's history into Mattermost | Matrix → Mattermost | ✅ | /matrix history from-matrix (preview, then confirm, optionally limit=N) copies the room's earlier messages into the channel as the people who wrote them, with their original times, the latest edit of each message applied, and reactions. Events already imported, the bridge's own and ones that cannot be read (encrypted without keys) are skipped and counted. Safe to repeat; each run reads up to 2,000 events. Files larger than the limit are announced. Threads follow the live path. |
| Panel lists who is in the Matrix room | Mattermost → Matrix | ✅ | The bridge panel lists the room's members with their Matrix ID and homeserver, marking people on Matrix, accounts the bridge runs for Mattermost people, and the bridge bot. |
| Structure backfill (users, teams, channels) | Mattermost → Matrix | ✅ | /matrix backfill creates ghosts, spaces and rooms. /matrix backfill structure goes through every bridged channel and brings its room up to date: name and topic (restoring a room that drifted), bridge information and widget, channel admins as power levels, pinned posts, and the custom emoji as the room's emoji pack (MSC2545). It is safe to repeat. Direct and group messages are not backfilled: they are bridged only for people who opted in with /matrix dm. |
| Message history import with original timestamps | Mattermost → Matrix | 🟡 | /matrix history (preview, then confirm) replays a channel's posts into its room oldest first, as their authors, with the original time, threads intact and safe to repeat. Not yet: attachments, reactions, edits history, direct messages, a bulk mode for very large histories. Accounts that use their own token (double puppeting) show the import time. |
| History import: attachments and reactions | Mattermost → Matrix | ✅ | Files are uploaded with their message and reactions are replayed from the people who reacted, all with their original times. Edits are imported as the latest text. |

## Operations

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Prometheus metrics endpoint | Mattermost → Matrix | ✅ | GET <site>/plugins/com.mattermost.plugin-matrix-bridge/metrics with the homeserver token as a bearer token: message and error counters, API latency, queue depth, accounts, rate-limit state and whether encryption is ready. |
| Retried homeserver transactions are handled once | Matrix → Mattermost | ✅ | Handled transaction IDs are kept in the key-value store, so a retry that reaches another node or follows a restart is recognised. Two nodes receiving the same transaction at the same instant are not guarded against. |
| Matrix events survive a restart and are delivered in order per room | Matrix → Mattermost | ✅ | Each event is written to the key-value store before the homeserver is told it arrived and removed when handled; what a crash leaves behind is replayed on start. Events of one room are handled by one worker in order, failures are retried three times, then set aside (/matrix retry replays them). |
| Mattermost changes reach Matrix after an outage, in order | Mattermost → Matrix | ✅ | Posts, edits, deletes and reactions are written to the key-value store before they are sent and removed once Matrix has them. While the homeserver is down they wait and are sent again with a growing delay (up to five minutes), channel by channel in order; after a day they are set aside for /matrix retry. A sent-again message carries the time it was sent again, not the time it was written. |
| /matrix doctor setup check | Mattermost → Matrix | ✅ | Checks the settings, the homeserver, the appservice token and server name, that the homeserver can call the plugin back (appservice ping), encryption, MAS, stuck deliveries, rate limits, and for the current channel its mapping, the bot's membership and power level. Never changes anything. Mirror mode namespace and registration namespaces cannot be probed safely and are reported as advice. |
| Adapts to the homeserver's 429 answers | Mattermost → Matrix | ✅ | Honours retry_after_ms: requests pause until the stated time, the refused request is repeated (up to three times), and the bridge's own limiters slow down until the homeserver stops complaining. Covered by unit tests with a fake homeserver; not exercised against a real rate-limited Synapse. |

## Membership

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Invite a Matrix user from Mattermost | Mattermost → Matrix | ✅ | /matrix invite @user:server (channel managers and administrators) has the bridge bot invite a Matrix user into the channel's room. Already present people and the bridge's own accounts are refused. |
| Inviting a Mattermost person's account from Matrix adds them to the channel | Matrix → Mattermost | ✅ | When a Matrix person invites the account that represents a Mattermost person (their ghost or mirror account) to a bridged room, that person is added to the channel, if they belong to its team. Direct messages and the bridge's own accounts are not affected. |
| Removing someone is carried across (kicks) | Matrix → Mattermost | ✅ | Removing a person from a channel takes their account out of the room (as a kick with the reason when Mattermost reports who removed them); a Matrix moderator's kick or ban of a Mattermost person's account, or of a Matrix person, removes them from the channel. Each side's own permissions decide who may. Mattermost has no channel bans, so a Matrix ban removes the person but does not stop Mattermost from adding them again; reports are not carried. |
| Rooms are repaired to match channel membership | Mattermost → Matrix | ✅ | /matrix repair (and a rotating background pass, twenty channels per run) adds the bridge accounts of channel members who are missing from the room and removes the accounts of people who left the channel. Real Matrix people, claimed mirror accounts and the bot are never touched; people with their own linked account must join themselves. |

## Files

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Image size is sent with the file | Mattermost → Matrix | ✅ | Width and height of images (from Mattermost's file information) are sent in the file event's info, so Matrix clients can reserve space before the picture loads. Duration, thumbnails, voice messages and streaming of large files are not done yet. A file over the size limit is announced with a short notice on the other side instead of being dropped silently; that notice is covered by unit tests only. |

## Federation

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Map and use a room that lives on another homeserver | Matrix → Mattermost | ✅ | /matrix map #room:other.server joins the room across federation; messages, edits, reactions and files from people on that server arrive, and Mattermost posts reach them. Tested with a second Synapse; other homeserver implementations are not tested yet. |
| A remote homeserver being unreachable does not stop the bridge | Mattermost → Matrix | ✅ | The bridge only talks to its own homeserver. While a remote member's server is down, Mattermost posts are accepted at normal speed and reach everyone on the bridge's server; the remote person's messages flow again once their server is back. Synapse retries delivery to a server that was down with a back-off, so the remote person may see the missed messages later than the others. |

## Totals

- ✅ supported: 97
- 🟡 partial: 4
- ❌ gap: 5
- — n/a: 0
