package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// A sign-in stack for testing shared login: Postgres, an OpenID Connect
// provider (Dex), Matrix Authentication Service and Synapse delegating to it,
// with an appservice registered the way mirror mode registers the bridge.
//
// Containers reach each other by these names. The test host reaches them
// through mapped ports, so the HTTP client returned by Client dials the names
// through a table: redirects that carry the in-network address keep working.

const (
	defaultDexImage     = "ghcr.io/dexidp/dex:v2.41.1"
	defaultMASImage     = "ghcr.io/element-hq/matrix-authentication-service:latest"
	defaultSynapseImage = "ghcr.io/element-hq/synapse:latest"

	masServerName = "hs.test"
	masProviderID = "01HQW90Z35CMXFJWQPHC3BGZGQ"
	masAdminID    = "01J44QC8BCY7FCFM7WGHQGKMTJ"
	masAdminPass  = "mas-admin-client-secret"
	dexClientID   = "mas"
	dexClientPass = "dex-client-secret-for-mas"
	dexUserPass   = "Dex-User-Password-1"
	asToken       = "as-token-for-the-bridge-0123456789"
	hsToken       = "hs-token-for-synapse-0123456789"
)

// masStackOptions picks the sign-in provider behind MAS.
type masStackOptions struct {
	// Mattermost uses a real Mattermost (an OAuth 2.0 provider, not OpenID
	// Connect) instead of Dex.
	Mattermost bool
}

type masStack struct {
	// In-network base URLs, as the containers and the OIDC redirects see them.
	MASBase, DexBase, SynapseBase string
	// MMBase, MMAdmin and MMApp are set when Mattermost is the provider.
	MMBase  string
	MMAdmin *model.Client4
	MMApp   *model.OAuthApp
	mm      *MattermostServer
	// Hosts maps "name:port" to the host:port that reaches it from the test.
	hosts map[string]string
}

// Client returns an HTTP client that resolves the stack's in-network names,
// keeps cookies and follows no redirects, so a flow can be walked step by step.
func (s *masStack) Client() *http.Client {
	jar, _ := cookiejar.New(nil)
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if mapped, ok := s.hosts[addr]; ok {
				addr = mapped
			}
			return dialer.DialContext(ctx, network, addr)
		}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func startMASStack(t *testing.T) *masStack { return startMASStackWith(t, masStackOptions{}) }

func startMASStackWith(t *testing.T, opts masStackOptions) *masStack {
	t.Helper()
	ctx := context.Background()
	stack := &masStack{
		MASBase:     "http://mas:8080",
		DexBase:     "http://dex:5556/dex",
		SynapseBase: "http://synapse:8008",
		hosts:       map[string]string{},
	}

	nw, err := network.New(ctx)
	require.NoError(t, err, "create network")
	t.Cleanup(func() { _ = nw.Remove(context.Background()) })

	// Postgres, with a C-collation database for each of Synapse and MAS.
	initSQL := `CREATE DATABASE synapse ENCODING 'UTF8' LC_COLLATE='C' LC_CTYPE='C' TEMPLATE=template0;
CREATE DATABASE mas ENCODING 'UTF8' LC_COLLATE='C' LC_CTYPE='C' TEMPLATE=template0;
`
	pg, err := testcontainers.Run(ctx, envOr("E2E_POSTGRES_IMAGE", defaultPostgresImage),
		testcontainers.WithEnv(map[string]string{"POSTGRES_USER": "postgres", "POSTGRES_PASSWORD": "pw"}),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			Reader: strings.NewReader(initSQL), ContainerFilePath: "/docker-entrypoint-initdb.d/init.sql", FileMode: 0o644,
		}),
		network.WithNetwork([]string{"postgres"}, nw),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(2*time.Minute)),
	)
	require.NoError(t, err, "start postgres")
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })

	if opts.Mattermost {
		stack.startMattermostProvider(t, ctx, nw)
	} else {
		// The OpenID Connect provider both Mattermost and MAS would trust.
		hash, err := bcrypt.GenerateFromPassword([]byte(dexUserPass), 10)
		require.NoError(t, err)
		dexConfig, err := yaml.Marshal(map[string]any{
			"issuer":           stack.DexBase,
			"storage":          map[string]any{"type": "memory"},
			"web":              map[string]any{"http": "0.0.0.0:5556"},
			"oauth2":           map[string]any{"skipApprovalScreen": true},
			"enablePasswordDB": true,
			"staticClients": []map[string]any{
				{"id": dexClientID, "secret": dexClientPass, "name": "MAS", "redirectURIs": []string{stack.MASBase + "/upstream/callback/" + masProviderID}},
				{"id": "probe", "secret": "probe-secret", "name": "Probe", "redirectURIs": []string{"http://probe.invalid/callback"}},
			},
			"staticPasswords": []map[string]any{
				{"email": "ada@example.com", "hash": string(hash), "username": "ada", "userID": "08a8684b-db88-4b73-90a9-3cd1661f5466"},
				{"email": "bob@example.com", "hash": string(hash), "username": "bob", "userID": "18a8684b-db88-4b73-90a9-3cd1661f5467"},
			},
		})
		require.NoError(t, err)
		dex, err := testcontainers.Run(ctx, envOr("E2E_DEX_IMAGE", defaultDexImage),
			testcontainers.WithCmd("dex", "serve", "/etc/dex/config.yaml"),
			testcontainers.WithFiles(testcontainers.ContainerFile{Reader: bytes.NewReader(dexConfig), ContainerFilePath: "/etc/dex/config.yaml", FileMode: 0o644}),
			testcontainers.WithExposedPorts("5556/tcp"),
			network.WithNetwork([]string{"dex"}, nw),
			testcontainers.WithWaitStrategy(wait.ForHTTP("/dex/.well-known/openid-configuration").WithPort("5556/tcp").WithStartupTimeout(2*time.Minute)),
		)
		require.NoError(t, err, "start dex")
		t.Cleanup(func() {
			if t.Failed() {
				dumpDockerLogs(t, dex, "dex")
			}
			_ = dex.Terminate(context.Background())
		})
		stack.hosts["dex:5556"] = mappedAddr(t, dex, "5556/tcp")

	}

	// Matrix Authentication Service: a generated base configuration (so the
	// schema is the image's own) with this stack's settings on top.
	masConfig := masConfiguration(t, stack)
	mas, err := testcontainers.Run(ctx, envOr("E2E_MAS_IMAGE", defaultMASImage),
		testcontainers.WithCmd("server", "--config", "/config/config.yaml"),
		testcontainers.WithFiles(testcontainers.ContainerFile{Reader: bytes.NewReader(masConfig), ContainerFilePath: "/config/config.yaml", FileMode: 0o644}),
		testcontainers.WithExposedPorts("8080/tcp"),
		network.WithNetwork([]string{"mas"}, nw),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/.well-known/openid-configuration").WithPort("8080/tcp").WithStartupTimeout(3*time.Minute)),
	)
	t.Cleanup(func() {
		if mas != nil && t.Failed() {
			dumpDockerLogs(t, mas, "mas")
		}
		if mas != nil {
			_ = mas.Terminate(context.Background())
		}
	})
	require.NoError(t, err, "start MAS")
	stack.hosts["mas:8080"] = mappedAddr(t, mas, "8080/tcp")

	// Synapse delegating sign-in to MAS, with the bridge's appservice.
	files := synapseFiles(t)
	syn, err := testcontainers.Run(ctx, envOr("E2E_SYNAPSE_IMAGE", defaultSynapseImage),
		testcontainers.WithEnv(map[string]string{"SYNAPSE_CONFIG_PATH": "/data/homeserver.yaml"}),
		testcontainers.WithFiles(files...),
		testcontainers.WithExposedPorts("8008/tcp"),
		network.WithNetwork([]string{"synapse"}, nw),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/health").WithPort("8008/tcp").WithStartupTimeout(3*time.Minute)),
	)
	t.Cleanup(func() {
		if syn != nil && t.Failed() {
			dumpDockerLogs(t, syn, "synapse")
		}
		if syn != nil {
			_ = syn.Terminate(context.Background())
		}
	})
	require.NoError(t, err, "start Synapse")
	stack.hosts["synapse:8008"] = mappedAddr(t, syn, "8008/tcp")

	if stack.mm != nil {
		// The bridge plugin serves the token endpoint MAS uses for Mattermost.
		stack.mm.InstallBridge(t, ctx, stack.MMAdmin, BridgeConfig{
			MatrixServerURL: stack.SynapseBase, MatrixServerDomain: masServerName, ASToken: asToken, HSToken: hsToken,
			MASURL: stack.MASBase,
		})
	}
	return stack
}

func mappedAddr(t *testing.T, c testcontainers.Container, port string) string {
	t.Helper()
	ctx := context.Background()
	host, err := c.Host(ctx)
	require.NoError(t, err)
	p, err := c.MappedPort(ctx, port)
	require.NoError(t, err)
	return net.JoinHostPort(host, p.Port())
}

// masConfiguration builds MAS's configuration: the image's generated defaults
// with this stack's endpoints, an admin client for the bridge and Dex as the
// upstream provider.
func masConfiguration(t *testing.T, s *masStack) []byte {
	t.Helper()
	out, err := exec.Command("docker", "run", "--rm", envOr("E2E_MAS_IMAGE", defaultMASImage), "config", "generate").Output()
	require.NoError(t, err, "generate a base MAS configuration")
	// The generator logs to stderr and writes the YAML to stdout.
	var cfg map[string]any
	require.NoError(t, yaml.Unmarshal(out, &cfg), "parse the generated MAS configuration")

	http := cfg["http"].(map[string]any)
	listeners := http["listeners"].([]any)
	web := listeners[0].(map[string]any)
	web["resources"] = append(web["resources"].([]any), map[string]any{"name": "adminapi"})
	// IPv4 only: not every Docker host has IPv6.
	web["binds"] = []any{map[string]any{"address": "0.0.0.0:8080"}}
	http["public_base"] = s.MASBase + "/"
	http["issuer"] = s.MASBase + "/"
	cfg["database"].(map[string]any)["uri"] = "postgresql://postgres:pw@postgres/mas"
	cfg["passwords"] = map[string]any{"enabled": false}
	cfg["matrix"] = map[string]any{"kind": "synapse", "homeserver": masServerName, "secret": masSharedSecret, "endpoint": s.SynapseBase + "/"}
	cfg["clients"] = []any{map[string]any{"client_id": masAdminID, "client_auth_method": "client_secret_basic", "client_secret": masAdminPass}}
	cfg["policy"] = map[string]any{"data": map[string]any{"admin_clients": []any{masAdminID}}}
	if s.MMApp != nil {
		cfg["upstream_oauth2"] = map[string]any{"providers": []any{map[string]any{
			"id": masProviderID, "human_name": "Mattermost",
			"client_id": s.MMApp.Id, "client_secret": s.MMApp.ClientSecret,
			// Mattermost is an OAuth 2.0 provider, not OpenID Connect: no discovery
			// and no id token, so MAS reads the person from the userinfo endpoint.
			"token_endpoint_auth_method": "client_secret_post",
			"discovery_mode":             "disabled",
			"authorization_endpoint":     s.MMBase + "/oauth/authorize",
			// Mattermost answers the token request with empty "scope" and "id_token"
			// fields, which MAS rejects; the plugin passes the exchange on without them.
			"token_endpoint":    s.MMBase + "/plugins/" + PluginID + "/sso/mattermost/token",
			"userinfo_endpoint": s.MMBase + "/api/v4/users/me",
			"fetch_userinfo":    true,
			"scope":             "user",
			"claims_imports": map[string]any{
				"subject":     map[string]any{"template": "{{ user.id }}"},
				"localpart":   map[string]any{"action": "require", "template": "{{ user.username }}", "on_conflict": "fail"},
				"displayname": map[string]any{"action": "suggest", "template": "{{ user.nickname | default(value=user.username) }}"},
				"email":       map[string]any{"action": "suggest", "template": "{{ user.email }}"},
			},
		}}}
		data, err := yaml.Marshal(cfg)
		require.NoError(t, err)
		return data
	}
	cfg["upstream_oauth2"] = map[string]any{"providers": []any{map[string]any{
		"id": masProviderID, "human_name": "Test login", "issuer": s.DexBase,
		"client_id": dexClientID, "client_secret": dexClientPass, "token_endpoint_auth_method": "client_secret_basic",
		"scope": "openid profile email",
		// The test provider is plain http; MAS only discovers https issuers otherwise.
		"discovery_mode": "insecure",
		"claims_imports": map[string]any{
			// Dex releases the username as "name"; most providers use preferred_username.
			"localpart":   map[string]any{"action": "require", "template": "{{ user.name }}", "on_conflict": "fail"},
			"displayname": map[string]any{"action": "suggest", "template": "{{ user.name }}"},
			"email":       map[string]any{"action": "suggest", "template": "{{ user.email }}"},
		},
	}}}
	data, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	return data
}

// startMattermostProvider runs a real Mattermost as the identity provider:
// its OAuth 2.0 provider is switched on and an application for MAS is created.
func (s *masStack) startMattermostProvider(t *testing.T, ctx context.Context, nw *testcontainers.DockerNetwork) {
	t.Helper()
	s.MMBase = "http://mattermost:8065"
	mm := StartMattermost(t, ctx, MattermostOptions{
		Network: nw.Name,
		Alias:   "mattermost",
		Env:     map[string]string{"MM_SERVICESETTINGS_ENABLEOAUTHSERVICEPROVIDER": "true"},
	})
	s.mm = mm
	s.hosts["mattermost:8065"] = strings.TrimPrefix(mm.URL, "http://")

	admin, _ := mm.CreateAdmin(t, ctx)
	s.MMAdmin = admin
	app, _, err := admin.CreateOAuthApp(ctx, &model.OAuthApp{
		Name:         "Matrix sign-in",
		Description:  "Matrix Authentication Service",
		Homepage:     s.MASBase,
		CallbackUrls: []string{s.MASBase + "/upstream/callback/" + masProviderID},
		IsTrusted:    true,
	})
	require.NoError(t, err, "create the OAuth application for MAS")
	s.MMApp = app
}

const masSharedSecret = "synapse-to-mas-shared-secret-0123456789"

// synapseFiles is Synapse's configuration: delegating to MAS, the bridge's
// appservice registration in mirror mode (it owns the whole user namespace),
// and a signing key.
func synapseFiles(t *testing.T) []testcontainers.ContainerFile {
	t.Helper()
	seed := make([]byte, 32)
	_, err := rand.Read(seed)
	require.NoError(t, err)
	signingKey := fmt.Sprintf("ed25519 a_test %s\n", base64.RawStdEncoding.EncodeToString(seed))

	homeserver := fmt.Sprintf(`server_name: %q
pid_file: /tmp/homeserver.pid
listeners:
  - port: 8008
    tls: false
    type: http
    bind_addresses: ['0.0.0.0']
    x_forwarded: true
    resources:
      - names: [client, federation]
        compress: false
database:
  name: psycopg2
  args:
    user: postgres
    password: pw
    database: synapse
    host: postgres
    cp_min: 2
    cp_max: 5
log_config: /data/log.config
media_store_path: /tmp/media_store
signing_key_path: /data/signing.key
report_stats: false
suppress_key_server_warning: true
trusted_key_servers: []
enable_registration: false
app_service_config_files:
  - /data/appservice.yaml
matrix_authentication_service:
  enabled: true
  endpoint: http://mas:8080/
  secret: %q
rc_message: {per_second: 1000, burst_count: 1000}
rc_registration: {per_second: 1000, burst_count: 1000}
rc_joins: {local: {per_second: 1000, burst_count: 1000}}
`, masServerName, masSharedSecret)

	logConfig := `version: 1
formatters:
  precise:
    format: '%(asctime)s - %(name)s - %(lineno)d - %(levelname)s - %(request)s - %(message)s'
handlers:
  console:
    class: logging.StreamHandler
    formatter: precise
loggers:
  synapse.storage.SQL:
    level: INFO
root:
  level: INFO
  handlers: [console]
disable_existing_loggers: false
`
	appservice := fmt.Sprintf(`id: mattermost-bridge
url: http://bridge.invalid:8065/plugins/com.mattermost.plugin-matrix-bridge
as_token: %s
hs_token: %s
sender_localpart: _mattermost_bridge
rate_limited: false
namespaces:
  users:
    - exclusive: true
      regex: "@_mattermost_.*:hs\\.test"
    - exclusive: true
      regex: "@.*:hs\\.test"
  aliases: []
  rooms: []
`, asToken, hsToken)

	f := func(path, body string) testcontainers.ContainerFile {
		return testcontainers.ContainerFile{Reader: strings.NewReader(body), ContainerFilePath: path, FileMode: 0o644}
	}
	return []testcontainers.ContainerFile{
		f("/data/homeserver.yaml", homeserver),
		f("/data/log.config", logConfig),
		f("/data/appservice.yaml", appservice),
		f("/data/signing.key", signingKey),
	}
}

// dumpDockerLogs prints the tail of a container's output.
func dumpDockerLogs(t *testing.T, c testcontainers.Container, name string) {
	t.Helper()
	r, err := c.Logs(context.Background())
	if err != nil {
		return
	}
	defer func() { _ = r.Close() }()
	data, _ := io.ReadAll(r)
	lines := strings.Split(string(data), "\n")
	if len(lines) > 80 {
		lines = lines[len(lines)-80:]
	}
	t.Logf("=== %s logs ===\n%s", name, strings.Join(lines, "\n"))
}
