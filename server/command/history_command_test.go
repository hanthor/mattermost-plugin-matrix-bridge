package command

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// historyPlugin answers the history methods and records what was asked.
type historyPlugin struct {
	*mockPlugin
	plan     HistoryReport
	planErr  error
	status   HistoryReport
	started  []int
	startErr error
}

func (h *historyPlugin) PlanChannelHistory(_ string, limit int) (HistoryReport, error) {
	h.started = append(h.started, -limit) // negative marks a plan call
	return h.plan, h.planErr
}

func (h *historyPlugin) StartChannelHistory(_ string, limit int) error {
	h.started = append(h.started, limit)
	return h.startErr
}

func (h *historyPlugin) ChannelHistoryStatus(_ string) HistoryReport { return h.status }

func runHistory(h *historyPlugin, command string) string {
	handler := &Handler{plugin: h}
	return handler.executeHistoryCommand(&model.CommandArgs{Command: command, ChannelId: "ch"}).Text
}

func TestHistoryCommandPreviewSendsNothing(t *testing.T) {
	day := func(d int) int64 { return time.Date(2025, 1, d, 12, 0, 0, 0, time.UTC).UnixMilli() }
	h := &historyPlugin{mockPlugin: &mockPlugin{}, plan: HistoryReport{Pending: 42, Files: 3, Oldest: day(2), Newest: day(20)}}

	text := runHistory(h, "/matrix history")
	assert.Contains(t, text, "42 messages would be imported")
	assert.Contains(t, text, "2025-01-02 to 2025-01-20")
	assert.Contains(t, text, "3 attachments are uploaded with them")
	assert.Contains(t, text, "/matrix history confirm")
	assert.Equal(t, []int{0}, h.started, "only a plan call (limit 0 recorded as -0), no start")
}

func TestHistoryCommandConfirmStartsAndPassesTheLimit(t *testing.T) {
	h := &historyPlugin{mockPlugin: &mockPlugin{}, plan: HistoryReport{Pending: 10, Oldest: 1, Newest: 2}}
	text := runHistory(h, "/matrix history confirm limit=500")
	assert.Contains(t, text, "History import started")
	assert.Contains(t, text, "10 messages")
	assert.Equal(t, []int{-500, 500}, h.started)
}

func TestHistoryCommandHasNothingToDoWhenEverythingIsThere(t *testing.T) {
	h := &historyPlugin{mockPlugin: &mockPlugin{}}
	assert.Contains(t, runHistory(h, "/matrix history confirm"), "Nothing to import")
	assert.Contains(t, runHistory(h, "/matrix history"), "already in its Matrix room")
}

func TestHistoryCommandRejectsBadInputAndReportsErrors(t *testing.T) {
	h := &historyPlugin{mockPlugin: &mockPlugin{}}
	assert.Contains(t, runHistory(h, "/matrix history confirm limit=abc"), "positive number")
	assert.Contains(t, runHistory(h, "/matrix history limit=0"), "positive number")
	assert.Contains(t, runHistory(h, "/matrix history please"), "Usage")

	h.planErr = errors.New("this channel is not bridged: run /matrix create first")
	assert.Contains(t, runHistory(h, "/matrix history"), "not bridged")

	h.planErr = nil
	h.plan = HistoryReport{Pending: 1}
	h.startErr = errors.New("an import is already running for this channel")
	assert.Contains(t, runHistory(h, "/matrix history confirm"), "already running")
}

func TestHistoryCommandShowsProgressAndTheLastRun(t *testing.T) {
	h := &historyPlugin{mockPlugin: &mockPlugin{}, status: HistoryReport{Running: true, Imported: 25, Skipped: 1, Failed: 2}}
	text := runHistory(h, "/matrix history confirm")
	assert.Contains(t, text, "History import is running")
	assert.Contains(t, text, "25 imported, 1 skipped, 2 failed")

	h = &historyPlugin{mockPlugin: &mockPlugin{}, plan: HistoryReport{Pending: 5, Oldest: 1, Newest: 2},
		status: HistoryReport{Imported: 100, Failed: 3, Error: "3 failures in a row"}}
	preview := runHistory(h, "/matrix history")
	assert.True(t, strings.Contains(preview, "Last run: 100 imported, 0 skipped, 3 failed."), preview)
	assert.Contains(t, preview, "Stopped: 3 failures in a row")
}

func TestHistoryIsOnlyForSystemAdmins(t *testing.T) {
	api := &plugintest.API{}
	api.On("HasPermissionTo", "admin", model.PermissionManageSystem).Return(true)
	api.On("HasPermissionTo", "member", model.PermissionManageSystem).Return(false)
	handler := &Handler{plugin: &mockPlugin{}, pluginAPI: api}

	denied := handler.checkCommandPermission(&model.CommandArgs{UserId: "member", ChannelId: "ch"}, "history")
	require.NotNil(t, denied)
	assert.Contains(t, denied.Text, "Only system administrators")
	assert.Nil(t, handler.checkCommandPermission(&model.CommandArgs{UserId: "admin", ChannelId: "ch"}, "history"))
}
