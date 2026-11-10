package main

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// Matrix clients show "this room is bridged to ..." from a bridge-information
// state event (MSC2346; Element reads the unstable uk.half-shot.bridge type).
// The bridge sets it when a channel is mapped and clears it when the mapping
// goes away, so people in Matrix can tell where a room leads.

const (
	bridgeInfoRoomKeyPrefix = "bridge_info_room_" // + channel ID -> room ID the event was set in
	bridgeInfoStablePrefix  = "m.bridge"
	bridgeInfoUnstable      = "uk.half-shot.bridge"
)

func (p *Plugin) bridgeInfoStateKey(channelID string) string {
	site := ""
	if cfg := p.API.GetConfig(); cfg != nil && cfg.ServiceSettings.SiteURL != nil {
		site = strings.TrimRight(*cfg.ServiceSettings.SiteURL, "/")
	}
	site = strings.TrimPrefix(strings.TrimPrefix(site, "https://"), "http://")
	return fmt.Sprintf("com.mattermost://%s/%s", site, channelID)
}

func (p *Plugin) bridgeInfoContent(channelID string) map[string]any {
	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil || channel == nil {
		return nil
	}
	site := ""
	if cfg := p.API.GetConfig(); cfg != nil && cfg.ServiceSettings.SiteURL != nil {
		site = strings.TrimRight(*cfg.ServiceSettings.SiteURL, "/")
	}
	bot := p.BridgeBotUserID()
	content := map[string]any{
		"bridgebot": bot,
		"creator":   bot,
		"protocol":  map[string]any{"id": "mattermost", "displayname": "Mattermost", "external_url": site},
		"channel":   map[string]any{"id": channelID, "displayname": channel.DisplayName},
	}
	if channel.TeamId != "" {
		if team, appErr := p.API.GetTeam(channel.TeamId); appErr == nil && team != nil {
			content["network"] = map[string]any{"id": team.Id, "displayname": team.DisplayName}
			if site != "" {
				content["channel"].(map[string]any)["external_url"] = site + "/" + team.Name + "/channels/" + channel.Name
			}
		}
	}
	return content
}

// syncBridgeInfoSafely runs syncBridgeInfo as a background task. The
// information is cosmetic, so a failure in it must never take the plugin down.
func (p *Plugin) syncBridgeInfoSafely(channelID string) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.LogWarn("Bridge information update failed", "channel_id", channelID, "panic", fmt.Sprint(r))
		}
	}()
	p.syncBridgeInfo(channelID)
}

// syncBridgeInfo makes the room's bridge-information event match the
// channel's mapping: set when mapped, cleared when not.
func (p *Plugin) syncBridgeInfo(channelID string) {
	if p.matrixClient == nil || p.mattermostToMatrixBridge == nil || p.kvstore == nil || !p.getConfiguration().EnableSync {
		return
	}
	if ch, appErr := p.API.GetChannel(channelID); appErr == nil && ch != nil && (ch.Type == model.ChannelTypeDirect || ch.Type == model.ChannelTypeGroup) {
		return
	}
	previous := ""
	if data, err := p.kvstore.Get(bridgeInfoRoomKeyPrefix + channelID); err == nil {
		previous = string(data)
	}
	key := p.bridgeInfoStateKey(channelID)
	setEverywhere := func(roomID string, content map[string]any) error {
		var firstErr error
		for _, eventType := range []string{bridgeInfoStablePrefix, bridgeInfoUnstable} {
			if err := p.setBridgeState(roomID, eventType, key, content); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}

	roomID := p.roomIDForChannel(channelID)

	if previous != "" && previous != roomID {
		if err := setEverywhere(previous, map[string]any{}); err != nil {
			p.logger.LogDebug("Could not clear the bridge information in the old room", "error", err, "room_id", previous)
		}
		_ = p.kvstore.Delete(bridgeInfoRoomKeyPrefix + channelID)
	}
	if roomID == "" {
		return
	}
	content := p.bridgeInfoContent(channelID)
	if content == nil {
		return
	}
	if err := setEverywhere(roomID, content); err != nil {
		// The bot may lack the power level in a room mapped with /matrix map;
		// the doctor says so.
		p.logger.LogDebug("Could not set the bridge information in the room", "error", err, "room_id", roomID)
		return
	}
	_ = p.kvstore.Set(bridgeInfoRoomKeyPrefix+channelID, []byte(roomID))
	if previous != roomID {
		p.announceBridging(channelID)
	}
}

// announceBridging tells a channel's members that it is now connected to a
// Matrix room, because they are otherwise not asked and what they write here
// becomes readable there.
func (p *Plugin) announceBridging(channelID string) {
	botID, err := p.API.EnsureBotUser(&model.Bot{Username: "matrixbridge", DisplayName: "Matrix bridge", Description: "Notices from the Matrix bridge"})
	if err != nil {
		p.logger.LogDebug("Could not set up the bridge notice account", "error", err)
		return
	}
	if _, appErr := p.API.CreatePost(&model.Post{
		UserId:    botID,
		ChannelId: channelID,
		Message:   "🔗 This channel is now bridged to a Matrix room. Messages, edits, reactions and files posted here are copied there and can be read by everyone in that room, including people on other servers. Use `/matrix status` to see who is in it.",
	}); appErr != nil {
		p.logger.LogDebug("Could not post the bridging notice", "error", appErr, "channel_id", channelID)
	}
}

func (p *Plugin) setBridgeState(roomID, eventType, key string, content map[string]any) error {
	_, err := p.matrixClient.SetRoomState(roomID, eventType, key, content, p.BridgeBotUserID())
	return err
}

// roomIDForChannel returns the Matrix room ID a channel is mapped to, or ""
// when it is not mapped or the homeserver cannot say.
func (p *Plugin) roomIDForChannel(channelID string) string {
	if p.matrixClient == nil || p.mattermostToMatrixBridge == nil {
		return ""
	}
	identifier, err := p.mattermostToMatrixBridge.GetMatrixRoomID(channelID)
	if err != nil || identifier == "" {
		return ""
	}
	roomID, err := p.matrixClient.ResolveRoomAlias(identifier)
	if err != nil {
		return ""
	}
	return roomID
}
