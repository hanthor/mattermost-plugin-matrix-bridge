package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// Erasing a person from the Matrix side. A deleted or anonymised Mattermost
// account leaves its messages behind in every bridged room; an administrator
// can run
//
//	/matrix erase <username> [confirm]
//
// to redact the messages the person's bridge account sent, take the account
// out of the rooms and, for a mirror account, deactivate and erase it on the
// homeserver (needs the Matrix admin token). Without "confirm" it only says
// what it would do. Mattermost's own data is untouched: that is Mattermost's
// to delete.

const (
	eraseScanPagesPerChannel = 100
	eraseScanPageSize        = 200
)

type eraseTarget struct {
	roomID  string
	eventID string
}

// EraseUser previews, or with confirm carries out, the erasure of a person's
// bridged messages and bridge account.
func (p *Plugin) EraseUser(adminID, username string, confirm bool) (string, error) {
	if p.kvstore == nil || p.API == nil || p.matrixClient == nil {
		return "", errors.New("the bridge is not ready")
	}
	if !p.API.HasPermissionTo(adminID, model.PermissionManageSystem) {
		return "", errors.New("only system administrators can do this")
	}
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	if username == "" {
		return "", errors.New("usage: `/matrix erase <username> [confirm]`")
	}
	user, appErr := p.API.GetUserByUsername(username)
	if appErr != nil || user == nil {
		return "", errors.Errorf("no Mattermost user named %q", username)
	}
	if user.IsRemote() {
		return "", errors.New("that account stands for a Matrix person; erase them on their homeserver")
	}
	accountID, ok := lookupBridgeAccount(p.kvstore, user.Id)
	if !ok {
		return fmt.Sprintf("**%s** has no bridge account, so nothing of theirs was sent to Matrix by the bridge.", user.Username), nil
	}
	notes := ""
	if link, linked := p.identity().Link(user.Id); linked && link.Puppeted() {
		notes = "\n\nTheir own Matrix account (linked with `!mattermost login`) sent some messages; the bridge cannot redact those, and the account is theirs to delete."
	}

	targets := p.erasableEvents(user.Id)
	rooms := map[string]bool{}
	for _, t := range targets {
		rooms[t.roomID] = true
	}
	cfg := p.getConfiguration()
	_, isMirror := newMirrorStore(p.kvstore).Account(user.Id)
	account := fmt.Sprintf("**%s** leaves the bridged rooms", accountID)
	if isMirror && cfg.MatrixAdminToken != "" {
		account += " and is deactivated and erased on the homeserver"
	} else if isMirror {
		account += "; it is not erased on the homeserver because no Matrix admin token is configured"
	}

	if !confirm {
		return fmt.Sprintf("Erasing **%s** would redact **%d messages** in %d rooms, and %s.%s\n\nRun `/matrix erase %s confirm` to do it. This cannot be undone.",
			user.Username, len(targets), len(rooms), account, notes, user.Username), nil
	}

	var redacted, failed int
	for _, t := range targets {
		if _, err := p.matrixClient.RedactEventAsGhost(t.roomID, t.eventID, accountID); err != nil {
			p.logger.LogWarn("Could not redact a message while erasing a person", "error", err, "room_id", t.roomID, "event_id", t.eventID)
			failed++
			continue
		}
		redacted++
	}
	p.leaveRoomsAndDeactivate(user.Id, accountID, isMirror, cfg.MatrixAdminToken)

	text := fmt.Sprintf("✅ Erased **%s**: %d messages redacted, and %s.%s", user.Username, redacted, account, notes)
	if failed > 0 {
		text += fmt.Sprintf("\n⚠️ %d messages could not be redacted; run the command again to retry them.", failed)
	}
	return text, nil
}

// erasableEvents lists the Matrix events the person's posts became, in
// bridged channels.
func (p *Plugin) erasableEvents(userID string) []eraseTarget {
	propertyKey := "matrix_event_id_" + extractServerDomain(p.API, p.getConfiguration().MatrixServerURL)
	var channelIDs []string
	rooms := map[string]string{}
	for page := 0; ; page++ {
		keys, err := p.kvstore.ListKeysWithPrefix(page, 200, kvstore.KeyPrefixChannelMapping)
		if err != nil {
			break
		}
		for _, k := range keys {
			id := strings.TrimPrefix(k, kvstore.KeyPrefixChannelMapping)
			channelIDs = append(channelIDs, id)
		}
		if len(keys) < 200 {
			break
		}
	}
	sort.Strings(channelIDs)
	var out []eraseTarget
	for _, channelID := range channelIDs {
		room := p.roomIDForChannel(channelID)
		if room == "" {
			continue
		}
		rooms[channelID] = room
		for page := 0; page < eraseScanPagesPerChannel; page++ {
			list, appErr := p.API.GetPostsForChannel(channelID, page, eraseScanPageSize)
			if appErr != nil || list == nil || len(list.Order) == 0 {
				break
			}
			for _, id := range list.Order {
				post := list.Posts[id]
				if post == nil || post.UserId != userID || post.DeleteAt != 0 {
					continue
				}
				if eventID, _ := post.Props[propertyKey].(string); eventID != "" {
					out = append(out, eraseTarget{roomID: room, eventID: eventID})
				}
			}
			if len(list.Order) < eraseScanPageSize {
				break
			}
		}
	}
	return out
}

// leaveRoomsAndDeactivate takes the account out of its rooms and, for a mirror
// account with an admin token, erases it on the homeserver.
func (p *Plugin) leaveRoomsAndDeactivate(userID, accountID string, isMirror bool, adminToken string) {
	for _, room := range p.roomsForUser(userID) {
		if err := p.matrixClient.LeaveRoomAsUser(room, accountID); err != nil {
			p.logger.LogWarn("The erased person's account could not leave a room", "error", err, "room_id", room)
			continue
		}
		p.forgetRoomMembership(userID, room)
	}
	if isMirror && adminToken != "" {
		if err := p.matrixClient.EraseUserAsAdmin(adminToken, accountID); err != nil {
			p.logger.LogWarn("Could not erase the mirror account on the homeserver", "error", err, "account", accountID)
		}
	}
}
