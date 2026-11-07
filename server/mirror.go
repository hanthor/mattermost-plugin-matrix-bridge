package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/command"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// Mirror mode gives every Mattermost user a real-looking Matrix account,
// @<username>:<server>, instead of an @_mattermost_<id> ghost. The design
// and the scenarios it has to handle are in docs/CALLS_AND_MIRROR_MODE.md.
//
// A user is always represented by exactly one bridge-controlled account,
// resolved by the same rules everywhere (see accountFor in the bridge):
// a linked account that holds a token is puppeted, otherwise the mirrored
// account in mirror mode, otherwise the ghost. The names ghost_user_* and
// getGhostUser predate mirror mode; they now mean "the bridge-controlled
// account for this user".

const (
	mirrorAccountKeyPrefix   = "mirror_account_"   // + Mattermost user ID -> mirrorAccount JSON
	mirrorMatrixKeyPrefix    = "mirror_mxid_"      // + Matrix user ID -> Mattermost user ID
	mirrorLocalpartKeyPrefix = "mirror_localpart_" // + localpart -> Mattermost user ID, or mirrorForeign
	mirrorPendingKeyPrefix   = "mirror_pending_"   // + localpart -> Mattermost user ID, set while registering
	legacyMirrorUserPrefix   = "mirror_user_"      // + Mattermost user ID -> Matrix user ID (before accounts were recorded)
	accountModeKey           = "bridge_account_mode"

	// mirrorForeign marks a localpart that exists on the homeserver but was
	// not created by the bridge.
	mirrorForeign = "foreign"

	accountModeGhost  = "ghost"
	accountModeMirror = "mirror"

	maxMirrorLocalpartAttempts = 10
)

// mirrorAccount is the record of a mirrored Matrix account.
type mirrorAccount struct {
	MattermostUserID string `json:"mattermost_user_id"`
	MatrixUserID     string `json:"matrix_user_id"`
	Localpart        string `json:"localpart"`
	// Collision is set when the natural localpart was taken and a suffixed
	// one was used instead.
	Collision bool `json:"collision,omitempty"`
	// Claimed is set once the person has the account: from then on the
	// bridge no longer manages its profile and treats events from it that it
	// did not send as the person's own.
	Claimed   bool  `json:"claimed,omitempty"`
	CreatedAt int64 `json:"created_at"`
}

type mirrorStore struct {
	kv kvstore.KVStore
}

func newMirrorStore(kv kvstore.KVStore) *mirrorStore {
	return &mirrorStore{kv: kv}
}

func (s *mirrorStore) get(key string) ([]byte, bool) {
	if s == nil || s.kv == nil {
		return nil, false
	}
	data, err := s.kv.Get(key)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

// Account returns the mirror account of a Mattermost user, upgrading a
// record written by older versions on first sight.
func (s *mirrorStore) Account(mattermostUserID string) (*mirrorAccount, bool) {
	if data, ok := s.get(mirrorAccountKeyPrefix + mattermostUserID); ok {
		var acct mirrorAccount
		if json.Unmarshal(data, &acct) == nil && acct.MatrixUserID != "" {
			return &acct, true
		}
	}
	if legacy, ok := s.get(legacyMirrorUserPrefix + mattermostUserID); ok {
		mxid := string(legacy)
		acct := &mirrorAccount{
			MattermostUserID: mattermostUserID,
			MatrixUserID:     mxid,
			Localpart:        strings.TrimPrefix(strings.SplitN(mxid, ":", 2)[0], "@"),
			CreatedAt:        time.Now().Unix(),
		}
		if err := s.save(acct); err == nil {
			return acct, true
		}
	}
	return nil, false
}

// AccountForMatrixUser returns the mirror account behind a Matrix user ID.
func (s *mirrorStore) AccountForMatrixUser(matrixUserID string) (*mirrorAccount, bool) {
	data, ok := s.get(mirrorMatrixKeyPrefix + matrixUserID)
	if !ok {
		return nil, false
	}
	return s.Account(string(data))
}

func (s *mirrorStore) save(acct *mirrorAccount) error {
	data, err := json.Marshal(acct)
	if err != nil {
		return errors.Wrap(err, "marshal mirror account")
	}
	if err := s.kv.Set(mirrorAccountKeyPrefix+acct.MattermostUserID, data); err != nil {
		return errors.Wrap(err, "store mirror account")
	}
	if err := s.kv.Set(mirrorMatrixKeyPrefix+acct.MatrixUserID, []byte(acct.MattermostUserID)); err != nil {
		return errors.Wrap(err, "store mirror account index")
	}
	if err := s.kv.Set(mirrorLocalpartKeyPrefix+acct.Localpart, []byte(acct.MattermostUserID)); err != nil {
		return errors.Wrap(err, "store localpart owner")
	}
	_ = s.kv.Delete(mirrorPendingKeyPrefix + acct.Localpart)
	return nil
}

// LocalpartOwner returns who owns a localpart: a Mattermost user ID,
// mirrorForeign, or "" when nobody does.
func (s *mirrorStore) LocalpartOwner(localpart string) string {
	data, _ := s.get(mirrorLocalpartKeyPrefix + localpart)
	return string(data)
}

func (s *mirrorStore) markForeign(localpart string) {
	_ = s.kv.Set(mirrorLocalpartKeyPrefix+localpart, []byte(mirrorForeign))
	_ = s.kv.Delete(mirrorPendingKeyPrefix + localpart)
}

func (s *mirrorStore) pendingOwner(localpart string) string {
	data, _ := s.get(mirrorPendingKeyPrefix + localpart)
	return string(data)
}

func (s *mirrorStore) setPending(localpart, mattermostUserID string) {
	_ = s.kv.Set(mirrorPendingKeyPrefix+localpart, []byte(mattermostUserID))
}

// Claim marks the account as the person's own.
func (s *mirrorStore) Claim(mattermostUserID string) (*mirrorAccount, error) {
	acct, ok := s.Account(mattermostUserID)
	if !ok {
		return nil, errors.New("this user has no mirrored account")
	}
	if acct.Claimed {
		return acct, nil
	}
	acct.Claimed = true
	if err := s.save(acct); err != nil {
		return nil, err
	}
	return acct, nil
}

// MirrorStats counts mirrored accounts for /matrix status.
type MirrorStats struct {
	Accounts   int
	Claimed    int
	Collisions int
}

// Stats walks the account records.
func (s *mirrorStore) Stats() MirrorStats {
	var stats MirrorStats
	if s == nil || s.kv == nil {
		return stats
	}
	for page := 0; ; page++ {
		keys, err := s.kv.ListKeysWithPrefix(page, 200, mirrorAccountKeyPrefix)
		if err != nil {
			return stats
		}
		for _, key := range keys {
			var acct mirrorAccount
			if data, ok := s.get(key); ok && json.Unmarshal(data, &acct) == nil {
				stats.Accounts++
				if acct.Claimed {
					stats.Claimed++
				}
				if acct.Collision {
					stats.Collisions++
				}
			}
		}
		if len(keys) < 200 {
			return stats
		}
	}
}

var invalidLocalpartChars = regexp.MustCompile(`[^a-z0-9._=/+-]`)

// mirrorLocalpart derives the Matrix localpart from a Mattermost username.
// Mattermost usernames are already lowercase letters, digits and ".-_", all
// valid in a Matrix localpart; anything else is replaced defensively.
func mirrorLocalpart(username string) string {
	lp := invalidLocalpartChars.ReplaceAllString(strings.ToLower(username), "_")
	lp = strings.TrimLeft(lp, "_.")
	if lp == "" {
		lp = "user"
	}
	return lp
}

func localpartCandidate(base string, attempt int) string {
	switch attempt {
	case 0:
		return base
	case 1:
		return base + "_mm"
	default:
		return fmt.Sprintf("%s_mm%d", base, attempt)
	}
}

// lookupBridgeAccount returns the bridge-controlled Matrix account of a
// Mattermost user without creating anything: the mirror account if there is
// one, otherwise the ghost.
func lookupBridgeAccount(kv kvstore.KVStore, mattermostUserID string) (string, bool) {
	if acct, ok := newMirrorStore(kv).Account(mattermostUserID); ok {
		return acct.MatrixUserID, true
	}
	if data, err := kv.Get(kvstore.BuildGhostUserKey(mattermostUserID)); err == nil && len(data) > 0 {
		return string(data), true
	}
	return "", false
}

// effectiveAccountMode returns the mode recorded the first time the bridge
// created an account, or "" when none exists yet.
func effectiveAccountMode(kv kvstore.KVStore) string {
	if kv == nil {
		return ""
	}
	data, err := kv.Get(accountModeKey)
	if err != nil {
		return ""
	}
	return string(data)
}

// recordAccountMode pins the account mode once the first account exists.
// Switching afterwards would leave every user with two Matrix identities, so
// the recorded mode wins over the setting until it is migrated explicitly.
func recordAccountMode(kv kvstore.KVStore, mode string) {
	if kv == nil || effectiveAccountMode(kv) != "" {
		return
	}
	_ = kv.Set(accountModeKey, []byte(mode))
}

// ensureAccount returns the bridge-controlled Matrix account for a
// Mattermost user, creating it (a ghost, or a mirror account in mirror
// mode) when it does not exist.
func (b *MattermostToMatrixBridge) ensureAccount(userID string) (string, error) {
	if b.configGetter.getConfiguration().EnableMirrorMode {
		return b.ensureMirrorAccount(userID)
	}
	recordAccountMode(b.kvstore, accountModeGhost)
	return b.CreateOrGetGhostUser(userID)
}

// ensureMirrorAccount creates the mirror account of a user, handling the
// cases where the natural localpart is not free.
func (b *MattermostToMatrixBridge) ensureMirrorAccount(userID string) (string, error) {
	store := newMirrorStore(b.kvstore)
	if acct, ok := store.Account(userID); ok {
		return acct.MatrixUserID, nil
	}

	user, appErr := b.API.GetUser(userID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to get Mattermost user for mirror mode")
	}

	config := b.configGetter.getConfiguration()
	password := config.MirrorModePassword
	if password != "" {
		b.logger.LogWarn("mirror_mode_password is set: every mirrored account gets this one password. Leave it empty and let people claim their account instead")
	}

	base := mirrorLocalpart(user.Username)
	for attempt := 0; attempt < maxMirrorLocalpartAttempts; attempt++ {
		localpart := localpartCandidate(base, attempt)
		if owner := store.LocalpartOwner(localpart); owner != "" && owner != userID {
			continue
		}

		// An earlier attempt may have registered the account and crashed
		// before recording it; that account is ours, anything else is not.
		wasPending := store.pendingOwner(localpart) == userID
		store.setPending(localpart, userID)

		matrixUserID, created, err := b.matrixClient.RegisterMirrorUser(localpart, password)
		if err != nil {
			return "", errors.Wrapf(err, "failed to register mirror account %q", localpart)
		}
		if !created && !wasPending {
			b.logger.LogWarn("Mirror account name is taken by an account the bridge does not own; using another name",
				"localpart", localpart, "mattermost_username", user.Username)
			store.markForeign(localpart)
			continue
		}

		acct := &mirrorAccount{
			MattermostUserID: userID,
			MatrixUserID:     matrixUserID,
			Localpart:        localpart,
			Collision:        attempt > 0,
			CreatedAt:        time.Now().Unix(),
		}
		if err := store.save(acct); err != nil {
			return "", err
		}
		recordAccountMode(b.kvstore, accountModeMirror)
		b.metrics.RecordGhostUserCreated()
		b.logger.LogInfo("Created mirror account", "mattermost_user_id", userID, "matrix_user_id", matrixUserID, "collision", acct.Collision)

		if config.SyncUserProfiles {
			b.syncMirrorProfile(user)
		}
		return matrixUserID, nil
	}
	return "", errors.Errorf("no free Matrix localpart for Mattermost user %q after %d attempts", user.Username, maxMirrorLocalpartAttempts)
}

// syncMirrorProfile sets the display name and avatar of a new mirror account.
func (b *MattermostToMatrixBridge) syncMirrorProfile(user *model.User) {
	if err := b.SyncUserToMatrix(user); err != nil {
		b.logger.LogWarn("Failed to sync profile of new mirror account", "error", err, "user_id", user.Id)
		return
	}
	if err := b.kvstore.Set(profileFingerprintKeyPrefix+user.Id, []byte(profileFingerprint(user))); err != nil {
		b.logger.LogDebug("Failed to store profile fingerprint", "error", err, "user_id", user.Id)
	}
}

// ownsAccount reports whether the bridge manages the profile of a Matrix
// account: ghosts and unclaimed mirror accounts. A claimed account is the
// person's own.
func (s *BridgeUtils) ownsAccount(matrixUserID string) bool {
	if strings.HasPrefix(matrixUserID, "@_mattermost_") {
		return true
	}
	acct, ok := newMirrorStore(s.kvstore).AccountForMatrixUser(matrixUserID)
	return ok && !acct.Claimed
}

// isBridgeAccount reports whether every event from this Matrix user is the
// bridge's own: ghosts and unclaimed mirror accounts. Events from a claimed
// account are the person's unless the bridge sent them (see SentByBridge).
func (p *Plugin) isBridgeAccount(matrixUserID string) bool {
	if p.isGhostUser(matrixUserID) {
		return true
	}
	acct, ok := newMirrorStore(p.kvstore).AccountForMatrixUser(matrixUserID)
	return ok && !acct.Claimed
}

// claimMirrorAccount hands the mirror account to the person: it is marked
// claimed and linked to their Mattermost account, so events from their own
// Matrix client are posted as them and the bridge keeps sending as them
// through the appservice.
func (p *Plugin) claimMirrorAccount(mattermostUserID string) (*mirrorAccount, error) {
	store := newMirrorStore(p.kvstore)
	acct, err := store.Claim(mattermostUserID)
	if err != nil {
		return nil, err
	}
	if existing, ok := p.identity().Link(mattermostUserID); !ok || existing.MatrixUserID != acct.MatrixUserID {
		if err := p.identity().save(&IdentityLink{
			MattermostUserID: mattermostUserID,
			MatrixUserID:     acct.MatrixUserID,
			LinkedAt:         time.Now().Unix(),
		}); err != nil {
			return nil, err
		}
	}
	return acct, nil
}

// mirrorUserForMatrixID maps a mirror-mode Matrix ID such as @ada:server to
// the Mattermost user it stands for: by the recorded account when it exists,
// otherwise by looking the localpart up as a Mattermost username so the
// account can be created on the homeserver's first query.
func (p *Plugin) mirrorUserForMatrixID(matrixUserID string) string {
	if acct, ok := newMirrorStore(p.kvstore).AccountForMatrixUser(matrixUserID); ok {
		return acct.MattermostUserID
	}
	localpart := strings.TrimPrefix(strings.SplitN(matrixUserID, ":", 2)[0], "@")
	if localpart == "" || strings.HasPrefix(localpart, "_") {
		return ""
	}
	if owner := newMirrorStore(p.kvstore).LocalpartOwner(localpart); owner != "" && owner != mirrorForeign {
		return owner
	}
	user, appErr := p.API.GetUserByUsername(localpart)
	if appErr != nil || user == nil || user.IsBot || user.IsRemote() {
		return ""
	}
	return user.Id
}

// claimOnFirstOwnMessage claims an unclaimed mirror account when a message
// arrives from it that the bridge did not send. Only messages count: other
// event types from a mirror account are indistinguishable from the bridge's
// own side effects (joins, membership, reactions) after a restart cleared the
// echo cache.
func (p *Plugin) claimOnFirstOwnMessage(event MatrixEvent) {
	if event.Type != "m.room.message" || p.isGhostUser(event.Sender) {
		return
	}
	acct, ok := newMirrorStore(p.kvstore).AccountForMatrixUser(event.Sender)
	if !ok || acct.Claimed {
		return
	}
	if postID, remoteID := (&BridgeUtils{}).extractMattermostMetadata(event); postID != "" || remoteID != "" {
		return // the bridge's own message
	}
	if _, err := p.claimMirrorAccount(acct.MattermostUserID); err != nil {
		p.logger.LogWarn("Failed to claim mirror account on first own message", "error", err, "matrix_user_id", event.Sender)
		return
	}
	p.logger.LogInfo("Mirror account claimed: the person is using it from their own client", "matrix_user_id", event.Sender, "mattermost_user_id", acct.MattermostUserID)
}

// MirrorStatus implements command.PluginAccessor.
func (p *Plugin) MirrorStatus() command.MirrorStatus {
	cfg := p.getConfiguration()
	mode := effectiveAccountMode(p.kvstore)
	if mode == "" {
		mode = accountModeGhost
		if cfg.EnableMirrorMode {
			mode = accountModeMirror
		}
	}
	st := command.MirrorStatus{
		Mode:             mode,
		Pinned:           cfg.accountModePinned,
		ConfiguredMirror: cfg.EnableMirrorMode != cfg.accountModePinned,
		ClaimSupported:   cfg.MatrixAdminToken != "",
	}
	if mode == accountModeMirror {
		stats := newMirrorStore(p.kvstore).Stats()
		st.Accounts, st.Claimed, st.Collisions = stats.Accounts, stats.Claimed, stats.Collisions
	}
	return st
}

// ClaimMirrorAccount implements command.PluginAccessor. With an admin token
// the account gets a one-time password the person must change; without one
// the account is claimed for use through shared login.
func (p *Plugin) ClaimMirrorAccount(mattermostUserID string) (string, string, error) {
	cfg := p.getConfiguration()
	if !cfg.EnableMirrorMode {
		return "", "", errors.New("mirror mode is not enabled")
	}
	if p.mattermostToMatrixBridge == nil || p.matrixClient == nil {
		return "", "", errors.New("the bridge is not connected to Matrix")
	}
	// Make sure the account exists before handing it over.
	matrixUserID, err := p.mattermostToMatrixBridge.ensureAccount(mattermostUserID)
	if err != nil {
		return "", "", errors.Wrap(err, "could not create your Matrix account")
	}

	password := ""
	if cfg.MatrixAdminToken != "" {
		password = oneTimePassword()
		if err := p.matrixClient.SetUserPasswordAsAdmin(cfg.MatrixAdminToken, matrixUserID, password); err != nil {
			return "", "", errors.Wrap(err, "could not set a password for your Matrix account")
		}
	}
	if _, err := p.claimMirrorAccount(mattermostUserID); err != nil {
		return "", "", err
	}
	return matrixUserID, password, nil
}

const passwordAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// oneTimePassword returns a random password for handing an account over.
func oneTimePassword() string {
	var sb strings.Builder
	limit := big.NewInt(int64(len(passwordAlphabet)))
	for i := 0; i < 20; i++ {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return model.NewRandomString(20)
		}
		sb.WriteByte(passwordAlphabet[n.Int64()])
	}
	return sb.String()
}
