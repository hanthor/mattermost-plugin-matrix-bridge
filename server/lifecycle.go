package main

import (
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// This file holds what happens to a person's Matrix account when their
// Mattermost account changes: deactivation, and linking a native Matrix
// account after a mirror account was already in use.

const mirrorRetiredKeyPrefix = "mirror_retired_" // + Mattermost user ID -> Matrix user ID it was replaced by

// UserHasBeenDeactivated makes a deactivated person's bridge account leave the
// bridged rooms. An unclaimed mirror account is also deactivated on the
// homeserver when an admin token is configured. A claimed mirror account and a
// linked account the bridge sends as belong to the person, so their room
// memberships stay; the bridge only stops acting as them.
func (p *Plugin) UserHasBeenDeactivated(_ *plugin.Context, user *model.User) {
	if user == nil || user.IsRemote() || !p.getConfiguration().EnableSync || p.matrixClient == nil || p.kvstore == nil {
		return
	}
	p.deactivateBridgeAccount(user.Id)
}

func (p *Plugin) deactivateBridgeAccount(userID string) {
	store := p.identity()
	link, linked := store.Link(userID)
	if linked && link.Puppeted() {
		// Their own account: just stop sending as them.
		link.AccessToken = ""
		if err := store.save(link); err != nil {
			p.logger.LogWarn("Could not stop acting as a deactivated user's linked account", "error", err, "user_id", userID)
		}
	}

	accountID, ok := lookupBridgeAccount(p.kvstore, userID)
	if !ok {
		return
	}
	mirror, isMirror := newMirrorStore(p.kvstore).Account(userID)
	if isMirror && mirror.Claimed {
		p.logger.LogInfo("A claimed mirror account's owner was deactivated; the account is left as it is", "user_id", userID)
		return
	}

	for _, room := range p.roomsForUser(userID) {
		if err := p.matrixClient.LeaveRoomAsUser(room, accountID); err != nil {
			p.logger.LogWarn("Deactivated user's account could not leave a room", "error", err, "account", accountID, "room_id", room)
			continue
		}
		p.forgetRoomMembership(userID, room)
	}

	if isMirror {
		cfg := p.getConfiguration()
		if cfg.MatrixAdminToken == "" {
			return
		}
		if err := p.matrixClient.DeactivateUserAsAdmin(cfg.MatrixAdminToken, accountID); err != nil {
			p.logger.LogWarn("Could not deactivate the mirror account on the homeserver", "error", err, "account", accountID)
		}
	}
}

// roomsForUser lists the Matrix rooms a person's bridge account is in: the
// rooms recorded as joined, plus the mapped rooms of every channel they are a
// member of (membership is also created when someone joins a channel, which
// leaves no record of its own).
func (p *Plugin) roomsForUser(userID string) []string {
	seen := map[string]bool{}
	var rooms []string
	add := func(room string) {
		if room != "" && !seen[room] {
			seen[room] = true
			rooms = append(rooms, room)
		}
	}
	for _, r := range p.ghostRooms(userID) {
		add(r)
	}
	if p.mattermostToMatrixBridge == nil {
		return rooms
	}
	teams, appErr := p.API.GetTeamsForUser(userID)
	if appErr != nil {
		return rooms
	}
	for _, team := range teams {
		for page := 0; ; page++ {
			members, appErr := p.API.GetChannelMembersForUser(team.Id, userID, page, 200)
			if appErr != nil {
				break
			}
			for _, m := range members {
				identifier, err := p.mattermostToMatrixBridge.GetMatrixRoomID(m.ChannelId)
				if err != nil || identifier == "" {
					continue
				}
				if strings.HasPrefix(identifier, "#") {
					if resolved, err := p.matrixClient.ResolveRoomAlias(identifier); err == nil {
						identifier = resolved
					}
				}
				add(identifier)
			}
			if len(members) < 200 {
				break
			}
		}
	}
	return rooms
}

func (p *Plugin) forgetRoomMembership(userID, roomID string) {
	key := kvstore.BuildGhostRoomKey(userID, roomID)
	_ = p.kvstore.Delete(key)
	if p.roomMembershipCache != nil {
		p.roomMembershipCache.Delete(key)
	}
}

// retireMirrorAccount hands a person's rooms from their mirror account to the
// native Matrix account they linked: the mirror account leaves every room, the
// linked account is invited, and the localpart stays reserved so nobody else
// is given it. A claimed mirror account is not made to leave; it is the
// person's own, so it is only no longer used.
func (p *Plugin) retireMirrorAccount(userID, linkedMatrixID string) {
	if p.kvstore == nil || p.matrixClient == nil {
		return
	}
	store := newMirrorStore(p.kvstore)
	acct, ok := store.Account(userID)
	if !ok || acct.MatrixUserID == linkedMatrixID {
		return
	}

	rooms := p.roomsForUser(userID)
	p.logger.LogInfo("Retiring a mirror account", "user_id", userID, "account", acct.MatrixUserID, "rooms", len(rooms), "claimed", acct.Claimed)
	for _, room := range rooms {
		if err := p.matrixClient.InviteUserToRoom(room, linkedMatrixID); err != nil {
			p.logger.LogDebug("Linked account could not be invited (it may already be in the room)", "error", err, "room_id", room)
		}
		if !acct.Claimed {
			if err := p.matrixClient.LeaveRoomAsUser(room, acct.MatrixUserID); err != nil {
				p.logger.LogWarn("Mirror account could not leave a room while being retired", "error", err, "account", acct.MatrixUserID, "room_id", room)
				continue
			}
		}
		p.forgetRoomMembership(userID, room)
	}

	// The localpart key stays, so the name remains reserved.
	_ = p.kvstore.Delete(mirrorAccountKeyPrefix + userID)
	_ = p.kvstore.Delete(mirrorMatrixKeyPrefix + acct.MatrixUserID)
	_ = p.kvstore.Delete(legacyMirrorUserPrefix + userID)
	_ = p.kvstore.Set(mirrorRetiredKeyPrefix+userID, []byte(linkedMatrixID))
	if p.mattermostToMatrixBridge != nil && p.mattermostToMatrixBridge.ghostUserCache != nil {
		p.mattermostToMatrixBridge.ghostUserCache.Delete(kvstore.BuildGhostUserKey(userID))
	}
	p.logger.LogInfo("Retired a mirror account in favour of a linked Matrix account", "user_id", userID, "retired", acct.MatrixUserID, "linked", linkedMatrixID)
}

// retiredMirrorAccount returns the Matrix account that replaced a user's
// mirror account, if it was retired.
func (p *Plugin) retiredMirrorAccount(userID string) (string, bool) {
	data, err := p.kvstore.Get(mirrorRetiredKeyPrefix + userID)
	if err != nil || len(data) == 0 {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}

// retireGhost takes a Mattermost person's ghost out of the rooms once they send
// as their own Matrix account, which joins in its place.
func (p *Plugin) retireGhost(userID, linkedMatrixID string) {
	if p.kvstore == nil || p.matrixClient == nil {
		return
	}
	data, err := p.kvstore.Get(kvstore.BuildGhostUserKey(userID))
	if err != nil || len(data) == 0 {
		return
	}
	ghostID := string(data)
	if ghostID == linkedMatrixID {
		return
	}
	for _, room := range p.roomsForUser(userID) {
		if err := p.matrixClient.InviteUserToRoom(room, linkedMatrixID); err != nil {
			p.logger.LogDebug("The linked account could not be invited (it may already be in the room)", "error", err, "room_id", room)
		}
		if err := p.matrixClient.LeaveRoomAsUser(room, ghostID); err != nil {
			p.logger.LogDebug("A ghost could not leave a room while being retired", "error", err, "ghost", ghostID, "room_id", room)
			continue
		}
		p.forgetRoomMembership(userID, room)
	}
	_ = p.kvstore.Delete(kvstore.BuildGhostUserKey(userID))
	if p.mattermostToMatrixBridge != nil && p.mattermostToMatrixBridge.ghostUserCache != nil {
		p.mattermostToMatrixBridge.ghostUserCache.Delete(kvstore.BuildGhostUserKey(userID))
	}
	p.logger.LogInfo("Retired a ghost in favour of the person's own Matrix account", "user_id", userID, "ghost", ghostID, "linked", linkedMatrixID)
}

// retireShadowAccount deactivates the account that stood for a Matrix person
// in Mattermost before they linked their real Mattermost account. What it
// posted stays attributed to it.
func (p *Plugin) retireShadowAccount(shadowID, realUserID string) {
	if shadowID == "" || shadowID == realUserID {
		return
	}
	user, appErr := p.API.GetUser(shadowID)
	if appErr != nil || user == nil || !user.IsRemote() || user.DeleteAt != 0 {
		return
	}
	if appErr := p.API.UpdateUserActive(shadowID, false); appErr != nil {
		p.logger.LogWarn("Could not retire a shadow account after linking", "error", appErr, "user_id", shadowID)
		return
	}
	p.logger.LogInfo("Retired a shadow account after the person linked their own", "shadow_user_id", shadowID, "user_id", realUserID)
}
