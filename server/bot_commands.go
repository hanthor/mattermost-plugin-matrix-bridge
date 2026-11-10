package main

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/matrix"
)

// bridgeBotLocalpart is the sender_localpart in the appservice registration.
const bridgeBotLocalpart = matrix.BridgeBotLocalpart

// botCommandPrefix starts every bridge-bot command in Matrix.
const botCommandPrefix = "!mattermost"

const botHelpText = `Mattermost bridge commands:
• !mattermost help — this message
• !mattermost whoami — show which Mattermost account this Matrix account is linked to
• !mattermost link <code> — link this Matrix account to a Mattermost account (get the code with /matrix link in Mattermost)
• !mattermost login <access_token> — let the bridge post as this Matrix account instead of a ghost (double puppeting)
• !mattermost logout — stop using this account's token
• !mattermost unlink — remove the link`

// isBridgeBotEvent reports whether a member event concerns the bridge bot.
func (p *Plugin) isBridgeBotEvent(event MatrixEvent) bool {
	return event.Type == "m.room.member" && event.StateKey != nil && *event.StateKey == p.BridgeBotUserID()
}

// handleBridgeBotMembership accepts invites to the bot so people can DM it.
func (p *Plugin) handleBridgeBotMembership(event MatrixEvent) {
	membership, _ := event.Content["membership"].(string)
	if membership != "invite" || p.matrixClient == nil {
		return
	}
	if err := p.matrixClient.JoinRoom(event.RoomID); err != nil {
		p.logger.LogWarn("Bridge bot could not accept an invite", "error", err, "room_id", event.RoomID, "inviter", event.Sender)
		return
	}
	p.logger.LogInfo("Bridge bot accepted an invite", "room_id", event.RoomID, "inviter", event.Sender)
	if strings.HasPrefix(event.Sender, "@") {
		_ = p.matrixClient.SendNotice(event.RoomID, "Hi! I'm the Mattermost bridge.\n\n"+botHelpText)
	}
}

// isBotCommand reports whether a message event is a bridge-bot command.
func isBotCommand(event MatrixEvent) bool {
	if event.Type != "m.room.message" {
		return false
	}
	body, _ := event.Content["body"].(string)
	fields := strings.Fields(body)
	return len(fields) > 0 && strings.EqualFold(fields[0], botCommandPrefix)
}

// handleBotCommand executes a "!mattermost ..." command and answers in the
// same room as the bot.
func (p *Plugin) handleBotCommand(event MatrixEvent) {
	body, _ := event.Content["body"].(string)
	reply := p.executeBotCommand(event.Sender, body)
	if reply == "" || p.matrixClient == nil {
		return
	}
	if fields := strings.Fields(body); len(fields) >= 2 && strings.EqualFold(fields[1], "login") && event.EventID != "" {
		reply += p.redactLoginMessage(event)
	}
	err := p.matrixClient.SendNotice(event.RoomID, reply)
	if err != nil && strings.Contains(err.Error(), "M_FORBIDDEN") {
		// The command can arrive in the same transaction as the invite,
		// before the bot has joined: join and try once more.
		if joinErr := p.matrixClient.JoinRoom(event.RoomID); joinErr == nil {
			err = p.matrixClient.SendNotice(event.RoomID, reply)
		}
	}
	if err != nil {
		p.logger.LogWarn("Failed to answer bridge bot command", "error", err, "room_id", event.RoomID)
	}
}

// executeBotCommand returns the reply for a command sent by sender.
func (p *Plugin) executeBotCommand(sender, body string) string {
	fields := strings.Fields(body)
	if len(fields) < 2 {
		return botHelpText
	}
	store := p.identity()

	switch strings.ToLower(fields[1]) {
	case "help":
		return botHelpText

	case "whoami":
		link, ok := store.LinkForMatrixUser(sender)
		if !ok {
			return "This Matrix account is not linked to a Mattermost account. Run `/matrix link` in Mattermost and send me the code with `!mattermost link <code>`."
		}
		user, appErr := p.API.GetUser(link.MattermostUserID)
		if appErr != nil {
			return fmt.Sprintf("Linked to Mattermost user %s (the account could not be loaded).", link.MattermostUserID)
		}
		mode := "messages are sent as a ghost user"
		if link.Puppeted() {
			mode = "messages are sent as this Matrix account"
		}
		return fmt.Sprintf("You are linked to Mattermost user @%s; %s.", user.Username, mode)

	case "link":
		if len(fields) < 3 {
			return "Usage: `!mattermost link <code>` — get a code with `/matrix link` in Mattermost."
		}
		link, err := store.Redeem(fields[2], sender)
		if err != nil {
			return "❌ " + err.Error()
		}
		username := link.MattermostUserID
		if user, appErr := p.API.GetUser(link.MattermostUserID); appErr == nil {
			username = "@" + user.Username
		}
		return fmt.Sprintf("✅ Linked to Mattermost user %s. Your Matrix messages in bridged rooms now post as that account. Send `!mattermost login <access_token>` if you also want Mattermost messages to appear from this Matrix account.", username)

	case "login":
		if len(fields) < 3 {
			return "Usage: `!mattermost login <access_token>` (Element: Settings → Help & About → Access Token)."
		}
		if p.matrixClient == nil {
			return "❌ The bridge is not connected to Matrix."
		}
		token := fields[2]
		owner, err := p.matrixClient.WhoAmI(token)
		if err != nil {
			return "❌ That access token was rejected by the homeserver."
		}
		if owner != sender {
			return fmt.Sprintf("❌ That access token belongs to %s, not to you.", owner)
		}
		link, err := store.SetToken(sender, token)
		if err != nil {
			return "❌ " + err.Error()
		}
		// From now on the person sends as this account, so a mirror account
		// they were using is retired.
		p.retireMirrorAccount(link.MattermostUserID, link.MatrixUserID)
		return "✅ Double puppeting enabled: your Mattermost messages will now appear from this Matrix account."

	case "logout":
		link, ok := store.LinkForMatrixUser(sender)
		if !ok {
			return "This Matrix account is not linked."
		}
		link.AccessToken = ""
		if err := store.save(link); err != nil {
			return "❌ " + err.Error()
		}
		_ = p.kvstore.Delete(mirrorRetiredKeyPrefix + link.MattermostUserID)
		return "✅ Token removed; your Mattermost messages will appear from a ghost user again."

	case "unlink":
		link, ok := store.LinkForMatrixUser(sender)
		if !ok {
			return "This Matrix account is not linked."
		}
		if err := store.Unlink(link.MattermostUserID); err != nil {
			return "❌ " + err.Error()
		}
		_ = p.kvstore.Delete(mirrorRetiredKeyPrefix + link.MattermostUserID)
		return "✅ Unlinked."

	default:
		return fmt.Sprintf("Unknown command %q.\n\n%s", fields[1], botHelpText)
	}
}

// redactLoginMessage removes a message that contains an access token from the
// room's history and returns a sentence for the reply. The bot can only do it
// where it has the power to redact other people's messages (a room it created
// or moderates); otherwise the person is asked to delete it themselves.
func (p *Plugin) redactLoginMessage(event MatrixEvent) string {
	if _, err := p.matrixClient.RedactEventAsGhost(event.RoomID, event.EventID, p.BridgeBotUserID()); err != nil {
		p.logger.LogDebug("Could not redact a login message", "error", err, "room_id", event.RoomID)
		return "\n\n⚠️ I could not remove your message, which contains the token. Please delete it yourself, and consider signing out of that session to get a new token."
	}
	return "\n\nI removed your message that contained the token."
}
