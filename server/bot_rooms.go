package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// Room management from Matrix: "!mattermost rooms | link-room team/channel |
// unlink-room | status". Each acts as the person's linked Mattermost account
// through the same slash commands (/matrix map and unmap), so the same checks
// apply; changing a room also needs moderator power in that room.

const botRoomsHelp = `Room commands (your Matrix account must be linked):
• !mattermost rooms — the channels you can see that are bridged, and their rooms
• !mattermost status — whether this room is bridged, and to what
• !mattermost link-room <team>/<channel> — bridge this room to a channel you manage (you need moderator rights here)
• !mattermost unlink-room — stop bridging this room (moderators only)`

const moderatorPower = 50

// isRoomBotCommand reports whether a command is one of the room commands.
func isRoomBotCommand(fields []string) bool {
	if len(fields) < 2 {
		return false
	}
	switch strings.ToLower(fields[1]) {
	case "rooms", "status", "link-room", "unlink-room":
		return true
	}
	return false
}

// executeRoomBotCommand runs a room command sent in event.RoomID.
func (p *Plugin) executeRoomBotCommand(event MatrixEvent, fields []string) string {
	link, ok := p.identity().LinkForMatrixUser(event.Sender)
	if !ok {
		return "This Matrix account is not linked to a Mattermost account. Run `/matrix link` in Mattermost and send me the code with `!mattermost link <code>`."
	}
	switch strings.ToLower(fields[1]) {
	case "rooms":
		return p.botListRooms(link.MattermostUserID)
	case "status":
		return p.botRoomStatus(event.RoomID)
	case "link-room":
		if len(fields) < 3 || !strings.Contains(fields[2], "/") {
			return "Usage: `!mattermost link-room <team>/<channel>` using the names in the channel's address."
		}
		return p.botLinkRoom(event, link.MattermostUserID, fields[2])
	case "unlink-room":
		return p.botUnlinkRoom(event, link.MattermostUserID)
	}
	return botRoomsHelp
}

func (p *Plugin) botListRooms(userID string) string {
	teams, appErr := p.API.GetTeamsForUser(userID)
	if appErr != nil {
		return "❌ Could not read your teams."
	}
	var lines []string
	for _, team := range teams {
		channels, appErr := p.API.GetChannelsForTeamForUser(team.Id, userID, false)
		if appErr != nil {
			continue
		}
		for _, ch := range channels {
			if ch.IsGroupOrDirect() {
				continue
			}
			if room := p.roomIDForChannel(ch.Id); room != "" {
				lines = append(lines, fmt.Sprintf("• %s/%s → %s", team.Name, ch.Name, room))
			}
		}
	}
	if len(lines) == 0 {
		return "None of your channels is bridged."
	}
	sort.Strings(lines)
	return "Bridged channels you are in:\n" + strings.Join(lines, "\n")
}

func (p *Plugin) botRoomStatus(roomID string) string {
	channelID, err := p.getChannelIDFromMatrixRoom(roomID)
	if err != nil || channelID == "" {
		return "This room is not bridged. Use `!mattermost link-room <team>/<channel>` to bridge it to a channel you manage."
	}
	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil || channel == nil {
		return "This room is bridged to a channel that could not be read."
	}
	name := channel.DisplayName
	if team, appErr := p.API.GetTeam(channel.TeamId); appErr == nil && team != nil {
		name = team.DisplayName + " / " + channel.DisplayName
	}
	return fmt.Sprintf("✅ This room is bridged to **%s** in Mattermost.", name)
}

// hasModeratorPower reports whether the person may change this room's setup.
func (p *Plugin) hasModeratorPower(roomID, matrixUserID string) (bool, error) {
	level, err := p.matrixClient.BotPowerLevel(roomID, matrixUserID)
	if err != nil {
		return false, err
	}
	return level >= moderatorPower, nil
}

func (p *Plugin) botLinkRoom(event MatrixEvent, userID, target string) string {
	if p.matrixClient == nil {
		return "❌ The bridge is not connected to Matrix."
	}
	if existing, _ := p.getChannelIDFromMatrixRoom(event.RoomID); existing != "" {
		return "This room is already bridged. Use `!mattermost unlink-room` first."
	}
	if ok, err := p.hasModeratorPower(event.RoomID, event.Sender); err != nil || !ok {
		return "❌ You need moderator rights in this room to bridge it."
	}
	parts := strings.SplitN(target, "/", 2)
	team, appErr := p.API.GetTeamByName(parts[0])
	if appErr != nil || team == nil {
		return fmt.Sprintf("❌ I cannot find the team `%s`.", parts[0])
	}
	channel, appErr := p.API.GetChannelByName(team.Id, parts[1], false)
	if appErr != nil || channel == nil {
		return fmt.Sprintf("❌ I cannot find the channel `%s` in that team.", parts[1])
	}
	// Mattermost's own rules decide whether this person may do it.
	return p.runSlashAs(userID, team.Id, channel.Id, "/matrix map "+event.RoomID)
}

func (p *Plugin) botUnlinkRoom(event MatrixEvent, userID string) string {
	if p.matrixClient == nil {
		return "❌ The bridge is not connected to Matrix."
	}
	channelID, err := p.getChannelIDFromMatrixRoom(event.RoomID)
	if err != nil || channelID == "" {
		return "This room is not bridged."
	}
	if ok, err := p.hasModeratorPower(event.RoomID, event.Sender); err != nil || !ok {
		return "❌ You need moderator rights in this room to stop bridging it."
	}
	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil || channel == nil {
		return "❌ I cannot find the channel."
	}
	return p.runSlashAs(userID, channel.TeamId, channelID, "/matrix unmap")
}

// runSlashAs runs a slash command as a Mattermost person and returns what it
// answered, so Mattermost's permission checks apply to them.
func (p *Plugin) runSlashAs(userID, teamID, channelID, command string) string {
	resp, err := p.API.ExecuteSlashCommand(&model.CommandArgs{UserId: userID, TeamId: teamID, ChannelId: channelID, Command: command})
	if err != nil {
		return "❌ " + err.Error()
	}
	if resp == nil || resp.Text == "" {
		return "Done."
	}
	return resp.Text
}
