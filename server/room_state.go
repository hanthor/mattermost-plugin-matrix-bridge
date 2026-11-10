package main

import (
	"github.com/pkg/errors"
)

// syncMatrixRoomNameToMattermost applies an m.room.name change to the mapped
// channel's display name.
func (p *Plugin) syncMatrixRoomNameToMattermost(event MatrixEvent, channelID string) error {
	name, _ := event.Content["name"].(string)
	if name == "" {
		return nil
	}

	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to get channel")
	}
	if channel.DisplayName == name || channel.IsGroupOrDirect() {
		return nil
	}

	channel.DisplayName = name
	updated, appErr := p.API.UpdateChannel(channel)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to update channel display name")
	}
	p.noteChannelStateFromMatrix(updated)
	p.logger.LogDebug("Synced Matrix room name to channel", "channel_id", channelID, "name", name)
	return nil
}

// syncMatrixRoomTopicToMattermost applies an m.room.topic change to the mapped
// channel's header.
func (p *Plugin) syncMatrixRoomTopicToMattermost(event MatrixEvent, channelID string) error {
	topic, _ := event.Content["topic"].(string)

	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to get channel")
	}
	if channel.Header == topic || channel.IsGroupOrDirect() {
		return nil
	}

	channel.Header = topic
	updated, appErr := p.API.UpdateChannel(channel)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to update channel header")
	}
	p.noteChannelStateFromMatrix(updated)
	p.logger.LogDebug("Synced Matrix room topic to channel header", "channel_id", channelID)
	return nil
}
