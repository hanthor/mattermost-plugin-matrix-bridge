package main

import (
	"fmt"
	"html"
	"sort"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

// Moving a person to Matrix should not leave them to find their account and
// rebuild their sidebar. WelcomeAccounts, for every mirror account nobody has
// claimed yet, tells the person their Matrix ID and how to claim it, and
// brings over what a Matrix client keeps in account data: which rooms are
// direct messages (m.direct) and which are favourites (m.favourite).

const welcomeSentKeyPrefix = "welcome_sent_" // + Mattermost user ID -> "1"

type welcomePlan struct {
	userID    string
	matrixID  string
	direct    map[string][]string // Matrix ID of the other person -> rooms
	favourite []string            // rooms
	notify    bool
}

// WelcomeAccounts previews, or with confirm carries out, the welcome for every
// unclaimed mirror account.
func (p *Plugin) WelcomeAccounts(userID string, confirm bool) (string, error) {
	if p.kvstore == nil || p.API == nil || p.matrixClient == nil {
		return "", errors.New("the bridge is not ready")
	}
	if !p.API.HasPermissionTo(userID, model.PermissionManageSystem) {
		return "", errors.New("only system administrators can do this")
	}
	if !p.getConfiguration().EnableMirrorMode {
		return "", errors.New("accounts are only handed over in mirror mode")
	}

	plans := p.welcomePlans()
	if len(plans) == 0 {
		return "No unclaimed mirror account is waiting for a welcome.", nil
	}
	var directs, favourites, notices int
	for _, pl := range plans {
		directs += len(pl.direct)
		favourites += len(pl.favourite)
		if pl.notify {
			notices++
		}
	}
	if !confirm {
		return fmt.Sprintf("**%d people** would be welcomed: %d would be told their Matrix ID and how to claim it, %d direct-message rooms and %d favourite rooms would be added to their Matrix accounts.\n\nRun `/matrix welcome confirm` to do it. It is safe to repeat.",
			len(plans), notices, directs, favourites), nil
	}

	var told, failed int
	for _, pl := range plans {
		if err := p.applyWelcome(pl); err != nil {
			p.logger.LogWarn("Could not complete a welcome", "error", err, "user_id", pl.userID)
			failed++
			continue
		}
		if pl.notify {
			told++
		}
	}
	text := fmt.Sprintf("✅ Welcomed %d people (%d told their Matrix ID, %d direct-message and %d favourite rooms brought over).", len(plans)-failed, told, directs, favourites)
	if failed > 0 {
		text += fmt.Sprintf("\n⚠️ %d could not be completed; the log says why. Run the command again to retry them.", failed)
	}
	return text, nil
}

// welcomePlans works out, for each unclaimed mirror account, what to do.
func (p *Plugin) welcomePlans() []welcomePlan {
	store := newMirrorStore(p.kvstore)
	var plans []welcomePlan
	for page := 0; ; page++ {
		keys, err := p.kvstore.ListKeysWithPrefix(page, 200, mirrorAccountKeyPrefix)
		if err != nil || len(keys) == 0 {
			break
		}
		for _, key := range keys {
			uid := strings.TrimPrefix(key, mirrorAccountKeyPrefix)
			acct, ok := store.Account(uid)
			if !ok || acct.Claimed {
				continue
			}
			if user, appErr := p.API.GetUser(uid); appErr != nil || user == nil || user.DeleteAt != 0 || user.IsBot || user.IsRemote() {
				continue
			}
			pl := welcomePlan{userID: uid, matrixID: acct.MatrixUserID, direct: map[string][]string{}}
			if data, err := p.kvstore.Get(welcomeSentKeyPrefix + uid); err != nil || len(data) == 0 {
				pl.notify = true
			}
			p.fillWelcomeRooms(&pl)
			plans = append(plans, pl)
		}
		if len(keys) < 200 {
			break
		}
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].userID < plans[j].userID })
	return plans
}

func (p *Plugin) fillWelcomeRooms(pl *welcomePlan) {
	for _, channelID := range p.bridgedDirectChannels(pl.userID) {
		room := p.roomIDForChannel(channelID)
		if room == "" {
			continue
		}
		members, appErr := p.API.GetChannelMembers(channelID, 0, dmRoomMembersLimit)
		if appErr != nil || len(members) != 2 {
			continue // a group conversation is not a direct message
		}
		for _, m := range members {
			if m.UserId == pl.userID {
				continue
			}
			if other, err := p.mirrorOrGhost(m.UserId); err == nil && other != "" {
				pl.direct[other] = append(pl.direct[other], room)
			}
		}
	}
	prefs, appErr := p.API.GetPreferencesForUser(pl.userID)
	if appErr != nil {
		return
	}
	for _, pref := range prefs {
		if pref.Category == model.PreferenceCategoryFavoriteChannel && pref.Value == "true" {
			if room := p.roomIDForChannel(pref.Name); room != "" {
				pl.favourite = append(pl.favourite, room)
			}
		}
	}
	sort.Strings(pl.favourite)
}

func (p *Plugin) mirrorOrGhost(userID string) (string, error) {
	if acct, ok := newMirrorStore(p.kvstore).Account(userID); ok {
		return acct.MatrixUserID, nil
	}
	if p.mattermostToMatrixBridge == nil {
		return "", errors.New("no bridge")
	}
	id, ok := p.mattermostToMatrixBridge.getGhostUser(userID)
	if !ok {
		return "", errors.New("no account")
	}
	return id, nil
}

func (p *Plugin) applyWelcome(pl welcomePlan) error {
	if len(pl.direct) > 0 {
		if err := p.matrixClient.SetAccountData(pl.matrixID, "m.direct", pl.direct); err != nil {
			return errors.Wrap(err, "could not set m.direct")
		}
	}
	for _, room := range pl.favourite {
		if err := p.matrixClient.AddRoomTag(pl.matrixID, room, "m.favourite", 0.5); err != nil {
			return errors.Wrap(err, "could not tag a favourite room")
		}
	}
	if !pl.notify {
		return nil
	}
	if err := p.sendWelcomeMessage(pl); err != nil {
		return err
	}
	return p.kvstore.Set(welcomeSentKeyPrefix+pl.userID, []byte("1"))
}

func (p *Plugin) sendWelcomeMessage(pl welcomePlan) error {
	botID, err := p.API.EnsureBotUser(&model.Bot{Username: "matrixbridge", DisplayName: "Matrix bridge", Description: "Notices from the Matrix bridge"})
	if err != nil {
		return errors.Wrap(err, "could not set up the bridge notice account")
	}
	dm, appErr := p.API.GetDirectChannel(botID, pl.userID)
	if appErr != nil {
		return errors.Wrap(appErr, "could not open a direct message")
	}
	text := fmt.Sprintf("👋 You have a Matrix account: **%s**. Your messages and direct messages are already there. To sign in from any Matrix client, run `/matrix claim` here, or sign in with your organisation's shared login.", pl.matrixID)
	if _, appErr := p.API.CreatePost(&model.Post{UserId: botID, ChannelId: dm.Id, Type: bridgeNoticePostType, Message: text}); appErr != nil {
		return errors.Wrap(appErr, "could not send the welcome")
	}
	if p.getConfiguration().WelcomeEmail {
		p.emailWelcome(pl)
	}
	return nil
}

// emailWelcome sends the same news by email. It is a courtesy: Mattermost's
// mail settings may be off, so a failure is logged and nothing else.
func (p *Plugin) emailWelcome(pl welcomePlan) {
	user, appErr := p.API.GetUser(pl.userID)
	if appErr != nil || user == nil || user.Email == "" {
		return
	}
	body := fmt.Sprintf("<p>Hello %s,</p><p>You now have a Matrix account: <b>%s</b>. Your messages and direct messages are already there.</p><p>To sign in from any Matrix client, run <code>/matrix claim</code> in Mattermost, or use your organisation's shared login.</p>",
		html.EscapeString(user.Username), html.EscapeString(pl.matrixID))
	if mailErr := p.API.SendMail(user.Email, "Your Matrix account", body); mailErr != nil {
		p.logger.LogWarn("Could not email the Matrix welcome", "error", mailErr.Error(), "user_id", pl.userID)
	}
}
