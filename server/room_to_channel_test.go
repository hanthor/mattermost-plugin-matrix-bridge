package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChannelNamesComeFromRoomNames(t *testing.T) {
	assert.Equal(t, "born-in-matrix", channelNameFor("Born in Matrix!", "!abc:hs"))
	assert.Equal(t, "matrix-abc", channelNameFor("", "!abc:hs"))
	assert.Equal(t, "matrix-abc", channelNameFor("???", "!abc:hs"))
	long := channelNameFor("a very long room name that goes on and on and on and on and on and on", "!x:hs")
	assert.LessOrEqual(t, len(long), 56)
	assert.GreaterOrEqual(t, len(channelNameFor("a", "!x:hs")), 2)
}
