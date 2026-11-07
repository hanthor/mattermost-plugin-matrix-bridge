package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// Mattermost has no plugin hook for profile changes, so ghost profiles are
// refreshed in two ways: whenever a user is active in a bridged channel
// (posts, reactions) and from a periodic sweep over every known ghost.
// A fingerprint of the relevant fields is kept in the KV store so an
// unchanged profile costs one KV read and no Matrix requests.

const (
	profileFingerprintKeyPrefix = "profile_fingerprint_"
	profileSweepInterval        = 15 * time.Minute
	profileSweepPageSize        = 200
)

// profileFingerprint captures the fields the bridge mirrors to Matrix.
func profileFingerprint(user *model.User) string {
	return fmt.Sprintf("%s|%d", user.GetDisplayName(model.ShowFullName), user.LastPictureUpdate)
}

// syncProfileIfChanged refreshes the user's ghost when the display name or
// avatar changed since the last sync. It is cheap enough to call on every post.
func (b *MattermostToMatrixBridge) syncProfileIfChanged(user *model.User) {
	if user == nil || user.IsBot || user.IsRemote() {
		return
	}
	if _, exists := b.getGhostUser(user.Id); !exists {
		return
	}
	key := profileFingerprintKeyPrefix + user.Id
	want := profileFingerprint(user)
	if have, err := b.kvstore.Get(key); err == nil && string(have) == want {
		return
	}
	if err := b.SyncUserToMatrix(user); err != nil {
		b.logger.LogWarn("Failed to sync changed profile to Matrix", "error", err, "user_id", user.Id)
		return
	}
	if err := b.kvstore.Set(key, []byte(want)); err != nil {
		b.logger.LogWarn("Failed to store profile fingerprint", "error", err, "user_id", user.Id)
	}
}

// sweepProfiles refreshes every ghost whose Mattermost profile changed.
func (p *Plugin) sweepProfiles() {
	if p.mattermostToMatrixBridge == nil || p.kvstore == nil || !p.getConfiguration().EnableSync {
		return
	}
	for page := 0; ; page++ {
		keys, err := p.kvstore.ListKeysWithPrefix(page, profileSweepPageSize, kvstore.KeyPrefixGhostUser)
		if err != nil {
			p.logger.LogWarn("Profile sweep could not list ghost users", "error", err)
			return
		}
		for _, key := range keys {
			userID := strings.TrimPrefix(key, kvstore.KeyPrefixGhostUser)
			user, appErr := p.API.GetUser(userID)
			if appErr != nil {
				continue
			}
			p.mattermostToMatrixBridge.syncProfileIfChanged(user)
		}
		if len(keys) < profileSweepPageSize {
			return
		}
	}
}
