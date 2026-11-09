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
| Deactivated users leave rooms | Mattermost → Matrix | ❌ | UserHasBeenDeactivated should make the ghost leave and deactivate. |
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
| Ghosts to mirror accounts migration | Mattermost → Matrix | ✅ | /matrix migrate ghosts-to-mirror (dry run first, then confirm) creates each mirror account, moves the room memberships, retires the ghost and switches the mode; it can be repeated after a failure. Covered by unit tests against a fake homeserver. |
| Cut-over mode | Mattermost → Matrix | ✅ | Freezes accounts and rooms: nothing new is created, everything that exists keeps syncing. |

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
| Direct and group messages between Mattermost users | Mattermost → Matrix | ❌ | Not bridged: only channels with a room mapping sync, and a new direct message has none, so no Matrix room is created. Bridging private conversations is a deliberate opt-in that is not built yet; it matters for migrating a community (see the issue on direct messages). |
| Direct messages | Matrix → Mattermost | ❌ | Mattermost refuses direct channels with remote users ("Cannot create a direct channel with a remote user"), so a DM started in Matrix cannot be created; needs a different account model for Matrix users. |
| Team ↔ space | Mattermost → Matrix | 🟡 | Spaces are created and rooms added; team membership is not mirrored. |
| End-to-end encrypted rooms | Matrix → Mattermost | ❌ | Refused, not half-working: an encrypted room cannot be mapped, and a mapped room that turns encryption on stops syncing in both directions with a warning in /matrix status and the panel; nothing is sent in the clear. Support through mautrix-go's crypto is planned (docs/ENCRYPTION.md). |

## Integration

| Capability | Direction | Status | Notes |
|---|---|---|---|
| Matrix widget showing the Mattermost channel | Mattermost → Matrix | ❌ | Plugin can serve a widget page; the bot sets im.vector.modular.widgets. |
| Bridge bot commands in Matrix (!mattermost …) | Matrix → Mattermost | 🟡 | help, whoami, link, login, logout, unlink work in DMs with the bot. Room linking from Matrix is not implemented yet. |
| Channel shows the linked room in Mattermost | Mattermost → Matrix | ✅ | The bridge panel (app bar icon) shows the channel's room, who is in it and a link to open it. There is no button in the channel header itself. |
| Structure backfill (users, teams, channels) | Mattermost → Matrix | ✅ | /matrix backfill creates ghosts, spaces and rooms. |
| Message history import with original timestamps | Mattermost → Matrix | 🟡 | /matrix history (preview, then confirm) replays a channel's posts into its room oldest first, as their authors, with the original time, threads intact and safe to repeat. Not yet: attachments, reactions, edits history, direct messages, a bulk mode for very large histories. Accounts that use their own token (double puppeting) show the import time. |

## Totals

- ✅ supported: 37
- 🟡 partial: 4
- ❌ gap: 28
- — n/a: 0
