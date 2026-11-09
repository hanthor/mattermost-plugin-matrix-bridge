package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/command"
)

// Importing a channel's past messages into its room, for moving a community
// to Matrix: oldest first, sent as their authors (ghosts or mirror accounts)
// and carrying the time they were written. The bridge sends them as the
// application service, which is the only sender Synapse lets set a timestamp.
//
// A run can be repeated: it skips what already reached the room (the post has
// the room's event ID) and starts after the last message it imported.

const (
	historyCursorKeyPrefix = "history_cursor_"
	historyReportKeyPrefix = "history_report_"
	historyPageSize        = 200
	// historyMaxPages bounds how much history one run examines.
	historyMaxPages = 500
	// historyStopAfter failures in a row end a run; the rest can be resumed.
	historyStopAfter = 3
)

var historyRuns sync.Map // channel ID -> struct{}: imports in progress

type historyPlan struct {
	posts     []*model.Post
	skipped   int
	files     int
	truncated bool
}

// historyEventKey is the post property that holds the room's event ID.
func (p *Plugin) historyEventKey() string {
	cfg := p.getConfiguration()
	return "matrix_event_id_" + extractServerDomain(p.logger, cfg.MatrixServerURL)
}

func (p *Plugin) historyRoom(channelID string) (string, error) {
	if p.mattermostToMatrixBridge == nil || p.matrixClient == nil {
		return "", errors.New("the bridge is not connected to Matrix")
	}
	identifier, err := p.mattermostToMatrixBridge.GetMatrixRoomID(channelID)
	if err != nil {
		return "", errors.Wrap(err, "could not look up this channel's room")
	}
	if identifier == "" {
		return "", errors.New("this channel is not bridged: run /matrix create first")
	}
	roomID, err := p.matrixClient.ResolveRoomAlias(identifier)
	if err != nil {
		return "", errors.Wrap(err, "could not resolve this channel's room")
	}
	return roomID, nil
}

func (p *Plugin) historyCursor(channelID string) int64 {
	data, err := p.kvstore.Get(historyCursorKeyPrefix + channelID)
	if err != nil || len(data) == 0 {
		return 0
	}
	n, _ := strconv.ParseInt(string(data), 10, 64)
	return n
}

// planHistory collects the messages to import, oldest first.
func (p *Plugin) planHistory(channelID string, limit int) (*historyPlan, error) {
	cursor := p.historyCursor(channelID)
	key := p.historyEventKey()
	plan := &historyPlan{}

	for page := 0; ; page++ {
		if page >= historyMaxPages {
			plan.truncated = true
			break
		}
		list, appErr := p.API.GetPostsForChannel(channelID, page, historyPageSize)
		if appErr != nil {
			return nil, errors.Wrap(appErr, "could not read the channel's messages")
		}
		if list == nil || len(list.Order) == 0 {
			break
		}
		for _, id := range list.Order {
			post := list.Posts[id]
			switch {
			case post == nil:
				continue
			case post.CreateAt <= cursor:
				// Already covered by an earlier run.
				plan.skipped++
			case post.DeleteAt != 0 || post.Type != "":
				plan.skipped++
			case p.remoteID != "" && post.GetRemoteID() == p.remoteID:
				plan.skipped++ // came from Matrix
			case hasString(post.Props, key):
				plan.skipped++ // already in the room
			default:
				plan.posts = append(plan.posts, post)
				plan.files += len(post.FileIds)
			}
		}
	}

	sort.SliceStable(plan.posts, func(i, j int) bool { return plan.posts[i].CreateAt < plan.posts[j].CreateAt })
	if limit > 0 && len(plan.posts) > limit {
		plan.posts = plan.posts[:limit]
	}
	return plan, nil
}

func hasString(props model.StringInterface, key string) bool {
	s, _ := props[key].(string)
	return s != ""
}

func (plan *historyPlan) report() command.HistoryReport {
	r := command.HistoryReport{Pending: len(plan.posts), Skipped: plan.skipped, Files: plan.files, Truncated: plan.truncated}
	if len(plan.posts) > 0 {
		r.Oldest = plan.posts[0].CreateAt
		r.Newest = plan.posts[len(plan.posts)-1].CreateAt
	}
	return r
}

// PlanChannelHistory implements command.PluginAccessor.
func (p *Plugin) PlanChannelHistory(channelID string, limit int) (command.HistoryReport, error) {
	if _, err := p.historyRoom(channelID); err != nil {
		return command.HistoryReport{}, err
	}
	plan, err := p.planHistory(channelID, limit)
	if err != nil {
		return command.HistoryReport{}, err
	}
	return plan.report(), nil
}

// ChannelHistoryStatus implements command.PluginAccessor.
func (p *Plugin) ChannelHistoryStatus(channelID string) command.HistoryReport {
	var r command.HistoryReport
	if data, err := p.kvstore.Get(historyReportKeyPrefix + channelID); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &r)
	}
	_, running := historyRuns.Load(channelID)
	r.Running = running
	return r
}

func (p *Plugin) saveHistoryReport(channelID string, r command.HistoryReport) {
	if data, err := json.Marshal(r); err == nil {
		_ = p.kvstore.Set(historyReportKeyPrefix+channelID, data)
	}
}

// StartChannelHistory implements command.PluginAccessor: it imports in the
// background and returns at once.
func (p *Plugin) StartChannelHistory(channelID string, limit int) error {
	roomID, err := p.historyRoom(channelID)
	if err != nil {
		return err
	}
	if _, busy := historyRuns.LoadOrStore(channelID, struct{}{}); busy {
		return errors.New("an import is already running for this channel")
	}
	plan, err := p.planHistory(channelID, limit)
	if err != nil {
		historyRuns.Delete(channelID)
		return err
	}
	go func() {
		defer historyRuns.Delete(channelID)
		p.runHistoryImport(channelID, roomID, plan)
	}()
	return nil
}

// runHistoryImport sends the planned messages in order. It stops after a few
// failures in a row, leaving the cursor after the last message that arrived so
// a later run resumes there.
func (p *Plugin) runHistoryImport(channelID, roomID string, plan *historyPlan) {
	key := p.historyEventKey()
	report := command.HistoryReport{Pending: len(plan.posts), Skipped: plan.skipped, Files: plan.files, Truncated: plan.truncated}
	consecutive := 0
	for i, post := range plan.posts {
		user, appErr := p.API.GetUser(post.UserId)
		if appErr != nil || user == nil || user.IsRemote() || user.IsBot {
			report.Skipped++
			continue
		}
		// Attachments are uploaded first so the message carries them.
		p.stageAttachmentsForPost(post)
		if err := p.mattermostToMatrixBridge.createPostInMatrix(post, roomID, user, key, post.CreateAt); err != nil {
			report.Failed++
			consecutive++
			p.logger.LogWarn("Failed to import a message into the room", "error", err, "post_id", post.Id, "channel_id", channelID)
			if consecutive >= historyStopAfter {
				report.Error = fmt.Sprintf("%d failures in a row, last: %v", consecutive, err)
				break
			}
			continue
		}
		consecutive = 0
		report.Imported++
		report.Reactions += p.importReactions(post, channelID)
		// Failures in between keep the cursor behind them: a rerun retries them.
		if report.Failed == 0 {
			_ = p.kvstore.Set(historyCursorKeyPrefix+channelID, []byte(strconv.FormatInt(post.CreateAt, 10)))
		}
		if (i+1)%25 == 0 {
			p.saveHistoryReport(channelID, report)
		}
	}
	report.Pending = 0
	p.saveHistoryReport(channelID, report)
	p.logger.LogInfo("History import finished", "channel_id", channelID, "imported", report.Imported, "skipped", report.Skipped, "failed", report.Failed, "at", time.Now().UTC().Format(time.RFC3339))
}

// importReactions replays a post's reactions into the room with their original
// times. A reaction that cannot be sent is skipped; the message itself is in.
func (p *Plugin) importReactions(post *model.Post, channelID string) int {
	reactions, appErr := p.API.GetReactions(post.Id)
	if appErr != nil || len(reactions) == 0 {
		return 0
	}
	sort.SliceStable(reactions, func(i, j int) bool { return reactions[i].CreateAt < reactions[j].CreateAt })
	sent := 0
	for _, r := range reactions {
		if err := p.mattermostToMatrixBridge.addReactionToMatrixAt(r, channelID, r.CreateAt); err != nil {
			p.logger.LogDebug("Could not import a reaction", "error", err, "post_id", post.Id, "emoji", r.EmojiName)
			continue
		}
		sent++
	}
	return sent
}
