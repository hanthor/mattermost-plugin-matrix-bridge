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
	{ID: "messages.mentions", Area: "Messages", Name: "User mentions", Direction: MattermostToMatrix, Status: Supported, Notes: "m.mentions and pills."},
	{ID: "messages.mentions.in", Area: "Messages", Name: "User mentions", Direction: MatrixToMattermost, Status: Supported, Notes: ""},
	{ID: "messages.channel_mentions", Area: "Messages", Name: "@channel / @here ↔ @room", Direction: MattermostToMatrix, Status: Unsupported, Notes: "@channel should set m.mentions.room; @room should become @channel."},
	{ID: "messages.channel_mentions.in", Area: "Messages", Name: "@room ↔ @channel", Direction: MatrixToMattermost, Status: Unsupported, Notes: ""},
	{ID: "messages.pins", Area: "Messages", Name: "Pinned messages", Direction: MattermostToMatrix, Status: Unsupported, Notes: "m.room.pinned_events."},
	{ID: "messages.pins.in", Area: "Messages", Name: "Pinned messages", Direction: MatrixToMattermost, Status: Unsupported, Notes: ""},

	// Reactions
	{ID: "reactions.add_remove", Area: "Reactions", Name: "Add and remove reactions", Direction: MattermostToMatrix, Status: Supported, Notes: "4,400+ emoji names mapped to Unicode."},
	{ID: "reactions.add_remove.in", Area: "Reactions", Name: "Add and remove reactions", Direction: MatrixToMattermost, Status: Supported, Notes: ""},
	{ID: "reactions.custom_emoji", Area: "Reactions", Name: "Custom emoji reactions", Direction: MattermostToMatrix, Status: Unsupported, Notes: "Should upload the emoji image and react with an mxc:// key (MSC4027)."},
	{ID: "reactions.custom_emoji.in", Area: "Reactions", Name: "Image reactions (MSC4027)", Direction: MatrixToMattermost, Status: Unsupported, Notes: "Should create a custom emoji on the fly."},

	// Attachments
	{ID: "files.attachments", Area: "Attachments", Name: "Files, images, audio, video", Direction: MattermostToMatrix, Status: Supported, Notes: "One m.file/m.image/m.audio/m.video event per attachment."},
	{ID: "files.attachments.in", Area: "Attachments", Name: "Files, images, audio, video", Direction: MatrixToMattermost, Status: Supported, Notes: "Downloaded through authenticated media endpoints."},
	{ID: "files.stickers.in", Area: "Attachments", Name: "Stickers (m.sticker)", Direction: MatrixToMattermost, Status: Unsupported, Notes: "Should be posted as an image attachment."},
	{ID: "files.location.in", Area: "Attachments", Name: "Location (m.location)", Direction: MatrixToMattermost, Status: Unsupported, Notes: "Should be posted as a map link."},
	{ID: "polls.in", Area: "Attachments", Name: "Polls (MSC3381)", Direction: MatrixToMattermost, Status: Unsupported, Notes: "Could map to interactive message attachments."},

	// Presence and ephemeral
	{ID: "ephemeral.typing", Area: "Ephemeral", Name: "Typing notifications", Direction: MattermostToMatrix, Status: Unsupported, Notes: "Mattermost exposes no plugin hook for typing; a websocket listener in the plugin could provide it."},
	{ID: "ephemeral.typing.in", Area: "Ephemeral", Name: "Typing notifications", Direction: MatrixToMattermost, Status: Supported, Notes: "Requires receive_ephemeral in the registration."},
	{ID: "ephemeral.presence", Area: "Ephemeral", Name: "Presence / status", Direction: MattermostToMatrix, Status: Unsupported, Notes: "Ghosts should mirror online/away/offline."},
	{ID: "ephemeral.presence.in", Area: "Ephemeral", Name: "Presence / status", Direction: MatrixToMattermost, Status: Supported, Notes: ""},
	{ID: "ephemeral.custom_status", Area: "Ephemeral", Name: "Custom status ↔ presence status_msg", Direction: MattermostToMatrix, Status: Unsupported, Notes: ""},
	{ID: "ephemeral.read_receipts", Area: "Ephemeral", Name: "Read receipts", Direction: MattermostToMatrix, Status: Unsupported, Notes: "Channel views could become m.read receipts from the ghost."},
	{ID: "ephemeral.read_receipts.in", Area: "Ephemeral", Name: "Read receipts", Direction: MatrixToMattermost, Status: Unsupported, Notes: "Mattermost has no plugin API to mark a channel viewed for another user."},

	// Users
	{ID: "users.profile", Area: "Users", Name: "Display name and avatar", Direction: MattermostToMatrix, Status: Supported, Notes: "Refreshed on the user's next activity or by a 15-minute sweep; Mattermost has no profile-change hook."},
	{ID: "users.profile.in", Area: "Users", Name: "Display name and avatar", Direction: MatrixToMattermost, Status: Supported, Notes: ""},
	{ID: "users.deactivation", Area: "Users", Name: "Deactivated users leave rooms", Direction: MattermostToMatrix, Status: Unsupported, Notes: "UserHasBeenDeactivated should make the ghost leave and deactivate."},
	{ID: "identity.link", Area: "Users", Name: "Self-service account linking", Direction: MatrixToMattermost, Status: Supported, Notes: "/matrix link issues a code; \"!mattermost link <code>\" to the bridge bot binds the accounts. Linked Matrix users post as their real Mattermost account."},
	{ID: "users.puppeting", Area: "Users", Name: "Double puppeting (use my own Matrix account)", Direction: MattermostToMatrix, Status: Supported, Notes: "\"!mattermost login <access_token>\" lets the bridge send as the linked Matrix account."},
	{ID: "identity.sso", Area: "Users", Name: "Automatic linking through shared SSO (OIDC / Matrix Authentication Service)", Direction: MattermostToMatrix, Status: Unsupported, Notes: "Match accounts by verified email from a shared identity provider so linking is zero-touch."},

	// Membership and rooms
	{ID: "membership.join_leave", Area: "Rooms", Name: "Join and leave", Direction: MattermostToMatrix, Status: Supported, Notes: ""},
	{ID: "membership.join_leave.in", Area: "Rooms", Name: "Join and leave", Direction: MatrixToMattermost, Status: Supported, Notes: ""},
	{ID: "membership.roles", Area: "Rooms", Name: "Channel admin ↔ power levels", Direction: MattermostToMatrix, Status: Unsupported, Notes: ""},
	{ID: "membership.roles.in", Area: "Rooms", Name: "Power levels ↔ channel admin", Direction: MatrixToMattermost, Status: Unsupported, Notes: ""},
	{ID: "rooms.create", Area: "Rooms", Name: "New channel creates a room", Direction: MattermostToMatrix, Status: Supported, Notes: "Public and private channels; DMs on first message."},
	{ID: "rooms.create.in", Area: "Rooms", Name: "Inviting the bot creates a channel", Direction: MatrixToMattermost, Status: Unsupported, Notes: "Only /matrix join from Mattermost today."},
	{ID: "rooms.name_topic", Area: "Rooms", Name: "Channel name/header → room name/topic", Direction: MattermostToMatrix, Status: Unsupported, Notes: "Mattermost has no channel-updated hook; needs polling or websocket."},
	{ID: "rooms.name_topic.in", Area: "Rooms", Name: "Room name/topic → channel name/header", Direction: MatrixToMattermost, Status: Supported, Notes: ""},
	{ID: "rooms.archive", Area: "Rooms", Name: "Archive ↔ tombstone/leave", Direction: MattermostToMatrix, Status: Unsupported, Notes: ""},
	{ID: "rooms.archive.in", Area: "Rooms", Name: "Tombstone ↔ archive", Direction: MatrixToMattermost, Status: Unsupported, Notes: ""},
	{ID: "rooms.dm", Area: "Rooms", Name: "Direct and group messages", Direction: MattermostToMatrix, Status: Supported, Notes: ""},
	{ID: "rooms.dm.in", Area: "Rooms", Name: "Direct messages", Direction: MatrixToMattermost, Status: Partial, Notes: "Works when a ghost is invited; message-only DMs are not bridged."},
	{ID: "spaces.team", Area: "Rooms", Name: "Team ↔ space", Direction: MattermostToMatrix, Status: Partial, Notes: "Spaces are created and rooms added; team membership is not mirrored."},
	{ID: "rooms.encryption.in", Area: "Rooms", Name: "End-to-end encrypted rooms", Direction: MatrixToMattermost, Status: Unsupported, Notes: "Needs MSC3202 appservice encryption; not planned."},

	// Deep integration
	{ID: "integration.widget", Area: "Integration", Name: "Matrix widget showing the Mattermost channel", Direction: MattermostToMatrix, Status: Unsupported, Notes: "Plugin can serve a widget page; the bot sets im.vector.modular.widgets."},
	{ID: "integration.bot_commands.in", Area: "Integration", Name: "Bridge bot commands in Matrix (!mattermost …)", Direction: MatrixToMattermost, Status: Partial, Notes: "help, whoami, link, login, logout, unlink work in DMs with the bot. Room linking from Matrix is not implemented yet."},
	{ID: "integration.channel_header", Area: "Integration", Name: "Channel header shows the linked room", Direction: MattermostToMatrix, Status: Unsupported, Notes: "Webapp channel header button and RHS."},
	{ID: "integration.backfill", Area: "Integration", Name: "Message history backfill", Direction: MattermostToMatrix, Status: Unsupported, Notes: "/matrix backfill only creates ghosts, spaces and rooms."},
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
