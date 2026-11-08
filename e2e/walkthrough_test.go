package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
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
//
// The run fails if any tutorial step failed, so the committed screenshots
// always reflect a bridge that did everything the tutorial claims.
func TestWalkthrough(t *testing.T) {
	runWalkthrough(t, "ghosts")
}

// TestWalkthroughMirror is the same for mirror mode, the way to migrate from
// Mattermost to Matrix: people have real accounts, which they can claim.
// Its screenshots go to docs/walkthrough/mirror.
func TestWalkthroughMirror(t *testing.T) {
	runWalkthrough(t, "mirror")
}

func runWalkthrough(t *testing.T, mode string) {
	if os.Getenv("E2E_WALKTHROUGH") == "" {
		t.Skip("set E2E_WALKTHROUGH=1 to generate the walkthrough screenshots")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the walkthrough: %v", err)
	}

	mirror := mode == "mirror"
	h := NewHarnessWithOptions(t, HarnessOptions{
		MirrorMode:     mirror,
		CallsMode:      "element_call",
		ElementCallURL: "https://call.example.com/room?roomId={room_id}&displayName={display_name}",
	})
	ctx := context.Background()

	// Personas. Matrix: Alice and Bob. Mattermost: Ada (admin), Carol, Dave.
	const matrixPassword = "Walkthrough-Password-1!"
	alice := h.NewMatrixUserWithPassword("alice", matrixPassword)
	bob := h.NewMatrixUserWithPassword("bob", matrixPassword)
	carol, _, carolPassword := h.CreateMattermostUserWithPassword("carol")
	dave, _, davePassword := h.CreateMattermostUserWithPassword("dave")

	homeserverURL := h.Deployment.UnauthenticatedClient(t, HSName).BaseURL
	elementURL := startElement(t, ctx, h.Deployment.Network(), homeserverURL, h.ServerName)

	outDir, err := filepath.Abs(envOr("WALKTHROUGH_OUT_DIR", filepath.Join("..", "docs", "walkthrough")))
	require.NoError(t, err)
	if mirror {
		outDir = filepath.Join(outDir, "mirror")
	}
	require.NoError(t, os.MkdirAll(outDir, 0o755))
	if !mirror {
		writeRedactedRegistration(t, filepath.Join(outDir, "registration.yaml"), h.Registration)
	}

	scenario := map[string]any{
		"mattermostURL": h.Mattermost.URL,
		"adminUsername": adminUsername,
		"adminPassword": adminPassword,
		"adminEmail":    adminEmail,
		"adminToken":    h.Admin.AuthToken,
		"teamName":      h.Team.Name,
		"carol":         map[string]string{"username": carol.Username, "password": carolPassword, "id": carol.Id},
		"dave":          map[string]string{"username": dave.Username, "password": davePassword, "id": dave.Id},
		"homeserverURL": homeserverURL,
		"serverName":    h.ServerName,
		"elementURL":    elementURL,
		"asToken":       h.ASToken,
		"alice": map[string]string{
			"userID": alice.UserID, "password": matrixPassword, "token": alice.AccessToken, "deviceID": alice.DeviceID,
		},
		"bob": map[string]string{
			"userID": bob.UserID, "password": matrixPassword, "token": bob.AccessToken, "deviceID": bob.DeviceID,
		},
		"bridgeBotUser":  h.BridgeBotUserID(),
		"ghostPrefix":    "@_mattermost_",
		"outDir":         outDir,
		"mode":           mode,
		"pluginID":       PluginID,
		"generatedAtUTC": time.Now().UTC().Format(time.RFC3339),
	}
	scenarioJSON, err := json.Marshal(scenario)
	require.NoError(t, err)

	scriptDir, err := filepath.Abs("walkthrough")
	require.NoError(t, err)

	cmd := exec.Command("node", filepath.Join(scriptDir, "walkthrough.mjs"))
	cmd.Dir = scriptDir
	cmd.Env = append(os.Environ(), "WALKTHROUGH_SCENARIO="+string(scenarioJSON))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "walkthrough script failed")

	// Every step must have produced its screenshot.
	raw, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	require.NoError(t, err, "walkthrough manifest")
	var manifest struct {
		Steps []struct {
			File    string `json:"file"`
			Error   string `json:"error"`
			Message string `json:"message"`
		} `json:"steps"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))
	var failures []string
	shots := 0
	for _, s := range manifest.Steps {
		if s.Error != "" {
			failures = append(failures, s.Error+": "+s.Message)
		}
		if s.File != "" {
			shots++
		}
	}
	require.Empty(t, failures, "walkthrough steps failed")
	minimum := 20
	if mirror {
		minimum = 6
	}
	require.Greater(t, shots, minimum, "expected the walkthrough to produce screenshots in %s", outDir)
	t.Logf("walkthrough produced %d screenshots in %s", shots, outDir)
}

// writeRedactedRegistration stores the appservice registration the demo
// homeserver runs with, with the secrets blanked, for the tutorial.
func writeRedactedRegistration(t *testing.T, path, registration string) {
	t.Helper()
	redacted := regexp.MustCompile(`(?m)^(as_token|hs_token):.*$`).ReplaceAllString(registration, "$1: <generated by the plugin; copy from the System Console>")
	redacted = strings.TrimSpace(redacted) + "\n"
	require.NoError(t, os.WriteFile(path, []byte(redacted), 0o644))
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
