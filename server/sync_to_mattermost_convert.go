package main

import (
	"regexp"
	"strings"
)

// convertMatrixToMattermost converts Matrix message format to Mattermost
func (b *MatrixToMattermostBridge) convertMatrixToMattermost(content string) string {
	// Basic conversion - can be enhanced later
	// Matrix uses different markdown syntax in some cases

	// Convert Matrix mentions (@username:server.com) to Mattermost format if possible
	// For now, keep as-is since we'd need user mapping

	return content
}

// stripEmojiVariation removes the variation selectors that decorate some emoji.
func stripEmojiVariation(s string) string {
	s = strings.ReplaceAll(s, "\uFE0F", "")    // variation selector-16
	return strings.ReplaceAll(s, "\uFE0E", "") // variation selector-15
}

// emojiNameForUnicode returns the Mattermost emoji name for a Unicode emoji.
// With stripped set, variation selectors are ignored on both sides.
func emojiNameForUnicode(emoji string, stripped bool) (string, bool) {
	best := ""
	for name, index := range emojiNameToIndex {
		hex, ok := emojiIndexToUnicode[index]
		if !ok {
			continue
		}
		candidate := hexToUnicode(hex)
		if stripped {
			candidate = stripEmojiVariation(candidate)
		}
		if candidate != emoji {
			continue
		}
		if best == "" || len(name) < len(best) || (len(name) == len(best) && name < best) {
			best = name
		}
	}
	return best, best != ""
}

// convertMatrixEmojiToMattermost converts Matrix emoji format to Mattermost
func (b *MatrixToMattermostBridge) convertMatrixEmojiToMattermost(matrixEmoji string) string {
	// Matrix reactions can be Unicode emoji or custom emoji

	// If it's already a simple name (like "thumbsup"), validate it exists in Mattermost
	if regexp.MustCompile(`^[a-z0-9_+-]+$`).MatchString(matrixEmoji) {
		// Check if this emoji name exists in our mapping
		if _, exists := emojiNameToIndex[matrixEmoji]; exists {
			return matrixEmoji
		}
		// Fall through to Unicode handling if name doesn't exist
	}

	// For Unicode emoji, find the Mattermost name. An emoji can have several
	// names (+1 and thumbsup), so pick one deterministically: the shortest,
	// then the first alphabetically. Iterating the map directly gave a
	// different name on every run.
	if name, ok := emojiNameForUnicode(matrixEmoji, false); ok {
		return name
	}

	// Try to find similar matches (without variation selectors)
	if stripped := stripEmojiVariation(matrixEmoji); stripped != matrixEmoji {
		if name, ok := emojiNameForUnicode(stripped, true); ok {
			return name
		}
	}

	// Fallback mapping for common emojis
	switch matrixEmoji {
	case "👍":
		return "+1"
	case "👎":
		return "-1"
	case "❤️", "❤":
		return "heart"
	case "😂":
		return "joy"
	case "😢":
		return "cry"
	case "😄":
		return "smile"
	case "😃":
		return "smiley"
	case "😊":
		return "blush"
	case "🔥":
		return "fire"
	case "🎉":
		return "tada"
	case "👏":
		return "clap"
	default:
		// If we can't find a match, use a safe fallback
		// Check if this is a complex emoji sequence that we should just skip
		if len([]rune(matrixEmoji)) > 3 {
			b.logger.LogWarn("Unknown complex emoji from Matrix", "emoji", matrixEmoji)
			return "question" // Use question mark emoji as fallback
		}

		// For simple unknown emoji, try to create a safe name
		safeName := regexp.MustCompile(`[^a-z0-9]`).ReplaceAllString(strings.ToLower(matrixEmoji), "")
		if safeName == "" || len(safeName) < 2 {
			return "question" // Use question mark emoji as fallback
		}
		return safeName
	}
}

const replyQuoteMaxRunes = 140

// replyQuote renders the message a Matrix reply points at as a Markdown quote:
// its author and the start of its first line.
func (b *MatrixToMattermostBridge) replyQuote(postID string) string {
	parent, appErr := b.API.GetPost(postID)
	if appErr != nil || parent == nil {
		return ""
	}
	text := strings.TrimSpace(parent.Message)
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	if text == "" && len(parent.FileIds) > 0 {
		text = "(attachment)"
	}
	if text == "" {
		return ""
	}
	if runes := []rune(text); len(runes) > replyQuoteMaxRunes {
		text = string(runes[:replyQuoteMaxRunes]) + "…"
	}
	author := ""
	if user, appErr := b.API.GetUser(parent.UserId); appErr == nil && user != nil {
		author = "**@" + user.Username + ":** "
	}
	return "> " + author + text
}
