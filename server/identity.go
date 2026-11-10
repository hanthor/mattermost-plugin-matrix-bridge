package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/secretbox"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// Identity linking lets one person be the same identity on both networks.
//
// A Mattermost user runs /matrix link and receives a one-time code. In Matrix
// they send "!mattermost link <code>" to the bridge bot, which binds their
// Matrix account to the Mattermost account. Optionally they hand the bot an
// access token with "!mattermost login <token>" so the bridge can post as
// their real Matrix account (double puppeting) instead of a ghost.

const (
	identityLinkKeyPrefix = "identity_link_" // + Mattermost user ID -> IdentityLink JSON
	identityCodeKeyPrefix = "identity_code_" // + code -> identityCode JSON
	identityCodeTTL       = 15 * time.Minute
	identityCodeLength    = 8
	identityCodeAlphabet  = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O/1/I
)

// IdentityLink binds a Mattermost account to a Matrix account.
type IdentityLink struct {
	MattermostUserID string `json:"mattermost_user_id"`
	MatrixUserID     string `json:"matrix_user_id"`
	// AccessToken, when set, lets the bridge act as the Matrix account.
	AccessToken string `json:"access_token,omitempty"`
	LinkedAt    int64  `json:"linked_at"`
}

// Puppeted reports whether the bridge can act as the linked Matrix account.
func (l *IdentityLink) Puppeted() bool {
	return l != nil && l.AccessToken != ""
}

type identityCode struct {
	MattermostUserID string `json:"mattermost_user_id"`
	ExpiresAt        int64  `json:"expires_at"`
}

// identityStore persists links and codes in the plugin KV store.
type identityStore struct {
	kv kvstore.KVStore
	// box seals access tokens at rest. It is looked up only when a token is
	// read or written; nil means tokens are stored as they are.
	box func() *secretbox.Box
}

func newIdentityStore(kv kvstore.KVStore, box func() *secretbox.Box) *identityStore {
	return &identityStore{kv: kv, box: box}
}

func (s *identityStore) secretBox() *secretbox.Box {
	if s.box == nil {
		return nil
	}
	return s.box()
}

// Link returns the link for a Mattermost user, if any.
func (s *identityStore) Link(mattermostUserID string) (*IdentityLink, bool) {
	data, err := s.kv.Get(identityLinkKeyPrefix + mattermostUserID)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	var link IdentityLink
	if err := json.Unmarshal(data, &link); err != nil {
		return nil, false
	}
	if link.AccessToken != "" {
		token, legacy, err := s.secretBox().Open(link.AccessToken)
		if err != nil {
			// The key changed (for example the System Console secret was
			// rotated): the person must run !mattermost login again.
			link.AccessToken = ""
		} else {
			link.AccessToken = token
			if legacy && s.secretBox() != nil {
				_ = s.save(&link) // upgrade a plain token to a sealed one
			}
		}
	}
	return &link, true
}

// LinkForMatrixUser returns the link for a Matrix user, if any.
func (s *identityStore) LinkForMatrixUser(matrixUserID string) (*IdentityLink, bool) {
	data, err := s.kv.Get(kvstore.BuildMatrixUserKey(matrixUserID))
	if err != nil || len(data) == 0 {
		return nil, false
	}
	link, ok := s.Link(string(data))
	if !ok || link.MatrixUserID != matrixUserID {
		return nil, false
	}
	return link, true
}

// save persists a link and the Matrix→Mattermost user mapping used by the
// inbound bridge, so the linked Matrix account posts as the real user.
func (s *identityStore) save(link *IdentityLink) error {
	stored := *link
	if stored.AccessToken != "" {
		sealed, err := s.secretBox().Seal(stored.AccessToken)
		if err != nil {
			return errors.Wrap(err, "seal access token")
		}
		stored.AccessToken = sealed
	}
	data, err := json.Marshal(&stored)
	if err != nil {
		return errors.Wrap(err, "marshal identity link")
	}
	if err := s.kv.Set(identityLinkKeyPrefix+link.MattermostUserID, data); err != nil {
		return errors.Wrap(err, "store identity link")
	}
	if err := s.kv.Set(kvstore.BuildMatrixUserKey(link.MatrixUserID), []byte(link.MattermostUserID)); err != nil {
		return errors.Wrap(err, "store matrix user mapping")
	}
	if err := s.kv.Set(kvstore.BuildMattermostUserKey(link.MattermostUserID), []byte(link.MatrixUserID)); err != nil {
		return errors.Wrap(err, "store mattermost user mapping")
	}
	return nil
}

// CreateCode issues a one-time code for a Mattermost user.
func (s *identityStore) CreateCode(mattermostUserID string) (string, error) {
	code, err := randomCode(identityCodeLength)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(identityCode{
		MattermostUserID: mattermostUserID,
		ExpiresAt:        time.Now().Add(identityCodeTTL).Unix(),
	})
	if err != nil {
		return "", errors.Wrap(err, "marshal identity code")
	}
	if err := s.kv.Set(identityCodeKeyPrefix+code, data); err != nil {
		return "", errors.Wrap(err, "store identity code")
	}
	return code, nil
}

// Redeem consumes a code and links the Matrix user to the code's Mattermost
// user. Any previous link of either account is replaced.
func (s *identityStore) Redeem(code, matrixUserID string) (*IdentityLink, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	key := identityCodeKeyPrefix + code
	data, err := s.kv.Get(key)
	if err != nil || len(data) == 0 {
		return nil, errors.New("unknown or expired code")
	}
	var ic identityCode
	if err := json.Unmarshal(data, &ic); err != nil {
		return nil, errors.New("unknown or expired code")
	}
	_ = s.kv.Delete(key)
	if time.Now().Unix() > ic.ExpiresAt {
		return nil, errors.New("unknown or expired code")
	}

	if old, ok := s.LinkForMatrixUser(matrixUserID); ok && old.MattermostUserID != ic.MattermostUserID {
		_ = s.Unlink(old.MattermostUserID)
	}
	if old, ok := s.Link(ic.MattermostUserID); ok && old.MatrixUserID != matrixUserID {
		_ = s.kv.Delete(kvstore.BuildMatrixUserKey(old.MatrixUserID))
	}

	link := &IdentityLink{
		MattermostUserID: ic.MattermostUserID,
		MatrixUserID:     matrixUserID,
		LinkedAt:         time.Now().Unix(),
	}
	if err := s.save(link); err != nil {
		return nil, err
	}
	return link, nil
}

// SetToken stores a verified access token for a linked Matrix user.
func (s *identityStore) SetToken(matrixUserID, token string) (*IdentityLink, error) {
	link, ok := s.LinkForMatrixUser(matrixUserID)
	if !ok {
		return nil, errors.New("account is not linked; run /matrix link in Mattermost first")
	}
	link.AccessToken = token
	if err := s.save(link); err != nil {
		return nil, err
	}
	return link, nil
}

// Unlink removes a link and the user mappings it created.
func (s *identityStore) Unlink(mattermostUserID string) error {
	link, ok := s.Link(mattermostUserID)
	if !ok {
		return errors.New("account is not linked")
	}
	_ = s.kv.Delete(kvstore.BuildMatrixUserKey(link.MatrixUserID))
	_ = s.kv.Delete(kvstore.BuildMattermostUserKey(mattermostUserID))
	return s.kv.Delete(identityLinkKeyPrefix + mattermostUserID)
}

func randomCode(n int) (string, error) {
	var sb strings.Builder
	limit := big.NewInt(int64(len(identityCodeAlphabet)))
	for i := 0; i < n; i++ {
		idx, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", errors.Wrap(err, "generate code")
		}
		sb.WriteByte(identityCodeAlphabet[idx.Int64()])
	}
	return sb.String(), nil
}

// identity returns the plugin's identity store.
func (p *Plugin) identity() *identityStore {
	return newIdentityStore(p.kvstore, p.tokenBox)
}

// CreateIdentityLinkCode implements command.PluginAccessor.
func (p *Plugin) CreateIdentityLinkCode(mattermostUserID string) (string, error) {
	return p.identity().CreateCode(mattermostUserID)
}

// GetIdentityLink implements command.PluginAccessor: it returns the linked
// Matrix user ID and whether the bridge can act as that account.
func (p *Plugin) GetIdentityLink(mattermostUserID string) (matrixUserID string, puppeted bool, ok bool) {
	link, ok := p.identity().Link(mattermostUserID)
	if !ok {
		return "", false, false
	}
	return link.MatrixUserID, link.Puppeted(), true
}

// UnlinkIdentity implements command.PluginAccessor.
func (p *Plugin) UnlinkIdentity(mattermostUserID string) error {
	return p.identity().Unlink(mattermostUserID)
}

// BridgeBotUserID is the Matrix ID of the appservice sender.
func (p *Plugin) BridgeBotUserID() string {
	return fmt.Sprintf("@%s:%s", bridgeBotLocalpart, p.matrixServerDomain())
}

// secretFor returns the secret that at-rest keys are derived from. It has to
// be held outside the key-value store, and a plugin cannot read Mattermost's
// own at-rest encryption key, so it is the appservice token from the plugin
// settings (a different table of the database). Rotating that token makes
// stored personal tokens unreadable; people then run !mattermost login again.
func secretFor(_ plugin.API, cfg *configuration) string {
	if cfg == nil {
		return ""
	}
	return cfg.MatrixASToken
}

// tokenBox seals the personal access tokens used for double puppeting.
func (p *Plugin) tokenBox() *secretbox.Box {
	return secretbox.New(secretFor(p.API, p.getConfiguration()), "identity-token")
}
