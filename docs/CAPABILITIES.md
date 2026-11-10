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
| @channel / @here ↔ @room | Mattermost → Matrix | ❌ | @channel should set m.mentions.room; @room should become @channel. |
| @room ↔ @channel | Matrix → Mattermost | ❌ |  |
| Pinned messages | Mattermost → Matrix | ❌ | m.room.pinned_events. |
| Pinned messages | Matrix → Mattermost | ❌ |  |

## Reactions

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Add and remove reactions | Mattermost → Matrix | ✅ | 4,400+ emoji names mapped to Unicode. |
| Add and remove reactions | Matrix → Mattermost | ✅ |  |
| Custom emoji reactions | Mattermost → Matrix | ❌ | Should upload the emoji image and react with an mxc:// key (MSC4027). |
| Image reactions (MSC4027) | Matrix → Mattermost | ❌ | Should create a custom emoji on the fly. |

## Attachments

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Files, images, audio, video | Mattermost → Matrix | ✅ | One m.file/m.image/m.audio/m.video event per attachment. |
| Files, images, audio, video | Matrix → Mattermost | ✅ | Downloaded through authenticated media endpoints. |
| Stickers (m.sticker) | Matrix → Mattermost | ❌ | Should be posted as an image attachment. |
| Location (m.location) | Matrix → Mattermost | ❌ | Should be posted as a map link. |
| Polls (MSC3381) | Matrix → Mattermost | ❌ | Could map to interactive message attachments. |

## Ephemeral

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Typing notifications | Mattermost → Matrix | ❌ | Mattermost exposes no plugin hook for typing; a websocket listener in the plugin could provide it. |
| Typing notifications | Matrix → Mattermost | ✅ | Requires receive_ephemeral in the registration. |
| Presence / status | Mattermost → Matrix | ❌ | Ghosts should mirror online/away/offline. |
| Presence / status | Matrix → Mattermost | ✅ |  |
| Custom status ↔ presence status_msg | Mattermost → Matrix | ❌ |  |
| Read receipts | Mattermost → Matrix | ❌ | Channel views could become m.read receipts from the ghost. |
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
| One call both sides can join | Mattermost → Matrix | 🟡 | /matrix call adds a widget to the room and posts a card with the configured Element Call URL. The bridge does not verify the call service itself. |
| Audio and video relayed between Mattermost Calls and MatrixRTC | Mattermost → Matrix | ❌ | Both sides run their own media servers and signalling; a relay would be a full WebRTC endpoint on each side. Not planned. |
| Call recordings and transcripts | Mattermost → Matrix | ❌ | The Calls plugin keeps them in the call's thread; not relayed yet. |

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
| Linking a native account retires the mirror account | Mattermost → Matrix | ✅ | When a person enables double puppeting with a native Matrix account, their mirror account leaves the bridged rooms, the native account is invited, and the old name stays reserved. A claimed mirror account is not made to leave. |
| Team cut-over: read-only channels, pinned link, report, archive | Mattermost → Matrix | ✅ | /matrix migrate-team report | readonly | writable | archive (each changing step needs confirm). Read-only channels still receive what arrives from Matrix; the report compares members and messages not yet in each room. |

## Rooms

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Join and leave | Mattermost → Matrix | ✅ |  |
| Join and leave | Matrix → Mattermost | ✅ |  |
| Channel admin ↔ power levels | Mattermost → Matrix | ❌ |  |
| Power levels ↔ channel admin | Matrix → Mattermost | ❌ |  |
| New channel creates a room | Mattermost → Matrix | ✅ | Public and private channels; DMs on first message. |
| Inviting the bot creates a channel | Matrix → Mattermost | ❌ | Only /matrix join from Mattermost today. |
| Channel name/header → room name/topic | Mattermost → Matrix | ❌ | Mattermost has no channel-updated hook; needs polling or websocket. |
| Room name/topic → channel name/header | Matrix → Mattermost | ✅ |  |
| Archive ↔ tombstone/leave | Mattermost → Matrix | ❌ |  |
| Tombstone ↔ archive | Matrix → Mattermost | ❌ |  |
| Direct and group messages between Mattermost users | Mattermost → Matrix | ✅ | Opt-in: an administrator turns on Bridge Direct Messages and everyone in a conversation runs /matrix dm on; then new messages go to a private Matrix room. /matrix dm off unbridges. Earlier messages are not copied. |
| Direct messages | Matrix → Mattermost | ❌ | Mattermost refuses direct channels with remote users ("Cannot create a direct channel with a remote user"), so a DM started in Matrix cannot be created; needs a different account model for Matrix users. |
| Team ↔ space | Mattermost → Matrix | 🟡 | Spaces are created and rooms added; team membership is not mirrored. |
| End-to-end encrypted rooms: reading | Matrix → Mattermost | ✅ | With "Bridge Encrypted Rooms" on, the bridge bot holds a device and the room keys, decrypts what Matrix people send, and posts it. Encryption ends at the bridge (docs/ENCRYPTION.md). Off by default; with it off, encrypted rooms are refused instead. |
| End-to-end encrypted rooms: writing | Mattermost → Matrix | ✅ | Mattermost posts are encrypted with the bot's room key before they leave, so the homeserver only holds ciphertext. Messages, reactions and attachments; Matrix clients show the sender as the bridge's device, not as verified. |

## Integration

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Matrix widget showing the Mattermost channel | Mattermost → Matrix | ❌ | Plugin can serve a widget page; the bot sets im.vector.modular.widgets. |
| Bridge bot commands in Matrix (!mattermost …) | Matrix → Mattermost | 🟡 | help, whoami, link, login, logout, unlink work in DMs with the bot. Room linking from Matrix is not implemented yet. |
| Channel shows the linked room in Mattermost | Mattermost → Matrix | ✅ | The bridge panel (app bar icon) shows the channel's room, who is in it and a link to open it. There is no button in the channel header itself. |
| Post menu: Open in Matrix and Copy Matrix event ID | Mattermost → Matrix | ✅ | Both actions appear on posts that were synced to Matrix. Open in Matrix uses Element Web when its address is configured and matrix.to otherwise. The server route is tested against a real homeserver; the menu itself is covered by webapp unit tests only. |
| Structure backfill (users, teams, channels) | Mattermost → Matrix | ✅ | /matrix backfill creates ghosts, spaces and rooms. |
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

## Totals

- ✅ supported: 54
- 🟡 partial: 4
- ❌ gap: 25
- — n/a: 0
