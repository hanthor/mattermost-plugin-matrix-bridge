package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// Cut-over tooling: the last steps of moving a team from Mattermost to
// Matrix. Channels with a room are made read-only (people can still read, and
// whatever arrives from Matrix still lands), a pinned message points to the
// room, a report compares both sides, and finally the channels are archived.
//
//	/matrix migrate-team report
//	/matrix migrate-team readonly [confirm]
//	/matrix migrate-team writable [confirm]
//	/matrix migrate-team archive [confirm]

const (
	cutoverReadOnlyKeyPrefix = "cutover_readonly_" // + channel ID -> "1"
	cutoverNoticeProp        = "matrix_bridge_cutover_notice"
	cutoverReportLimit       = 40
)

// teamMappedChannels lists the channels of a team that have a Matrix room,
// with their room identifiers, sorted by name.
func (p *Plugin) teamMappedChannels(teamID string) ([]*model.Channel, map[string]string, error) {
	prefix := kvstore.KeyPrefixChannelMapping
	var channels []*model.Channel
	rooms := map[string]string{}
	for page := 0; ; page++ {
		keys, err := p.kvstore.ListKeysWithPrefix(page, 200, prefix)
		if err != nil {
			return nil, nil, errors.Wrap(err, "could not list the channel mappings")
		}
		for _, key := range keys {
			channelID := strings.TrimPrefix(key, prefix)
			room, err := p.kvstore.Get(key)
			if err != nil || len(room) == 0 {
				continue
			}
			ch, appErr := p.API.GetChannel(channelID)
			if appErr != nil || ch == nil || ch.TeamId != teamID || ch.DeleteAt != 0 || ch.Type == model.ChannelTypeDirect || ch.Type == model.ChannelTypeGroup {
				continue
			}
			channels = append(channels, ch)
			rooms[ch.Id] = string(room)
		}
		if len(keys) < 200 {
			break
		}
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i].Name < channels[j].Name })
	return channels, rooms, nil
}

func (p *Plugin) channelReadOnly(channelID string) bool {
	data, err := p.kvstore.Get(cutoverReadOnlyKeyPrefix + channelID)
	return err == nil && len(data) > 0
}

// MessageWillBePosted keeps read-only channels read-only for people. Posts
// that come from Matrix, system messages and the cut-over notice itself are
// allowed.
func (p *Plugin) MessageWillBePosted(_ *plugin.Context, post *model.Post) (*model.Post, string) {
	if p.kvstore == nil || post == nil || post.Type != "" || post.ChannelId == "" {
		return post, ""
	}
	if !p.channelReadOnly(post.ChannelId) {
		return post, ""
	}
	if post.GetProp(cutoverNoticeProp) != nil || post.GetProp("from_matrix") != nil {
		return post, ""
	}
	if remote := post.GetRemoteID(); remote != "" {
		return post, ""
	}
	if user, appErr := p.API.GetUser(post.UserId); appErr == nil && user.IsRemote() {
		return post, ""
	}
	return nil, "This channel has moved to Matrix and is read-only. See the pinned message for the link."
}

// roomLink is a link people can open to join the room.
func (p *Plugin) roomLink(identifier string) string {
	if base := strings.TrimRight(p.getConfiguration().ElementURL, "/"); base != "" {
		return base + "/#/room/" + identifier
	}
	return "https://matrix.to/#/" + identifier
}

// MigrateTeam implements command.PluginAccessor.
func (p *Plugin) MigrateTeam(userID, teamID, action string, confirm bool) (string, error) {
	if p.kvstore == nil || p.API == nil {
		return "", errors.New("the bridge is not ready")
	}
	if !p.API.HasPermissionTo(userID, model.PermissionManageSystem) {
		return "", errors.New("only system administrators can do this")
	}
	channels, rooms, err := p.teamMappedChannels(teamID)
	if err != nil {
		return "", err
	}
	if len(channels) == 0 {
		return "No channel of this team is bridged to a Matrix room, so there is nothing to cut over.", nil
	}

	switch action {
	case "report":
		return p.cutoverReport(channels, rooms), nil
	case "readonly":
		return p.cutoverReadOnly(userID, channels, rooms, confirm), nil
	case "writable":
		return p.cutoverWritable(channels, confirm), nil
	case "archive":
		return p.cutoverArchive(channels, confirm), nil
	}
	return "", errors.New("unknown action; use report, readonly, writable or archive")
}

func (p *Plugin) cutoverReport(channels []*model.Channel, rooms map[string]string) string {
	var sb strings.Builder
	sb.WriteString("📋 **Cut-over report**\n\n")
	sb.WriteString("| Channel | Read-only | Members (MM / Matrix) | Messages not in the room |\n|---|---|---|---|\n")
	ready := 0
	for i, ch := range channels {
		if i >= cutoverReportLimit {
			sb.WriteString(fmt.Sprintf("\n…and %d more channels.", len(channels)-i))
			break
		}
		mmMembers := "?"
		if stats, appErr := p.API.GetChannelStats(ch.Id); appErr == nil && stats != nil {
			mmMembers = fmt.Sprint(stats.MemberCount)
		}
		mxMembers := "?"
		if roomID, err := p.matrixClient.ResolveRoomAlias(rooms[ch.Id]); err == nil {
			if ov, err := p.matrixClient.GetRoomOverview(roomID); err == nil {
				mxMembers = fmt.Sprint(ov.Members)
			}
		}
		pending := "?"
		plan, err := p.planHistory(ch.Id, 0)
		clean := false
		if err == nil {
			pending = fmt.Sprint(len(plan.posts))
			if plan.truncated {
				pending += "+"
			}
			clean = len(plan.posts) == 0
		}
		readOnly := "no"
		if p.channelReadOnly(ch.Id) {
			readOnly = "yes"
		}
		mark := ""
		if clean {
			mark = " ✅"
			ready++
		}
		sb.WriteString(fmt.Sprintf("| ~%s | %s | %s / %s | %s%s |\n", ch.Name, readOnly, mmMembers, mxMembers, pending, mark))
	}
	sb.WriteString(fmt.Sprintf("\n%d of %d channels have every message in their room (✅). Import the rest with `/matrix history confirm` in the channel. Matrix members include the bridge's own accounts.", ready, len(channels)))
	return sb.String()
}

func (p *Plugin) cutoverReadOnly(userID string, channels []*model.Channel, rooms map[string]string, confirm bool) string {
	var todo []*model.Channel
	for _, ch := range channels {
		if !p.channelReadOnly(ch.Id) {
			todo = append(todo, ch)
		}
	}
	if len(todo) == 0 {
		return "✅ Every bridged channel of this team is already read-only."
	}
	if !confirm {
		return fmt.Sprintf("🔒 **%d channels would become read-only**, each with a pinned message that links to its Matrix room. People can still read them, and messages from Matrix keep arriving.\n\nRun `/matrix migrate-team readonly confirm` to do it. `/matrix migrate-team writable confirm` undoes it.", len(todo))
	}
	done, failed := 0, 0
	for _, ch := range todo {
		if err := p.kvstore.Set(cutoverReadOnlyKeyPrefix+ch.Id, []byte("1")); err != nil {
			failed++
			continue
		}
		p.postCutoverNotice(userID, ch, rooms[ch.Id])
		done++
	}
	out := fmt.Sprintf("🔒 **%d channels are now read-only** with a pinned link to their room.", done)
	if failed > 0 {
		out += fmt.Sprintf(" %d could not be changed; run it again.", failed)
	}
	return out
}

func (p *Plugin) postCutoverNotice(userID string, ch *model.Channel, room string) {
	post, appErr := p.API.CreatePost(&model.Post{
		UserId:    userID,
		ChannelId: ch.Id,
		Message:   fmt.Sprintf("📣 **This channel has moved to Matrix.** It is now read-only here.\n\nJoin the conversation: %s", p.roomLink(room)),
		Props:     model.StringInterface{cutoverNoticeProp: true},
	})
	if appErr != nil {
		p.logger.LogWarn("Could not post the cut-over notice", "error", appErr, "channel_id", ch.Id)
		return
	}
	post.IsPinned = true
	if _, appErr := p.API.UpdatePost(post); appErr != nil {
		p.logger.LogWarn("Could not pin the cut-over notice", "error", appErr, "channel_id", ch.Id)
	}
}

func (p *Plugin) cutoverWritable(channels []*model.Channel, confirm bool) string {
	var todo []*model.Channel
	for _, ch := range channels {
		if p.channelReadOnly(ch.Id) {
			todo = append(todo, ch)
		}
	}
	if len(todo) == 0 {
		return "No bridged channel of this team is read-only."
	}
	if !confirm {
		return fmt.Sprintf("🔓 %d read-only channels would accept messages again. Run `/matrix migrate-team writable confirm` to do it. The pinned notices stay; delete them by hand.", len(todo))
	}
	for _, ch := range todo {
		_ = p.kvstore.Delete(cutoverReadOnlyKeyPrefix + ch.Id)
	}
	return fmt.Sprintf("🔓 %d channels accept messages again.", len(todo))
}

func (p *Plugin) cutoverArchive(channels []*model.Channel, confirm bool) string {
	var todo []*model.Channel
	skipped := 0
	for _, ch := range channels {
		if p.channelReadOnly(ch.Id) {
			todo = append(todo, ch)
		} else {
			skipped++
		}
	}
	if len(todo) == 0 {
		return "❌ Nothing to archive: only read-only channels are archived. Run `/matrix migrate-team readonly confirm` first."
	}
	note := ""
	if skipped > 0 {
		note = fmt.Sprintf(" %d channels that are not read-only are left alone.", skipped)
	}
	if !confirm {
		return fmt.Sprintf("🗄️ **%d read-only channels would be archived.** Their posts stay in the database and can be restored by unarchiving, but people lose the channel from their sidebar.%s\n\nRun `/matrix migrate-team archive confirm` to do it.", len(todo), note)
	}
	done, failed := 0, 0
	for _, ch := range todo {
		if appErr := p.API.DeleteChannel(ch.Id); appErr != nil {
			failed++
			p.logger.LogWarn("Could not archive a channel", "error", appErr, "channel_id", ch.Id)
			continue
		}
		done++
	}
	out := fmt.Sprintf("🗄️ %d channels archived.%s", done, note)
	if failed > 0 {
		out += fmt.Sprintf(" %d failed; see the log.", failed)
	}
	return out
}
