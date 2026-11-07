package main

import (
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// Ephemeral events (EDUs) are delivered when the registration opts in with
// receive_ephemeral / de.sorunome.msc2409.push_ephemeral. They are not
// persisted on either side, so failures here are logged and otherwise ignored.

// processEphemeralEvent routes a single ephemeral event.
func (p *Plugin) processEphemeralEvent(event MatrixEvent) error {
	switch event.Type {
	case "m.typing":
		return p.handleMatrixTyping(event)
	case "m.presence":
		return p.handleMatrixPresence(event)
	default:
		// m.receipt has no Mattermost equivalent a plugin can set on behalf of
		// another user, so read receipts are intentionally not bridged.
		return nil
	}
}

// handleMatrixTyping republishes Matrix typing notifications as Mattermost
// typing events for mapped users in mapped channels.
func (p *Plugin) handleMatrixTyping(event MatrixEvent) error {
	channelID, err := p.getChannelIDFromMatrixRoom(event.RoomID)
	if err != nil || channelID == "" {
		return nil
	}

	rawUserIDs, ok := event.Content["user_ids"].([]any)
	if !ok {
		return nil
	}

	for _, raw := range rawUserIDs {
		matrixUserID, ok := raw.(string)
		if !ok || p.isBridgeAccount(matrixUserID) {
			continue
		}
		mattermostUserID := p.lookupMattermostUserForMatrixUser(matrixUserID)
		if mattermostUserID == "" {
			continue
		}
		if appErr := p.API.PublishUserTyping(mattermostUserID, channelID, ""); appErr != nil {
			p.logger.LogDebug("Failed to publish typing event", "error", appErr, "user_id", mattermostUserID, "channel_id", channelID)
		}
	}
	return nil
}

// handleMatrixPresence mirrors Matrix presence onto the Mattermost status of
// the corresponding bridged user.
func (p *Plugin) handleMatrixPresence(event MatrixEvent) error {
	if event.Sender == "" || p.isBridgeAccount(event.Sender) {
		return nil
	}
	mattermostUserID := p.lookupMattermostUserForMatrixUser(event.Sender)
	if mattermostUserID == "" {
		return nil
	}

	presence, _ := event.Content["presence"].(string)
	var status string
	switch presence {
	case "online":
		status = model.StatusOnline
	case "unavailable":
		status = model.StatusAway
	case "offline":
		status = model.StatusOffline
	default:
		return nil
	}

	if _, appErr := p.API.UpdateUserStatus(mattermostUserID, status); appErr != nil {
		return errors.Wrap(appErr, "failed to update user status")
	}
	return nil
}

// lookupMattermostUserForMatrixUser returns the Mattermost user bridged for a
// Matrix user, or "" when the Matrix user has never been seen.
func (p *Plugin) lookupMattermostUserForMatrixUser(matrixUserID string) string {
	data, err := p.kvstore.Get(kvstore.BuildMatrixUserKey(matrixUserID))
	if err != nil || len(data) == 0 {
		return ""
	}
	return string(data)
}
