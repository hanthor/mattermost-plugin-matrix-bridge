package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// With a team set in **Create Channels for Invited Rooms**, inviting the bridge
// bot to a new Matrix room creates a public channel in that team and bridges
// it. The inviter must be a moderator of the room. When their Matrix account is
// linked to a Mattermost account, that person becomes the channel's admin.

const channelCreatorBotUsername = "matrix-bridge-channels"

var nonNameCharacters = regexp.MustCompile(`[^a-z0-9]+`)

// channelNameFor makes a Mattermost channel name from a room name.
func channelNameFor(roomName, roomID string) string {
	name := strings.Trim(nonNameCharacters.ReplaceAllString(strings.ToLower(roomName), "-"), "-")
	if name == "" {
		name = "matrix-" + strings.Trim(nonNameCharacters.ReplaceAllString(strings.ToLower(strings.SplitN(roomID, ":", 2)[0]), "-"), "-")
	}
	if len(name) > 56 {
		name = strings.Trim(name[:56], "-")
	}
	if len(name) < 2 {
		name += "-room"
	}
	return name
}

// createChannelForRoom handles the bot being invited to a room: it makes and
// bridges a channel when the setting is on and the inviter may. It reports
// whether it did.
func (p *Plugin) createChannelForRoom(event MatrixEvent) bool {
	defer func() { _ = recover() }()
	teamName := p.getConfiguration().InviteCreatesChannelsTeam
	if teamName == "" || p.matrixClient == nil || p.mattermostToMatrixBridge == nil {
		return false
	}
	if direct, _ := event.Content["is_direct"].(bool); direct {
		return false
	}
	if existing, _ := p.getChannelIDFromMatrixRoom(event.RoomID); existing != "" {
		return false
	}
	team, appErr := p.API.GetTeamByName(teamName)
	if appErr != nil || team == nil {
		p.logger.LogWarn("Create Channels for Invited Rooms names a team that does not exist", "team", teamName)
		return false
	}
	if ok, err := p.hasModeratorPower(event.RoomID, event.Sender); err != nil || !ok {
		_ = p.matrixClient.SendNotice(event.RoomID, "I only bridge a room when a moderator of the room invites me.")
		return false
	}
	creatorID, linkedUser := "", ""
	if link, ok := p.identity().LinkForMatrixUser(event.Sender); ok {
		linkedUser = link.MattermostUserID
		creatorID = linkedUser
	}
	if creatorID == "" {
		botID, err := p.API.EnsureBotUser(&model.Bot{Username: channelCreatorBotUsername, DisplayName: "Matrix bridge", Description: "Creates channels for rooms invited to the Matrix bridge"})
		if err != nil {
			p.logger.LogWarn("Could not set up the bot that creates channels", "error", err)
			return false
		}
		creatorID = botID
		if _, appErr := p.API.GetTeamMember(team.Id, botID); appErr != nil {
			_, _ = p.API.CreateTeamMember(team.Id, botID)
		}
	}

	roomName := ""
	if overview, err := p.matrixClient.GetRoomOverview(event.RoomID); err == nil {
		roomName = overview.Name
	}
	base := channelNameFor(roomName, event.RoomID)
	display := roomName
	if display == "" {
		display = base
	}
	var channel *model.Channel
	for i := 0; i < 5; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%d", base, i+1)
		}
		created, appErr := p.API.CreateChannel(&model.Channel{
			TeamId: team.Id, Type: model.ChannelTypeOpen, Name: name, DisplayName: display,
			Purpose: "Bridged from the Matrix room " + event.RoomID, CreatorId: creatorID,
		})
		if appErr == nil {
			channel = created
			break
		}
	}
	if channel == nil {
		_ = p.matrixClient.SendNotice(event.RoomID, "I could not create a channel for this room.")
		return false
	}
	if err := p.mattermostToMatrixBridge.setChannelRoomMapping(channel.Id, event.RoomID); err != nil {
		p.logger.LogWarn("Could not map the new channel to the room", "error", err, "channel_id", channel.Id)
		return false
	}
	p.InvalidateChannelMapping(channel.Id)
	if linkedUser != "" {
		if _, appErr := p.API.GetTeamMember(team.Id, linkedUser); appErr != nil {
			_, _ = p.API.CreateTeamMember(team.Id, linkedUser)
		}
		_, _ = p.API.AddChannelMember(channel.Id, linkedUser)
		_, _ = p.API.UpdateChannelMemberRoles(channel.Id, linkedUser, channelAdminRoles)
	}
	_ = p.matrixClient.SendNotice(event.RoomID, fmt.Sprintf("✅ This room is now bridged to the Mattermost channel **%s** in the %s team.", display, team.DisplayName))
	return true
}
