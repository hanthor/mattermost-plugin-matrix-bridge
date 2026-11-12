package main

import (
	"fmt"
	"regexp"

	"github.com/mattermost/mattermost/server/public/model"
)

// MattermostMentionResults represents extracted mentions from a Mattermost post
type MattermostMentionResults struct {
	UserMentions    []string // usernames mentioned
	ChannelMentions bool     // @channel/@all/@here
}

// extractMattermostMentions extracts @mentions from Mattermost post content
func (b *MattermostToMatrixBridge) extractMattermostMentions(post *model.Post) *MattermostMentionResults {
	text := post.Message
	results := &MattermostMentionResults{}

	// Extract @username mentions (similar to Mattermost's regex)
	// Match @word where word contains letters, numbers, dots, hyphens, underscores, and colons
	// Include colon to support bridged usernames like "matrix:username"
	// Use word boundary \b to avoid matching @username in email@username.com
	userMentionRegex := regexp.MustCompile(`\B@([a-zA-Z0-9\.\-_:]+)\b`)
	matches := userMentionRegex.FindAllStringSubmatch(text, -1)

	for _, match := range matches {
		if len(match) > 1 {
			username := match[1]
			switch username {
			case "here", "channel", "all":
				results.ChannelMentions = true
			default:
				results.UserMentions = append(results.UserMentions, username)
			}
		}
	}

	b.logger.LogDebug("Extracted mentions from Mattermost post", "post_id", post.Id, "message", text, "user_mentions", results.UserMentions, "channel_mentions", results.ChannelMentions)
	return results
}

// addMatrixMentionsWithData converts Mattermost mentions to Matrix format using pre-extracted mention data
func (b *MattermostToMatrixBridge) addMatrixMentionsWithData(content map[string]any, post *model.Post, mentions *MattermostMentionResults) {
	b.addMatrixUserMentions(content, post, mentions)
	// @channel, @all and @here notify everyone: in Matrix that is a room mention.
	if hasChannelMention(post.Message) {
		m, _ := content["m.mentions"].(map[string]any)
		if m == nil {
			m = map[string]any{}
			content["m.mentions"] = m
		}
		m["room"] = true
	}
}

// addMatrixUserMentions turns @username mentions into Matrix user mentions.
func (b *MattermostToMatrixBridge) addMatrixUserMentions(content map[string]any, post *model.Post, mentions *MattermostMentionResults) {
	b.logger.LogDebug("Processing mentions for Matrix", "post_id", post.Id, "user_mentions_count", len(mentions.UserMentions), "user_mentions", mentions.UserMentions)

	// Only process if we have user mentions (ignore channel mentions for now)
	if len(mentions.UserMentions) == 0 {
		b.logger.LogDebug("No user mentions found, skipping mention processing", "post_id", post.Id)
		return
	}

	var matrixUserIDs []string
	var mentionReplacements []struct {
		username    string
		ghostUserID string
		displayName string
	}

	// First pass: collect all mention data
	for _, username := range mentions.UserMentions {
		// Look up Mattermost user by username
		user, appErr := b.API.GetUserByUsername(username)
		if appErr != nil {
			b.logger.LogDebug("Failed to find Mattermost user for mention", "username", username, "error", appErr)
			continue
		}

		displayName := user.GetDisplayName(model.ShowFullName)
		if displayName == "" {
			displayName = user.Username
		}

		var matrixUserID string
		// A user who came from Matrix is mentioned as their real Matrix
		// account, never as a ghost of their Mattermost shadow.
		if originalMatrixUserID, err := b.GetMatrixUserIDFromMattermostUser(user.Id); err == nil && originalMatrixUserID != "" {
			matrixUserID = originalMatrixUserID
			b.logger.LogDebug("Mentioning bridged Matrix user by their own account", "username", username, "matrix_user_id", matrixUserID)
		} else if user.IsRemote() {
			b.logger.LogDebug("Skipping mention of remote user without a Matrix mapping", "username", username)
			continue
		} else if ghostUserID, exists := b.getGhostUser(user.Id); exists {
			matrixUserID = ghostUserID
		} else {
			ghostUserID, err := b.ensureAccount(user.Id)
			if err != nil {
				b.logger.LogWarn("Failed to create ghost user for mentioned user", "username", username, "user_id", user.Id, "error", err)
				continue
			}
			matrixUserID = ghostUserID
		}

		matrixUserIDs = append(matrixUserIDs, matrixUserID)
		mentionReplacements = append(mentionReplacements, struct {
			username    string
			ghostUserID string
			displayName string
		}{username, matrixUserID, displayName})
	}

	// Only proceed if we have Matrix users to mention
	if len(matrixUserIDs) == 0 {
		b.logger.LogDebug("No Matrix ghost users found for any mentions, skipping mention processing", "post_id", post.Id, "attempted_usernames", mentions.UserMentions)
		return
	}

	// Handle HTML content replacement
	updatedHTML, hasHTML := content["formatted_body"].(string)
	if !hasHTML {
		// If no HTML content, create it from plain text
		if plainText, hasPlain := content["body"].(string); hasPlain {
			updatedHTML = plainText
		} else {
			return // No content to process
		}
	}

	// Second pass: replace mentions in HTML content
	for _, replacement := range mentionReplacements {
		// Replace @username with proper Matrix mention pill format
		// Use regex with word boundaries to avoid matching substrings (e.g., @alice in email@alice.com)
		usernamePattern := fmt.Sprintf(`\B@%s\b`, regexp.QuoteMeta(replacement.username))
		usernameRegex := regexp.MustCompile(usernamePattern)

		// Create Matrix mention pill format to match native Matrix mentions
		matrixMentionPill := fmt.Sprintf(`<a href="https://matrix.to/#/%s">@%s</a>`,
			replacement.ghostUserID, replacement.displayName)
		updatedHTML = usernameRegex.ReplaceAllString(updatedHTML, matrixMentionPill)
	}

	// Add Matrix mentions structure
	mentionsField := map[string]any{
		"user_ids": matrixUserIDs,
	}
	content["m.mentions"] = mentionsField
	content["formatted_body"] = updatedHTML
	content["format"] = "org.matrix.custom.html"

	b.logger.LogDebug("Added Matrix mentions to message", "post_id", post.Id, "mentioned_users", len(matrixUserIDs), "matrix_user_ids", matrixUserIDs, "m_mentions", mentionsField)
}
