package e2e

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	defaultMattermostImage = "mattermost/mattermost-enterprise-edition:12.1.0"
	defaultPostgresImage   = "postgres:16-alpine"

	adminUsername = "admin"
	adminPassword = "E2e-Admin-Password-1!"
	adminEmail    = "admin@example.com"
)

// MattermostOptions controls how the Mattermost container is started.
type MattermostOptions struct {
	// Network is the Docker network to attach to (the Complement network).
	Network string
	// Alias is the DNS alias other containers use to reach Mattermost.
	Alias string
}

// MattermostServer is a running Mattermost container.
type MattermostServer struct {
	// URL is the base URL reachable from the test host.
	URL string
	// InternalURL is the base URL reachable from other containers.
	InternalURL string
}

// BridgeConfig is the plugin configuration applied after installation.
type BridgeConfig struct {
	MatrixServerURL    string
	MatrixServerDomain string
	ASToken            string
	HSToken            string
}

// StartMattermost starts Postgres and Mattermost on the given network.
//
// The Enterprise image runs unlicensed (Team Edition feature set) unless
// E2E_MM_LICENSE provides a license, which additionally enables Shared
// Channels.
func StartMattermost(t *testing.T, ctx context.Context, opts MattermostOptions) *MattermostServer {
	t.Helper()

	pgImage := envOr("E2E_POSTGRES_IMAGE", defaultPostgresImage)
	mmImage := envOr("E2E_MATTERMOST_IMAGE", defaultMattermostImage)
	dbAlias := opts.Alias + "-db"

	pg, err := testcontainers.Run(ctx, pgImage,
		testcontainers.WithEnv(map[string]string{
			"POSTGRES_USER":     "mmuser",
			"POSTGRES_PASSWORD": "mmpass",
			"POSTGRES_DB":       "mattermost",
		}),
		network.WithNetworkName([]string{dbAlias}, opts.Network),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(2*time.Minute),
		),
	)
	require.NoError(t, err, "start postgres")
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })

	env := map[string]string{
		"MM_SQLSETTINGS_DRIVERNAME": "postgres",
		"MM_SQLSETTINGS_DATASOURCE": fmt.Sprintf("postgres://mmuser:mmpass@%s:5432/mattermost?sslmode=disable&connect_timeout=10", dbAlias),
		// Plugins need a site URL; this is the in-network address Synapse uses too.
		"MM_SERVICESETTINGS_SITEURL":                    fmt.Sprintf("http://%s:8065", opts.Alias),
		"MM_SERVICESETTINGS_LISTENADDRESS":              ":8065",
		"MM_SERVICESETTINGS_ENABLELOCALMODE":            "false",
		"MM_SERVICESETTINGS_ENABLEDEVELOPER":            "true",
		"MM_SERVICESETTINGS_ENABLETESTING":              "true",
		"MM_SERVICESETTINGS_ENABLEUSERACCESSTOKENS":     "true",
		"MM_SERVICESETTINGS_ALLOWCORSFROM":              "*",
		"MM_SERVICESETTINGS_ENABLEONBOARDINGFLOW":       "false",
		"MM_TEAMSETTINGS_ENABLEOPENSERVER":              "true",
		"MM_TEAMSETTINGS_MAXUSERSPERTEAM":               "1000",
		"MM_PLUGINSETTINGS_ENABLE":                      "true",
		"MM_PLUGINSETTINGS_ENABLEUPLOADS":               "true",
		"MM_PLUGINSETTINGS_AUTOMATICPREPACKAGEDPLUGINS": "false",
		"MM_LOGSETTINGS_CONSOLELEVEL":                   "INFO",
		"MM_LOGSETTINGS_ENABLEFILE":                     "false",
		"MM_EMAILSETTINGS_REQUIREEMAILVERIFICATION":     "false",
		"MM_EMAILSETTINGS_SENDEMAILNOTIFICATIONS":       "false",
		"MM_FILESETTINGS_ENABLEFILEATTACHMENTS":         "true",
	}
	if license := os.Getenv("E2E_MM_LICENSE"); license != "" {
		env["MM_LICENSE"] = license
		env["MM_CONNECTEDWORKSPACESSETTINGS_ENABLESHAREDCHANNELS"] = "true"
		env["MM_CONNECTEDWORKSPACESSETTINGS_ENABLEREMOTECLUSTERSERVICE"] = "true"
	}

	mm, err := testcontainers.Run(ctx, mmImage,
		testcontainers.WithEnv(env),
		testcontainers.WithExposedPorts("8065/tcp"),
		network.WithNetworkName([]string{opts.Alias}, opts.Network),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/api/v4/system/ping").WithPort("8065/tcp").WithStartupTimeout(4*time.Minute),
		),
	)
	require.NoError(t, err, "start mattermost")
	t.Cleanup(func() {
		if t.Failed() {
			dumpContainerLogs(t, mm, "mattermost")
		}
		_ = mm.Terminate(context.Background())
	})

	host, err := mm.Host(ctx)
	require.NoError(t, err)
	port, err := mm.MappedPort(ctx, "8065/tcp")
	require.NoError(t, err)

	return &MattermostServer{
		URL:         fmt.Sprintf("http://%s:%s", host, port.Port()),
		InternalURL: fmt.Sprintf("http://%s:8065", opts.Alias),
	}
}

// CreateAdmin creates the first user, who becomes system administrator, and
// returns a logged-in client.
func (m *MattermostServer) CreateAdmin(t *testing.T, ctx context.Context) (*model.Client4, *model.User) {
	t.Helper()
	cl := model.NewAPIv4Client(m.URL)
	user, _, err := cl.CreateUser(ctx, &model.User{
		Username: adminUsername,
		Email:    adminEmail,
		Password: adminPassword,
	})
	require.NoError(t, err, "create admin user")
	_, _, err = cl.Login(ctx, adminUsername, adminPassword)
	require.NoError(t, err, "login as admin")
	return cl, user
}

// CreateTeam creates an open team.
func (m *MattermostServer) CreateTeam(t *testing.T, ctx context.Context, admin *model.Client4, name string) *model.Team {
	t.Helper()
	team, _, err := admin.CreateTeam(ctx, &model.Team{
		Name:        name,
		DisplayName: "E2E",
		Type:        model.TeamOpen,
	})
	require.NoError(t, err, "create team")
	return team
}

// InstallBridge uploads the plugin bundle, configures and enables it.
func (m *MattermostServer) InstallBridge(t *testing.T, ctx context.Context, admin *model.Client4, cfg BridgeConfig) {
	t.Helper()

	bundle := findPluginBundle(t)
	f, err := os.Open(bundle)
	require.NoError(t, err, "open plugin bundle %s", bundle)
	defer func() { _ = f.Close() }()

	manifest, _, err := admin.UploadPluginForced(ctx, f)
	require.NoError(t, err, "upload plugin %s", bundle)
	require.Equal(t, PluginID, manifest.Id)

	patch := &model.Config{}
	patch.LogSettings.ConsoleLevel = model.NewPointer("DEBUG")
	patch.LogSettings.FileLevel = model.NewPointer("DEBUG")
	patch.PluginSettings.Plugins = map[string]map[string]any{
		PluginID: {
			"matrix_server_url":    cfg.MatrixServerURL,
			"matrix_server_domain": cfg.MatrixServerDomain,
			"matrix_as_token":      cfg.ASToken,
			"matrix_hs_token":      cfg.HSToken,
			"enable_sync":          true,
			"rate_limiting_mode":   "disabled",
		},
	}
	_, _, err = admin.PatchConfig(ctx, patch)
	require.NoError(t, err, "configure plugin")

	_, err = admin.EnablePlugin(ctx, PluginID)
	require.NoError(t, err, "enable plugin")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func dumpContainerLogs(t *testing.T, c testcontainers.Container, name string) {
	t.Helper()
	ctx := context.Background()

	// Prefer the server's own log file, filtered to the plugin, because the
	// docker log stream is not reliably complete at this point.
	if code, out, err := c.Exec(ctx, []string{"sh", "-c", "grep -F '" + PluginID + "' /mattermost/logs/mattermost.log | tail -n 400"}); err == nil && code == 0 {
		data, _ := io.ReadAll(out)
		if len(data) > 0 {
			t.Logf("=== %s plugin logs (from log file) ===\n%s", name, string(data))
			return
		}
	}

	reader, err := c.Logs(ctx)
	if err != nil {
		return
	}
	defer func() { _ = reader.Close() }()
	all, _ := io.ReadAll(reader)
	var pluginLines []string
	for _, line := range strings.Split(string(all), "\n") {
		if strings.Contains(line, PluginID) {
			pluginLines = append(pluginLines, line)
		}
	}
	if len(pluginLines) > 400 {
		pluginLines = pluginLines[len(pluginLines)-400:]
	}
	t.Logf("=== %s plugin logs (last %d lines) ===\n%s", name, len(pluginLines), strings.Join(pluginLines, "\n"))
}
