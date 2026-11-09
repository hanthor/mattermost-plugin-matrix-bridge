package e2ee

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maunium.net/go/mautrix/id"
)

func TestParseExtrasReadsBothSpellings(t *testing.T) {
	stable := []byte(`{"events":[],"to_device":[{"type":"m.room.encrypted","sender":"@a:hs","content":{"algorithm":"m.olm.v1.curve25519-aes-sha2"}}],
		"device_lists":{"changed":["@a:hs"]},
		"device_one_time_keys_count":{"@_bot:hs":{"DEV":{"signed_curve25519":7}}}}`)
	x, err := ParseExtras(stable)
	require.NoError(t, err)
	assert.False(t, x.Empty())
	assert.Len(t, x.ToDevice, 1)
	assert.Equal(t, []id.UserID{"@a:hs"}, x.deviceLists().Changed)
	assert.Equal(t, 7, x.otkCounts()["@_bot:hs"]["DEV"].SignedCurve25519)

	unstable := []byte(`{"events":[],"de.sorunome.msc2409.to_device":[{"type":"m.room_key_request","sender":"@a:hs","content":{}}],
		"org.matrix.msc3202.device_lists":{"changed":["@b:hs"]},
		"org.matrix.msc3202.device_one_time_keys_count":{"@_bot:hs":{"DEV":{"signed_curve25519":3}}}}`)
	x, err = ParseExtras(unstable)
	require.NoError(t, err)
	assert.False(t, x.Empty())
	assert.Len(t, x.MSC2409ToDevice, 1)
	assert.Equal(t, []id.UserID{"@b:hs"}, x.deviceLists().Changed)
	assert.Equal(t, 3, x.otkCounts()["@_bot:hs"]["DEV"].SignedCurve25519)
}

func TestTransactionsWithoutEncryptionDataAreEmpty(t *testing.T) {
	x, err := ParseExtras([]byte(`{"events":[{"type":"m.room.message"}]}`))
	require.NoError(t, err)
	assert.True(t, x.Empty())
}
