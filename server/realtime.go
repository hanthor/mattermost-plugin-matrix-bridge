package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/secretbox"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// Typing, presence, custom status and read receipts have no plugin hook, so
// they are read from the server's own event stream (typing) and polled
// (status and read state). It is off by default. One node in a cluster does the
// work: it holds a cluster lock for as long as it runs.
//
// Typing events only reach channel members, so a small bot account joins every
// bridged channel when this is switched on; its join shows in the channel once.

const (
	realtimeBotUsername = "matrix-bridge-events"
	realtimeTokenKey    = "realtime_token"
	realtimeLockKey     = "matrix-bridge-realtime"
	realtimePollEvery   = 30 * time.Second
	typingTimeoutMs     = 6000
)

type realtimeState struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	started bool
}

// realtimeMemory is what the pollers remember so they send only changes.
type realtimeMemory struct {
	mu        sync.Mutex
	presence  map[string]string // Mattermost user ID -> "presence|status_msg" last sent
	receipted map[string]string // channel ID + user ID -> event ID last marked read
}

func newRealtimeMemory() *realtimeMemory {
	return &realtimeMemory{presence: map[string]string{}, receipted: map[string]string{}}
}

func (p *Plugin) realtimeEnabled() bool {
	cfg := p.getConfiguration()
	return cfg.EnableRealtime && cfg.EnableSync
}

// startRealtime (re)starts the event client when the setting is on.
func (p *Plugin) startRealtime() {
	p.stopRealtime()
	if !p.realtimeEnabled() || p.kvstore == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.realtime.mu.Lock()
	p.realtime.cancel, p.realtime.started = cancel, true
	p.realtime.mu.Unlock()
	go p.realtimeLoop(ctx)
}

func (p *Plugin) stopRealtime() {
	p.realtime.mu.Lock()
	cancel := p.realtime.cancel
	p.realtime.cancel, p.realtime.started = nil, false
	p.realtime.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// realtimeLoop takes the cluster lock, runs, and starts again after a pause
// when the connection drops, until the context ends.
func (p *Plugin) realtimeLoop(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.LogError("The real-time event client stopped unexpectedly", "panic", fmt.Sprint(r))
		}
	}()
	mutex, err := cluster.NewMutex(p.API, realtimeLockKey)
	if err != nil {
		p.logger.LogError("Could not create the real-time lock", "error", err)
		return
	}
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		if err := mutex.LockWithContext(ctx); err != nil {
			return
		}
		ran := time.Now()
		err := p.runRealtime(ctx)
		mutex.Unlock()
		if ctx.Err() != nil {
			return
		}
		p.logger.LogWarn("The real-time event client disconnected; trying again", "error", fmt.Sprint(err))
		if time.Since(ran) > time.Minute {
			backoff = 2 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

// realtimeBot makes sure the bot account and its token exist.
func (p *Plugin) realtimeBot() (botID, token string, err error) {
	botID, err = p.API.EnsureBotUser(&model.Bot{
		Username: realtimeBotUsername, DisplayName: "Matrix bridge events",
		Description: "Reads typing events for the Matrix bridge",
	})
	if err != nil {
		return "", "", err
	}
	box := secretbox.New(secretFor(p.API, p.getConfiguration()), "realtime-token")
	if data, kerr := p.kvstore.Get(realtimeTokenKey); kerr == nil && len(data) > 0 {
		if plain, _, oerr := box.Open(string(data)); oerr == nil && plain != "" {
			return botID, plain, nil
		}
	}
	created, appErr := p.API.CreateUserAccessToken(&model.UserAccessToken{UserId: botID, Description: "Matrix bridge event stream"})
	if appErr != nil {
		return "", "", appErr
	}
	sealed, serr := box.Seal(created.Token)
	if serr != nil {
		return "", "", serr
	}
	_ = p.kvstore.Set(realtimeTokenKey, []byte(sealed))
	return botID, created.Token, nil
}

func (p *Plugin) websocketURL() string {
	port := "8065"
	if cfg := p.API.GetConfig(); cfg != nil && cfg.ServiceSettings.ListenAddress != nil {
		addr := *cfg.ServiceSettings.ListenAddress
		if i := strings.LastIndex(addr, ":"); i >= 0 && i+1 < len(addr) {
			port = addr[i+1:]
		}
	}
	return "ws://localhost:" + port
}

// runRealtime connects and handles events until the connection ends.
func (p *Plugin) runRealtime(ctx context.Context) error {
	botID, token, err := p.realtimeBot()
	if err != nil {
		return fmt.Errorf("could not set up the event bot: %w", err)
	}
	p.ensureBotInMappedChannels(botID)

	ws, err := model.NewWebSocketClient4(p.websocketURL(), token)
	if err != nil {
		// A revoked token is made again next time.
		_ = p.kvstore.Delete(realtimeTokenKey)
		return fmt.Errorf("could not connect: %w", err)
	}
	ws.Listen()
	defer ws.Close()
	p.logger.LogInfo("The real-time event client is connected")

	memory := newRealtimeMemory()
	ticker := time.NewTicker(p.realtimePollInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-ws.EventChannel:
			if !ok {
				return fmt.Errorf("the event stream closed: %v", ws.ListenError)
			}
			p.handleRealtimeEvent(ev, botID)
		case <-ws.PingTimeoutChannel:
			return fmt.Errorf("ping timeout")
		case <-ticker.C:
			if ws.ListenError != nil {
				return fmt.Errorf("the event stream failed: %v", ws.ListenError)
			}
			p.pollPresence(memory)
			p.pollReadReceipts(memory)
			p.ensureBotInMappedChannels(botID)
			p.syncSomeChannelRoles()
		}
	}
}

// handleRealtimeEvent relays typing indicators.
func (p *Plugin) handleRealtimeEvent(ev *model.WebSocketEvent, botID string) {
	if ev == nil {
		return
	}
	if ev.EventType() != model.WebsocketEventTyping {
		return
	}
	userID, _ := ev.GetData()["user_id"].(string)
	if userID == "" || userID == botID {
		return
	}
	p.relayTyping(ev.GetBroadcast().ChannelId, userID)
}

// relayTyping shows that a person is typing in the channel's room.
func (p *Plugin) relayTyping(channelID, userID string) {
	if p.matrixClient == nil || p.mattermostToMatrixBridge == nil || channelID == "" {
		return
	}
	roomID := p.roomIDForChannel(channelID)
	if roomID == "" {
		return
	}
	user, appErr := p.API.GetUser(userID)
	if appErr != nil || user == nil || user.IsRemote() || user.IsBot {
		return
	}
	matrixUserID, client, _, err := p.mattermostToMatrixBridge.resolveSender(userID)
	if err != nil || matrixUserID == "" {
		return
	}
	if err := client.SetTyping(roomID, matrixUserID, true, typingTimeoutMs); err != nil {
		p.logger.LogDebug("Could not show typing in the room", "error", err, "room_id", roomID)
	}
}

// ensureBotInMappedChannels adds the event bot to every bridged channel it is
// not in yet.
func (p *Plugin) ensureBotInMappedChannels(botID string) {
	for page := 0; ; page++ {
		keys, err := p.kvstore.ListKeysWithPrefix(page, 200, kvstore.KeyPrefixChannelMapping)
		if err != nil {
			return
		}
		for _, k := range keys {
			p.ensureBotInChannel(botID, strings.TrimPrefix(k, kvstore.KeyPrefixChannelMapping))
		}
		if len(keys) < 200 {
			return
		}
	}
}

func (p *Plugin) ensureBotInChannel(botID, channelID string) {
	if _, appErr := p.API.GetChannelMember(channelID, botID); appErr == nil {
		return
	}
	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil || channel == nil || channel.DeleteAt != 0 || channel.IsGroupOrDirect() {
		return
	}
	// A bot can only join a channel of a team it belongs to.
	if channel.TeamId != "" {
		if _, appErr := p.API.GetTeamMember(channel.TeamId, botID); appErr != nil {
			if _, appErr := p.API.CreateTeamMember(channel.TeamId, botID); appErr != nil {
				p.logger.LogWarn("The event bot could not join a team", "error", appErr, "team_id", channel.TeamId)
				return
			}
		}
	}
	if _, appErr := p.API.AddChannelMember(channelID, botID); appErr != nil {
		p.logger.LogWarn("The event bot could not join a channel", "error", appErr, "channel_id", channelID)
		return
	}
	p.logger.LogInfo("The event bot joined a bridged channel", "channel_id", channelID)
}

// presenceFor maps a Mattermost status to Matrix presence.
func presenceFor(status string) string {
	switch status {
	case model.StatusOnline:
		return "online"
	case model.StatusAway, model.StatusDnd:
		return "unavailable"
	default:
		return "offline"
	}
}

// customStatusText reads a person's custom status ("🌴 On holiday") from their
// user properties; an expired one is empty.
func customStatusText(user *model.User) string {
	raw := user.Props[model.UserPropsKeyCustomStatus]
	if raw == "" {
		return ""
	}
	var cs struct {
		Emoji     string `json:"emoji"`
		Text      string `json:"text"`
		ExpiresAt string `json:"expires_at"`
	}
	if json.Unmarshal([]byte(raw), &cs) != nil {
		return ""
	}
	if cs.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, cs.ExpiresAt); err == nil && time.Now().After(t) && !t.IsZero() && t.Year() > 1 {
			return ""
		}
	}
	text := strings.TrimSpace(cs.Text)
	if text == "" && cs.Emoji == "" {
		return ""
	}
	if cs.Emoji != "" {
		if e := emojiForName(cs.Emoji); e != "" {
			text = strings.TrimSpace(e + " " + text)
		}
	}
	return text
}

// emojiForName turns a Mattermost emoji name into its Unicode character.
func emojiForName(name string) string {
	if index, ok := emojiNameToIndex[name]; ok {
		if hex, ok := emojiIndexToUnicode[index]; ok {
			return hexToUnicode(hex)
		}
	}
	return ""
}

// pollPresence sends status and custom status of the people the bridge acts
// for, when they changed.
func (p *Plugin) pollPresence(memory *realtimeMemory) {
	if p.matrixClient == nil || p.mattermostToMatrixBridge == nil {
		return
	}
	ids := p.bridgedMattermostUserIDs()
	for start := 0; start < len(ids); start += 100 {
		end := start + 100
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		statuses, appErr := p.API.GetUserStatusesByIds(batch)
		if appErr != nil {
			return
		}
		users, appErr := p.API.GetUsersByIds(batch)
		if appErr != nil {
			return
		}
		byID := map[string]*model.User{}
		for _, u := range users {
			byID[u.Id] = u
		}
		for _, st := range statuses {
			user := byID[st.UserId]
			if user == nil || user.IsRemote() || user.IsBot {
				continue
			}
			presence, msg := presenceFor(st.Status), customStatusText(user)
			value := presence + "|" + msg
			memory.mu.Lock()
			same := memory.presence[st.UserId] == value
			memory.presence[st.UserId] = value
			memory.mu.Unlock()
			if same {
				continue
			}
			matrixUserID, client, _, err := p.mattermostToMatrixBridge.resolveSender(st.UserId)
			if err != nil || matrixUserID == "" {
				continue
			}
			if err := client.SetPresence(matrixUserID, presence, msg); err != nil {
				p.logger.LogDebug("Could not set a person's presence", "error", err, "user_id", st.UserId)
			}
		}
	}
}

// pollReadReceipts marks the latest bridged message of a recently active
// channel as read for people who viewed the channel after it was posted.
func (p *Plugin) pollReadReceipts(memory *realtimeMemory) {
	if p.matrixClient == nil || p.mattermostToMatrixBridge == nil {
		return
	}
	key := p.historyEventKey()
	cutoff := time.Now().Add(-30 * time.Minute).UnixMilli()
	for page := 0; ; page++ {
		keys, err := p.kvstore.ListKeysWithPrefix(page, 200, kvstore.KeyPrefixChannelMapping)
		if err != nil {
			return
		}
		for _, k := range keys {
			channelID := strings.TrimPrefix(k, kvstore.KeyPrefixChannelMapping)
			channel, appErr := p.API.GetChannel(channelID)
			if appErr != nil || channel == nil || channel.DeleteAt != 0 || channel.LastPostAt < cutoff {
				continue
			}
			p.receiptsForChannel(channel, key, memory)
		}
		if len(keys) < 200 {
			return
		}
	}
}

func (p *Plugin) receiptsForChannel(channel *model.Channel, key string, memory *realtimeMemory) {
	roomID := p.roomIDForChannel(channel.Id)
	if roomID == "" {
		return
	}
	posts, appErr := p.API.GetPostsForChannel(channel.Id, 0, 20)
	if appErr != nil || posts == nil {
		return
	}
	var eventID string
	var postedAt int64
	for _, id := range posts.Order { // newest first
		post := posts.Posts[id]
		if ev, _ := post.Props[key].(string); ev != "" {
			eventID, postedAt = ev, post.CreateAt
			break
		}
	}
	if eventID == "" {
		return
	}
	members, appErr := p.API.GetChannelMembers(channel.Id, 0, 200)
	if appErr != nil {
		return
	}
	for _, m := range members {
		if m.LastViewedAt < postedAt {
			continue
		}
		mapKey := channel.Id + "|" + m.UserId
		memory.mu.Lock()
		done := memory.receipted[mapKey] == eventID
		memory.receipted[mapKey] = eventID
		memory.mu.Unlock()
		if done {
			continue
		}
		user, appErr := p.API.GetUser(m.UserId)
		if appErr != nil || user == nil || user.IsRemote() || user.IsBot {
			continue
		}
		matrixUserID, client, _, err := p.mattermostToMatrixBridge.resolveSender(m.UserId)
		if err != nil || matrixUserID == "" {
			continue
		}
		if err := client.SendReadReceipt(roomID, eventID, matrixUserID); err != nil {
			p.logger.LogDebug("Could not send a read receipt", "error", err, "room_id", roomID)
		}
	}
}

// bridgedMattermostUserIDs lists the Mattermost users the bridge acts for:
// those with a ghost and those with a mirror account.
func (p *Plugin) bridgedMattermostUserIDs() []string {
	seen := map[string]bool{}
	var ids []string
	for _, prefix := range []string{kvstore.KeyPrefixGhostUser, mirrorAccountKeyPrefix} {
		for page := 0; ; page++ {
			keys, err := p.kvstore.ListKeysWithPrefix(page, 200, prefix)
			if err != nil {
				return ids
			}
			for _, k := range keys {
				id := strings.TrimPrefix(k, prefix)
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
			if len(keys) < 200 {
				break
			}
		}
	}
	return ids
}

// realtimePollInterval is how often status and read state are checked.
func (p *Plugin) realtimePollInterval() time.Duration {
	if n := p.getConfiguration().RealtimePollSeconds; n >= 2 {
		return time.Duration(n) * time.Second
	}
	return realtimePollEvery
}
