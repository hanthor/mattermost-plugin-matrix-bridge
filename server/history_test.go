package main

import (
	"strconv"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

const historyChannel = "chan1"

// historyFixture is a bridged channel with a few old posts.
func historyFixture(t *testing.T, posts ...*model.Post) (*Plugin, *fakeSynapse, *plugintest.API) {
	t.Helper()
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	bob := &model.User{Id: "mm-bob", Username: "bob"}
	p, api := newMirrorTestBridge(t, hs, ada, bob)
	p.mattermostToMatrixBridge.fileTracker = NewPendingFileTracker()
	p.mattermostToMatrixBridge.postTracker = NewPostTracker(100)
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey(historyChannel), []byte("!room:example.com")))

	list := &model.PostList{Posts: map[string]*model.Post{}}
	// The API returns the newest first.
	for i := len(posts) - 1; i >= 0; i-- {
		list.Order = append(list.Order, posts[i].Id)
		list.Posts[posts[i].Id] = posts[i]
	}
	api.On("GetPostsForChannel", historyChannel, 0, historyPageSize).Return(list, nil).Maybe()
	api.On("GetPostsForChannel", historyChannel, mock.Anything, historyPageSize).Return(&model.PostList{}, nil).Maybe()
	api.On("GetReactions", mock.Anything).Return([]*model.Reaction{}, nil).Maybe()
	api.On("UpdatePost", mock.Anything).Return(func(post *model.Post) *model.Post {
		post.UpdateAt++
		return post
	}, nil).Maybe()
	for _, post := range posts {
		api.On("GetPost", post.Id).Return(post, nil).Maybe()
	}
	return p, hs, api
}

func oldPost(id, user, msg string, daysAgo int) *model.Post {
	return &model.Post{
		Id: id, ChannelId: historyChannel, UserId: user, Message: msg,
		CreateAt: time.Now().AddDate(0, 0, -daysAgo).UnixMilli(),
	}
}

func waitForHistory(t *testing.T, p *Plugin) {
	t.Helper()
	require.Eventually(t, func() bool { return !p.ChannelHistoryStatus(historyChannel).Running }, 5*time.Second, 10*time.Millisecond)
}

func TestHistoryPlanCountsWhatWouldBeImportedAndSkipsTheRest(t *testing.T) {
	key := "matrix_event_id_example_com"
	posts := []*model.Post{
		oldPost("p1", "mm-ada", "first", 30),
		oldPost("p2", "mm-bob", "second", 20),
		{Id: "p3", ChannelId: historyChannel, UserId: "mm-ada", Message: "joined", Type: model.PostTypeJoinChannel, CreateAt: time.Now().AddDate(0, 0, -25).UnixMilli()},
		{Id: "p4", ChannelId: historyChannel, UserId: "mm-ada", Message: "deleted", DeleteAt: 5, CreateAt: time.Now().AddDate(0, 0, -15).UnixMilli()},
		{Id: "p5", ChannelId: historyChannel, UserId: "mm-ada", Message: "already there", CreateAt: time.Now().AddDate(0, 0, -10).UnixMilli(), Props: model.StringInterface{key: "$old"}},
		{Id: "p6", ChannelId: historyChannel, UserId: "mm-ada", Message: "with file", FileIds: []string{"f1", "f2"}, CreateAt: time.Now().AddDate(0, 0, -5).UnixMilli()},
	}
	p, hs, _ := historyFixture(t, posts...)
	p.configuration.MatrixServerURL = "https://example.com"

	plan, err := p.PlanChannelHistory(historyChannel, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, plan.Pending, "p1, p2 and p6")
	assert.Equal(t, 3, plan.Skipped, "the join notice, the deleted post and the one already in the room")
	assert.Equal(t, 2, plan.Files)
	assert.Equal(t, posts[0].CreateAt, plan.Oldest)
	assert.Equal(t, posts[5].CreateAt, plan.Newest)
	assert.Empty(t, hs.messages(), "a preview sends nothing")

	limited, err := p.PlanChannelHistory(historyChannel, 2)
	require.NoError(t, err)
	assert.Equal(t, 2, limited.Pending)
}

func TestHistoryPlanNeedsABridgedChannel(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	_, err := p.PlanChannelHistory("unmapped", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not bridged")
}

func TestHistoryImportSendsOldestFirstAsTheAuthorsWithTheirOriginalTimes(t *testing.T) {
	posts := []*model.Post{
		oldPost("p1", "mm-ada", "Kick-off notes", 400),
		oldPost("p2", "mm-bob", "Looks good to me", 399),
		{Id: "p3", ChannelId: historyChannel, UserId: "mm-ada", RootId: "p1", Message: "A reply in the thread", CreateAt: time.Now().AddDate(0, 0, -398).UnixMilli()},
	}
	p, hs, _ := historyFixture(t, posts...)
	p.configuration.MatrixServerURL = "https://example.com"

	require.NoError(t, p.StartChannelHistory(historyChannel, 0))
	waitForHistory(t, p)

	sent := hs.messages()
	require.Len(t, sent, 3)
	assert.Equal(t, []string{"Kick-off notes", "Looks good to me", "A reply in the thread"}, []string{sent[0].Body, sent[1].Body, sent[2].Body})
	assert.Equal(t, "@ada:example.com", sent[0].UserID)
	assert.Equal(t, "@bob:example.com", sent[1].UserID)
	for i, post := range posts {
		assert.Equal(t, int64Str(post.CreateAt), sent[i].TS, "message %d keeps the time it was written", i)
	}
	relates, _ := sent[2].Content["m.relates_to"].(map[string]any)
	assert.Equal(t, "m.thread", relates["rel_type"], "a reply stays in its thread")

	status := p.ChannelHistoryStatus(historyChannel)
	assert.Equal(t, 3, status.Imported)
	assert.Zero(t, status.Failed)
	assert.Equal(t, posts[2].CreateAt, p.historyCursor(historyChannel))
}

func int64Str(n int64) string { return strconv.FormatInt(n, 10) }

func TestHistoryImportCanBeRepeatedWithoutDuplicates(t *testing.T) {
	posts := []*model.Post{oldPost("p1", "mm-ada", "one", 10), oldPost("p2", "mm-bob", "two", 9)}
	p, hs, _ := historyFixture(t, posts...)
	p.configuration.MatrixServerURL = "https://example.com"

	require.NoError(t, p.StartChannelHistory(historyChannel, 0))
	waitForHistory(t, p)
	require.Len(t, hs.messages(), 2)

	plan, err := p.PlanChannelHistory(historyChannel, 0)
	require.NoError(t, err)
	assert.Zero(t, plan.Pending, "the cursor covers what was imported")
	require.NoError(t, p.StartChannelHistory(historyChannel, 0))
	waitForHistory(t, p)
	assert.Len(t, hs.messages(), 2, "a second run sends nothing more")
}

func TestHistoryImportRespectsTheLimitAndContinuesFromThere(t *testing.T) {
	posts := []*model.Post{oldPost("p1", "mm-ada", "one", 10), oldPost("p2", "mm-bob", "two", 9), oldPost("p3", "mm-ada", "three", 8)}
	p, hs, _ := historyFixture(t, posts...)
	p.configuration.MatrixServerURL = "https://example.com"

	require.NoError(t, p.StartChannelHistory(historyChannel, 2))
	waitForHistory(t, p)
	require.Len(t, hs.messages(), 2)

	require.NoError(t, p.StartChannelHistory(historyChannel, 0))
	waitForHistory(t, p)
	sent := hs.messages()
	require.Len(t, sent, 3)
	assert.Equal(t, "three", sent[2].Body)
}

func TestHistoryImportSkipsBotsAndMatrixUsers(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	bot := &model.User{Id: "mm-bot", Username: "bot", IsBot: true}
	p, api := newMirrorTestBridge(t, hs, ada, bot)
	p.configuration.MatrixServerURL = "https://example.com"
	p.mattermostToMatrixBridge.fileTracker = NewPendingFileTracker()
	p.mattermostToMatrixBridge.postTracker = NewPostTracker(100)
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey(historyChannel), []byte("!room:example.com")))
	posts := []*model.Post{oldPost("p1", "mm-bot", "I am a bot", 5), oldPost("p2", "mm-ada", "I am a person", 4)}
	list := &model.PostList{Order: []string{"p2", "p1"}, Posts: map[string]*model.Post{"p1": posts[0], "p2": posts[1]}}
	api.On("GetPostsForChannel", historyChannel, 0, historyPageSize).Return(list, nil)
	api.On("GetPostsForChannel", historyChannel, mock.Anything, historyPageSize).Return(&model.PostList{}, nil).Maybe()
	api.On("UpdatePost", mock.Anything).Return(func(post *model.Post) *model.Post { return post }, nil).Maybe()
	api.On("GetReactions", mock.Anything).Return([]*model.Reaction{}, nil).Maybe()

	require.NoError(t, p.StartChannelHistory(historyChannel, 0))
	waitForHistory(t, p)
	sent := hs.messages()
	require.Len(t, sent, 1)
	assert.Equal(t, "I am a person", sent[0].Body)
	assert.Equal(t, 1, p.ChannelHistoryStatus(historyChannel).Skipped)
}

func TestHistoryImportRefusesToRunTwiceAtOnce(t *testing.T) {
	p, _, _ := historyFixture(t, oldPost("p1", "mm-ada", "one", 3))
	p.configuration.MatrixServerURL = "https://example.com"
	historyRuns.Store(historyChannel, struct{}{})
	defer historyRuns.Delete(historyChannel)

	err := p.StartChannelHistory(historyChannel, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already running")
	assert.True(t, p.ChannelHistoryStatus(historyChannel).Running)
}

func TestHistoryImportCarriesAttachmentsAndReactions(t *testing.T) {
	withFile := oldPost("p1", "mm-ada", "Spec attached", 300)
	withFile.FileIds = []string{"f1"}
	reactedAt := time.Now().AddDate(0, 0, -299).UnixMilli()
	p, hs, api := historyFixture(t, withFile)
	p.configuration.MatrixServerURL = "https://example.com"
	p.pendingFiles = NewPendingFileTracker()
	p.mattermostToMatrixBridge.fileTracker = p.pendingFiles
	p.maxFileSize = 1 << 20
	api.On("GetFileInfo", "f1").Return(&model.FileInfo{Id: "f1", Name: "spec.pdf", MimeType: "application/pdf", Size: 5}, nil)
	api.On("GetFile", "f1").Return([]byte("12345"), nil)
	api.ExpectedCalls = filterCalls(api.ExpectedCalls, "GetReactions")
	api.On("GetReactions", "p1").Return([]*model.Reaction{
		{PostId: "p1", UserId: "mm-bob", EmojiName: "+1", CreateAt: reactedAt},
	}, nil)

	plan, err := p.PlanChannelHistory(historyChannel, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, plan.Files)

	require.NoError(t, p.StartChannelHistory(historyChannel, 0))
	waitForHistory(t, p)

	assert.Equal(t, []string{"spec.pdf"}, hs.uploads)
	var fileEvent *fakeSent
	for _, m := range hs.messages() {
		if m.Content["msgtype"] == "m.file" {
			m := m
			fileEvent = &m
		}
	}
	require.NotNil(t, fileEvent, "the attachment is sent as a file message")
	assert.Equal(t, "mxc://example.com/uploaded", fileEvent.Content["url"])
	assert.Equal(t, int64Str(withFile.CreateAt), fileEvent.TS, "the file keeps the time of its post")

	require.Len(t, hs.reactions, 1)
	assert.Equal(t, "@bob:example.com", hs.reactions[0].UserID, "the reaction comes from the person who reacted")
	assert.Equal(t, int64Str(reactedAt), hs.reactions[0].TS)
	assert.Equal(t, 1, p.ChannelHistoryStatus(historyChannel).Reactions)
}

// filterCalls drops the registered expectations of one mocked method.
func filterCalls(calls []*mock.Call, method string) []*mock.Call {
	var kept []*mock.Call
	for _, c := range calls {
		if c.Method != method {
			kept = append(kept, c)
		}
	}
	return kept
}
