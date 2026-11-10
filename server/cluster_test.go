package main

import (
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

func TestAnEchoIsRecognisedOnAnotherNode(t *testing.T) {
	hs := newFakeSynapse(t)
	nodeA, _ := newMirrorTestBridge(t, hs)
	nodeB, _ := newMirrorTestBridge(t, hs)
	// Both nodes share one database.
	nodeB.kvstore = nodeA.kvstore
	nodeA.matrixClient.SetSharedSentStore(sharedSentStore{nodeA})
	nodeB.matrixClient.SetSharedSentStore(sharedSentStore{nodeB})

	nodeA.matrixClient.RecordSentEvent("$sent-by-a")
	require.Eventually(t, func() bool { return nodeB.matrixClient.SentByBridge("$sent-by-a") }, time.Second, 10*time.Millisecond,
		"the echo reaches node B, which did not send the event")
	assert.False(t, nodeB.matrixClient.SentByBridge("$someone-elses"))
}

func TestExpiredSentRecordsAreSwept(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	require.NoError(t, p.kvstore.Set(sentEventKeyPrefix+"$old", []byte("1")))
	sharedSentStore{p}.MarkSent("$new")

	p.sweepSentEvents()
	assert.False(t, sharedSentStore{p}.WasSent("$old"))
	assert.True(t, sharedSentStore{p}.WasSent("$new"))
}

func TestHistoryImportIsRefusedWhileAnotherNodeRunsIt(t *testing.T) {
	p, _, api := historyFixture(t, oldPost("p1", "mm-ada", "one", 1))
	api.ExpectedCalls = filterCalls(api.ExpectedCalls, "KVSetWithOptions")
	api.On("KVSetWithOptions", mock.Anything, mock.Anything, mock.Anything).Return(false, nil)

	err := p.StartChannelHistory(historyChannel, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "another server")
	_, running := historyRuns.Load(historyChannel)
	assert.False(t, running, "the local marker is released too")
}

func TestUnrelatedConfigurationChangesDoNotRebuildTheBridge(t *testing.T) {
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	var override *configuration // what the server reports next; nil means the current settings
	api.On("LoadPluginConfiguration", mock.Anything).Run(func(args mock.Arguments) {
		cfg := p.getConfiguration().Clone()
		if override != nil {
			cfg = override
		}
		*(args.Get(0).(*configuration)) = *cfg
	}).Return(nil)
	api.On("LogInfo", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()

	// The first change normalises the settings and builds the client.
	require.NoError(t, p.OnConfigurationChange())
	client, bridge := p.matrixClient, p.mattermostToMatrixBridge

	// A change elsewhere in the server's configuration brings the same settings.
	require.NoError(t, p.OnConfigurationChange())
	assert.Same(t, client, p.matrixClient, "no setting changed: the client is kept")
	assert.Same(t, bridge, p.mattermostToMatrixBridge)

	// A changed setting does rebuild them.
	override = p.getConfiguration().Clone()
	override.MatrixUsernamePrefix = "changed"
	require.NoError(t, p.OnConfigurationChange())
	assert.NotSame(t, client, p.matrixClient)
}

func TestAClusterEventDropsTheCachedMapping(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	key := kvstore.BuildChannelMappingKey("ch1")
	p.roomMappingCache = NewCache(10, time.Minute)
	p.roomMappingCache.Set(key, "!old:example.com")

	p.OnPluginClusterEvent(nil, model.PluginClusterEvent{Id: clusterEventInvalidateChannel, Data: []byte("ch1")})
	_, found := p.roomMappingCache.Get(key)
	assert.False(t, found)
}
