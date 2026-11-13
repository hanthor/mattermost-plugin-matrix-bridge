package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// Importing a room's history into its channel is the mirror image of
// /matrix history: when an existing Matrix room is mapped, its past messages
// are copied into the channel with their original times, as the people who
// wrote them, through the same path live messages take. Mattermost orders a
// channel by creation time, so they land where they belong.

const (
	matrixHistoryDefaultLimit = 200
	matrixHistoryMaxLimit     = 2000
	matrixHistoryPage         = 100
)

type matrixHistoryPlan struct {
	events    []MatrixEvent // oldest first: messages, then reactions
	messages  int
	files     int
	reactions int
	edits     int
	already   int // events that already have a post
	encrypted int // events the bridge cannot read
	byBridge  int // events written by the bridge's own accounts
	reached   bool
	oldest    int64
	newest    int64
}

// planMatrixHistory reads up to limit message events of the room, newest
// first, and works out what would be imported.
func (p *Plugin) planMatrixHistory(channelID string, limit int) (*matrixHistoryPlan, string, error) {
	roomID := p.roomIDForChannel(channelID)
	if roomID == "" {
		return nil, "", fmt.Errorf("this channel is not linked to a Matrix room. Run `/matrix map` or `/matrix join` first")
	}
	if limit <= 0 {
		limit = matrixHistoryDefaultLimit
	}
	if limit > matrixHistoryMaxLimit {
		limit = matrixHistoryMaxLimit
	}

	plan := &matrixHistoryPlan{}
	var collected []MatrixEvent
	replacements := map[string]MatrixEvent{} // original event ID -> newest replacement
	token := ""
	for len(collected) < limit {
		chunk, next, err := p.matrixClient.RoomMessagesBackwards(roomID, token, matrixHistoryPage)
		if err != nil {
			return nil, "", err
		}
		for _, raw := range chunk {
			event, ok := matrixEventFromMap(raw)
			if !ok {
				continue
			}
			event.RoomID = roomID
			if event.Type == "m.room.encrypted" {
				// Read it if the bridge holds the room key (it kept it, or got it
				// back from its key backup); otherwise count it as unreadable.
				dec, ok := p.readOldEncryptedEvent(event)
				if !ok {
					plan.encrypted++
					continue
				}
				event = dec
			}
			switch event.Type {
			case "m.room.message", "m.reaction":
				if p.isBridgeAccount(event.Sender) {
					plan.byBridge++
					continue
				}
				if isReplacement(event) {
					orig := replacedEventID(event)
					if _, seen := replacements[orig]; !seen { // newest first: the first is the latest edit
						replacements[orig] = event
					}
					plan.edits++
					continue
				}
				if data, _ := p.kvstore.Get(kvstore.BuildMatrixEventPostKey(event.EventID)); len(data) > 0 {
					plan.already++
					continue
				}
				collected = append(collected, event)
			}
		}
		if next == "" || len(chunk) == 0 {
			plan.reached = true
			break
		}
		token = next
	}
	if len(collected) > limit {
		collected = collected[:limit]
		plan.reached = false
	}

	// Apply the latest edit of each message, then order oldest first.
	for i, ev := range collected {
		if rep, ok := replacements[ev.EventID]; ok && ev.Type == "m.room.message" {
			if newContent, ok := rep.Content["m.new_content"].(map[string]any); ok {
				collected[i].Content = newContent
			}
		}
	}
	sort.SliceStable(collected, func(i, j int) bool { return collected[i].Timestamp < collected[j].Timestamp })
	// Messages before reactions, so a reaction finds its post.
	var messages, reactions []MatrixEvent
	for _, ev := range collected {
		if ev.Type == "m.reaction" {
			reactions = append(reactions, ev)
		} else {
			messages = append(messages, ev)
		}
	}
	// Reactions are not recorded once imported, so on their own they would be
	// planned again on every run. They travel with new messages only.
	if len(messages) == 0 {
		reactions = nil
	}
	plan.events = append(messages, reactions...)
	plan.reactions = len(reactions)
	plan.messages = len(messages)
	for _, ev := range messages {
		if mt, _ := ev.Content["msgtype"].(string); mt == "m.image" || mt == "m.file" || mt == "m.video" || mt == "m.audio" {
			plan.files++
		}
	}
	if len(messages) > 0 {
		plan.oldest = messages[0].Timestamp
		plan.newest = messages[len(messages)-1].Timestamp
	}
	return plan, roomID, nil
}

func isReplacement(ev MatrixEvent) bool {
	rel, _ := ev.Content["m.relates_to"].(map[string]any)
	t, _ := rel["rel_type"].(string)
	return t == "m.replace"
}

func replacedEventID(ev MatrixEvent) string {
	rel, _ := ev.Content["m.relates_to"].(map[string]any)
	id, _ := rel["event_id"].(string)
	return id
}

func matrixEventFromMap(raw map[string]any) (MatrixEvent, bool) {
	ev := MatrixEvent{}
	ev.Type, _ = raw["type"].(string)
	ev.EventID, _ = raw["event_id"].(string)
	ev.Sender, _ = raw["sender"].(string)
	ev.Content, _ = raw["content"].(map[string]any)
	switch ts := raw["origin_server_ts"].(type) {
	case float64:
		ev.Timestamp = int64(ts)
	}
	if ev.Type == "" || ev.EventID == "" || ev.Content == nil || len(ev.Content) == 0 {
		return ev, false // a redacted event has no content
	}
	return ev, true
}

func (plan *matrixHistoryPlan) describe() string {
	var sb strings.Builder
	if plan.messages == 0 && plan.reactions == 0 {
		sb.WriteString("Nothing to import: every message in the room already has a post here.")
	} else {
		sb.WriteString(fmt.Sprintf("%d messages and %d reactions would be imported, oldest first (%s to %s), as the people who wrote them and with their original times.",
			plan.messages, plan.reactions, time.UnixMilli(plan.oldest).UTC().Format("2006-01-02"), time.UnixMilli(plan.newest).UTC().Format("2006-01-02")))
		if plan.files > 0 {
			sb.WriteString(fmt.Sprintf("\n\n%d of them are files; files above the size limit are announced instead of copied.", plan.files))
		}
		if plan.edits > 0 {
			sb.WriteString(fmt.Sprintf("\n\n%d edits are applied to the messages they change.", plan.edits))
		}
	}
	if plan.already > 0 {
		sb.WriteString(fmt.Sprintf("\n\n%d events were imported before and are skipped.", plan.already))
	}
	if plan.encrypted > 0 {
		sb.WriteString(fmt.Sprintf("\n\n⚠️ %d events are encrypted and cannot be read by the bridge; they are not imported.", plan.encrypted))
	}
	if !plan.reached {
		sb.WriteString("\n\nThe room has more history than one run reads. Run it again afterwards to continue; events already imported are skipped.")
	}
	return sb.String()
}

// ImportMatrixHistory implements command.PluginAccessor. Without confirm it
// only describes what would be imported; with it the import runs in the
// background and the person is told the result.
func (p *Plugin) ImportMatrixHistory(userID, channelID string, limit int, confirm bool) (string, error) {
	plan, roomID, err := p.planMatrixHistory(channelID, limit)
	if err != nil {
		return "", err
	}
	if !confirm {
		return "📜 **Matrix history import preview**\n\n" + plan.describe() + "\n\nRun `/matrix history from-matrix confirm` to start.", nil
	}
	if len(plan.events) == 0 {
		return "ℹ️ " + plan.describe(), nil
	}
	if !p.acquireHistoryLock(channelID) {
		return "", fmt.Errorf("a history import is already running for this channel")
	}
	go func() {
		defer p.releaseHistoryLock(channelID)
		imported, failed := 0, 0
		for i, ev := range plan.events {
			var err error
			switch ev.Type {
			case "m.reaction":
				err = p.matrixToMattermostBridge.syncMatrixReactionToMattermost(ev, channelID)
			default:
				err = p.matrixToMattermostBridge.syncMatrixMessageToMattermost(ev, channelID)
			}
			if err != nil {
				failed++
				p.logger.LogWarn("Could not import a Matrix event", "error", err, "event_id", ev.EventID, "room_id", roomID)
			} else {
				imported++
			}
			if (i+1)%25 == 0 {
				p.refreshHistoryLock(channelID)
			}
		}
		p.tellUser(userID, channelID, fmt.Sprintf("✅ **Matrix history imported**: %d events imported, %d failed. Run the command again to continue with older history.", imported, failed))
	}()
	return fmt.Sprintf("🔄 **History import started**: %d messages and %d reactions, oldest first. It runs in the background; you will be told when it finishes.", plan.messages, plan.reactions), nil
}

// tellUser sends a private message into a channel.
func (p *Plugin) tellUser(userID, channelID, text string) {
	post := &model.Post{UserId: userID, ChannelId: channelID, Message: text}
	p.API.SendEphemeralPost(userID, post)
}
