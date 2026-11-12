package main

import (
	"strings"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/secretbox"
)

// autoLinkByEmail links a Matrix person to the Mattermost account that has the
// same verified email address, so they appear as one person instead of as a
// remote user next to their real account. It only ever links when both sides
// have verified the address, the Mattermost account is a real, active one and
// it is not already linked to someone else; anything else falls back to the
// usual remote account.
func (b *MatrixToMattermostBridge) autoLinkByEmail(matrixUserID string) (string, bool) {
	cfg := b.getConfiguration()
	if !cfg.AutoLinkByEmail || cfg.MatrixAdminToken == "" || b.matrixClient == nil {
		return "", false
	}
	emails, err := b.matrixClient.VerifiedEmailsAsAdmin(cfg.MatrixAdminToken, matrixUserID)
	if err != nil {
		b.logger.LogDebug("Could not read the verified email of a Matrix account", "error", err, "matrix_user_id", matrixUserID)
		return "", false
	}
	store := newIdentityStore(b.kvstore, func() *secretbox.Box {
		return secretbox.New(secretFor(b.API, cfg), "identity-token")
	})
	for _, email := range emails {
		user, appErr := b.API.GetUserByEmail(strings.ToLower(email))
		if appErr != nil || user == nil || user.IsRemote() || user.DeleteAt != 0 || user.IsBot || !user.EmailVerified {
			continue
		}
		if existing, linked := store.Link(user.Id); linked && existing.MatrixUserID != matrixUserID {
			b.logger.LogWarn("Not linking by email: the Mattermost account is already linked to another Matrix account", "user_id", user.Id)
			continue
		}
		if _, err := store.linkAccounts(user.Id, matrixUserID); err != nil {
			b.logger.LogWarn("Could not link by email", "error", err, "user_id", user.Id)
			continue
		}
		b.logger.LogInfo("Linked a Matrix person to the Mattermost account with the same verified email", "user_id", user.Id, "matrix_user_id", matrixUserID)
		b.puppetThroughMAS(store, cfg, matrixUserID)
		return user.Id, true
	}
	return "", false
}

// puppetThroughMAS completes the automatic link: when the homeserver signs
// people in through MAS, the bridge asks it for a session of the person's own
// account, so their Mattermost messages appear from it without any command.
// The verified email that justified the link is the proof of ownership. It is
// best effort: without it the person is linked and uses a ghost, as before.
func (b *MatrixToMattermostBridge) puppetThroughMAS(store *identityStore, cfg *configuration, matrixUserID string) {
	if !cfg.masConfigured() {
		return
	}
	token, err := masPersonalToken(cfg, matrixUserID)
	if err != nil {
		b.logger.LogDebug("No own-account token from MAS for an automatic link", "error", err, "matrix_user_id", matrixUserID)
		return
	}
	if owner, err := b.matrixClient.WhoAmI(token); err != nil || owner != matrixUserID {
		b.logger.LogWarn("The MAS token did not belong to the linked account; not using it", "matrix_user_id", matrixUserID)
		return
	}
	if _, err := store.SetToken(matrixUserID, token); err != nil {
		b.logger.LogWarn("Could not store the own-account token from MAS", "error", err, "matrix_user_id", matrixUserID)
	}
}
