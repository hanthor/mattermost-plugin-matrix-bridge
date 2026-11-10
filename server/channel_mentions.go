package main

import (
	"regexp"
	"strings"
)

var (
	channelMentionPattern = regexp.MustCompile(`(^|[^\w@])@(channel|all|here)\b`)
	roomWordPattern       = regexp.MustCompile(`(^|[^\w@])@room\b`)
)

// hasChannelMention reports whether text notifies the whole channel.
func hasChannelMention(text string) bool {
	return channelMentionPattern.MatchString(text)
}

// roomMentionToChannel makes a Matrix room mention a Mattermost @channel
// mention. Clients mark it in m.mentions and usually write "@room" in the
// text; either is enough.
func roomMentionToChannel(eventContent map[string]any, text string) string {
	mentions, _ := eventContent["m.mentions"].(map[string]any)
	room, _ := mentions["room"].(bool)
	hasWord := roomWordPattern.MatchString(text)
	if !room && !hasWord {
		return text
	}
	if hasWord {
		return roomWordPattern.ReplaceAllString(text, "${1}@channel")
	}
	if strings.Contains(text, "@channel") {
		return text
	}
	return "@channel " + text
}
