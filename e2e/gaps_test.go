package e2e

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// onePixelPNG is a valid 1x1 PNG used for custom emoji and avatars.
var onePixelPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")

// TestCapabilityGaps probes every capability that is documented as a gap (or
// partial) in e2e/capabilities. Each probe reports through
// capabilities.Verify, so a gap that starts working fails the run until the
// matrix is updated, and the test log lists every known gap. This is the
// test that shows where the bridge still falls short of what both networks
// can do.
func TestCapabilityGaps(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("gaps", "Gaps")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Gaps")
	adminGhost := h.GhostUserID(h.AdminUser.Id)

	// Seed one message in each direction so both users exist on both sides.
	seedPost, _, err := h.Admin.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "seed from mattermost"})
	require.NoError(t, err)
	seedEvent := WaitForMatrixMessage(t, alice, roomID, "seed from mattermost")
	seedEventID := seedEvent.Get("event_id").Str
	aliceEventID := SendMatrixText(t, alice, roomID, "seed from matrix")
	alicePost := WaitForPostWithMessage(t, h.Admin, channel.Id, "seed from matrix")
	aliceMM, _, err := h.Admin.GetUser(ctx, alicePost.UserId, "")
	require.NoError(t, err)

	t.Run("@channel becomes a room mention", func(t *testing.T) {
		_, _, err := h.Admin.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "@channel heads up"})
		require.NoError(t, err)
		works := TryFor(probeTimeout, func() bool {
			return timelineHas(t, alice, roomID, func(ev gjson.Result) bool {
				return strings.Contains(ev.Get("content.body").Str, "heads up") && ev.Get("content.m\\.mentions.room").Bool()
			})
		})
		capabilities.Verify(t, "messages.channel_mentions", works)
	})

	t.Run("@room becomes @channel", func(t *testing.T) {
		alice.SendEventSynced(t, roomID, b.Event{Type: "m.room.message", Content: map[string]interface{}{
			"msgtype": "m.text", "body": "@room everyone look", "m.mentions": map[string]interface{}{"room": true},
		}})
		works := TryFor(probeTimeout, func() bool {
			posts, _, err := h.Admin.GetPostsForChannel(ctx, channel.Id, 0, 50, "", false, false)
			if err != nil {
				return false
			}
			for _, p := range posts.Posts {
				if strings.Contains(p.Message, "everyone look") && strings.Contains(p.Message, "@channel") {
					return true
				}
			}
			return false
		})
		capabilities.Verify(t, "messages.channel_mentions.in", works)
	})

	t.Run("pinned post becomes m.room.pinned_events", func(t *testing.T) {
		_, err := h.Admin.PinPost(ctx, seedPost.Id)
		require.NoError(t, err)
		works := TryFor(probeTimeout, func() bool {
			state, ok := roomState(t, alice, roomID, "m.room.pinned_events", "")
			if !ok {
				return false
			}
			for _, p := range state.Get("pinned").Array() {
				if p.Str == seedEventID {
					return true
				}
			}
			return false
		})
		capabilities.Verify(t, "messages.pins", works)
	})

	t.Run("m.room.pinned_events pins the post", func(t *testing.T) {
		alice.SendEventSynced(t, roomID, b.Event{Type: "m.room.pinned_events", StateKey: model.NewPointer(""), Content: map[string]interface{}{
			"pinned": []string{aliceEventID},
		}})
		works := TryFor(probeTimeout, func() bool {
			pinned, _, err := h.Admin.GetPinnedPosts(ctx, channel.Id, "")
			if err != nil {
				return false
			}
			_, ok := pinned.Posts[alicePost.Id]
			return ok
		})
		capabilities.Verify(t, "messages.pins.in", works)
	})

	t.Run("custom emoji reaction", func(t *testing.T) {
		emoji, _, err := h.Admin.CreateEmoji(ctx, &model.Emoji{CreatorId: h.AdminUser.Id, Name: "bridgeparrot"}, onePixelPNG, "parrot.png")
		require.NoError(t, err)
		_, _, err = h.Admin.SaveReaction(ctx, &model.Reaction{UserId: h.AdminUser.Id, PostId: seedPost.Id, EmojiName: emoji.Name})
		require.NoError(t, err)
		works := TryFor(probeTimeout, func() bool {
			return timelineHas(t, alice, roomID, func(ev gjson.Result) bool {
				rel := ev.Get("content.m\\.relates_to")
				return ev.Get("type").Str == "m.reaction" && rel.Get("event_id").Str == seedEventID && strings.HasPrefix(rel.Get("key").Str, "mxc://")
			})
		})
		capabilities.Verify(t, "reactions.custom_emoji", works)
	})

	t.Run("image reaction (MSC4027)", func(t *testing.T) {
		mxc := alice.UploadContent(t, onePixelPNG, "react.png", "image/png")
		alice.SendEventSynced(t, roomID, b.Event{Type: "m.reaction", Content: map[string]interface{}{
			"m.relates_to":                      map[string]interface{}{"rel_type": "m.annotation", "event_id": seedEventID, "key": mxc},
			"com.beeper.reaction.shortcode":     ":react:",
			"org.matrix.msc4027.image_reaction": true,
		}})
		// Real support means a custom emoji exists for the image and the
		// reaction uses it; a reaction with a placeholder name does not count.
		works := TryFor(probeTimeout, func() bool {
			reactions, _, err := h.Admin.GetReactions(ctx, seedPost.Id)
			if err != nil {
				return false
			}
			for _, r := range reactions {
				if r.UserId != aliceMM.Id {
					continue
				}
				if _, _, err := h.Admin.GetEmojiByName(ctx, r.EmojiName); err == nil {
					return true
				}
			}
			return false
		})
		capabilities.Verify(t, "reactions.custom_emoji.in", works)
	})

	t.Run("sticker becomes an image attachment", func(t *testing.T) {
		mxc := alice.UploadContent(t, onePixelPNG, "sticker.png", "image/png")
		alice.SendEventSynced(t, roomID, b.Event{Type: "m.sticker", Content: map[string]interface{}{
			"body": "a sticker", "url": mxc, "info": map[string]interface{}{"mimetype": "image/png", "w": 1, "h": 1},
		}})
		works := TryFor(probeTimeout, func() bool {
			posts, _, err := h.Admin.GetPostsForChannel(ctx, channel.Id, 0, 50, "", false, false)
			if err != nil {
				return false
			}
			for _, p := range posts.Posts {
				if p.UserId == aliceMM.Id && len(p.FileIds) > 0 && p.CreateAt > alicePost.CreateAt {
					return true
				}
			}
			return false
		})
		capabilities.Verify(t, "files.stickers.in", works)
	})

	t.Run("location becomes a map link", func(t *testing.T) {
		alice.SendEventSynced(t, roomID, b.Event{Type: "m.room.message", Content: map[string]interface{}{
			"msgtype": "m.location", "body": "Meet here", "geo_uri": "geo:51.5074,-0.1278",
		}})
		works := TryFor(probeTimeout, func() bool {
			posts, _, err := h.Admin.GetPostsForChannel(ctx, channel.Id, 0, 50, "", false, false)
			if err != nil {
				return false
			}
			for _, p := range posts.Posts {
				if strings.Contains(p.Message, "51.5074") {
					return true
				}
			}
			return false
		})
		capabilities.Verify(t, "files.location.in", works)
	})

	t.Run("poll becomes a post", func(t *testing.T) {
		alice.SendEventSynced(t, roomID, b.Event{Type: "org.matrix.msc3381.poll.start", Content: map[string]interface{}{
			"org.matrix.msc3381.poll.start": map[string]interface{}{
				"question": map[string]interface{}{"org.matrix.msc1767.text": "Tabs or spaces?"},
				"kind":     "org.matrix.msc3381.poll.disclosed",
				"answers": []map[string]interface{}{
					{"id": "tabs", "org.matrix.msc1767.text": "Tabs"},
					{"id": "spaces", "org.matrix.msc1767.text": "Spaces"},
				},
			},
			"org.matrix.msc1767.text": "Tabs or spaces?\n1. Tabs\n2. Spaces",
		}})
		works := TryFor(probeTimeout, func() bool {
			posts, _, err := h.Admin.GetPostsForChannel(ctx, channel.Id, 0, 50, "", false, false)
			if err != nil {
				return false
			}
			for _, p := range posts.Posts {
				if strings.Contains(p.Message, "Tabs or spaces") {
					return true
				}
			}
			return false
		})
		capabilities.Verify(t, "polls.in", works)
	})

	t.Run("Mattermost typing reaches Matrix", func(t *testing.T) {
		works := TryFor(probeTimeout, func() bool {
			_, _ = h.Admin.PublishUserTyping(ctx, h.AdminUser.Id, model.TypingRequest{ChannelId: channel.Id})
			return ephemeralHas(t, alice, roomID, func(ev gjson.Result) bool {
				if ev.Get("type").Str != "m.typing" {
					return false
				}
				for _, u := range ev.Get("content.user_ids").Array() {
					if u.Str == adminGhost {
						return true
					}
				}
				return false
			})
		})
		capabilities.Verify(t, "ephemeral.typing", works)
	})

	t.Run("Mattermost status reaches Matrix presence", func(t *testing.T) {
		_, _, err := h.Admin.UpdateUserStatus(ctx, h.AdminUser.Id, &model.Status{UserId: h.AdminUser.Id, Status: model.StatusAway})
		require.NoError(t, err)
		works := TryFor(probeTimeout, func() bool {
			presence, _ := presenceOf(t, alice, adminGhost)
			return presence == "unavailable"
		})
		capabilities.Verify(t, "ephemeral.presence", works)
	})

	t.Run("custom status reaches Matrix status_msg", func(t *testing.T) {
		_, _, err := h.Admin.UpdateUserCustomStatus(ctx, h.AdminUser.Id, &model.CustomStatus{Emoji: "palm_tree", Text: "On vacation"})
		require.NoError(t, err)
		works := TryFor(probeTimeout, func() bool {
			_, msg := presenceOf(t, alice, adminGhost)
			return strings.Contains(msg, "On vacation")
		})
		capabilities.Verify(t, "ephemeral.custom_status", works)
	})

	t.Run("channel view becomes a read receipt", func(t *testing.T) {
		_, _, err := h.Admin.ViewChannel(ctx, h.AdminUser.Id, &model.ChannelView{ChannelId: channel.Id})
		require.NoError(t, err)
		works := TryFor(probeTimeout, func() bool {
			return ephemeralHas(t, alice, roomID, func(ev gjson.Result) bool {
				if ev.Get("type").Str != "m.receipt" {
					return false
				}
				found := false
				ev.Get("content").ForEach(func(_, receipts gjson.Result) bool {
					if receipts.Get("m\\.read." + client.GjsonEscape(adminGhost)).Exists() {
						found = true
						return false
					}
					return true
				})
				return found
			})
		})
		capabilities.Verify(t, "ephemeral.read_receipts", works)
	})

	t.Run("Matrix read receipt marks the channel viewed", func(t *testing.T) {
		var before int64
		if member, _, err := h.Admin.GetChannelMember(ctx, channel.Id, aliceMM.Id, ""); err == nil {
			before = member.LastViewedAt
		}
		res := alice.Do(t, http.MethodPost, []string{"_matrix", "client", "v3", "rooms", roomID, "receipt", "m.read", seedEventID}, client.WithJSONBody(t, map[string]interface{}{}))
		_ = res.Body.Close()
		works := TryFor(probeTimeout, func() bool {
			after, _, err := h.Admin.GetChannelMember(ctx, channel.Id, aliceMM.Id, "")
			return err == nil && after.LastViewedAt > before
		})
		capabilities.Verify(t, "ephemeral.read_receipts.in", works)
	})

	t.Run("deactivated user leaves the room", func(t *testing.T) {
		frank, _ := h.CreateMattermostUser("frank")
		_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, frank.Id)
		require.NoError(t, err)
		frankGhost := h.GhostUserID(frank.Id)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(frankGhost, roomID))

		_, err = h.Admin.UpdateUserActive(ctx, frank.Id, false)
		require.NoError(t, err)
		works := TryFor(probeTimeout, func() bool {
			state, ok := roomState(t, alice, roomID, "m.room.member", frankGhost)
			return ok && state.Get("membership").Str == "leave"
		})
		capabilities.Verify(t, "users.deactivation", works)
	})

	t.Run("channel admin becomes a moderator power level", func(t *testing.T) {
		_, err := h.Admin.UpdateChannelMemberSchemeRoles(ctx, channel.Id, h.AdminUser.Id, &model.SchemeRoles{SchemeUser: true, SchemeAdmin: true})
		require.NoError(t, err)
		works := TryFor(probeTimeout, func() bool {
			state, ok := roomState(t, alice, roomID, "m.room.power_levels", "")
			return ok && state.Get("users."+client.GjsonEscape(adminGhost)).Int() >= 50
		})
		capabilities.Verify(t, "membership.roles", works)
	})

	t.Run("power level becomes channel admin", func(t *testing.T) {
		// Room creators cannot appear in power_levels.users since room version
		// 12, so promote a second Matrix user instead.
		bob := h.NewMatrixUser("bob")
		bob.MustJoinRoom(t, roomID, nil)
		bobMM := WaitForBridgedUserInChannel(t, h, channel.Id, "bob")

		levels, ok := roomState(t, alice, roomID, "m.room.power_levels", "")
		require.True(t, ok, "room must have power levels")
		var content map[string]interface{}
		require.NoError(t, jsonUnmarshal(levels.Raw, &content))
		users, _ := content["users"].(map[string]interface{})
		if users == nil {
			users = map[string]interface{}{}
		}
		users[bob.UserID] = 50
		content["users"] = users
		alice.SendEventSynced(t, roomID, b.Event{Type: "m.room.power_levels", StateKey: model.NewPointer(""), Content: content})
		works := TryFor(probeTimeout, func() bool {
			member, _, err := h.Admin.GetChannelMember(ctx, channel.Id, bobMM.Id, "")
			return err == nil && member.SchemeAdmin
		})
		capabilities.Verify(t, "membership.roles.in", works)
	})

	t.Run("inviting the bot to a new room creates a channel", func(t *testing.T) {
		newRoom := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat", "name": "Born in Matrix", "invite": []string{h.BridgeBotUserID()}})
		works := TryFor(probeTimeout, func() bool {
			channels, _, err := h.Admin.GetChannelsForTeamForUser(ctx, h.Team.Id, h.AdminUser.Id, false, "")
			if err != nil {
				return false
			}
			for _, c := range channels {
				if strings.Contains(strings.ToLower(c.DisplayName), "born in matrix") || strings.Contains(c.Purpose, newRoom) {
					return true
				}
			}
			return false
		})
		capabilities.Verify(t, "rooms.create.in", works)
	})

	t.Run("channel rename and header reach the room", func(t *testing.T) {
		_, _, err := h.Admin.PatchChannel(ctx, channel.Id, &model.ChannelPatch{DisplayName: model.NewPointer("Gaps Renamed"), Header: model.NewPointer("Header from Mattermost")})
		require.NoError(t, err)
		works := TryFor(probeTimeout, func() bool {
			name, ok := roomState(t, alice, roomID, "m.room.name", "")
			topic, ok2 := roomState(t, alice, roomID, "m.room.topic", "")
			return ok && ok2 && name.Get("name").Str == "Gaps Renamed" && topic.Get("topic").Str == "Header from Mattermost"
		})
		capabilities.Verify(t, "rooms.name_topic", works)
	})

	t.Run("bridge widget is present in the room", func(t *testing.T) {
		works := false
		res := alice.Do(t, http.MethodGet, []string{"_matrix", "client", "v3", "rooms", roomID, "state"})
		if res.StatusCode == http.StatusOK {
			gjson.ParseBytes(client.ParseJSON(t, res)).ForEach(func(_, ev gjson.Result) bool {
				if ev.Get("type").Str == "im.vector.modular.widgets" && strings.Contains(ev.Get("content.url").Str, "mattermost") {
					works = true
					return false
				}
				return true
			})
		}
		_ = res.Body.Close()
		capabilities.Verify(t, "integration.widget", works)
	})

	t.Run("bridge bot answers commands in a DM", func(t *testing.T) {
		dm := alice.MustCreateRoom(t, map[string]interface{}{"preset": "private_chat", "is_direct": true, "invite": []string{h.BridgeBotUserID()}})
		SendMatrixText(t, alice, dm, "!mattermost help")
		works := TryFor(probeTimeout, func() bool {
			return timelineHas(t, alice, dm, func(ev gjson.Result) bool {
				return ev.Get("type").Str == "m.room.message" && ev.Get("sender").Str == h.BridgeBotUserID()
			})
		})
		capabilities.Verify(t, "integration.bot_commands.in", works)
	})

	t.Run("archiving the channel tombstones the room", func(t *testing.T) {
		archived := h.CreateChannel("to-archive", "To Archive")
		archivedRoom := h.CreateMappedRoom(alice, archived.Id, "To Archive")
		_, err := h.Admin.DeleteChannel(ctx, archived.Id)
		require.NoError(t, err)
		works := TryFor(probeTimeout, func() bool {
			if _, ok := roomState(t, alice, archivedRoom, "m.room.tombstone", ""); ok {
				return true
			}
			state, ok := roomState(t, alice, archivedRoom, "m.room.member", h.BridgeBotUserID())
			return ok && state.Get("membership").Str == "leave"
		})
		capabilities.Verify(t, "rooms.archive", works)
	})

	t.Run("tombstoning the room archives the channel", func(t *testing.T) {
		doomed := h.CreateChannel("to-tombstone", "To Tombstone")
		doomedRoom := h.CreateMappedRoom(alice, doomed.Id, "To Tombstone")
		alice.SendEventSynced(t, doomedRoom, b.Event{Type: "m.room.tombstone", StateKey: model.NewPointer(""), Content: map[string]interface{}{
			"body": "This room has been replaced", "replacement_room": "!nowhere:" + h.ServerName,
		}})
		works := TryFor(probeTimeout, func() bool {
			ch, _, err := h.Admin.GetChannel(ctx, doomed.Id)
			return err == nil && ch.DeleteAt != 0
		})
		capabilities.Verify(t, "rooms.archive.in", works)
	})
}
