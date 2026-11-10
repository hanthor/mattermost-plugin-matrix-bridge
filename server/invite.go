package main

import (
	"fmt"
	"regexp"
	"strings"
)

var matrixUserIDPattern = regexp.MustCompile(`^@[a-z0-9._=\-/+]+:[A-Za-z0-9.\-]+(:\d+)?$`)

// InviteMatrixUser invites a Matrix user into the room a channel is mapped to,
// as the bridge bot. It lets a channel manager bring a colleague or guest who
// uses Matrix into the conversation without leaving Mattermost.
func (p *Plugin) InviteMatrixUser(channelID, matrixUserID string) (string, error) {
	matrixUserID = strings.TrimSpace(matrixUserID)
	if !matrixUserIDPattern.MatchString(matrixUserID) {
		return "", fmt.Errorf("`%s` is not a Matrix user ID; it looks like `@name:server.example`", matrixUserID)
	}
	if p.isBridgeAccount(matrixUserID) || matrixUserID == p.BridgeBotUserID() {
		return "", fmt.Errorf("%s is one of the bridge's own accounts; people in this channel are added to the room automatically", matrixUserID)
	}
	roomID := p.roomIDForChannel(channelID)
	if roomID == "" {
		return "", fmt.Errorf("this channel is not linked to a Matrix room. Run `/matrix create` or `/matrix map` first")
	}
	if members, err := p.matrixClient.JoinedMembers(roomID); err == nil {
		for _, m := range members {
			if m == matrixUserID {
				return fmt.Sprintf("%s is already in the room.", matrixUserID), nil
			}
		}
	}
	if err := p.matrixClient.InviteUserToRoom(roomID, matrixUserID); err != nil {
		if strings.Contains(err.Error(), "M_FORBIDDEN") {
			return "", fmt.Errorf("the bridge bot is not allowed to invite people in that room. Give it moderator rights (power level 50) in the Matrix room, or invite %s from Matrix", matrixUserID)
		}
		return "", fmt.Errorf("the homeserver refused the invitation: %v", err)
	}
	return fmt.Sprintf("✉️ Invited %s to the Matrix room. They appear in the channel as a Matrix user once they accept.", matrixUserID), nil
}
