package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/mas"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// /matrix doctor runs the checks a first setup tends to fail, each with what
// to do about it. A check never changes anything.

type doctorStatus int

const (
	doctorOK doctorStatus = iota
	doctorWarn
	doctorFail
)

type doctorCheck struct {
	Status doctorStatus
	Title  string
	Detail string // what was found, or what to do
}

func (c doctorCheck) line() string {
	icon := map[doctorStatus]string{doctorOK: "✅", doctorWarn: "⚠️", doctorFail: "❌"}[c.Status]
	if c.Detail == "" {
		return fmt.Sprintf("%s **%s**", icon, c.Title)
	}
	return fmt.Sprintf("%s **%s** — %s", icon, c.Title, c.Detail)
}

// registrationID is the id: of the registration file the setup guide and the
// downloadable file use.
const registrationID = "mattermost-bridge"

// Doctor implements command.PluginAccessor: it runs the setup checks and,
// for a bridged channel, the checks for that channel.
func (p *Plugin) Doctor(channelID string) string {
	checks := p.doctorChecks(channelID)
	var sb strings.Builder
	sb.WriteString("🩺 **Bridge check**\n\n")
	worst := doctorOK
	for _, c := range checks {
		sb.WriteString(c.line() + "\n")
		if c.Status > worst {
			worst = c.Status
		}
	}
	switch worst {
	case doctorOK:
		sb.WriteString("\nEverything checked is in order.")
	case doctorWarn:
		sb.WriteString("\nThe bridge works; the warnings are worth a look.")
	default:
		sb.WriteString("\nFix the ❌ items first; the rest may follow from them.")
	}
	return sb.String()
}

func (p *Plugin) doctorChecks(channelID string) []doctorCheck {
	cfg := p.getConfiguration()
	var out []doctorCheck
	add := func(s doctorStatus, title, detail string) { out = append(out, doctorCheck{s, title, detail}) }

	// Settings
	var missing []string
	if cfg.MatrixServerURL == "" {
		missing = append(missing, "homeserver URL")
	}
	if cfg.MatrixASToken == "" {
		missing = append(missing, "appservice token")
	}
	if cfg.MatrixHSToken == "" {
		missing = append(missing, "homeserver token")
	}
	if cfg.MatrixServerDomain == "" {
		missing = append(missing, "Matrix server name")
	}
	switch {
	case len(missing) > 0:
		add(doctorFail, "Settings", "not set: "+strings.Join(missing, ", ")+". They are in System Console → Plugins → Mattermost bridge for Matrix.")
	case !cfg.EnableSync:
		add(doctorFail, "Message sync is off", "turn on **Enable Message Sync** in the plugin settings.")
	default:
		add(doctorOK, "Settings", "")
	}
	if p.matrixClient == nil || len(missing) > 0 {
		return out
	}

	// Homeserver
	versions, err := p.matrixClient.ServerVersions()
	if err != nil || len(versions) == 0 {
		add(doctorFail, "Homeserver reachable", fmt.Sprintf("could not reach %s from Mattermost (%v). Check the URL and that Mattermost may connect to it.", cfg.MatrixServerURL, err))
		return out
	}
	add(doctorOK, "Homeserver reachable", fmt.Sprintf("speaks Matrix %s", versions[len(versions)-1]))

	// Appservice token and server name
	botID, err := p.matrixClient.WhoAmIAsBridge()
	switch {
	case err != nil:
		add(doctorFail, "Appservice token", "the homeserver does not accept it. It must match `as_token` in the registration file, and the homeserver must have loaded that file (restart it after installing the file).")
	case botID != p.BridgeBotUserID():
		add(doctorFail, "Matrix server name", fmt.Sprintf("the homeserver says the bot is %s but the bridge expects %s. Set **Matrix Server Name** to the homeserver's `server_name`, and check `sender_localpart` in the registration.", botID, p.BridgeBotUserID()))
	default:
		add(doctorOK, "Appservice token and server name", "the bot is "+botID)
	}

	// The homeserver calling the plugin back
	if ms, err := p.matrixClient.PingAppservice(registrationID); err != nil {
		add(doctorWarn, "Homeserver can reach the plugin", fmt.Sprintf("the ping failed (%v). The `url:` in the registration must be an address the homeserver can open (`https://<mattermost>/plugins/%s`), the `hs_token` must match, and the registration `id:` is expected to be `%s`.", err, manifestID(), registrationID))
	} else {
		add(doctorOK, "Homeserver can reach the plugin", fmt.Sprintf("round trip %d ms, so the registration URL and homeserver token are right", ms))
	}

	// Features that depend on the registration or the homeserver
	if cfg.EnableEncryption {
		if p.EncryptionSupported() {
			add(doctorOK, "Encrypted rooms", "the bot's device is running")
		} else {
			add(doctorFail, "Encrypted rooms", "switched on but the bot's device is not running. The registration needs `org.matrix.msc3202: true` and the homeserver the experimental settings in docs/ENCRYPTION.md; the log says what failed.")
		}
	}
	if cfg.EnableMirrorMode {
		add(doctorWarn, "Mirror mode namespace", "this check cannot run on its own: the registration must reserve the whole user namespace (`regex: \"@.*:"+cfg.MatrixServerDomain+"\"`, exclusive); without it creating accounts fails with M_EXCLUSIVE or M_USER_IN_USE.")
	}
	if cfg.masConfigured() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := mas.New(cfg.MASURL, cfg.MASAdminClientID, cfg.MASAdminClientSecret).Check(ctx)
		cancel()
		if err != nil {
			add(doctorFail, "Matrix Authentication Service", fmt.Sprintf("could not get an admin token (%v). Check the URL, the client id and secret, and that the client is in `policy.data.admin_clients`.", err))
		} else {
			add(doctorOK, "Matrix Authentication Service", "admin API reachable")
		}
	}
	if cfg.callsMode() == callsModeElementCall && cfg.ElementCallURL == "" {
		add(doctorWarn, "Element Call", "calls mode is Element Call but no URL template is set.")
	}
	if cfg.ElementURL == "" {
		add(doctorWarn, "Element Web address", "not set: \"Open in Element\" and message links fall back to matrix.to.")
	}

	// Delivery
	if dead := p.DeadLetters(); dead > 0 {
		add(doctorFail, "Delivery", fmt.Sprintf("%d changes could not be delivered and were set aside; `/matrix retry` tries them again.", dead))
	} else if pending := p.pendingInbound() + p.OutboundPending(); pending > 25 {
		add(doctorWarn, "Delivery", fmt.Sprintf("%d changes are waiting; if that number is not falling, the homeserver or Mattermost is slow or down.", pending))
	} else {
		add(doctorOK, "Delivery", "nothing is stuck")
	}
	if p.matrixClient.RateLimitFactor() < 1 {
		add(doctorWarn, "Rate limits", fmt.Sprintf("the homeserver has been answering 429; the bridge is running at %.0f%% of its configured rate. Raise the homeserver's limits for the appservice (`rate_limited: false`).", p.matrixClient.RateLimitFactor()*100))
	}

	// The current channel
	if channelID != "" {
		out = append(out, p.doctorChannel(channelID)...)
	}
	return out
}

func manifestID() string { return "com.mattermost.plugin-matrix-bridge" }

func (p *Plugin) doctorChannel(channelID string) []doctorCheck {
	var out []doctorCheck
	add := func(s doctorStatus, title, detail string) { out = append(out, doctorCheck{s, title, detail}) }

	identifier, _ := p.kvstore.Get(kvstore.BuildChannelMappingKey(channelID))
	if len(identifier) == 0 {
		add(doctorWarn, "This channel", "is not bridged. `/matrix create` makes a room for it, `/matrix map` connects an existing one.")
		return out
	}
	roomID := string(identifier)
	if strings.HasPrefix(roomID, "#") {
		resolved, err := p.matrixClient.ResolveRoomAlias(roomID)
		if err != nil {
			add(doctorFail, "This channel's room", fmt.Sprintf("%s does not resolve (%v). The room may have been deleted; `/matrix unmap` and map it again.", roomID, err))
			return out
		}
		roomID = resolved
	}
	back, _ := p.kvstore.Get(kvstore.BuildRoomMappingKey(string(identifier)))
	if len(back) == 0 {
		if altBack, _ := p.kvstore.Get(kvstore.BuildRoomMappingKey(roomID)); len(altBack) == 0 {
			add(doctorFail, "This channel's mapping", "the room does not point back at the channel, so messages from Matrix are not posted. `/matrix unmap` and map it again.")
		}
	}
	in, err := p.matrixClient.BotIsInRoom(roomID)
	switch {
	case err != nil:
		add(doctorWarn, "Bridge bot in the room", fmt.Sprintf("could not check (%v)", err))
	case !in:
		add(doctorFail, "Bridge bot in the room", "the bot is not in "+roomID+"; without it nothing is sent or received. Invite it, or `/matrix join` again.")
	default:
		add(doctorOK, "Bridge bot in the room", "")
		if level, err := p.matrixClient.BotPowerLevel(roomID, p.BridgeBotUserID()); err == nil && level < 50 {
			add(doctorWarn, "Bridge bot power level", fmt.Sprintf("it is %d; calls, widgets and the room name need 50 or more. Give the bot power in the room's settings.", level))
		}
	}
	if encrypted, err := p.matrixClient.IsRoomEncrypted(roomID); err == nil && encrypted && !p.EncryptionSupported() {
		add(doctorFail, "This room is encrypted", "and encrypted rooms are not switched on, so its messages are not bridged. See docs/ENCRYPTION.md.")
	}
	if channel, appErr := p.API.GetChannel(channelID); appErr == nil && channel != nil && channel.IsShared() {
		add(doctorWarn, "Delivery path", "this channel is shared through Connected Workspaces, so its messages take the Shared Channels path. That path is not covered by the automated tests (they need a licensed server); every other channel uses the plugin-hook path. See docs/ARCHITECTURE.md, \"Two delivery paths\".")
	}
	if p.channelReadOnly(channelID) {
		add(doctorWarn, "This channel is read-only", "from a cut-over: people cannot post here. `/matrix migrate-team writable confirm` undoes it.")
	}
	return out
}
