package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/mattermost/logr/v2"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
	"github.com/pkg/errors"
	"maunium.net/go/mautrix/id"
)

// maxTransactionBodyBytes bounds one appservice transaction.
const maxTransactionBodyBytes = 8 << 20

// MatrixEvent represents a Matrix event received via Application Service webhook
type MatrixEvent struct {
	Type       string         `json:"type"`
	EventID    string         `json:"event_id"`
	Sender     string         `json:"sender"`
	RoomID     string         `json:"room_id"`
	Content    map[string]any `json:"content"`
	StateKey   *string        `json:"state_key,omitempty"`
	Timestamp  int64          `json:"origin_server_ts"`
	Unsigned   map[string]any `json:"unsigned,omitempty"`
	PrevEvents []string       `json:"prev_events,omitempty"`
}

// MatrixTransaction represents a transaction from the Matrix homeserver
type MatrixTransaction struct {
	Events []MatrixEvent `json:"events"`
	// Ephemeral carries EDUs (typing, presence, receipts) when the registration
	// requests them with receive_ephemeral (spec v1.13).
	Ephemeral []MatrixEvent `json:"ephemeral,omitempty"`
	// MSC2409Ephemeral is the unstable-prefixed field older homeservers use.
	MSC2409Ephemeral []MatrixEvent `json:"de.sorunome.msc2409.ephemeral,omitempty"`
}

// EphemeralEvents returns the ephemeral events of the transaction regardless
// of which field the homeserver used.
func (t *MatrixTransaction) EphemeralEvents() []MatrixEvent {
	if len(t.Ephemeral) > 0 {
		return t.Ephemeral
	}
	return t.MSC2409Ephemeral
}

// handleMatrixTransaction processes a Matrix Application Service transaction
func (p *Plugin) handleMatrixTransaction(w http.ResponseWriter, r *http.Request) {
	// Verify HTTP method
	if r.Method != http.MethodPut {
		p.logger.LogWarn("Matrix webhook received non-PUT request", "method", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract transaction ID from URL path
	vars := mux.Vars(r)
	txnID := vars["txnId"]
	if txnID == "" {
		p.logger.LogWarn("Matrix webhook received request without transaction ID")
		http.Error(w, "Missing transaction ID", http.StatusBadRequest)
		return
	}

	// Authentication is handled by MatrixAuthorizationRequired middleware

	// Check for duplicate transaction (idempotency)
	if p.transactionSeen(txnID) {
		p.logger.LogDebug("Duplicate Matrix transaction ignored", "txn_id", txnID)
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("{}")); err != nil {
			p.logger.LogWarn("Failed to write webhook response", "error", err)
		}
		return
	}

	// A transaction is a handful of JSON events (a few KB in practice); files
	// travel by media URL, not inside it. A fixed cap keeps a bad caller from
	// making the plugin buffer what the file limit would allow.
	r.Body = http.MaxBytesReader(w, r.Body, maxTransactionBodyBytes)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		p.logger.LogError("Failed to read Matrix webhook body", "error", err, "txn_id", txnID)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	// Log the raw transaction body for debugging (only if MM_MATRIX_LOG_FILESPEC is set)
	p.transactionLogger.Debug("Received Matrix transaction", logr.String("txn_id", txnID), logr.String("body", string(body)))

	// Parse transaction JSON
	var transaction MatrixTransaction
	if err := json.Unmarshal(body, &transaction); err != nil {
		p.logger.LogError("Failed to parse Matrix transaction JSON", "error", err, "txn_id", txnID, "body", string(body))
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Room keys and key counts arrive here too when encryption is on; they
	// must be in place before the encrypted events are handled.
	p.handleEncryptionExtras(body)

	p.logger.LogDebug("Processing Matrix transaction", "txn_id", txnID, "event_count", len(transaction.Events))

	// Each event is journaled and queued before the homeserver is told the
	// transaction arrived, so nothing acknowledged can be lost. Events of one
	// room go to one worker, in order. An event that could be neither
	// journaled nor queued makes the homeserver send the transaction again.
	for _, event := range transaction.Events {
		if journaled, err := p.dispatchInbound(event, true); err != nil && !journaled {
			p.logger.LogError("Could not accept a Matrix event; asking the homeserver to retry", "error", err, "event_id", event.EventID, "txn_id", txnID)
			writeMatrixError(w, http.StatusServiceUnavailable, "M_UNKNOWN", "bridge is busy; retry the transaction")
			return
		} else if err != nil {
			p.logger.LogWarn("Event queue full; the event stays in the journal and is replayed shortly", "event_id", event.EventID)
		}
	}

	// Clean up old transactions periodically
	if p.markTransaction(txnID) {
		go p.cleanupOldTransactions()
	}

	// Ephemeral events are cheap and time-sensitive, so handle them inline.
	for _, edu := range transaction.EphemeralEvents() {
		if err := p.processEphemeralEvent(edu); err != nil {
			p.logger.LogDebug("Failed to process ephemeral event", "error", err, "event_type", edu.Type)
		}
	}

	p.logger.LogDebug("Successfully processed Matrix transaction", "txn_id", txnID, "event_count", len(transaction.Events))

	// Return success response
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("{}")); err != nil {
		p.logger.LogWarn("Failed to write webhook response", "error", err)
	}
}

// processMatrixEvent routes a single Matrix event to the appropriate handler
func (p *Plugin) processMatrixEvent(event MatrixEvent) error {
	// The bridge bot accepts invites (so people can DM it) and answers
	// "!mattermost" commands in any room it is in.
	if p.isBridgeBotEvent(event) {
		p.handleBridgeBotMembership(event)
		return nil
	}
	if isBotCommand(event) && !p.isBridgeAccount(event.Sender) && event.Sender != p.BridgeBotUserID() {
		p.handleBotCommand(event)
		return nil
	}

	if event.Type == "m.room.encryption" {
		p.noteEncryption(event)
		return nil
	}
	if event.Type == "m.room.encrypted" {
		// What the bridge sent itself comes back encrypted; nothing to read.
		if p.matrixClient != nil && p.matrixClient.SentByBridge(event.EventID) {
			return nil
		}
		decrypted, ok := p.decryptMatrixEvent(event)
		if !ok {
			return nil
		}
		event = decrypted
	}
	if event.Type == "m.room.member" {
		p.noteMember(event)
	}

	// An event the bridge sent comes back through the transaction; never
	// relay it again. Exact for every account, including a claimed mirror
	// account whose own client shares the sender with the bridge.
	if p.matrixClient != nil && p.matrixClient.SentByBridge(event.EventID) {
		p.logger.LogDebug("Ignoring echo of an event the bridge sent", "event_id", event.EventID, "event_type", event.Type)
		return nil
	}
	// In mirror mode, the first message from an unclaimed mirror account that
	// the bridge did not send means the person is using their account: claim
	// it, then relay the message as theirs.
	p.claimOnFirstOwnMessage(event)

	// Check if we have an existing mapping for this room
	channelID, err := p.getChannelIDFromMatrixRoom(event.RoomID)
	if err != nil {
		return errors.Wrap(err, "failed to get channel ID from Matrix room")
	}

	// If no existing mapping, check if this is a DM that we should create
	if channelID == "" {
		// Only attempt DM creation for room membership events or messages
		if event.Type == "m.room.member" || event.Type == "m.room.message" {
			// Check if this is a Matrix-initiated DM that we should bridge
			newChannelID, err := p.handleMatrixInitiatedDM(event)
			if err != nil {
				p.logger.LogWarn("Failed to handle Matrix-initiated DM", "room_id", event.RoomID, "error", err)
				return nil // Don't error - just skip processing
			}
			if newChannelID != "" {
				channelID = newChannelID
				p.logger.LogInfo("Created new DM channel for Matrix room", "room_id", event.RoomID, "channel_id", channelID)
			}
		}

		// If still no mapping, ignore the event
		if channelID == "" {
			p.logger.LogDebug("Ignoring event from unmapped Matrix room", "room_id", event.RoomID, "event_type", event.Type)
			return nil // Not an error - just not mapped
		}
	}

	// Skip events from accounts only the bridge drives (ghosts and unclaimed
	// mirror accounts) to prevent loops
	if p.isBridgeAccount(event.Sender) {
		p.logger.LogDebug("Ignoring event from a bridge account", "sender", event.Sender, "event_type", event.Type, "room_id", event.RoomID)
		return nil
	}

	p.logger.LogDebug("Processing Matrix event", "event_id", event.EventID, "event_type", event.Type, "sender", event.Sender, "room_id", event.RoomID, "channel_id", channelID)

	// Route event based on type
	switch event.Type {
	case "m.room.message":
		return p.matrixToMattermostBridge.syncMatrixMessageToMattermost(event, channelID)
	case "m.sticker":
		return p.matrixToMattermostBridge.syncMatrixFileToMattermost(stickerAsImage(event), channelID)
	case pollStartStable, pollStartUnstable:
		event.Type = "m.room.message"
		return p.matrixToMattermostBridge.syncMatrixMessageToMattermost(event, channelID)
	case "m.reaction":
		return p.matrixToMattermostBridge.syncMatrixReactionToMattermost(event, channelID)
	case "m.room.member":
		return p.matrixToMattermostBridge.syncMatrixMemberEventToMattermost(event, channelID)
	case "m.room.redaction":
		return p.matrixToMattermostBridge.syncMatrixRedactionToMattermost(event, channelID)
	case callMemberEventLegacy, callMemberEventRTC:
		return p.handleMatrixCallEvent(event, channelID)
	case "m.call.invite", "m.call.answer", "m.call.hangup", "m.call.reject":
		return p.handleLegacyCallEvent(event, channelID)
	case pinnedEventsType:
		if event.Sender == p.BridgeBotUserID() {
			return nil
		}
		return p.syncMatrixPins(event, channelID)
	case "m.room.tombstone":
		if event.Sender == p.BridgeBotUserID() {
			return nil
		}
		return p.archiveChannelFromMatrix(channelID)
	case "m.room.name":
		return p.syncMatrixRoomNameToMattermost(event, channelID)
	case "m.room.topic":
		return p.syncMatrixRoomTopicToMattermost(event, channelID)
	default:
		p.logger.LogDebug("Ignoring unsupported event type", "event_type", event.Type, "event_id", event.EventID, "room_id", event.RoomID)
		return nil
	}
}

// getChannelIDFromMatrixRoom finds the Mattermost channel ID for a Matrix room ID
func (p *Plugin) getChannelIDFromMatrixRoom(roomID string) (string, error) {
	// First check KV store mapping (trusted source): room_mapping_<roomID> -> channelID
	roomMappingKey := kvstore.BuildRoomMappingKey(roomID)
	channelIDBytes, err := p.kvstore.Get(roomMappingKey)
	if err == nil && len(channelIDBytes) > 0 {
		channelID := string(channelIDBytes)
		p.logger.LogDebug("Found channel ID in KV store", "room_id", roomID, "channel_id", channelID)
		return channelID, nil
	}

	// Fallback: get channel ID from Matrix room state (for race condition during room creation)
	if p.matrixClient != nil {
		channelID, err := p.matrixClient.GetMattermostChannelID(roomID)
		if err != nil {
			p.logger.LogDebug("Failed to get channel ID from room state", "room_id", roomID, "error", err)
		} else if channelID != "" {
			p.logger.LogDebug("Found channel ID in room state (no KV mapping yet)", "room_id", roomID, "channel_id", channelID)
			return channelID, nil
		}
	}

	return "", nil // No mapping found
}

// isGhostUser checks if a Matrix user ID belongs to one of our ghost users
func (p *Plugin) isGhostUser(userID string) bool {
	serverDomain := p.matrixServerDomain()
	if serverDomain == "" {
		p.logger.LogDebug("isGhostUser: no Matrix server domain configured", "user_id", userID)
		return false
	}

	// Ghost users follow the pattern: @_mattermost_<mattermost_user_id>:<server_domain>
	return strings.HasPrefix(userID, "@_mattermost_") && strings.HasSuffix(userID, ":"+serverDomain)
}

// handleMatrixInitiatedDM checks if an unmapped Matrix room is a potential DM that should be bridged
// This uses a heuristic approach based on the event information available
func (p *Plugin) handleMatrixInitiatedDM(event MatrixEvent) (string, error) {
	// Handle different event types that might indicate DM creation
	switch event.Type {
	case "m.room.member":
		return p.handleMatrixMemberDM(event)
	case "m.room.message":
		return p.handleMatrixMessageDM(event)
	default:
		return "", nil // Only handle member and message events
	}
}

// handleMatrixMemberDM processes member events to detect DM room creation with ghost users
func (p *Plugin) handleMatrixMemberDM(event MatrixEvent) (string, error) {
	// Only handle join events to avoid creating channels for leave/invite events
	if event.Content == nil {
		p.logger.LogDebug("Member event has no content", "room_id", event.RoomID, "event_id", event.EventID)
		return "", nil
	}

	membership, ok := event.Content["membership"].(string)
	if !ok || (membership != "join" && membership != "invite") {
		p.logger.LogDebug("Member event is not a join or invite", "room_id", event.RoomID, "membership", membership)
		return "", nil
	}

	// The state_key in member events is the user being affected
	if event.StateKey == nil {
		p.logger.LogDebug("Member event has no state_key", "room_id", event.RoomID, "event_id", event.EventID)
		return "", nil
	}

	targetUserID := *event.StateKey // User being invited/joined
	actingUserID := event.Sender    // User doing the invite/join

	p.logger.LogDebug("Processing member event", "room_id", event.RoomID, "membership", membership, "target_user", targetUserID, "acting_user", actingUserID)

	// Check if either user is one of our ghost users
	var ghostUserID, matrixUserID string
	if p.isGhostUser(targetUserID) {
		ghostUserID = targetUserID
		matrixUserID = actingUserID
		p.logger.LogDebug("Detected ghost user as target", "room_id", event.RoomID, "ghost_user", ghostUserID, "matrix_user", matrixUserID, "membership", membership)
	} else if p.isGhostUser(actingUserID) {
		ghostUserID = actingUserID
		matrixUserID = targetUserID
		p.logger.LogDebug("Detected ghost user as actor", "room_id", event.RoomID, "ghost_user", ghostUserID, "matrix_user", matrixUserID, "membership", membership)
	} else {
		// Neither user is a ghost user - not a DM we care about
		p.logger.LogDebug("Neither user is a ghost user", "room_id", event.RoomID, "target_user", targetUserID, "acting_user", actingUserID, "membership", membership)
		return "", nil
	}

	return p.createDMChannelForGhostUser(event.RoomID, ghostUserID, matrixUserID)
}

// handleMatrixMessageDM processes message events that might be in a DM with a ghost user
func (p *Plugin) handleMatrixMessageDM(event MatrixEvent) (string, error) {
	// For message events, we can only work with the sender
	// If the sender is not a ghost user, we can't determine if this should be a DM
	matrixUserID := event.Sender

	// We can't reliably determine DM participants from a message event alone
	// without querying Matrix for room members. Skip for now.
	p.logger.LogDebug("Cannot determine DM participants from message event alone", "room_id", event.RoomID, "sender", matrixUserID)
	return "", nil
}

// createDMChannelForGhostUser creates a Mattermost DM channel for a Matrix DM involving a verified ghost user
func (p *Plugin) createDMChannelForGhostUser(roomID, ghostUserID, matrixUserID string) (string, error) {
	// Extract Mattermost user ID from ghost user
	mattermostUserID := p.extractMattermostUserIDFromGhost(ghostUserID)
	if mattermostUserID == "" {
		return "", errors.New("failed to extract Mattermost user ID from ghost user")
	}

	// Verify that this ghost user exists in our KV store (meaning we created it)
	ghostUserKey := kvstore.BuildGhostUserKey(mattermostUserID)
	ghostUserData, err := p.kvstore.Get(ghostUserKey)
	if err != nil || len(ghostUserData) == 0 {
		p.logger.LogDebug("Rejecting DM creation for unrecognized ghost user", "ghost_user_id", ghostUserID, "mattermost_user_id", mattermostUserID)
		return "", nil // Not our ghost user - reject silently
	}

	// Verify the stored ghost user ID matches what we expect
	storedGhostUserID := string(ghostUserData)
	if storedGhostUserID != ghostUserID {
		p.logger.LogWarn("Ghost user ID mismatch in KV store", "expected", ghostUserID, "stored", storedGhostUserID, "mattermost_user_id", mattermostUserID)
		return "", nil // ID mismatch - reject silently
	}

	// Get the Mattermost user to verify they exist
	mattermostUser, appErr := p.API.GetUser(mattermostUserID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to get Mattermost user")
	}

	// Get or create the Matrix user in Mattermost
	bridgeUtils := NewBridgeUtils(BridgeUtilsConfig{
		Logger:       p.logger,
		API:          p.API,
		KVStore:      p.kvstore,
		MatrixClient: p.matrixClient,
		RemoteID:     p.remoteID,
		ConfigGetter: p,
	})

	matrixToMattermostBridge := NewMatrixToMattermostBridge(bridgeUtils)

	// Create or get the Matrix user in Mattermost (using a dummy team - will be fixed by addUserToChannelTeam)
	mattermostMatrixUserID, err := matrixToMattermostBridge.getOrCreateMattermostUser(matrixUserID, "")
	if err != nil {
		return "", errors.Wrap(err, "failed to get or create Mattermost user for Matrix user")
	}

	// Create DM channel in Mattermost between the original Mattermost user and the Matrix user
	dmChannel, appErr := p.API.GetDirectChannel(mattermostUserID, mattermostMatrixUserID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to create DM channel in Mattermost")
	}

	// Store the mapping between Matrix room and Mattermost channel
	err = bridgeUtils.setChannelRoomMapping(dmChannel.Id, roomID)
	if err != nil {
		return "", errors.Wrap(err, "failed to store channel room mapping")
	}

	p.logger.LogInfo("Created Matrix-initiated DM",
		"matrix_room_id", roomID,
		"mattermost_channel_id", dmChannel.Id,
		"matrix_user", matrixUserID,
		"mattermost_user", mattermostUser.Username,
		"ghost_user", ghostUserID)

	return dmChannel.Id, nil
}

// noteEncryption keeps track of rooms that turn on end-to-end encryption.
func (p *Plugin) noteEncryption(event MatrixEvent) {
	if p.matrixClient != nil {
		p.matrixClient.NoteRoomEncrypted(event.RoomID)
	}
	if mgr := p.cryptoManager(); mgr != nil {
		mgr.NoteEncryption(id.RoomID(event.RoomID))
	}
	channelID, _ := p.getChannelIDFromMatrixRoom(event.RoomID)
	if channelID != "" && !p.EncryptionSupported() {
		p.logger.LogWarn("A bridged Matrix room turned on end-to-end encryption; the bridge cannot read or send into it, so syncing stops for this room",
			"room_id", event.RoomID, "channel_id", channelID)
	}
}
