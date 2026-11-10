package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

func TestInviteMatrixUser(t *testing.T) {
	hs := newFakeSynapse(t)
	hs.members = []string{"@_mattermost_bridge:example.com", "@present:other.org"}
	p, _ := newMirrorTestBridge(t, hs)
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("ch1"), []byte("!room:example.com")))

	out, err := p.InviteMatrixUser("ch1", "@guest:other.org")
	require.NoError(t, err)
	assert.Contains(t, out, "Invited @guest:other.org")
	assert.Equal(t, []string{"@guest:other.org to !room:example.com"}, hs.invites)

	out, err = p.InviteMatrixUser("ch1", "@present:other.org")
	require.NoError(t, err)
	assert.Contains(t, out, "already in the room")
	assert.Len(t, hs.invites, 1, "no second invitation")

	_, err = p.InviteMatrixUser("ch1", "not-an-id")
	assert.ErrorContains(t, err, "not a Matrix user ID")

	_, err = p.InviteMatrixUser("ch1", "@_mattermost_ada:example.com")
	assert.ErrorContains(t, err, "bridge's own accounts")

	_, err = p.InviteMatrixUser("unmapped", "@guest:other.org")
	assert.ErrorContains(t, err, "not linked")
}
