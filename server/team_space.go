package main

import (
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// Team membership follows into the team's Matrix space: a person who joins the
// team has their account join the space, and one who leaves has it leave.

func (p *Plugin) teamSpaceID(teamID string) string {
	if p.kvstore == nil {
		return ""
	}
	data, err := p.kvstore.Get(kvstore.BuildTeamMappingKey(teamID))
	if err != nil || len(data) == 0 {
		return ""
	}
	return string(data)
}

// bridgedTeamPerson returns the Mattermost user for a team member, unless it is
// someone the bridge should not act for.
func (p *Plugin) bridgedTeamPerson(teamMember *model.TeamMember) *model.User {
	if teamMember == nil || !p.getConfiguration().EnableSync || p.matrixClient == nil || p.mattermostToMatrixBridge == nil {
		return nil
	}
	user, appErr := p.API.GetUser(teamMember.UserId)
	if appErr != nil || user == nil || user.IsBot || user.IsRemote() || user.DeleteAt != 0 {
		return nil
	}
	return user
}

// UserHasJoinedTeam puts the person's account in the team's space.
func (p *Plugin) UserHasJoinedTeam(_ *plugin.Context, teamMember *model.TeamMember, _ *model.User) {
	defer func() { _ = recover() }()
	user := p.bridgedTeamPerson(teamMember)
	if user == nil {
		return
	}
	spaceID := p.teamSpaceID(teamMember.TeamId)
	if spaceID == "" {
		return
	}
	matrixUserID, client, _, err := p.mattermostToMatrixBridge.resolveSender(user.Id)
	if err != nil || matrixUserID == "" {
		return
	}
	if err := p.mattermostToMatrixBridge.ensureSenderInRoom(client, matrixUserID, spaceID, user.Id); err != nil {
		p.logger.LogDebug("Could not put a person in their team's space", "error", err, "space_id", spaceID)
	}
}

// UserHasLeftTeam takes the person's account out of the team's space.
func (p *Plugin) UserHasLeftTeam(_ *plugin.Context, teamMember *model.TeamMember, _ *model.User) {
	defer func() { _ = recover() }()
	user := p.bridgedTeamPerson(teamMember)
	if user == nil {
		return
	}
	spaceID := p.teamSpaceID(teamMember.TeamId)
	if spaceID == "" {
		return
	}
	matrixUserID, ok := lookupBridgeAccount(p.kvstore, user.Id)
	if !ok {
		return
	}
	if err := p.matrixClient.LeaveRoomAsUser(spaceID, matrixUserID); err != nil {
		p.logger.LogDebug("Could not take a person out of their team's space", "error", err, "space_id", spaceID)
	}
	p.forgetRoomMembership(user.Id, spaceID)
}
