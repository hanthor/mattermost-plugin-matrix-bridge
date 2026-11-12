package command

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/matrix"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
	"github.com/mattermost/mattermost/server/public/model"
)

// executeLinkCommand issues a one-time code the user redeems with the bridge
// bot in Matrix ("!mattermost link <code>").
func (c *Handler) executeLinkCommand(args *model.CommandArgs) *model.CommandResponse {
	if matrixUserID, puppeted, ok := c.plugin.GetIdentityLink(args.UserId); ok {
		mode := "Your Mattermost messages are sent by a ghost user; send `!mattermost login <access_token>` to the bridge bot to post as your own account."
		if puppeted {
			mode = "Your Mattermost messages are sent as that account."
		}
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("✅ Already linked to `%s`. %s Use `/matrix unlink` to remove the link.", matrixUserID, mode),
		}
	}

	code, err := c.plugin.CreateIdentityLinkCode(args.UserId)
	if err != nil {
		c.client.Log.Error("Failed to create identity link code", "error", err, "user_id", args.UserId)
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "❌ Could not create a link code. Check plugin logs for details.",
		}
	}

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text: fmt.Sprintf("🔗 **Link your Matrix account**\n\n"+
			"1. In your Matrix client, start a direct message with the bridge bot `@%s:<your Matrix server>`.\n"+
			"2. Send it: `!mattermost link %s`\n\n"+
			"The code expires in 15 minutes. Afterwards your Matrix messages in bridged rooms post as you here, "+
			"and `!mattermost login <access_token>` lets your Mattermost messages appear from your Matrix account.",
			matrix.BridgeBotLocalpart, code),
	}
}

// executeUnlinkCommand removes the user's identity link.
func (c *Handler) executeUnlinkCommand(args *model.CommandArgs) *model.CommandResponse {
	if err := c.plugin.UnlinkIdentity(args.UserId); err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "❌ " + err.Error(),
		}
	}
	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         "✅ Your Matrix account has been unlinked. Your messages will be bridged through a ghost user again.",
	}
}

// channelEncryptionStatus tells whether this channel's room is encrypted and
// what that means for the bridge.
func (c *Handler) channelEncryptionStatus(channelID string) string {
	matrixClient := c.plugin.GetMatrixClient()
	if matrixClient == nil {
		return ""
	}
	room, err := c.kvstore.Get(kvstore.BuildChannelMappingKey(channelID))
	if err != nil || len(room) == 0 {
		return ""
	}
	roomID := string(room)
	if strings.HasPrefix(roomID, "#") {
		resolved, err := matrixClient.ResolveRoomAlias(roomID)
		if err != nil {
			return ""
		}
		roomID = resolved
	}
	if encrypted, err := matrixClient.IsRoomEncrypted(roomID); err != nil || !encrypted {
		return ""
	}
	if c.plugin.EncryptionSupported() {
		return "\n- 🔒 This room is end-to-end encrypted; the bridge encrypts and decrypts its messages (see docs/ENCRYPTION.md)."
	}
	return "\n- 🔒 **This room is end-to-end encrypted, which the bridge cannot read or send into: messages are not synced either way.** See docs/ENCRYPTION.md."
}

// accountModeStatus reports how Mattermost users appear in Matrix.
func (c *Handler) accountModeStatus() string {
	st := c.plugin.MirrorStatus()
	var sb strings.Builder
	if st.Mode == "mirror" {
		sb.WriteString(fmt.Sprintf("\n- Accounts: **mirror mode**, %d mirrored account(s), %d claimed by their people", st.Accounts, st.Claimed))
		if st.Collisions > 0 {
			sb.WriteString(fmt.Sprintf("\n- ⚠️ %d account(s) use a suffixed name because the natural name was already taken on the homeserver", st.Collisions))
		}
		if !st.ClaimSupported {
			sb.WriteString("\n- People can claim an account through shared login; set an admin token to also let `/matrix claim` hand out a one-time password")
		}
	} else {
		sb.WriteString("\n- Accounts: **ghost users** (`@_mattermost_<id>`)")
	}
	if st.Cutover {
		sb.WriteString("\n- 🔒 **Cut-over mode**: accounts and rooms are frozen; no new ones are created")
	}
	if st.Pinned {
		want := "ghost users"
		if st.ConfiguredMirror {
			want = "mirror mode"
		}
		sb.WriteString(fmt.Sprintf("\n- ⚠️ The setting asks for %s, but accounts already exist in the other mode, so it is ignored. Use `/matrix migrate ghosts-to-mirror` to change modes", want))
	}
	return sb.String()
}

// executeClaimCommand lets a person take over their mirrored account.
func (c *Handler) executeClaimCommand(args *model.CommandArgs) *model.CommandResponse {
	reply := func(text string) *model.CommandResponse {
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: text}
	}
	if c.plugin.MirrorStatus().Mode != "mirror" {
		return reply("ℹ️ Claiming applies to mirror mode, where every Mattermost user has a Matrix account of their own. Use `/matrix link` to connect an existing Matrix account instead.")
	}
	matrixUserID, password, err := c.plugin.ClaimMirrorAccount(args.UserId)
	if err != nil {
		return reply("❌ " + err.Error())
	}
	if password == "" {
		return reply(fmt.Sprintf("✅ `%s` is now yours. Log in to Matrix with your single sign-on account; the bridge keeps posting as you from Mattermost.", matrixUserID))
	}
	return reply(fmt.Sprintf("✅ `%s` is now yours.\n\nOne-time password (shown once, change it after your first login): `%s`\n\nThe bridge keeps posting as you from Mattermost.", matrixUserID, password))
}

// executeCallCommand starts a call that people on both sides can join.
func (c *Handler) executeCallCommand(args *model.CommandArgs, title string) *model.CommandResponse {
	joinURL, err := c.plugin.StartCall(args.UserId, args.ChannelId, title)
	if err != nil {
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: "❌ " + err.Error()}
	}
	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         fmt.Sprintf("📞 The call is announced in the channel and in the Matrix room. Join: %s", joinURL),
	}
}

// executeEncryptionVerify previews, or with confirm carries out, the
// cross-signing of the bridge bot's device. The recovery key is shown once in
// the reply and is not kept anywhere by the bridge.
func (c *Handler) executeEncryptionVerify(args *model.CommandArgs) *model.CommandResponse {
	reply := func(text string) *model.CommandResponse {
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: text}
	}
	if !strings.Contains(" "+args.Command+" ", " confirm ") {
		return reply("This gives the bridge's encryption device a verified identity, so Matrix clients stop showing a warning shield on messages that come from Mattermost.\n\n" +
			"It produces a **recovery key** that is shown **once** and is **not stored** by the bridge: save it in your password manager. Without it, losing the bridge's stored state means a new device and unreadable history.\n\n" +
			"Run `/matrix encryption-verify confirm` to do it.")
	}
	key, err := c.plugin.CrossSignBridgeDevice(args.UserId)
	if err != nil {
		return reply("❌ " + err.Error())
	}
	return reply("✅ The bridge's device is verified.\n\nRecovery key (shown once, save it now):\n```\n" + key + "\n```")
}
