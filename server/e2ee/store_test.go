package e2ee

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/id"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/secretbox"
)

type memKV struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newMemKV() *memKV { return &memKV{m: map[string][]byte{}} }
func (k *memKV) Get(key string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.m[key], nil
}
func (k *memKV) Set(key string, v []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(v) > 50000 {
		panic("value too large for the plugin KV store")
	}
	k.m[key] = append([]byte(nil), v...)
	return nil
}
func (k *memKV) Delete(key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.m, key)
	return nil
}

func TestStoreSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	kv := newMemKV()
	s, err := NewStore(kv, nil)
	require.NoError(t, err)

	acct := crypto.NewOlmAccount()
	acct.Shared = true
	require.NoError(t, s.PutAccount(ctx, acct))
	signing, identity := acct.Keys()

	// Many inbound sessions, so the snapshot spans several KV values.
	var firstRoom id.RoomID
	var firstSession id.SessionID
	for i := 0; i < 400; i++ {
		room := id.RoomID("!room" + string(rune('a'+i%26)) + ":hs")
		ogs, err := crypto.NewOutboundGroupSession(room, nil, nil)
		require.NoError(t, err)
		igs, err := crypto.NewInboundGroupSession(identity, signing, room, ogs.Internal.Key(), 0, 0, nil, false)
		require.NoError(t, err)
		require.NoError(t, s.PutGroupSession(ctx, igs))
		if i == 0 {
			firstRoom, firstSession = room, igs.ID()
		}
	}
	ogs, err := crypto.NewOutboundGroupSession("!out:hs", nil, nil)
	require.NoError(t, err)
	ogs.Users = map[crypto.UserDevice]crypto.OGSState{{UserID: "@a:hs", DeviceID: "D"}: crypto.OGSAlreadyShared}
	require.NoError(t, s.AddOutboundGroupSession(ctx, ogs))
	require.NoError(t, s.PutDevice(ctx, "@a:hs", &id.Device{UserID: "@a:hs", DeviceID: "D", IdentityKey: "ik", SigningKey: "sk"}))

	require.NoError(t, s.Persist())
	s.Close()
	assert.Greater(t, len(kv.m), 3, "the snapshot was split into pieces")

	again, err := NewStore(kv, nil)
	require.NoError(t, err)
	defer again.Close()

	got, err := again.GetAccount(ctx)
	require.NoError(t, err)
	require.NotNil(t, got)
	gotSigning, gotIdentity := got.Keys()
	assert.Equal(t, signing, gotSigning)
	assert.Equal(t, identity, gotIdentity)
	assert.True(t, got.Shared)

	igs, err := again.GetGroupSession(ctx, firstRoom, firstSession)
	require.NoError(t, err)
	assert.Equal(t, firstRoom, igs.RoomID)

	out, err := again.GetOutboundGroupSession(ctx, "!out:hs")
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, ogs.ID(), out.ID())
	assert.Equal(t, crypto.OGSAlreadyShared, out.Users[crypto.UserDevice{UserID: "@a:hs", DeviceID: "D"}])

	dev, err := again.GetDevice(ctx, "@a:hs", "D")
	require.NoError(t, err)
	assert.Equal(t, id.IdentityKey("ik"), dev.IdentityKey)
}

func TestStoreFlushesInTheBackground(t *testing.T) {
	kv := newMemKV()
	s, err := NewStore(kv, nil)
	require.NoError(t, err)
	require.NoError(t, s.PutAccount(context.Background(), crypto.NewOlmAccount()))
	s.Close() // Close flushes pending changes.

	again, err := NewStore(kv, nil)
	require.NoError(t, err)
	defer again.Close()
	acct, err := again.GetAccount(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, acct)
}

func TestPickleKeyIsSealedAndLegacyKeysAreUpgraded(t *testing.T) {
	kv := newMemKV()
	box := secretbox.New("appservice-token", "e2ee-pickle-key")

	// An earlier version stored the key as is.
	legacy := bytes.Repeat([]byte{7}, 32)
	require.NoError(t, kv.Set(pickleKeyKey, legacy))
	s, err := NewStore(kv, box)
	require.NoError(t, err)
	assert.Equal(t, legacy, s.pickleKey, "the existing key keeps working so existing pickles still open")
	s.Close()

	stored, _ := kv.Get(pickleKeyKey)
	assert.True(t, secretbox.IsSealed(string(stored)))
	assert.NotContains(t, string(stored), string(legacy))

	again, err := NewStore(kv, box)
	require.NoError(t, err)
	assert.Equal(t, legacy, again.pickleKey)
	again.Close()

	_, err = NewStore(kv, secretbox.New("another-token", "e2ee-pickle-key"))
	assert.Error(t, err, "a changed secret must not silently start with a new key")
}
