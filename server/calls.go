package main

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// Calls. Mattermost Calls and MatrixRTC each run their own media servers and
// signalling, so the bridge does not relay audio or video. It bridges what is
// around a call: that one started, who is in it, how to join, and that it
// ended. Optionally (calls_mode element_call) one call can be joined from both
// sides through a URL both networks can open. The design is in
// docs/CALLS_AND_MIRROR_MODE.md.

const (
	callPostType       = "custom_calls"       // posted by the Mattermost Calls plugin
	matrixCallPostType = "custom_matrix_call" // rendered by this plugin's webapp

	callStateEventType   = "com.mattermost.bridge.call"
	widgetStateEventType = "im.vector.modular.widgets"

	// Call membership state events: MSC3401 as shipped by Element Call, and
	// its standardisation as MSC4143.
	callMemberEventLegacy = "org.matrix.msc3401.call.member"
	callMemberEventRTC    = "m.rtc.member"

	mattermostCallKeyPrefix = "call_mm_" // + post ID -> mattermostCallRecord
	matrixCallKeyPrefix     = "call_mx_" // + room ID -> matrixCallRecord

	callsModeOff         = "off"
	callsModePresence    = "presence"
	callsModeElementCall = "element_call"

	// A call nobody ever joined is dropped after this long; a call whose
	// membership never changes is considered abandoned after callStaleAfter
	// (clients that crash leave their membership event behind).
	callUnjoinedTimeout = 15 * time.Minute
	callStaleAfter      = 12 * time.Hour
)

// callsMode returns the configured calls mode; presence is the default.
func (c *configuration) callsMode() string {
	switch c.CallsMode {
	case callsModeOff, callsModeElementCall:
		return c.CallsMode
	default:
		return callsModePresence
	}
}

var (
	callPostLocksMu sync.Mutex
	callPostLocks   = map[string]*callPostLock{}
)

type callPostLock struct {
	mu   sync.Mutex
	refs int
}

// lockCallPost serializes work on one call post and returns the unlock.
func lockCallPost(postID string) func() {
	callPostLocksMu.Lock()
	l := callPostLocks[postID]
	if l == nil {
		l = &callPostLock{}
		callPostLocks[postID] = l
	}
	l.refs++
	callPostLocksMu.Unlock()

	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		callPostLocksMu.Lock()
		if l.refs--; l.refs == 0 {
			delete(callPostLocks, postID)
		}
		callPostLocksMu.Unlock()
	}
}

func propInt64(props model.StringInterface, key string) int64 {
	switch v := props[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}

func propStrings(props model.StringInterface, key string) []string {
	var out []string
	switch v := props[key].(type) {
	case []string:
		out = append(out, v...)
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func loadJSON(kv kvstore.KVStore, key string, out any) bool {
	data, err := kv.Get(key)
	if err != nil || len(data) == 0 {
		return false
	}
	return json.Unmarshal(data, out) == nil
}

func storeJSON(kv kvstore.KVStore, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return errors.Wrap(err, "marshal call record")
	}
	return kv.Set(key, data)
}

func formatCallDuration(startMs, endMs int64) string {
	if startMs <= 0 || endMs < startMs {
		return ""
	}
	minutes := int((endMs - startMs) / 60000)
	if minutes < 1 {
		minutes = 1
	}
	if minutes < 60 {
		return fmt.Sprintf("%d min", minutes)
	}
	if minutes%60 == 0 {
		return fmt.Sprintf("%d h", minutes/60)
	}
	return fmt.Sprintf("%d h %d min", minutes/60, minutes%60)
}

// ---------------------------------------------------------------------------
// Mattermost → Matrix

// mattermostCallRecord remembers what was announced in Matrix for a call
// started with the Mattermost Calls plugin.
type mattermostCallRecord struct {
	RoomID        string `json:"room_id"`
	NoticeEventID string `json:"notice_event_id"`
	StartAt       int64  `json:"start_at"`
	// Participants is a fingerprint of the participant list last published.
	Participants string `json:"participants"`
	Ended        bool   `json:"ended"`
}

// syncCallPost mirrors a Mattermost Calls post into the room: an m.notice with
// a join link when the call starts (edited when it ends) plus a room state
// event describing it, so clients and widgets can render the call.
func (b *MattermostToMatrixBridge) syncCallPost(post *model.Post, roomID string) error {
	if b.configGetter.getConfiguration().callsMode() == callsModeOff {
		return nil
	}
	user, appErr := b.API.GetUser(post.UserId)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to get the user who started the call")
	}
	if user.IsRemote() || user.IsBot {
		return nil
	}

	startAt := propInt64(post.Props, "start_at")
	endAt := propInt64(post.Props, "end_at")
	participants := propStrings(post.Props, "participants")
	sort.Strings(participants)
	fingerprint := strings.Join(participants, ",")

	// The start and the end of a call can arrive within milliseconds on
	// different workers; handle one post at a time so the end sees the record
	// the start stores.
	unlock := lockCallPost(post.Id)
	defer unlock()

	var rec mattermostCallRecord
	known := loadJSON(b.kvstore, mattermostCallKeyPrefix+post.Id, &rec)

	switch {
	case !known && endAt > 0:
		return nil // ended before the bridge saw it start
	case !known:
		return b.announceMattermostCall(post, user, roomID, startAt, participants, fingerprint)
	case endAt > 0 && !rec.Ended:
		return b.announceMattermostCallEnd(post, user, &rec, startAt, endAt, len(participants))
	case !rec.Ended && fingerprint != rec.Participants:
		rec.Participants = fingerprint
		b.publishCallState(rec.RoomID, post, user, startAt, participants)
		return storeJSON(b.kvstore, mattermostCallKeyPrefix+post.Id, &rec)
	}
	return nil
}

func (b *MattermostToMatrixBridge) displayNames(userIDs []string) []string {
	var names []string
	for _, id := range userIDs {
		if len(names) >= 50 {
			break
		}
		if u, appErr := b.API.GetUser(id); appErr == nil && u != nil {
			name := u.GetDisplayName(model.ShowFullName)
			if name == "" {
				name = u.Username
			}
			names = append(names, name)
		}
	}
	return names
}

func (b *MattermostToMatrixBridge) siteURL() string {
	if cfg := b.API.GetConfig(); cfg != nil && cfg.ServiceSettings.SiteURL != nil {
		return strings.TrimRight(*cfg.ServiceSettings.SiteURL, "/")
	}
	return ""
}

// mattermostCallURL is the address that joins the call of a channel in
// Mattermost, or "" when it cannot be built (no site URL, direct messages).
func (b *MattermostToMatrixBridge) mattermostCallURL(channelID string) string {
	site := b.siteURL()
	if site == "" {
		return ""
	}
	channel, appErr := b.API.GetChannel(channelID)
	if appErr != nil || channel == nil || channel.TeamId == "" {
		return ""
	}
	team, appErr := b.API.GetTeam(channel.TeamId)
	if appErr != nil || team == nil {
		return ""
	}
	return fmt.Sprintf("%s/%s/channels/%s?join_call=true", site, url.PathEscape(team.Name), url.PathEscape(channel.Name))
}

func (b *MattermostToMatrixBridge) publishCallState(roomID string, post *model.Post, user *model.User, startAt int64, participantIDs []string) {
	state := map[string]any{
		"post_id":      post.Id,
		"started_by":   user.Username,
		"start_at":     startAt,
		"participants": b.displayNames(participantIDs),
		"url":          b.mattermostCallURL(post.ChannelId),
	}
	// State events need a power level ghosts do not have; the bridge bot
	// created the room and acts for them.
	if _, err := b.matrixClient.SetRoomState(roomID, callStateEventType, post.Id, state, ""); err != nil {
		b.logger.LogWarn("Could not publish call state to the room", "error", err, "room_id", roomID, "post_id", post.Id)
	}
}

func (b *MattermostToMatrixBridge) announceMattermostCall(post *model.Post, user *model.User, roomID string, startAt int64, participants []string, fingerprint string) error {
	matrixUserID, sender, _, err := b.resolveSender(user.Id)
	if err != nil {
		return errors.Wrap(err, "failed to get the sending Matrix account")
	}
	if err := b.ensureSenderInRoom(sender, matrixUserID, roomID, user.Id); err != nil {
		return errors.Wrap(err, "failed to ensure the sender is in the room")
	}

	name := user.GetDisplayName(model.ShowFullName)
	if name == "" {
		name = user.Username
	}
	body := fmt.Sprintf("📞 %s started a call in Mattermost.", name)
	htmlBody := fmt.Sprintf("📞 <strong>%s</strong> started a call in Mattermost.", html.EscapeString(name))
	if link := b.mattermostCallURL(post.ChannelId); link != "" {
		body += " Join: " + link
		htmlBody += fmt.Sprintf(` <a href="%s">Join the call</a>`, html.EscapeString(link))
	} else {
		body += " Open Mattermost to join."
	}

	resp, err := sender.SendNoticeAsUser(roomID, body, htmlBody, matrixUserID)
	if err != nil {
		return errors.Wrap(err, "failed to announce the call")
	}
	b.publishCallState(roomID, post, user, startAt, participants)

	return storeJSON(b.kvstore, mattermostCallKeyPrefix+post.Id, &mattermostCallRecord{
		RoomID:        roomID,
		NoticeEventID: resp.EventID,
		StartAt:       startAt,
		Participants:  fingerprint,
	})
}

func (b *MattermostToMatrixBridge) announceMattermostCallEnd(post *model.Post, user *model.User, rec *mattermostCallRecord, startAt, endAt int64, participantCount int) error {
	matrixUserID, sender, _, err := b.resolveSender(user.Id)
	if err != nil {
		return errors.Wrap(err, "failed to get the sending Matrix account")
	}
	text := "📞 Call ended"
	if d := formatCallDuration(startAt, endAt); d != "" {
		text += " · " + d
	}
	if participantCount > 0 {
		text += fmt.Sprintf(" · %d participant(s)", participantCount)
	}
	if _, err := sender.EditNoticeAsUser(rec.RoomID, rec.NoticeEventID, text, "", matrixUserID); err != nil {
		return errors.Wrap(err, "failed to update the call announcement")
	}
	// State cannot be deleted; empty content means no call is running.
	if _, err := b.matrixClient.SetRoomState(rec.RoomID, callStateEventType, post.Id, map[string]any{}, ""); err != nil {
		b.logger.LogWarn("Could not clear call state in the room", "error", err, "room_id", rec.RoomID)
	}
	rec.Ended = true
	return storeJSON(b.kvstore, mattermostCallKeyPrefix+post.Id, rec)
}

// ---------------------------------------------------------------------------
// Matrix → Mattermost

type callMember struct {
	User string `json:"user"`
	// Expires is a unix time in milliseconds after which the membership no
	// longer counts, or 0 for none.
	Expires int64 `json:"expires,omitempty"`
}

// matrixCallRecord is the state of a call in a Matrix room.
type matrixCallRecord struct {
	PostID    string                `json:"post_id"`
	ChannelID string                `json:"channel_id"`
	RoomID    string                `json:"room_id"`
	Title     string                `json:"title,omitempty"`
	JoinURL   string                `json:"join_url,omitempty"`
	StartedAt int64                 `json:"started_at"`
	LastEvent int64                 `json:"last_event"`
	Members   map[string]callMember `json:"members"`
	// Everyone lists every Matrix user who was in the call at any point, so
	// the card can say who took part after it ended.
	Everyone []string `json:"everyone,omitempty"`
	// SeenMembers is set once anyone has joined: a call the bridge started
	// is not over just because nobody has joined yet.
	SeenMembers bool `json:"seen_members,omitempty"`
	Ended       bool `json:"ended,omitempty"`
	// ByBridge is set for calls started with /matrix call.
	ByBridge bool `json:"by_bridge,omitempty"`
}

func (r *matrixCallRecord) prune(nowMs int64) {
	for key, m := range r.Members {
		if m.Expires > 0 && m.Expires < nowMs {
			delete(r.Members, key)
		}
	}
}

// roomFragment escapes a room ID for a URL fragment the way Element writes
// it: "!" and ":" stay readable.
func roomFragment(roomID string) string {
	return strings.ReplaceAll(url.PathEscape(roomID), "%21", "!")
}

func (p *Plugin) elementRoomURL(roomID string) string {
	if base := strings.TrimRight(p.getConfiguration().ElementURL, "/"); base != "" {
		return base + "/#/room/" + roomFragment(roomID)
	}
	return "https://matrix.to/#/" + roomFragment(roomID)
}

// handleMatrixCallEvent follows call membership state in a bridged room and
// keeps one card post in the channel in step with it.
func (p *Plugin) handleMatrixCallEvent(event MatrixEvent, channelID string) error {
	cfg := p.getConfiguration()
	if cfg.callsMode() == callsModeOff || event.StateKey == nil || p.matrixToMattermostBridge == nil {
		return nil
	}

	var rec matrixCallRecord
	known := loadJSON(p.kvstore, matrixCallKeyPrefix+event.RoomID, &rec)
	if !known || rec.Ended {
		rec = matrixCallRecord{ChannelID: channelID, RoomID: event.RoomID, Members: map[string]callMember{}}
	}
	if rec.Members == nil {
		rec.Members = map[string]callMember{}
	}

	if len(event.Content) > 0 {
		member := callMember{User: event.Sender}
		if expires, ok := event.Content["expires"].(float64); ok && expires > 0 {
			member.Expires = event.Timestamp + int64(expires)
		}
		rec.Members[*event.StateKey] = member
		rec.SeenMembers = true
	} else {
		delete(rec.Members, *event.StateKey)
	}
	rec.LastEvent = event.Timestamp
	rec.prune(time.Now().UnixMilli())

	return p.updateMatrixCall(&rec, event.Sender, event.Timestamp)
}

// handleLegacyCallEvent follows the one-to-one call events most clients send
// in direct messages (m.call.invite, answer, hangup, reject) and keeps the same
// card post in step. An invite starts the call, an answer joins the callee, and
// a hangup or rejection ends it for both.
func (p *Plugin) handleLegacyCallEvent(event MatrixEvent, channelID string) error {
	cfg := p.getConfiguration()
	if cfg.callsMode() == callsModeOff || p.matrixToMattermostBridge == nil {
		return nil
	}
	var rec matrixCallRecord
	known := loadJSON(p.kvstore, matrixCallKeyPrefix+event.RoomID, &rec)
	if !known || rec.Ended {
		rec = matrixCallRecord{ChannelID: channelID, RoomID: event.RoomID, Members: map[string]callMember{}}
	}
	if rec.Members == nil {
		rec.Members = map[string]callMember{}
	}
	switch event.Type {
	case "m.call.invite", "m.call.answer":
		member := callMember{User: event.Sender}
		if lifetime, ok := event.Content["lifetime"].(float64); ok && lifetime > 0 {
			member.Expires = event.Timestamp + int64(lifetime)
		}
		rec.Members[event.Sender] = member
		rec.SeenMembers = true
	case "m.call.hangup", "m.call.reject":
		rec.Members = map[string]callMember{}
	default:
		return nil
	}
	rec.LastEvent = event.Timestamp
	rec.prune(time.Now().UnixMilli())
	return p.updateMatrixCall(&rec, event.Sender, event.Timestamp)
}

// updateMatrixCall starts, updates or ends the card for the record.
func (p *Plugin) updateMatrixCall(rec *matrixCallRecord, starter string, nowMs int64) error {
	switch {
	case rec.PostID == "" && len(rec.Members) > 0:
		return p.startMatrixCallCard(rec, starter, nowMs)
	case rec.PostID != "" && !rec.Ended && len(rec.Members) == 0 && rec.SeenMembers:
		return p.endMatrixCallCard(rec, nowMs)
	case rec.PostID != "" && !rec.Ended:
		return p.refreshMatrixCallCard(rec)
	}
	return storeJSON(p.kvstore, matrixCallKeyPrefix+rec.RoomID, rec)
}

func (r *matrixCallRecord) remember(matrixUserID string) {
	for _, u := range r.Everyone {
		if u == matrixUserID {
			return
		}
	}
	r.Everyone = append(r.Everyone, matrixUserID)
}

// matrixCallParticipantNames returns the display names of the people in the
// call, or of everyone who took part when everyone is true.
func (p *Plugin) matrixCallParticipantNames(rec *matrixCallRecord, everyone bool) []string {
	var users []string
	if everyone {
		users = append(users, rec.Everyone...)
	} else {
		for _, m := range rec.Members {
			users = append(users, m.User)
		}
	}
	seen := map[string]bool{}
	var names []string
	for _, user := range users {
		if seen[user] {
			continue
		}
		seen[user] = true
		name := strings.TrimPrefix(strings.SplitN(user, ":", 2)[0], "@")
		if mmID := p.lookupMattermostUserForMatrixUser(user); mmID != "" {
			if u, appErr := p.API.GetUser(mmID); appErr == nil && u != nil {
				if dn := u.GetDisplayName(model.ShowFullName); dn != "" {
					name = dn
				}
			}
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (p *Plugin) callCardProps(rec *matrixCallRecord, endAt int64) model.StringInterface {
	for _, m := range rec.Members {
		rec.remember(m.User)
	}
	title := rec.Title
	if title == "" {
		title = "Call in Matrix"
	}
	join := rec.JoinURL
	if join == "" {
		join = p.elementRoomURL(rec.RoomID)
	}
	return model.StringInterface{
		// from_matrix keeps the post out of the Mattermost → Matrix relay.
		"from_matrix":    true,
		"matrix_room_id": rec.RoomID,
		"title":          title,
		"start_at":       rec.StartedAt,
		"end_at":         endAt,
		"participants":   p.matrixCallParticipantNames(rec, endAt > 0),
		"join_url":       join,
	}
}

func (p *Plugin) startMatrixCallCard(rec *matrixCallRecord, starter string, nowMs int64) error {
	mmUserID, err := p.matrixToMattermostBridge.getOrCreateMattermostUser(starter, rec.ChannelID)
	if err != nil {
		return errors.Wrap(err, "failed to get the Mattermost user for the call starter")
	}
	rec.StartedAt = nowMs
	props := p.callCardProps(rec, 0)
	name := starter
	if u, appErr := p.API.GetUser(mmUserID); appErr == nil && u != nil {
		if dn := u.GetDisplayName(model.ShowFullName); dn != "" {
			name = dn
		}
	}
	created, appErr := p.API.CreatePost(&model.Post{
		UserId:    mmUserID,
		ChannelId: rec.ChannelID,
		Type:      matrixCallPostType,
		Message:   fmt.Sprintf("📞 %s started a call in Matrix", name),
		Props:     props,
	})
	if appErr != nil {
		return errors.Wrap(appErr, "failed to post the call card")
	}
	rec.PostID = created.Id
	return storeJSON(p.kvstore, matrixCallKeyPrefix+rec.RoomID, rec)
}

func (p *Plugin) refreshMatrixCallCard(rec *matrixCallRecord) error {
	post, appErr := p.API.GetPost(rec.PostID)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to load the call card")
	}
	props := p.callCardProps(rec, 0)
	if fmt.Sprint(post.Props["participants"]) != fmt.Sprint(props["participants"]) {
		post.Props = props
		if _, appErr := p.API.UpdatePost(post); appErr != nil {
			return errors.Wrap(appErr, "failed to update the call card")
		}
	}
	return storeJSON(p.kvstore, matrixCallKeyPrefix+rec.RoomID, rec)
}

func (p *Plugin) endMatrixCallCard(rec *matrixCallRecord, nowMs int64) error {
	post, appErr := p.API.GetPost(rec.PostID)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to load the call card")
	}
	post.Props = p.callCardProps(rec, nowMs) // lists everyone who took part
	text := "📞 Call ended"
	if d := formatCallDuration(rec.StartedAt, nowMs); d != "" {
		text += " · " + d
	}
	post.Message = text
	if _, appErr := p.API.UpdatePost(post); appErr != nil {
		return errors.Wrap(appErr, "failed to update the call card")
	}
	rec.Ended = true
	if rec.ByBridge && p.matrixClient != nil {
		// An empty widget removes it from the room.
		if _, err := p.matrixClient.SetRoomState(rec.RoomID, widgetStateEventType, callWidgetID(rec.ChannelID), map[string]any{}, ""); err != nil {
			p.logger.LogDebug("Could not remove the call widget", "error", err, "room_id", rec.RoomID)
		}
	}
	return storeJSON(p.kvstore, matrixCallKeyPrefix+rec.RoomID, rec)
}

// sweepCalls ends calls that can no longer end themselves: memberships that
// expired, calls nobody joined, and calls whose client vanished.
func (p *Plugin) sweepCalls() {
	if p.kvstore == nil || p.matrixToMattermostBridge == nil {
		return
	}
	now := time.Now().UnixMilli()
	for page := 0; ; page++ {
		keys, err := p.kvstore.ListKeysWithPrefix(page, 100, matrixCallKeyPrefix)
		if err != nil {
			return
		}
		for _, key := range keys {
			var rec matrixCallRecord
			if !loadJSON(p.kvstore, key, &rec) || rec.Ended || rec.PostID == "" {
				continue
			}
			rec.prune(now)
			abandoned := len(rec.Members) == 0 && (rec.SeenMembers || now-rec.StartedAt > callUnjoinedTimeout.Milliseconds())
			stale := rec.LastEvent > 0 && now-rec.LastEvent > callStaleAfter.Milliseconds() && !rec.ByBridge
			if abandoned || stale {
				rec.Members = map[string]callMember{}
				rec.SeenMembers = true
				if err := p.endMatrixCallCard(&rec, now); err != nil {
					p.logger.LogWarn("Failed to end an abandoned call", "error", err, "room_id", rec.RoomID)
				}
			}
		}
		if len(keys) < 100 {
			return
		}
	}
}

// ---------------------------------------------------------------------------
// One call both sides can join

func callWidgetID(channelID string) string {
	return "mattermost_call_" + channelID
}

// expandCallURL fills the placeholders of the Element Call URL template and
// checks that the result is an http(s) address.
func expandCallURL(template string, vars map[string]string) (string, error) {
	out := template
	for key, value := range vars {
		out = strings.ReplaceAll(out, "{"+key+"}", url.QueryEscape(value))
	}
	parsed, err := url.Parse(out)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("the Element Call URL must be an http(s) address")
	}
	return out, nil
}

// StartCall implements command.PluginAccessor: it starts a call in a bridged
// channel that people on both sides can join through the configured URL.
func (p *Plugin) StartCall(mattermostUserID, channelID, title string) (string, error) {
	cfg := p.getConfiguration()
	if cfg.callsMode() != callsModeElementCall || cfg.ElementCallURL == "" {
		return "", errors.New("calls that both sides can join are not configured: an administrator sets Calls mode to \"Presence and Element Call\" and an Element Call URL in the plugin settings")
	}
	if p.mattermostToMatrixBridge == nil || p.matrixClient == nil {
		return "", errors.New("the bridge is not connected to Matrix")
	}
	roomID, err := p.mattermostToMatrixBridge.GetMatrixRoomID(channelID)
	if err != nil || roomID == "" {
		return "", errors.New("this channel is not bridged to a Matrix room")
	}
	if strings.HasPrefix(roomID, "#") {
		if roomID, err = p.matrixClient.ResolveRoomAlias(roomID); err != nil {
			return "", errors.Wrap(err, "could not resolve the Matrix room")
		}
	}

	var existing matrixCallRecord
	if loadJSON(p.kvstore, matrixCallKeyPrefix+roomID, &existing) && !existing.Ended && existing.PostID != "" && existing.JoinURL != "" {
		return existing.JoinURL, nil // one call per room at a time
	}

	user, appErr := p.API.GetUser(mattermostUserID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "could not load your account")
	}
	name := user.GetDisplayName(model.ShowFullName)
	if name == "" {
		name = user.Username
	}
	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "could not load the channel")
	}
	if title == "" {
		title = channel.DisplayName
	}

	joinURL, err := expandCallURL(cfg.ElementCallURL, map[string]string{
		"room_id":      roomID,
		"room_name":    channel.DisplayName,
		"display_name": name,
		"user_id":      mattermostUserID,
	})
	if err != nil {
		return "", err
	}

	// A widget makes Element show the call in the room.
	widget := map[string]any{
		"type":          "m.custom",
		"url":           joinURL,
		"name":          "Call: " + title,
		"id":            callWidgetID(channelID),
		"creatorUserId": p.BridgeBotUserID(),
		"data":          map[string]any{"title": title},
	}
	if _, err := p.matrixClient.SetRoomState(roomID, widgetStateEventType, callWidgetID(channelID), widget, ""); err != nil {
		p.logger.LogWarn("Could not add the call widget to the room", "error", err, "room_id", roomID)
		p.askForWidgetPower(roomID, err)
	}

	// Announce it in Matrix as the person who started it.
	matrixUserID, sender, _, err := p.mattermostToMatrixBridge.resolveSender(mattermostUserID)
	if err != nil {
		return "", errors.Wrap(err, "failed to get the sending Matrix account")
	}
	if err := p.mattermostToMatrixBridge.ensureSenderInRoom(sender, matrixUserID, roomID, mattermostUserID); err != nil {
		return "", errors.Wrap(err, "failed to join the room")
	}
	body := fmt.Sprintf("📞 %s started a call: %s Join: %s", name, title, joinURL)
	htmlBody := fmt.Sprintf(`📞 <strong>%s</strong> started a call: %s <a href="%s">Join the call</a>`, html.EscapeString(name), html.EscapeString(title), html.EscapeString(joinURL))
	if _, err := sender.SendNoticeAsUser(roomID, body, htmlBody, matrixUserID); err != nil {
		return "", errors.Wrap(err, "failed to announce the call in Matrix")
	}

	// And in Mattermost as a card with a join button.
	rec := &matrixCallRecord{
		ChannelID: channelID,
		RoomID:    roomID,
		Title:     title,
		JoinURL:   joinURL,
		StartedAt: time.Now().UnixMilli(),
		LastEvent: time.Now().UnixMilli(),
		Members:   map[string]callMember{},
		ByBridge:  true,
	}
	created, appErr := p.API.CreatePost(&model.Post{
		UserId:    mattermostUserID,
		ChannelId: channelID,
		Type:      matrixCallPostType,
		Message:   fmt.Sprintf("📞 %s started a call", name),
		Props:     p.callCardProps(rec, 0),
	})
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to post the call card")
	}
	rec.PostID = created.Id
	if err := storeJSON(p.kvstore, matrixCallKeyPrefix+roomID, rec); err != nil {
		return "", err
	}
	return joinURL, nil
}

// askForWidgetPower tells a room, once, that the bridge bot cannot add the call
// widget because it may not set room state. The call still works: the
// announcement carries the join link, so it falls back to a link only.
func (p *Plugin) askForWidgetPower(roomID string, cause error) {
	if !strings.Contains(cause.Error(), "M_FORBIDDEN") && !strings.Contains(cause.Error(), "403") {
		return
	}
	key := "widget_power_asked_" + roomID
	if data, _ := p.kvstore.Get(key); len(data) > 0 {
		return
	}
	_ = p.kvstore.Set(key, []byte("1"))
	_ = p.matrixClient.SendNotice(roomID, "ℹ️ The bridge cannot show calls in this room's call panel because it is not allowed to change room settings. Calls started from Mattermost still work through the join link. To get the panel, give the bridge bot moderator rights (power level 50) in this room.")
}
