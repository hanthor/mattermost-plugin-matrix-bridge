package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const defaultElementImage = "vectorim/element-web:latest"

// TestWalkthrough deploys the full stack plus Element Web and drives both web
// UIs with Playwright (e2e/walkthrough/walkthrough.mjs) to produce the
// screenshots behind docs/WALKTHROUGH.md. It runs only when E2E_WALKTHROUGH
// is set, because it needs Node.js and a Chromium install on the test host.
func TestWalkthrough(t *testing.T) {
	if os.Getenv("E2E_WALKTHROUGH") == "" {
		t.Skip("set E2E_WALKTHROUGH=1 to generate the walkthrough screenshots")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the walkthrough: %v", err)
	}

	h := NewHarness(t)
	ctx := context.Background()

	const alicePassword = "Walkthrough-Alice-Password-1!"
	alice := h.NewMatrixUserWithPassword("alice", alicePassword)
	carol, _, carolPassword := h.CreateMattermostUserWithPassword("carol")

	homeserverURL := h.Deployment.UnauthenticatedClient(t, HSName).BaseURL
	elementURL := startElement(t, ctx, h.Deployment.Network(), homeserverURL, h.ServerName)

	outDir, err := filepath.Abs(envOr("WALKTHROUGH_OUT_DIR", filepath.Join("..", "docs", "walkthrough")))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(outDir, 0o755))

	scenario := map[string]any{
		"mattermostURL":  h.Mattermost.URL,
		"adminUsername":  adminUsername,
		"adminPassword":  adminPassword,
		"adminToken":     h.Admin.AuthToken,
		"teamName":       h.Team.Name,
		"carolUsername":  carol.Username,
		"carolPassword":  carolPassword,
		"homeserverURL":  homeserverURL,
		"serverName":     h.ServerName,
		"elementURL":     elementURL,
		"aliceUserID":    alice.UserID,
		"alicePassword":  alicePassword,
		"aliceToken":     alice.AccessToken,
		"aliceDeviceID":  alice.DeviceID,
		"bridgeBotUser":  h.BridgeBotUserID(),
		"ghostPrefix":    "@_mattermost_",
		"outDir":         outDir,
		"pluginID":       PluginID,
		"generatedAtUTC": time.Now().UTC().Format(time.RFC3339),
	}
	scenarioJSON, err := json.Marshal(scenario)
	require.NoError(t, err)

	scriptDir, err := filepath.Abs(filepath.Join("walkthrough"))
	require.NoError(t, err)

	cmd := exec.Command("node", filepath.Join(scriptDir, "walkthrough.mjs"))
	cmd.Dir = scriptDir
	cmd.Env = append(os.Environ(), "WALKTHROUGH_SCENARIO="+string(scenarioJSON))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "walkthrough script failed")

	entries, err := os.ReadDir(outDir)
	require.NoError(t, err)
	pngs := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".png" {
			pngs++
		}
	}
	require.Greater(t, pngs, 5, "expected the walkthrough to produce screenshots in %s", outDir)
	t.Logf("walkthrough produced %d screenshots in %s", pngs, outDir)
}

// startElement runs Element Web pointed at the deployed homeserver and
// returns the URL a browser on the test host can open.
func startElement(t *testing.T, ctx context.Context, networkName, homeserverURL, serverName string) string {
	t.Helper()

	config := map[string]any{
		"default_server_config": map[string]any{
			"m.homeserver": map[string]any{
				"base_url":    homeserverURL,
				"server_name": serverName,
			},
		},
		"disable_custom_urls":       true,
		"disable_guests":            true,
		"brand":                     "Element",
		"default_theme":             "light",
		"show_labs_settings":        false,
		"room_directory":            map[string]any{"servers": []string{serverName}},
		"setting_defaults":          map[string]any{"UIFeature.feedback": false},
		"map_style_url":             "",
		"enable_presence_by_hs_url": map[string]any{},
	}
	configJSON, err := json.Marshal(config)
	require.NoError(t, err)

	c, err := testcontainers.Run(ctx, envOr("E2E_ELEMENT_IMAGE", defaultElementImage),
		testcontainers.WithExposedPorts("80/tcp"),
		network.WithNetworkName([]string{"element"}, networkName),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			Reader:            bytes.NewReader(configJSON),
			ContainerFilePath: "/app/config.json",
			FileMode:          0o644,
		}),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/config.json").WithPort("80/tcp").WithStartupTimeout(2*time.Minute)),
	)
	require.NoError(t, err, "start element web")
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })

	host, err := c.Host(ctx)
	require.NoError(t, err)
	port, err := c.MappedPort(ctx, "80/tcp")
	require.NoError(t, err)
	return fmt.Sprintf("http://%s:%s", host, port.Port())
}
