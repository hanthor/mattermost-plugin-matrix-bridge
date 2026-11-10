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
		return user.Id, true
	}
	return "", false
}
