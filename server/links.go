package main

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// Links in a message should lead somewhere for the person reading it. A
// Mattermost permalink to a bridged post becomes a link to the Matrix event; a
// matrix.to or Element link to a bridged event becomes a Mattermost permalink;
// channel and room links are swapped the same way. Links to anything that is
// not bridged are left alone.

var (
	mattermostPermalink = regexp.MustCompile(`https?://[^\s<>()\[\]]+?/([a-z0-9][a-z0-9_-]*)/pl/([a-z0-9]{26})`)
	channelMention      = regexp.MustCompile(`(^|[\s(])~([a-z0-9][a-z0-9_-]{0,63})\b`)
	matrixToLink        = regexp.MustCompile(`https://matrix\.to/#/([!#][^/\s?)>\]]+)(?:/(\$[^\s?)>\]]+))?(?:\?[^\s)>\]]*)?`)
)

// buildEventLink is a link that opens a Matrix event or room: in Element Web
// when its address is configured, otherwise through matrix.to.
func buildEventLink(elementURL, roomID, eventID string) string {
	path := roomID
	if eventID != "" {
		path += "/" + eventID
	}
	if base := strings.TrimRight(elementURL, "/"); base != "" {
		return base + "/#/room/" + path
	}
	return "https://matrix.to/#/" + path
}

func (b *BridgeUtils) siteURL() string {
	if cfg := b.API.GetConfig(); cfg != nil && cfg.ServiceSettings.SiteURL != nil {
		return strings.TrimRight(*cfg.ServiceSettings.SiteURL, "/")
	}
	return ""
}

// rewriteLinksToMatrix makes Mattermost permalinks and ~channel mentions in
// text usable from Matrix. channelID is the channel the text is posted in.
func (b *BridgeUtils) rewriteLinksToMatrix(text, channelID string) string {
	if !strings.Contains(text, "/pl/") && !strings.Contains(text, "~") {
		return text
	}
	site := b.siteURL()
	cfg := b.getConfiguration()
	propertyKey := "matrix_event_id_" + extractServerDomain(b.logger, cfg.MatrixServerURL)

	if site != "" {
		text = mattermostPermalink.ReplaceAllStringFunc(text, func(link string) string {
			if !strings.HasPrefix(link, site+"/") {
				return link
			}
			m := mattermostPermalink.FindStringSubmatch(link)
			post, appErr := b.API.GetPost(m[2])
			if appErr != nil || post == nil {
				return link
			}
			eventID, _ := post.Props[propertyKey].(string)
			roomID := b.roomIDForLink(post.ChannelId)
			if eventID == "" || roomID == "" {
				return link
			}
			return buildEventLink(cfg.ElementURL, roomID, eventID)
		})
	}

	if strings.Contains(text, "~") {
		if channel, appErr := b.API.GetChannel(channelID); appErr == nil && channel != nil && channel.TeamId != "" {
			text = channelMention.ReplaceAllStringFunc(text, func(match string) string {
				m := channelMention.FindStringSubmatch(match)
				target, appErr := b.API.GetChannelByName(channel.TeamId, m[2], false)
				if appErr != nil || target == nil {
					return match
				}
				roomID := b.roomIDForLink(target.Id)
				if roomID == "" {
					return match
				}
				return m[1] + "[#" + target.DisplayName + "](" + buildEventLink(cfg.ElementURL, roomID, "") + ")"
			})
		}
	}
	return text
}

// roomIDForLink returns the room a channel is mapped to, as an ID when the
// homeserver can resolve it, otherwise as stored.
func (b *BridgeUtils) roomIDForLink(channelID string) string {
	identifier, err := b.GetMatrixRoomID(channelID)
	if err != nil || identifier == "" {
		return ""
	}
	if strings.HasPrefix(identifier, "#") && b.matrixClient != nil {
		if resolved, err := b.matrixClient.ResolveRoomAlias(identifier); err == nil {
			return resolved
		}
	}
	return identifier
}

// rewriteLinksToMattermost makes matrix.to and Element links to bridged rooms
// and events in text usable from Mattermost.
func (b *MatrixToMattermostBridge) rewriteLinksToMattermost(text string) string {
	if !strings.Contains(text, "matrix.to/#/") && b.getConfiguration().ElementURL == "" {
		return text
	}
	site := b.siteURL()
	if site == "" {
		return text
	}
	pattern := matrixToLink
	if element := strings.TrimRight(b.getConfiguration().ElementURL, "/"); element != "" {
		pattern = regexp.MustCompile(`(?:https://matrix\.to/#/|` + regexp.QuoteMeta(element) + `/#/room/)([!#][^/\s?)>\]]+)(?:/(\$[^\s?)>\]]+))?(?:\?[^\s)>\]]*)?`)
	}
	return pattern.ReplaceAllStringFunc(text, func(link string) string {
		m := pattern.FindStringSubmatch(link)
		room, err := url.PathUnescape(m[1])
		if err != nil {
			return link
		}
		channelIDBytes, err := b.kvstore.Get(kvstore.BuildRoomMappingKey(room))
		if err != nil || len(channelIDBytes) == 0 {
			return link
		}
		channelID := string(channelIDBytes)
		channel, appErr := b.API.GetChannel(channelID)
		if appErr != nil || channel == nil {
			return link
		}
		if m[2] == "" {
			return "~" + channel.Name
		}
		eventID, _ := url.PathUnescape(m[2])
		postID := b.getPostIDFromMatrixEvent(eventID, channelID)
		if postID == "" {
			return link
		}
		team, appErr := b.API.GetTeam(channel.TeamId)
		if appErr != nil || team == nil {
			return link
		}
		return site + "/" + team.Name + "/pl/" + postID
	})
}
