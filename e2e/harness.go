package e2e

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	dockerclient "github.com/docker/docker/client"
	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const (
	// PluginID is the Mattermost plugin ID of the bridge.
	PluginID = "com.mattermost.plugin-matrix-bridge"

	// HSName is the Complement homeserver name. It is also the Matrix server
	// name (the domain in user IDs) and the DNS alias of the Synapse container
	// on the Complement network.
	HSName = "hs1"

	// MattermostAlias is the DNS alias of the Mattermost container on the
	// Complement network. Synapse delivers appservice transactions to it.
	MattermostAlias = "mattermost"

	appServiceID       = "mattermost-bridge"
	bridgeBotLocalpart = "_mattermost_bridge"

	// complementAppServiceDir is where Complement writes registration files
	// inside the homeserver container.
	complementAppServiceDir = "/complement/appservice/"

	// Default timeouts for asynchronous bridging.
	bridgeTimeout = 45 * time.Second
	pollInterval  = 500 * time.Millisecond
)

// Harness holds a deployed Synapse + Mattermost pair with the bridge installed.
type Harness struct {
	t          *testing.T
	Deployment complement.Deployment

	// ServerName is the Matrix server name of hs1.
	ServerName string
	// ASToken and HSToken are the appservice tokens Complement generated.
	ASToken, HSToken string
	// Registration is the appservice registration file installed on hs1.
	Registration string

	// Mattermost is the Mattermost deployment reachable from the test host.
	Mattermost *MattermostServer
	// Admin is a Mattermost API client logged in as the system administrator.
	Admin *model.Client4
	// AdminUser is the system administrator account.
	AdminUser *model.User
	// Team is the team every test channel is created in.
	Team *model.Team

	settings map[string]any
}

// SetBridgeSetting changes one plugin setting while the plugin runs, as an
// administrator would in the System Console. The whole settings map is sent so
// secrets are not blanked.
func (h *Harness) SetBridgeSetting(key string, value any) {
	h.t.Helper()
	h.settings[key] = value
	patch := &model.Config{}
	patch.PluginSettings.Plugins = map[string]map[string]any{PluginID: h.settings}
	_, _, err := h.Admin.PatchConfig(context.Background(), patch)
	require.NoError(h.t, err, "change plugin setting %s", key)
}

// bridgeBlueprint describes a single homeserver with the bridge registered as
// an application service. Complement generates fresh tokens on every deploy;
// the harness reads them back from the container.
func bridgeBlueprint() b.Blueprint {
	return b.MustValidate(b.Blueprint{
		Name: "mattermost_bridge",
		Homeservers: []b.Homeserver{{
			Name: HSName,
			ApplicationServices: []b.ApplicationService{{
				ID:              appServiceID,
				URL:             fmt.Sprintf("http://%s:8065/plugins/%s", MattermostAlias, PluginID),
				SenderLocalpart: bridgeBotLocalpart,
				RateLimited:     false,
				SendEphemeral:   true,
			}},
		}},
	})
}

// NewHarness deploys everything and returns a ready-to-use harness. It
// registers cleanup with t.
func NewHarness(t *testing.T) *Harness {
	t.Helper()
	return NewHarnessWithOptions(t, HarnessOptions{})
}

// HarnessOptions tunes the deployment.
type HarnessOptions struct {
	// MirrorMode runs the bridge in mirror mode: Mattermost users get real
	// @username accounts instead of ghosts, and a Matrix admin token is
	// configured so /matrix claim can hand accounts over.
	MirrorMode bool
	// CallsMode and ElementCallURL configure call bridging ("" keeps the
	// default, presence).
	CallsMode      string
	ElementCallURL string
}

// NewHarnessWithOptions is NewHarness with options.
func NewHarnessWithOptions(t *testing.T, opts HarnessOptions) *Harness {
	t.Helper()
	ctx := context.Background()

	deployment := complement.OldDeploy(t, bridgeBlueprint())
	t.Cleanup(func() { deployment.Destroy(t) })

	asToken, hsToken, registration := readAppServiceTokens(t, deployment.ContainerID(t, HSName))

	mm := StartMattermost(t, ctx, MattermostOptions{
		Network: deployment.Network(),
		Alias:   MattermostAlias,
	})

	h := &Harness{
		t:            t,
		Deployment:   deployment,
		ServerName:   HSName,
		ASToken:      asToken,
		Registration: registration,
		HSToken:      hsToken,
		Mattermost:   mm,
	}

	h.Admin, h.AdminUser = mm.CreateAdmin(t, ctx)
	h.Team = mm.CreateTeam(t, ctx, h.Admin, "e2e")
	cfg := BridgeConfig{
		MatrixServerURL:    fmt.Sprintf("http://%s:8008", HSName),
		MatrixServerDomain: HSName,
		ASToken:            asToken,
		HSToken:            hsToken,
		MirrorMode:         opts.MirrorMode,
		CallsMode:          opts.CallsMode,
		ElementCallURL:     opts.ElementCallURL,
	}
	if opts.MirrorMode {
		admin := deployment.Register(t, HSName, helpers.RegistrationOpts{LocalpartSuffix: "matrixadmin", IsAdmin: true})
		cfg.AdminToken = admin.AccessToken
	}
	h.settings = cfg.settings()
	mm.InstallBridge(t, ctx, h.Admin, cfg)
	h.waitForBridgeCommands(ctx)

	return h
}

// readAppServiceTokens reads the registration Complement wrote into the
// homeserver container and extracts the tokens.
func readAppServiceTokens(t *testing.T, containerID string) (asToken, hsToken, registration string) {
	t.Helper()
	ctx := context.Background()

	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	require.NoError(t, err, "docker client")
	defer func() { _ = cli.Close() }()

	reader, _, err := cli.CopyFromContainer(ctx, containerID, complementAppServiceDir+appServiceID+".yaml")
	require.NoError(t, err, "copy appservice registration from homeserver container")
	defer func() { _ = reader.Close() }()

	tr := tar.NewReader(reader)
	_, err = tr.Next()
	require.NoError(t, err, "read registration tar header")
	raw, err := io.ReadAll(tr)
	require.NoError(t, err, "read registration")

	// Complement inlines newlines when it stores the registration as an image
	// label; be tolerant in case the literal sequence survives.
	content := strings.ReplaceAll(string(raw), `\n`, "\n")

	var reg struct {
		ASToken string `yaml:"as_token"`
		HSToken string `yaml:"hs_token"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(content), &reg), "parse registration: %s", content)
	require.NotEmpty(t, reg.ASToken, "as_token missing in registration: %s", content)
	require.NotEmpty(t, reg.HSToken, "hs_token missing in registration: %s", content)
	return reg.ASToken, reg.HSToken, content
}

// waitForBridgeCommands waits until the plugin has registered its slash
// command, which means OnActivate finished.
func (h *Harness) waitForBridgeCommands(ctx context.Context) {
	h.t.Helper()
	channel := h.TownSquare()
	WaitFor(h.t, bridgeTimeout, "bridge slash command to be registered", func() bool {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix status")
		return err == nil && resp != nil
	})
}

// TownSquare returns the default channel of the test team.
func (h *Harness) TownSquare() *model.Channel {
	h.t.Helper()
	channel, _, err := h.Admin.GetChannelByName(context.Background(), "town-square", h.Team.Id, "")
	require.NoError(h.t, err)
	return channel
}

// NewMatrixUser registers a fresh Matrix user on hs1 and returns a client with
// a generous sync timeout.
func (h *Harness) NewMatrixUser(localpart string) *client.CSAPI {
	h.t.Helper()
	return h.NewMatrixUserWithPassword(localpart, "")
}

// NewMatrixUserWithPassword registers a Matrix user with a known password so
// a browser can log in as them. An empty password uses Complement's default.
func (h *Harness) NewMatrixUserWithPassword(localpart, password string) *client.CSAPI {
	h.t.Helper()
	cs := h.Deployment.Register(h.t, HSName, helpers.RegistrationOpts{LocalpartSuffix: localpart, Password: password})
	cs.SyncUntilTimeout = bridgeTimeout
	return cs
}

// BridgeBotUserID is the Matrix ID of the appservice sender.
func (h *Harness) BridgeBotUserID() string {
	return fmt.Sprintf("@%s:%s", bridgeBotLocalpart, h.ServerName)
}

// GhostUserID returns the Matrix ghost representing a Mattermost user.
func (h *Harness) GhostUserID(mattermostUserID string) string {
	return fmt.Sprintf("@_mattermost_%s:%s", mattermostUserID, h.ServerName)
}

// CreateChannel creates a public channel in the test team.
func (h *Harness) CreateChannel(name, displayName string) *model.Channel {
	h.t.Helper()
	channel, _, err := h.Admin.CreateChannel(context.Background(), &model.Channel{
		TeamId:      h.Team.Id,
		Name:        name,
		DisplayName: displayName,
		Type:        model.ChannelTypeOpen,
	})
	require.NoError(h.t, err, "create channel %s", name)
	return channel
}

// CreateMattermostUser creates a regular Mattermost user, adds them to the
// test team and returns a logged-in client for them.
func (h *Harness) CreateMattermostUser(username string) (*model.User, *model.Client4) {
	h.t.Helper()
	user, cl, _ := h.CreateMattermostUserWithPassword(username)
	return user, cl
}

// CreateMattermostUserWithPassword is CreateMattermostUser but also returns
// the generated password, for browser logins.
func (h *Harness) CreateMattermostUserWithPassword(username string) (*model.User, *model.Client4, string) {
	h.t.Helper()
	ctx := context.Background()
	password := "E2e-Password-" + model.NewId()[:8]
	user, _, err := h.Admin.CreateUser(ctx, &model.User{
		Username: username,
		Email:    username + "@example.com",
		Password: password,
	})
	require.NoError(h.t, err, "create user %s", username)
	_, _, err = h.Admin.AddTeamMember(ctx, h.Team.Id, user.Id)
	require.NoError(h.t, err, "add %s to team", username)

	cl := model.NewAPIv4Client(h.Mattermost.URL)
	_, _, err = cl.Login(ctx, username, password)
	require.NoError(h.t, err, "login %s", username)
	return user, cl, password
}

// MapChannelToRoom runs /matrix map so the channel bridges to an existing
// Matrix room. The room must be joinable by the bridge bot (public, or the bot
// has been invited).
func (h *Harness) MapChannelToRoom(channelID, roomID string) *model.CommandResponse {
	h.t.Helper()
	resp, _, err := h.Admin.ExecuteCommand(context.Background(), channelID, "/matrix map "+roomID)
	require.NoError(h.t, err, "/matrix map")
	require.NotNil(h.t, resp)
	h.t.Logf("/matrix map %s -> %s", roomID, resp.Text)
	require.NotContains(h.t, resp.Text, "❌", "/matrix map failed: %s", resp.Text)
	require.Contains(h.t, resp.Text, "Mapping Saved", "/matrix map did not confirm the mapping: %s", resp.Text)
	return resp
}

// BotClient returns a client that acts as the appservice bot using the
// as_token, for inspecting what the bridge did on the Matrix side.
func (h *Harness) BotClient() *client.CSAPI {
	h.t.Helper()
	cs := h.Deployment.UnauthenticatedClient(h.t, HSName)
	cs.UserID = h.BridgeBotUserID()
	cs.AccessToken = h.ASToken
	cs.SyncUntilTimeout = bridgeTimeout
	return cs
}

// JoinedMembers lists the members of a room as seen by the bot, for
// diagnostics.
func (h *Harness) JoinedMembers(roomID string) string {
	h.t.Helper()
	res := h.BotClient().Do(h.t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "joined_members"})
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	return fmt.Sprintf("HTTP %d %s", res.StatusCode, string(body))
}

// CreateMappedRoom creates a public Matrix room as the given user, maps the
// channel to it and waits until the bridge bot has joined.
func (h *Harness) CreateMappedRoom(owner *client.CSAPI, channelID, roomName string) string {
	h.t.Helper()
	roomID := owner.MustCreateRoom(h.t, map[string]interface{}{
		"preset": "public_chat",
		"name":   roomName,
	})
	h.MapChannelToRoom(channelID, roomID)
	joined := TryFor(bridgeTimeout, func() bool {
		res := owner.Do(h.t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.member", h.BridgeBotUserID()})
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != 200 {
			return false
		}
		return strings.Contains(string(client.ParseJSON(h.t, res)), `"join"`)
	})
	if !joined {
		h.t.Fatalf("bridge bot %s did not join room %s after /matrix map; members as seen by the bot: %s",
			h.BridgeBotUserID(), roomID, h.JoinedMembers(roomID))
	}
	return roomID
}

// GrantBridgePower gives the bridge bot a power level in a room the owner
// created, as an admin would for a room mapped with /matrix map. Without it
// the bot cannot set the state events that calls and widgets use.
func (h *Harness) GrantBridgePower(owner *client.CSAPI, roomID string, level int) {
	h.t.Helper()
	res := owner.MustDo(h.t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.power_levels"})
	var levels map[string]any
	if err := json.Unmarshal(client.ParseJSON(h.t, res), &levels); err != nil {
		h.t.Fatalf("decode power levels: %v", err)
	}
	users, _ := levels["users"].(map[string]any)
	if users == nil {
		users = map[string]any{}
	}
	users[h.BridgeBotUserID()] = level
	levels["users"] = users
	owner.MustDo(h.t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.power_levels"}, client.WithJSONBody(h.t, levels))
}

// WaitForChannelRoom waits until the bridge has created and mapped the room
// for a new channel (it does so in the background) and returns the room ID.
func (h *Harness) WaitForChannelRoom(channel *model.Channel) string {
	h.t.Helper()
	var roomID string
	WaitFor(h.t, bridgeTimeout, "a room for channel "+channel.DisplayName, func() bool {
		resp, _, err := h.Admin.ExecuteCommand(context.Background(), channel.Id, "/matrix list")
		if err != nil {
			return false
		}
		m := regexp.MustCompile(regexp.QuoteMeta(channel.DisplayName) + " → `(![^`]+)`").FindStringSubmatch(resp.Text)
		if m == nil {
			return false
		}
		roomID = m[1]
		return true
	})
	return roomID
}

// WaitFor polls cond until it returns true or the timeout elapses.
func WaitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
		time.Sleep(pollInterval)
	}
}

// findPluginBundle locates the plugin tarball to install.
func findPluginBundle(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("E2E_PLUGIN_BUNDLE"); path != "" {
		return path
	}
	matches, _ := filepath.Glob(filepath.Join("..", "dist", PluginID+"-*.tar.gz"))
	if len(matches) == 0 {
		t.Fatalf("no plugin bundle found: run `make dist` first or set E2E_PLUGIN_BUNDLE")
	}
	// Prefer the newest bundle.
	newest := matches[0]
	var newestTime time.Time
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && info.ModTime().After(newestTime) {
			newest, newestTime = m, info.ModTime()
		}
	}
	return newest
}
