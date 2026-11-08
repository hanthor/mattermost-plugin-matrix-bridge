package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMattermostUI drives the Mattermost web client in a browser while Matrix
// users act, and checks what it renders: formatting, authors and avatars,
// mentions and unread badges, edits, reactions, threads, images, typing,
// deleted messages, the channel header, membership notices, the call card and
// the bridge panel. It is the browser-level counterpart of TestMattermostClientView.
//
// It needs Node.js and Playwright's Chromium on the test host, so it runs only
// when E2E_UI is set (see `make ui-tests`).
func TestMattermostUI(t *testing.T) {
	if os.Getenv("E2E_UI") == "" {
		t.Skip("set E2E_UI=1 to run the browser-level Mattermost tests")
	}
	h := NewHarnessWithOptions(t, HarnessOptions{CallsMode: "element_call", ElementCallURL: "https://call.example.com/room?roomId={room_id}"})
	ctx := context.Background()

	alice := h.NewMatrixUserWithPassword("alice", "UI-Password-1!")
	bob := h.NewMatrixUserWithPassword("bob", "UI-Password-1!")
	carol, _, carolPassword := h.CreateMattermostUserWithPassword("carol")

	channel := h.CreateChannel("ui-checks", "UI checks")
	roomID := h.CreateMappedRoom(alice, channel.Id, "UI checks")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)

	scenario, err := json.Marshal(map[string]any{
		"mattermostURL": h.Mattermost.URL,
		"teamName":      h.Team.Name,
		"channelName":   channel.Name,
		"pluginID":      PluginID,
		"carol":         map[string]string{"username": carol.Username, "password": carolPassword, "id": carol.Id, "ghost": h.GhostUserID(carol.Id)},
		"homeserverURL": h.Deployment.UnauthenticatedClient(t, HSName).BaseURL,
		"roomID":        roomID,
		"alice":         map[string]string{"userID": alice.UserID, "token": alice.AccessToken},
		"bob":           map[string]string{"userID": bob.UserID, "token": bob.AccessToken},
	})
	require.NoError(t, err)

	scriptDir, err := filepath.Abs("walkthrough")
	require.NoError(t, err)
	outDir := envOr("UI_OUT_DIR", filepath.Join(scriptDir, "..", "..", "docs", "walkthrough-debug", "ui"))
	require.NoError(t, os.MkdirAll(outDir, 0o755))
	resultsPath := filepath.Join(outDir, "results.json")

	cmd := exec.Command("node", filepath.Join(scriptDir, "ui-checks.mjs"))
	cmd.Dir = scriptDir
	cmd.Env = append(os.Environ(), "UI_SCENARIO="+string(scenario), "UI_OUT_DIR="+outDir)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	runErr := cmd.Run()

	raw, err := os.ReadFile(resultsPath)
	require.NoError(t, err, "the script writes results.json")
	var results []struct {
		Name    string `json:"name"`
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(raw, &results))
	require.NotEmpty(t, results)
	for _, r := range results {
		t.Run(r.Name, func(t *testing.T) {
			assert.True(t, r.OK, r.Message)
		})
	}
	require.NoError(t, runErr, "the browser script failed")
}
