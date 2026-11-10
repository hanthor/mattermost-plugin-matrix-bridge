package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// Membership is kept in step by events (someone joins or leaves a channel,
// an account joins or leaves a room) and an event that was missed, for
// instance while the homeserver was down, would otherwise stay wrong
// forever. Reconciliation compares who is in the Mattermost channel with who
// is in the Matrix room and fixes the difference for the bridge's own
// accounts. Real Matrix people, claimed mirror accounts and the bridge bot are
// never touched: they belong to themselves.

const (
	ghostLocalpartPrefix  = "_mattermost_"
	reconcileCursorKey    = "reconcile_cursor"
	reconcileChannelsEach = 20
)

type reconcileResult struct {
	Channel      string
	Joined       []string // bridge accounts added to the room
	Left         []string // bridge accounts removed from the room
	CannotFix    []string // linked people who must join with their own account
	MissingAfter int
}

func (r reconcileResult) outOfStep() int { return len(r.Joined) + len(r.Left) + len(r.CannotFix) }

// mattermostUserForBridgeAccount returns the Mattermost user a ghost or an
// unclaimed mirror account stands for, or "" for any other account.
func (p *Plugin) mattermostUserForBridgeAccount(matrixUserID string) string {
	if matrixUserID == p.BridgeBotUserID() {
		return ""
	}
	if acct, ok := newMirrorStore(p.kvstore).AccountForMatrixUser(matrixUserID); ok {
		if acct.Claimed {
			return ""
		}
		return acct.MattermostUserID
	}
	local := strings.TrimPrefix(strings.SplitN(matrixUserID, ":", 2)[0], "@")
	if strings.HasPrefix(local, ghostLocalpartPrefix) {
		return strings.TrimPrefix(local, ghostLocalpartPrefix)
	}
	return ""
}

// reconcileChannel compares a bridged channel with its room. Without apply it
// only reports what would change.
func (p *Plugin) reconcileChannel(channelID string, apply bool) (reconcileResult, error) {
	res := reconcileResult{Channel: channelID}
	if p.mattermostToMatrixBridge == nil || p.matrixClient == nil {
		return res, errors.New("the bridge is not connected to Matrix")
	}
	identifier, err := p.mattermostToMatrixBridge.GetMatrixRoomID(channelID)
	if err != nil || identifier == "" {
		return res, errors.New("this channel is not bridged")
	}
	roomID, err := p.matrixClient.ResolveRoomAlias(identifier)
	if err != nil {
		return res, errors.Wrap(err, "could not resolve the room")
	}
	joinedList, err := p.matrixClient.JoinedMembers(roomID)
	if err != nil {
		return res, errors.Wrap(err, "could not list the room's members")
	}
	joined := map[string]bool{}
	for _, id := range joinedList {
		joined[id] = true
	}

	// Who should be there: every local, human member of the channel.
	members := map[string]bool{}
	for page := 0; ; page++ {
		batch, appErr := p.API.GetChannelMembers(channelID, page, 200)
		if appErr != nil {
			return res, errors.Wrap(appErr, "could not list the channel's members")
		}
		for _, m := range batch {
			user, appErr := p.API.GetUser(m.UserId)
			if appErr != nil || user == nil || user.IsRemote() || user.IsBot {
				continue
			}
			members[m.UserId] = true
		}
		if len(batch) < 200 {
			break
		}
	}

	cutover := p.getConfiguration().CutoverMode
	for userID := range members {
		if link, linked := p.identity().Link(userID); linked && link.Puppeted() {
			if !joined[link.MatrixUserID] {
				res.CannotFix = append(res.CannotFix, link.MatrixUserID)
			}
			continue
		}
		account, known := lookupBridgeAccount(p.kvstore, userID)
		if !known {
			if !apply || cutover {
				res.Joined = append(res.Joined, userID)
				continue
			}
			created, err := p.mattermostToMatrixBridge.ensureAccount(userID)
			if err != nil {
				res.CannotFix = append(res.CannotFix, userID)
				continue
			}
			account = created
		}
		if joined[account] {
			continue
		}
		res.Joined = append(res.Joined, account)
		if apply {
			if err := p.matrixClient.InviteAndJoinGhostUser(roomID, account); err != nil {
				res.CannotFix = append(res.CannotFix, account)
				res.Joined = res.Joined[:len(res.Joined)-1]
			}
		}
	}

	// Who should not be there: bridge accounts of people who left the channel.
	for account := range joined {
		userID := p.mattermostUserForBridgeAccount(account)
		if userID == "" || members[userID] {
			continue
		}
		res.Left = append(res.Left, account)
		if apply {
			if err := p.matrixClient.LeaveRoomAsUser(roomID, account); err != nil {
				res.CannotFix = append(res.CannotFix, account)
				res.Left = res.Left[:len(res.Left)-1]
				continue
			}
			p.forgetRoomMembership(userID, roomID)
		}
	}
	sort.Strings(res.Joined)
	sort.Strings(res.Left)
	sort.Strings(res.CannotFix)
	res.MissingAfter = len(res.CannotFix)
	return res, nil
}

// RepairMembership implements command.PluginAccessor for /matrix repair.
func (p *Plugin) RepairMembership(channelID string) (string, error) {
	// A repair also brings the room's name, topic and archived notice up to date.
	p.syncChannelStateByID(channelID)
	res, err := p.reconcileChannel(channelID, true)
	if err != nil {
		return "", err
	}
	if res.outOfStep() == 0 {
		return "✅ The room and the channel have the same people.", nil
	}
	var sb strings.Builder
	sb.WriteString("🔧 **Membership repaired**\n")
	if len(res.Joined) > 0 {
		sb.WriteString(fmt.Sprintf("\n- %d people were missing from the room and were added.", len(res.Joined)))
	}
	if len(res.Left) > 0 {
		sb.WriteString(fmt.Sprintf("\n- %d accounts of people who are no longer in the channel left the room.", len(res.Left)))
	}
	if len(res.CannotFix) > 0 {
		sb.WriteString(fmt.Sprintf("\n- ⚠️ %d could not be fixed: %s. People who use their own Matrix account must join the room themselves.", len(res.CannotFix), strings.Join(res.CannotFix, ", ")))
	}
	return sb.String(), nil
}

// reconcileSome repairs a few channels per run, continuing where the last run
// stopped, so a large deployment is covered over time without a burst.
func (p *Plugin) reconcileSome() {
	if p.kvstore == nil || p.mattermostToMatrixBridge == nil || p.matrixClient == nil || !p.getConfiguration().EnableSync {
		return
	}
	var channelIDs []string
	for page := 0; ; page++ {
		keys, err := p.kvstore.ListKeysWithPrefix(page, 200, kvstore.KeyPrefixChannelMapping)
		if err != nil {
			return
		}
		for _, k := range keys {
			channelIDs = append(channelIDs, strings.TrimPrefix(k, kvstore.KeyPrefixChannelMapping))
		}
		if len(keys) < 200 {
			break
		}
	}
	sort.Strings(channelIDs)
	cursor, _ := p.kvstore.Get(reconcileCursorKey)
	start := sort.SearchStrings(channelIDs, string(cursor))
	if start >= len(channelIDs) {
		start = 0
	}
	end := start + reconcileChannelsEach
	if end > len(channelIDs) {
		end = len(channelIDs)
	}
	for _, id := range channelIDs[start:end] {
		if channel, appErr := p.API.GetChannel(id); appErr != nil || channel == nil || channel.DeleteAt != 0 || p.channelReadOnly(id) {
			continue
		}
		res, err := p.reconcileChannel(id, true)
		if err != nil {
			p.logger.LogDebug("Membership check skipped a channel", "channel_id", id, "error", err)
			continue
		}
		if res.outOfStep() > 0 {
			p.logger.LogInfo("Membership repaired", "channel_id", id, "joined", len(res.Joined), "left", len(res.Left), "cannot_fix", len(res.CannotFix))
		}
	}
	next := ""
	if end < len(channelIDs) {
		next = channelIDs[end]
	}
	_ = p.kvstore.Set(reconcileCursorKey, []byte(next))
}
