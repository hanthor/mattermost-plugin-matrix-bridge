package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestALocationBecomesAMapLink(t *testing.T) {
	text, ok := locationText(map[string]any{"msgtype": "m.location", "body": "Town hall", "geo_uri": "geo:51.5008,-0.1247;u=20"})
	assert.True(t, ok)
	assert.Equal(t, "📍 [Town hall](https://www.openstreetmap.org/?mlat=51.5008&mlon=-0.1247#map=16/51.5008/-0.1247)", text)

	text, ok = locationText(map[string]any{"msgtype": "m.location", "body": "Location geo:1,2", "geo_uri": "geo:1,2"})
	assert.True(t, ok)
	assert.Contains(t, text, "Shared a location")

	_, ok = locationText(map[string]any{"msgtype": "m.location", "geo_uri": "geo:nonsense"})
	assert.False(t, ok)
	_, ok = locationText(map[string]any{"msgtype": "m.text", "body": "hi"})
	assert.False(t, ok)
}

func TestAPollIsWrittenOutWithItsOptions(t *testing.T) {
	content := map[string]any{pollStartUnstable: map[string]any{
		"question": map[string]any{"org.matrix.msc1767.text": "Lunch?"},
		"answers": []any{
			map[string]any{"id": "a", "org.matrix.msc1767.text": "Pizza"},
			map[string]any{"id": "b", "org.matrix.msc1767.text": "Sushi"},
		},
	}}
	text, ok := pollText(content)
	assert.True(t, ok)
	assert.Contains(t, text, "📊 **Lunch?**")
	assert.Contains(t, text, "1. Pizza\n2. Sushi")

	_, ok = pollText(map[string]any{"body": "plain"})
	assert.False(t, ok)
	assert.True(t, isPollStart(pollStartStable))
}

func TestAStickerIsAnImage(t *testing.T) {
	ev := stickerAsImage(MatrixEvent{Type: "m.sticker", Content: map[string]any{"url": "mxc://x/y", "body": "wave"}})
	assert.Equal(t, "m.room.message", ev.Type)
	assert.Equal(t, "m.image", ev.Content["msgtype"])
	assert.Equal(t, "wave", ev.Content["body"])
}
