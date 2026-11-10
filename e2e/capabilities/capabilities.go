// Package capabilities is the single source of truth for what the bridge can
// and cannot do. Every feature either network offers is listed with its
// bridging status in each direction. The end-to-end suite probes each entry
// and fails when reality disagrees with this list in either direction: a
// documented capability that broke, or a documented gap that now works and
// should be promoted.
//
// docs/CAPABILITIES.md is generated from this file; see TestCapabilitiesDoc.
package capabilities

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// Status describes how well a capability is bridged.
type Status string

const (
	// Supported means the capability is bridged and covered by the e2e suite.
	Supported Status = "supported"
	// Partial means some cases work; the notes say which.
	Partial Status = "partial"
	// Unsupported means the capability is not bridged yet. The e2e suite
	// still probes it so the gap is visible in every run.
	Unsupported Status = "gap"
	// NotApplicable means the other network has no equivalent.
	NotApplicable Status = "n/a"
)

// Direction is the direction of a bridged capability.
type Direction string

const (
	// MattermostToMatrix flows from Mattermost into Matrix.
	MattermostToMatrix Direction = "Mattermost → Matrix"
	// MatrixToMattermost flows from Matrix into Mattermost.
	MatrixToMattermost Direction = "Matrix → Mattermost"
)

// Capability is one row of the matrix.
type Capability struct {
	// ID is a stable identifier used by tests, e.g. "messages.text".
	ID string
	// Area groups capabilities in the generated document.
	Area string
	// Name is a short human-readable name.
	Name string
	// Direction of the flow.
	Direction Direction
	// Status of the bridging.
	Status Status
	// Notes explain partial support, gaps and planned work.
	Notes string
}

// All lists every capability. Keep IDs stable; tests reference them.
var All = []Capability{
	// Messages
	{ID: "messages.text", Area: "Messages", Name: "Text messages with formatting", Direction: MattermostToMatrix, Status: Supported, Notes: "Markdown → HTML, sent as the author's ghost."},
	{ID: "messages.text.in", Area: "Messages", Name: "Text messages with formatting", Direction: MatrixToMattermost, Status: Supported, Notes: "HTML → Markdown, posted as a remote user."},
	{ID: "messages.edit", Area: "Messages", Name: "Edits", Direction: MattermostToMatrix, Status: Supported, Notes: "m.replace."},
	{ID: "messages.edit.in", Area: "Messages", Name: "Edits", Direction: MatrixToMattermost, Status: Supported, Notes: ""},
	{ID: "messages.delete", Area: "Messages", Name: "Deletions", Direction: MattermostToMatrix, Status: Supported, Notes: "Redaction of the message and its file events."},
	{ID: "messages.delete.in", Area: "Messages", Name: "Redactions", Direction: MatrixToMattermost, Status: Supported, Notes: ""},
	{ID: "messages.thread", Area: "Messages", Name: "Threads / replies", Direction: MattermostToMatrix, Status: Supported, Notes: "m.thread relation."},
	{ID: "messages.thread.in", Area: "Messages", Name: "Threads / replies", Direction: MatrixToMattermost, Status: Supported, Notes: "m.thread and m.in_reply_to land in the post's thread."},
	{ID: "messages.reply_fallback", Area: "Messages", Name: "Reply fallbacks keep context on both sides", Direction: MattermostToMatrix, Status: Supported, Notes: "Thread messages sent to Matrix also carry m.in_reply_to with is_falling_back, so clients without thread support show a reply to the thread's root."},
	{ID: "messages.reply_fallback.in", Area: "Messages", Name: "Matrix replies keep the quoted message in Mattermost", Direction: MatrixToMattermost, Status: Supported, Notes: "A Matrix reply to a message that is not the thread's root appears in the thread with the replied-to message quoted (author and first line), since Mattermost threads have no reply-to."},
	{ID: "messages.mentions", Area: "Messages", Name: "User mentions", Direction: MattermostToMatrix, Status: Supported, Notes: "m.mentions and pills."},
	{ID: "messages.mentions.in", Area: "Messages", Name: "User mentions", Direction: MatrixToMattermost, Status: Supported, Notes: ""},
	{ID: "messages.channel_mentions", Area: "Messages", Name: "@channel / @here ↔ @room", Direction: MattermostToMatrix, Status: Supported, Notes: "@channel, @all and @here in a Mattermost post set m.mentions.room on the Matrix message, so room-wide notifications fire."},
	{ID: "messages.channel_mentions.in", Area: "Messages", Name: "@room ↔ @channel", Direction: MatrixToMattermost, Status: Supported, Notes: "A Matrix room mention (m.mentions.room, or the text @room) becomes @channel in the Mattermost post."},
	{ID: "messages.pins", Area: "Messages", Name: "Pinned messages", Direction: MattermostToMatrix, Status: Supported, Notes: "Pinning or unpinning a post updates the room's m.room.pinned_events with the events of the pinned posts that were synced. In a room mapped with /matrix map the bot needs moderator rights (power level 50) to set it."},
	{ID: "messages.pins.in", Area: "Messages", Name: "Pinned messages", Direction: MatrixToMattermost, Status: Supported, Notes: "Pinning an event in Matrix pins its post and unpinning unpins it; only events that have a post are affected."},

	// Reactions
	{ID: "reactions.add_remove", Area: "Reactions", Name: "Add and remove reactions", Direction: MattermostToMatrix, Status: Supported, Notes: "4,400+ emoji names mapped to Unicode."},
	{ID: "reactions.add_remove.in", Area: "Reactions", Name: "Add and remove reactions", Direction: MatrixToMattermost, Status: Supported, Notes: ""},
	{ID: "reactions.custom_emoji", Area: "Reactions", Name: "Custom emoji reactions", Direction: MattermostToMatrix, Status: Unsupported, Notes: "Should upload the emoji image and react with an mxc:// key (MSC4027)."},
	{ID: "reactions.custom_emoji.in", Area: "Reactions", Name: "Image reactions (MSC4027)", Direction: MatrixToMattermost, Status: Unsupported, Notes: "Should create a custom emoji on the fly."},

	// Attachments
	{ID: "files.attachments", Area: "Attachments", Name: "Files, images, audio, video", Direction: MattermostToMatrix, Status: Supported, Notes: "One m.file/m.image/m.audio/m.video event per attachment."},
	{ID: "files.attachments.in", Area: "Attachments", Name: "Files, images, audio, video", Direction: MatrixToMattermost, Status: Supported, Notes: "Downloaded through authenticated media endpoints."},
	{ID: "files.stickers.in", Area: "Attachments", Name: "Stickers (m.sticker)", Direction: MatrixToMattermost, Status: Supported, Notes: "A sticker is posted as an image attachment."},
	{ID: "files.location.in", Area: "Attachments", Name: "Location (m.location)", Direction: MatrixToMattermost, Status: Supported, Notes: "A location is posted as a link to OpenStreetMap with the shared label."},
	{ID: "polls.in", Area: "Attachments", Name: "Polls (MSC3381)", Direction: MatrixToMattermost, Status: Partial, Notes: "A poll start is posted as its question and numbered options. Votes are not relayed."},

	// Presence and ephemeral
	{ID: "ephemeral.typing", Area: "Ephemeral", Name: "Typing notifications", Direction: MattermostToMatrix, Status: Supported, Notes: "Needs Send Typing, Presence and Read Receipts to be on. A small bot (matrix-bridge-events) joins bridged channels, reads the server's typing events and shows the person typing in the room. In a cluster one server does it."},
	{ID: "ephemeral.typing.in", Area: "Ephemeral", Name: "Typing notifications", Direction: MatrixToMattermost, Status: Supported, Notes: "Requires receive_ephemeral in the registration."},
	{ID: "ephemeral.presence", Area: "Ephemeral", Name: "Presence / status", Direction: MattermostToMatrix, Status: Supported, Notes: "Needs the same setting. Online, away and do-not-disturb become online, unavailable and unavailable; checked every 30 seconds (adjustable)."},
	{ID: "ephemeral.presence.in", Area: "Ephemeral", Name: "Presence / status", Direction: MatrixToMattermost, Status: Supported, Notes: ""},
	{ID: "ephemeral.custom_status", Area: "Ephemeral", Name: "Custom status ↔ presence status_msg", Direction: MattermostToMatrix, Status: Supported, Notes: "Needs the same setting. The custom status (emoji and text) becomes the presence status message; expired ones are cleared."},
	{ID: "ephemeral.read_receipts", Area: "Ephemeral", Name: "Read receipts", Direction: MattermostToMatrix, Status: Supported, Notes: "Needs the same setting. People who viewed a recently active channel after the latest bridged message get a read receipt on it, checked every 30 seconds. The other direction needs a Mattermost API that does not exist."},
	{ID: "ephemeral.read_receipts.in", Area: "Ephemeral", Name: "Read receipts", Direction: MatrixToMattermost, Status: Unsupported, Notes: "Mattermost has no plugin API to mark a channel viewed for another user."},

	// Users
	{ID: "users.profile", Area: "Users", Name: "Display name and avatar", Direction: MattermostToMatrix, Status: Supported, Notes: "Refreshed on the user's next activity or by a 15-minute sweep; Mattermost has no profile-change hook."},
	{ID: "users.profile.in", Area: "Users", Name: "Display name and avatar", Direction: MatrixToMattermost, Status: Supported, Notes: ""},
	{ID: "users.deactivation", Area: "Users", Name: "Deactivated users leave rooms", Direction: MattermostToMatrix, Status: Supported, Notes: "A deactivated person's ghost or unclaimed mirror account leaves every bridged room; with an admin token the mirror account is also deactivated on the homeserver. A claimed account is the person's own and is left alone."},
	{ID: "identity.link", Area: "Users", Name: "Self-service account linking", Direction: MatrixToMattermost, Status: Supported, Notes: "/matrix link issues a code; \"!mattermost link <code>\" to the bridge bot binds the accounts. Linked Matrix users post as their real Mattermost account."},
	{ID: "users.puppeting", Area: "Users", Name: "Double puppeting (use my own Matrix account)", Direction: MattermostToMatrix, Status: Supported, Notes: "\"!mattermost login <access_token>\" lets the bridge send as the linked Matrix account."},
	{ID: "identity.sso", Area: "Users", Name: "Automatic linking through shared SSO (OIDC / Matrix Authentication Service)", Direction: MattermostToMatrix, Status: Unsupported, Notes: "Match accounts by verified email from a shared identity provider so linking is zero-touch."},

	{ID: "ui.panel", Area: "Users", Name: "Bridge panel in Mattermost (channel status, your Matrix account, claim, link, call)", Direction: MattermostToMatrix, Status: Supported, Notes: "An icon in the Mattermost app bar opens a side panel backed by the plugin's /api/v1 routes; everything in it is also available as a slash command."},

	{ID: "client.realtime", Area: "Mattermost clients", Name: "Matrix activity reaches connected Mattermost clients in real time", Direction: MatrixToMattermost, Status: Supported, Notes: "Posts, edits, deletes, reactions, typing, header, membership and profile changes arrive as websocket events, with unread counts."},
	{ID: "client.mentions", Area: "Mattermost clients", Name: "A mention from Matrix notifies the Mattermost user", Direction: MatrixToMattermost, Status: Supported, Notes: "The post event lists the mentioned person and the mention count rises."},

	// Calls
	{ID: "calls.presence", Area: "Calls", Name: "Call announced in the room (who started, who is in, join link, ended)", Direction: MattermostToMatrix, Status: Supported, Notes: "A Mattermost Calls post becomes a notice with a join link and a com.mattermost.bridge.call state event; the notice is edited when the call ends."},
	{ID: "calls.presence.in", Area: "Calls", Name: "Call shown as a card in the channel (participants, duration, join button)", Direction: MatrixToMattermost, Status: Supported, Notes: "m.rtc.member and org.matrix.msc3401.call.member state become a custom_matrix_call post that is updated as people join and leave."},
	{ID: "calls.join", Area: "Calls", Name: "One call both sides can join", Direction: MattermostToMatrix, Status: Partial, Notes: "/matrix call adds a widget to the room and posts a card with the configured Element Call URL. The bridge does not verify the call service itself."},
	{ID: "calls.media", Area: "Calls", Name: "Audio and video relayed between Mattermost Calls and MatrixRTC", Direction: MattermostToMatrix, Status: Unsupported, Notes: "Both sides run their own media servers and signalling; a relay would be a full WebRTC endpoint on each side. Not planned."},
	{ID: "calls.recordings", Area: "Calls", Name: "Call recordings and transcripts", Direction: MattermostToMatrix, Status: Unsupported, Notes: "The Calls plugin keeps them in the call's thread; not relayed yet."},

	// Mirror mode
	{ID: "mirror.accounts", Area: "Mirror mode", Name: "Real @username accounts instead of ghosts", Direction: MattermostToMatrix, Status: Supported, Notes: "Created without credentials; the first account pins the mode so it cannot be switched under existing accounts."},
	{ID: "mirror.collisions", Area: "Mirror mode", Name: "Name collisions with existing accounts", Direction: MattermostToMatrix, Status: Supported, Notes: "A name taken by an account the bridge did not create gets a suffixed one (@name_mm) and is reported by /matrix status."},
	{ID: "mirror.claim", Area: "Mirror mode", Name: "Claiming an account", Direction: MatrixToMattermost, Status: Supported, Notes: "/matrix claim with an admin token, or the first message from the person's own client after shared login. Their messages then post as them and the bridge keeps sending through the appservice."},
	{ID: "mirror.sso_provider", Area: "Mirror mode", Name: "Shared login with MAS and an OpenID Connect provider", Direction: MatrixToMattermost, Status: Supported, Notes: "The bridge creates each mirror account in MAS and links it to the person's OpenID Connect subject, so the company login lands on the account it made. Verified against real Synapse, MAS and Dex (docs/MAS_SETUP.md); the plugin driving a Mattermost signed in with OpenID Connect is not covered end to end."},
	{ID: "mirror.sso_mattermost", Area: "Mirror mode", Name: "Shared login with Mattermost as the identity provider", Direction: MatrixToMattermost, Status: Supported, Notes: "Mattermost is an OAuth 2.0 provider, not OpenID Connect, so MAS is configured without discovery and reads the person from /api/v4/users/me (docs/MAS_SETUP.md). Tested against a real Mattermost and MAS."},
	{ID: "mirror.migrate", Area: "Mirror mode", Name: "Ghosts to mirror accounts migration", Direction: MattermostToMatrix, Status: Supported, Notes: "/matrix migrate ghosts-to-mirror (dry run first, then confirm) creates each mirror account, moves the room memberships, retires the ghost and switches the mode; it can be repeated after a failure. Covered by unit tests against a fake homeserver."},
	{ID: "mirror.cutover", Area: "Mirror mode", Name: "Cut-over mode", Direction: MattermostToMatrix, Status: Supported, Notes: "Freezes accounts and rooms: nothing new is created, everything that exists keeps syncing."},
	{ID: "mirror.link_retire", Area: "Mirror mode", Name: "Linking a native account retires the mirror account", Direction: MattermostToMatrix, Status: Supported, Notes: "When a person enables double puppeting with a native Matrix account, their mirror account leaves the bridged rooms, the native account is invited, and the old name stays reserved. A claimed mirror account is not made to leave."},
	{ID: "mirror.team_cutover", Area: "Mirror mode", Name: "Team cut-over: read-only channels, pinned link, report, archive", Direction: MattermostToMatrix, Status: Supported, Notes: "/matrix migrate-team report | readonly | writable | archive (each changing step needs confirm). Read-only channels still receive what arrives from Matrix; the report compares members and messages not yet in each room."},

	// Membership and rooms
	{ID: "membership.join_leave", Area: "Rooms", Name: "Join and leave", Direction: MattermostToMatrix, Status: Supported, Notes: ""},
	{ID: "membership.join_leave.in", Area: "Rooms", Name: "Join and leave", Direction: MatrixToMattermost, Status: Supported, Notes: ""},
	{ID: "membership.roles", Area: "Rooms", Name: "Channel admin ↔ power levels", Direction: MattermostToMatrix, Status: Unsupported, Notes: ""},
	{ID: "membership.roles.in", Area: "Rooms", Name: "Power levels ↔ channel admin", Direction: MatrixToMattermost, Status: Unsupported, Notes: ""},
	{ID: "rooms.create", Area: "Rooms", Name: "New channel creates a room", Direction: MattermostToMatrix, Status: Supported, Notes: "Public and private channels; DMs on first message."},
	{ID: "rooms.create.in", Area: "Rooms", Name: "Inviting the bot creates a channel", Direction: MatrixToMattermost, Status: Unsupported, Notes: "Only /matrix join from Mattermost today."},
	{ID: "rooms.name_topic", Area: "Rooms", Name: "Channel name/header → room name/topic", Direction: MattermostToMatrix, Status: Supported, Notes: "A rename becomes the room name and a header change becomes the topic, as soon as Mattermost's system post announces it. A sweep every 15 minutes catches anything missed."},
	{ID: "rooms.name_topic.in", Area: "Rooms", Name: "Room name/topic → channel name/header", Direction: MatrixToMattermost, Status: Supported, Notes: ""},
	{ID: "rooms.archive", Area: "Rooms", Name: "Archive ↔ tombstone/leave", Direction: MattermostToMatrix, Status: Supported, Notes: "Archiving a channel posts a notice in the room and the bridge bot leaves; restoring the channel rejoins and says so."},
	{ID: "rooms.archive.in", Area: "Rooms", Name: "Tombstone ↔ archive", Direction: MatrixToMattermost, Status: Supported, Notes: "A tombstoned room archives its channel."},
	{ID: "rooms.dm", Area: "Rooms", Name: "Direct and group messages between Mattermost users", Direction: MattermostToMatrix, Status: Supported, Notes: "Opt-in: an administrator turns on Bridge Direct Messages and everyone in a conversation runs /matrix dm on; then new messages go to a private Matrix room. /matrix dm off unbridges. Earlier messages are not copied."},
	{ID: "rooms.dm.in", Area: "Rooms", Name: "Direct messages", Direction: MatrixToMattermost, Status: Unsupported, Notes: "Mattermost refuses direct channels with remote users (\"Cannot create a direct channel with a remote user\"), so a DM started in Matrix cannot be created; needs a different account model for Matrix users."},
	{ID: "spaces.team", Area: "Rooms", Name: "Team ↔ space", Direction: MattermostToMatrix, Status: Partial, Notes: "Spaces are created and rooms added; team membership is not mirrored."},
	{ID: "rooms.encryption.in", Area: "Rooms", Name: "End-to-end encrypted rooms: reading", Direction: MatrixToMattermost, Status: Supported, Notes: "With \"Bridge Encrypted Rooms\" on, the bridge bot holds a device and the room keys, decrypts what Matrix people send, and posts it. Encryption ends at the bridge (docs/ENCRYPTION.md). Off by default; with it off, encrypted rooms are refused instead."},
	{ID: "rooms.encryption.out", Area: "Rooms", Name: "End-to-end encrypted rooms: writing", Direction: MattermostToMatrix, Status: Supported, Notes: "Mattermost posts are encrypted with the bot's room key before they leave, so the homeserver only holds ciphertext. Messages, reactions and attachments; Matrix clients show the sender as the bridge's device, not as verified."},

	// Deep integration
	{ID: "integration.widget", Area: "Integration", Name: "Matrix widget showing the Mattermost channel", Direction: MattermostToMatrix, Status: Unsupported, Notes: "Plugin can serve a widget page; the bot sets im.vector.modular.widgets."},
	{ID: "integration.bot_commands.in", Area: "Integration", Name: "Bridge bot commands in Matrix (!mattermost …)", Direction: MatrixToMattermost, Status: Partial, Notes: "help, whoami, link, login, logout, unlink work in DMs with the bot. Room linking from Matrix is not implemented yet."},
	{ID: "integration.channel_header", Area: "Integration", Name: "Channel shows the linked room in Mattermost", Direction: MattermostToMatrix, Status: Supported, Notes: "The bridge panel (app bar icon) shows the channel's room, who is in it and a link to open it. There is no button in the channel header itself."},
	{ID: "integration.post_actions", Area: "Integration", Name: "Post menu: Open in Matrix and Copy Matrix event ID", Direction: MattermostToMatrix, Status: Supported, Notes: "Both actions appear on posts that were synced to Matrix. Open in Matrix uses Element Web when its address is configured and matrix.to otherwise. The server route is tested against a real homeserver; the menu itself is covered by webapp unit tests only."},
	{ID: "ops.metrics", Area: "Operations", Name: "Prometheus metrics endpoint", Direction: MattermostToMatrix, Status: Supported, Notes: "GET <site>/plugins/com.mattermost.plugin-matrix-bridge/metrics with the homeserver token as a bearer token: message and error counters, API latency, queue depth, accounts, rate-limit state and whether encryption is ready."},
	{ID: "ops.txn_dedup", Area: "Operations", Name: "Retried homeserver transactions are handled once", Direction: MatrixToMattermost, Status: Supported, Notes: "Handled transaction IDs are kept in the key-value store, so a retry that reaches another node or follows a restart is recognised. Two nodes receiving the same transaction at the same instant are not guarded against."},
	{ID: "ops.inbound_journal", Area: "Operations", Name: "Matrix events survive a restart and are delivered in order per room", Direction: MatrixToMattermost, Status: Supported, Notes: "Each event is written to the key-value store before the homeserver is told it arrived and removed when handled; what a crash leaves behind is replayed on start. Events of one room are handled by one worker in order, failures are retried three times, then set aside (/matrix retry replays them)."},
	{ID: "ops.outbox", Area: "Operations", Name: "Mattermost changes reach Matrix after an outage, in order", Direction: MattermostToMatrix, Status: Supported, Notes: "Posts, edits, deletes and reactions are written to the key-value store before they are sent and removed once Matrix has them. While the homeserver is down they wait and are sent again with a growing delay (up to five minutes), channel by channel in order; after a day they are set aside for /matrix retry. A sent-again message carries the time it was sent again, not the time it was written."},
	{ID: "ops.doctor", Area: "Operations", Name: "/matrix doctor setup check", Direction: MattermostToMatrix, Status: Supported, Notes: "Checks the settings, the homeserver, the appservice token and server name, that the homeserver can call the plugin back (appservice ping), encryption, MAS, stuck deliveries, rate limits, and for the current channel its mapping, the bot's membership and power level. Never changes anything. Mirror mode namespace and registration namespaces cannot be probed safely and are reported as advice."},
	{ID: "membership.invite", Area: "Membership", Name: "Invite a Matrix user from Mattermost", Direction: MattermostToMatrix, Status: Supported, Notes: "/matrix invite @user:server (channel managers and administrators) has the bridge bot invite a Matrix user into the channel's room. Already present people and the bridge's own accounts are refused."},
	{ID: "membership.reconcile", Area: "Membership", Name: "Rooms are repaired to match channel membership", Direction: MattermostToMatrix, Status: Supported, Notes: "/matrix repair (and a rotating background pass, twenty channels per run) adds the bridge accounts of channel members who are missing from the room and removes the accounts of people who left the channel. Real Matrix people, claimed mirror accounts and the bot are never touched; people with their own linked account must join themselves."},
	{ID: "ops.rate_limit_adapt", Area: "Operations", Name: "Adapts to the homeserver's 429 answers", Direction: MattermostToMatrix, Status: Supported, Notes: "Honours retry_after_ms: requests pause until the stated time, the refused request is repeated (up to three times), and the bridge's own limiters slow down until the homeserver stops complaining. Covered by unit tests with a fake homeserver; not exercised against a real rate-limited Synapse."},
	{ID: "integration.bridge_info", Area: "Integration", Name: "Matrix clients show where a room is bridged to", Direction: MattermostToMatrix, Status: Supported, Notes: "A mapped room carries the bridge-information state event (m.bridge and Element's uk.half-shot.bridge) naming the Mattermost team, channel and link; it is emptied when the channel is unmapped. Needs the bot to be allowed to send state events, so rooms mapped with /matrix map where the bot is not a moderator keep no event (the doctor says so). Direct messages carry none."},
	{ID: "messages.integrations", Area: "Messages", Name: "Integration posts (webhooks, bots) keep their content", Direction: MattermostToMatrix, Status: Partial, Notes: "The text of Slack-style attachments (pretext, title and link, text, fields, image link, footer) is sent as the message, and an overridden sender name is written as a bold prefix. Not yet: a per-message profile (name and icon) in Matrix, image attachments as files, and bot authors in history import."},
	{ID: "messages.long", Area: "Messages", Name: "Long Mattermost posts fit a Matrix event", Direction: MattermostToMatrix, Status: Supported, Notes: "A post whose text and HTML copy would exceed Matrix's event size loses the HTML copy first, then is cut and marked. Ordinary long posts (up to Mattermost's 16,383 characters) arrive whole."},
	{ID: "messages.long.in", Area: "Messages", Name: "Long Matrix messages are split into several posts", Direction: MatrixToMattermost, Status: Supported, Notes: "A Matrix message longer than Mattermost's 16,383 characters becomes consecutive posts, cut at paragraph breaks and marked as continued; an edit that is too long is cut with a marker instead of failing. Edits and reactions refer to the first post."},
	{ID: "messages.links", Area: "Messages", Name: "Mattermost permalinks and ~channel mentions work in Matrix", Direction: MattermostToMatrix, Status: Supported, Notes: "A permalink to a bridged post becomes a link to its Matrix event (Element Web when its address is set, otherwise matrix.to), and ~channel becomes a link to the channel's room. Links to posts and channels that are not bridged are left as they are. History import applies the same rewriting, so old permalinks keep working after a cut-over."},
	{ID: "messages.links.in", Area: "Messages", Name: "Matrix event and room links work in Mattermost", Direction: MatrixToMattermost, Status: Supported, Notes: "A matrix.to or Element link to a bridged event becomes a Mattermost permalink, and a link to a bridged room becomes ~channel. Links to rooms and events that are not bridged, and user links, are left as they are."},
	{ID: "files.dimensions", Area: "Files", Name: "Image size is sent with the file", Direction: MattermostToMatrix, Status: Supported, Notes: "Width and height of images (from Mattermost's file information) are sent in the file event's info, so Matrix clients can reserve space before the picture loads. Duration, thumbnails, voice messages and streaming of large files are not done yet. A file over the size limit is announced with a short notice on the other side instead of being dropped silently; that notice is covered by unit tests only."},
	{ID: "federation.remote_room", Area: "Federation", Name: "Map and use a room that lives on another homeserver", Direction: MatrixToMattermost, Status: Supported, Notes: "/matrix map #room:other.server joins the room across federation; messages, edits, reactions and files from people on that server arrive, and Mattermost posts reach them. Tested with a second Synapse; other homeserver implementations are not tested yet."},
	{ID: "history.import.in", Area: "Integration", Name: "Import a Matrix room's history into Mattermost", Direction: MatrixToMattermost, Status: Supported, Notes: "/matrix history from-matrix (preview, then confirm, optionally limit=N) copies the room's earlier messages into the channel as the people who wrote them, with their original times, the latest edit of each message applied, and reactions. Events already imported, the bridge's own and ones that cannot be read (encrypted without keys) are skipped and counted. Safe to repeat; each run reads up to 2,000 events. Files larger than the limit are announced. Threads follow the live path."},
	{ID: "rooms.encryption.undecryptable_notice", Area: "Rooms", Name: "A message the bridge cannot decrypt is announced", Direction: MatrixToMattermost, Status: Supported, Notes: "When an encrypted message cannot be read (the bridge was not given its key, for example because it was sent before the bridge joined), the channel gets a short notice from the sender instead of nothing, once per message."},
	{ID: "integration.backfill", Area: "Integration", Name: "Structure backfill (users, teams, channels)", Direction: MattermostToMatrix, Status: Supported, Notes: "/matrix backfill creates ghosts, spaces and rooms."},
	{ID: "history.import", Area: "Integration", Name: "Message history import with original timestamps", Direction: MattermostToMatrix, Status: Partial, Notes: "/matrix history (preview, then confirm) replays a channel's posts into its room oldest first, as their authors, with the original time, threads intact and safe to repeat. Not yet: attachments, reactions, edits history, direct messages, a bulk mode for very large histories. Accounts that use their own token (double puppeting) show the import time."},
	{ID: "history.import.files", Area: "Integration", Name: "History import: attachments and reactions", Direction: MattermostToMatrix, Status: Supported, Notes: "Files are uploaded with their message and reactions are replayed from the people who reacted, all with their original times. Edits are imported as the latest text."},
}

// ByID returns the capability with the given ID or panics; an unknown ID is
// a programming error in a test.
func ByID(id string) Capability {
	for _, c := range All {
		if c.ID == id {
			return c
		}
	}
	panic(fmt.Sprintf("capabilities: unknown capability %q", id))
}

// Verify records the outcome of probing a capability. A supported capability
// that failed fails the test. A gap that unexpectedly works also fails the
// test, so the matrix is promoted instead of silently drifting. Partial
// capabilities only log.
func Verify(t testing.TB, id string, works bool) {
	t.Helper()
	c := ByID(id)
	switch c.Status {
	case Supported:
		if !works {
			t.Errorf("capability %s (%s, %s) is documented as supported but does not work", c.ID, c.Name, c.Direction)
			return
		}
		t.Logf("capability %s works as documented", c.ID)
	case Unsupported:
		if works {
			t.Errorf("capability %s (%s, %s) is documented as a gap but works; promote it in e2e/capabilities", c.ID, c.Name, c.Direction)
			return
		}
		t.Logf("KNOWN GAP %s (%s, %s): %s", c.ID, c.Name, c.Direction, c.Notes)
	case Partial:
		t.Logf("partial capability %s probed, works=%v: %s", c.ID, works, c.Notes)
	case NotApplicable:
		t.Logf("capability %s is not applicable", c.ID)
	}
}

// Markdown renders the matrix as the content of docs/CAPABILITIES.md.
func Markdown() string {
	var sb strings.Builder
	sb.WriteString("# Bridge capabilities\n\n")
	sb.WriteString("Generated from `e2e/capabilities/capabilities.go`; do not edit by hand. ")
	sb.WriteString("Run `make capabilities` after changing the Go file.\n\n")
	sb.WriteString("The end-to-end suite probes every row. A **supported** row that stops working fails CI; ")
	sb.WriteString("a **gap** that starts working also fails CI until it is promoted here, so this table always ")
	sb.WriteString("reflects what the bridge really does.\n\n")
	sb.WriteString("Legend: ✅ supported · 🟡 partial · ❌ gap · — not applicable\n\n")

	areas := []string{}
	seen := map[string]bool{}
	for _, c := range All {
		if !seen[c.Area] {
			seen[c.Area] = true
			areas = append(areas, c.Area)
		}
	}

	counts := map[Status]int{}
	for _, area := range areas {
		sb.WriteString("## " + area + "\n\n")
		sb.WriteString("| Capability | Direction | Status | Notes |\n|---|---|---|---|\n")
		for _, c := range All {
			if c.Area != area {
				continue
			}
			counts[c.Status]++
			sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n", c.Name, c.Direction, icon(c.Status), c.Notes))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Totals\n\n")
	statuses := []Status{Supported, Partial, Unsupported, NotApplicable}
	sort.SliceStable(statuses, func(i, j int) bool { return i < j })
	for _, s := range statuses {
		sb.WriteString(fmt.Sprintf("- %s %s: %d\n", icon(s), s, counts[s]))
	}
	return sb.String()
}

func icon(s Status) string {
	switch s {
	case Supported:
		return "✅"
	case Partial:
		return "🟡"
	case Unsupported:
		return "❌"
	default:
		return "—"
	}
}
