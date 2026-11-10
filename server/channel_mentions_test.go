package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChannelMentionsAreDetected(t *testing.T) {
	for _, text := range []string{"@channel heads up", "hey @all", "ping @here please", "(@channel)"} {
		assert.True(t, hasChannelMention(text), text)
	}
	for _, text := range []string{"mail me@channel.example", "@channels are nice", "no mention", "x@all"} {
		assert.False(t, hasChannelMention(text), text)
	}
}

func TestARoomMentionBecomesAChannelMention(t *testing.T) {
	room := map[string]any{"m.mentions": map[string]any{"room": true}}
	assert.Equal(t, "@channel everyone look", roomMentionToChannel(room, "@room everyone look"))
	assert.Equal(t, "@channel plain text", roomMentionToChannel(room, "plain text"), "marked in m.mentions only")
	assert.Equal(t, "@channel already", roomMentionToChannel(room, "@channel already"))
	assert.Equal(t, "hey @channel", roomMentionToChannel(map[string]any{}, "hey @room"), "the word alone is enough")
	assert.Equal(t, "no mention", roomMentionToChannel(map[string]any{}, "no mention"))
	assert.Equal(t, "user@room.example", roomMentionToChannel(map[string]any{}, "user@room.example"))
}
