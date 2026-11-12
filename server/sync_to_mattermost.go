package main

import (
	"encoding/json"
	"strings"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

// MatrixToMattermostBridge handles syncing FROM Matrix TO Mattermost
type MatrixToMattermostBridge struct {
	*BridgeUtils
}

// NewMatrixToMattermostBridge creates a new MatrixToMattermostBridge instance
func NewMatrixToMattermostBridge(utils *BridgeUtils) *MatrixToMattermostBridge {
	return &MatrixToMattermostBridge{
		BridgeUtils: utils,
	}
}

// syncMatrixMessageToMattermost handles syncing Matrix messages to Mattermost posts
func (b *MatrixToMattermostBridge) syncMatrixMessageToMattermost(event MatrixEvent, channelID string) error {
	b.logger.LogDebug("Syncing Matrix message to Mattermost", "event_id", event.EventID, "sender", event.Sender, "channel_id", channelID)

	// A replayed event (after a restart, or when a transaction was retried)
	// must not become a second post.
	if existing, err := b.kvstore.Get(kvstore.BuildMatrixEventPostKey(event.EventID)); err == nil && len(existing) > 0 {
		b.logger.LogDebug("Matrix event already has a post; skipping", "event_id", event.EventID, "post_id", string(existing))
		return nil
	}

	// Extract Mattermost metadata if present
	mattermostPostID, mattermostRemoteID := b.extractMattermostMetadata(event)
	if mattermostPostID != "" || mattermostRemoteID != "" {
		b.logger.LogDebug("Found Mattermost metadata in Matrix event", "event_id", event.EventID, "mattermost_post_id", mattermostPostID, "mattermost_remote_id", mattermostRemoteID)
	}

	// Check if this Matrix event originated from Mattermost to prevent loops
	if mattermostPostID != "" {
		// This event contains a Mattermost post ID - check if we already have this post
		if existingPost, appErr := b.API.GetPost(mattermostPostID); appErr == nil && existingPost != nil {
			b.logger.LogDebug("Skipping Matrix message that originated from Mattermost", "event_id", event.EventID, "mattermost_post_id", mattermostPostID, "existing_post_id", existingPost.Id)
			return nil // Skip processing - this is our own post echoed back from Matrix
		}
		// If GetPost fails, the post might have been deleted, so continue processing
	}

	// Also check remote ID for additional loop prevention
	if mattermostRemoteID != "" && mattermostRemoteID == b.remoteID {
		b.logger.LogDebug("Skipping Matrix message from our own remote ID", "event_id", event.EventID, "remote_id", mattermostRemoteID)
		return nil // Skip processing - this originated from our bridge
	}

	// Check if this is a message edit (has m.relates_to with rel_type: m.replace)
	if relatesTo, exists := event.Content["m.relates_to"].(map[string]any); exists {
		if relType, exists := relatesTo["rel_type"].(string); exists && relType == "m.replace" {
			// This is an edit - handle separately (allow empty content for deletions)
			return b.handleMatrixMessageEdit(event, channelID)
		}
	}

	// Check if this is a file/image attachment
	if msgType, exists := event.Content["msgtype"].(string); exists {
		switch msgType {
		case "m.image", "m.file", "m.video", "m.audio":
			return b.syncMatrixFileToMattermost(event, channelID)
		}
	}

	// Extract message content (smart format detection: prefer formatted text, fallback to plain text)
	content := b.extractMatrixMessageContent(event)
	if loc, ok := locationText(event.Content); ok {
		content = loc
	}
	if poll, ok := pollText(event.Content); ok {
		content = poll
	}

	// For new messages (not edits), skip if content is empty
	if content == "" {
		b.logger.LogDebug("Matrix message has no body content, skipping new message", "event_id", event.EventID, "content", event.Content)
		return nil // Empty new messages don't need to be synced
	}

	// A room mention notifies everyone: in Mattermost that is @channel.
	content = roomMentionToChannel(event.Content, content)

	// Get or create Mattermost user for the Matrix sender
	mattermostUserID, err := b.getOrCreateMattermostUser(event.Sender, channelID)
	if err != nil {
		return errors.Wrap(err, "failed to get or create Mattermost user")
	}

	// Convert Matrix content to Mattermost format
	mattermostContent := b.rewriteLinksToMattermost(b.convertMatrixToMattermost(content))

	// Check if this is a threaded message (reply)
	var rootID string
	var repliedToPostID string // the message a real (not fallback) reply points at
	if relatesTo, exists := event.Content["m.relates_to"].(map[string]any); exists {
		// Check for Matrix reply structure (m.in_reply_to)
		if inReplyTo, hasInReplyTo := relatesTo["m.in_reply_to"].(map[string]any); hasInReplyTo {
			if parentEventID, hasEventID := inReplyTo["event_id"].(string); hasEventID {
				// Find the Mattermost post ID for this Matrix event
				var mattermostPostID string
				if mattermostPostID = b.getPostIDFromMatrixEvent(parentEventID, channelID); mattermostPostID != "" {
					if fallback, _ := relatesTo["is_falling_back"].(bool); !fallback {
						repliedToPostID = mattermostPostID
					}
					// Found direct mapping, now get the actual thread root
					rootID = b.getThreadRootFromPostID(mattermostPostID)
					b.logger.LogDebug("Found Matrix reply to primary message", "matrix_event_id", event.EventID, "parent_event_id", parentEventID, "parent_post_id", mattermostPostID, "thread_root_id", rootID)
				} else {
					// Try to find if parent is a file attachment related to a primary message
					if mattermostPostID = b.getPostIDFromRelatedMatrixEvent(parentEventID, channelID); mattermostPostID != "" {
						// Found the primary message through file relation, now get the actual thread root
						rootID = b.getThreadRootFromPostID(mattermostPostID)
						b.logger.LogDebug("Found Matrix reply to file attachment, mapped to primary message", "matrix_event_id", event.EventID, "parent_event_id", parentEventID, "primary_post_id", mattermostPostID, "thread_root_id", rootID)
					} else {
						b.logger.LogDebug("Matrix reply parent not found in Mattermost", "matrix_event_id", event.EventID, "parent_event_id", parentEventID)
					}
				}
			}
		}
		// Also check for thread relation (m.thread) as fallback
		if rootID == "" {
			if relType, exists := relatesTo["rel_type"].(string); exists && relType == "m.thread" {
				if parentEventID, exists := relatesTo["event_id"].(string); exists {
					// Find the Mattermost post ID for this Matrix event
					var mattermostPostID string
					if mattermostPostID = b.getPostIDFromMatrixEvent(parentEventID, channelID); mattermostPostID != "" {
						// Found direct mapping, now get the actual thread root
						rootID = b.getThreadRootFromPostID(mattermostPostID)
						b.logger.LogDebug("Found Matrix thread to primary message", "matrix_event_id", event.EventID, "parent_event_id", parentEventID, "parent_post_id", mattermostPostID, "thread_root_id", rootID)
					} else {
						// Try to find if parent is a file attachment related to a primary message
						if mattermostPostID = b.getPostIDFromRelatedMatrixEvent(parentEventID, channelID); mattermostPostID != "" {
							// Found the primary message through file relation, now get the actual thread root
							rootID = b.getThreadRootFromPostID(mattermostPostID)
							b.logger.LogDebug("Found Matrix thread to file attachment, mapped to primary message", "matrix_event_id", event.EventID, "parent_event_id", parentEventID, "primary_post_id", mattermostPostID, "thread_root_id", rootID)
						}
					}
				}
			}
		}
	}

	// Mattermost threads have no "reply to this message"; keep the context by
	// quoting the message that was replied to when it is not the thread's root.
	if repliedToPostID != "" && repliedToPostID != rootID {
		if quote := b.replyQuote(repliedToPostID); quote != "" {
			mattermostContent = quote + "\n\n" + mattermostContent
		}
	}

	// Mattermost refuses posts above its limit: a long message becomes a few
	// consecutive posts instead of being lost.
	parts := splitLongMessage(mattermostContent, maxPostRunes)
	mattermostContent = parts[0]

	// Create Mattermost post
	post := &model.Post{
		UserId:    mattermostUserID,
		ChannelId: channelID,
		Message:   mattermostContent,
		CreateAt:  event.Timestamp,
		RootId:    rootID,
		RemoteId:  &b.remoteID, // Attribute to Matrix remote
		Props:     make(map[string]any),
	}

	// Store Matrix event ID in post properties for reaction mapping and edit tracking
	config := b.getConfiguration()
	serverDomain := extractServerDomain(b.logger, config.MatrixServerURL)
	propertyKey := "matrix_event_id_" + serverDomain
	post.Props[propertyKey] = event.EventID
	post.Props["from_matrix"] = true

	// Create the post in Mattermost
	createdPost, appErr := b.API.CreatePost(post)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to create Mattermost post")
	}

	// Store Matrix event ID to Mattermost post ID mapping for efficient reverse lookups
	b.storeMatrixEventPostMapping(event.EventID, createdPost.Id)

	// The rest of a long message follows as plain posts. Edits and reactions
	// refer to the first one.
	for i, part := range parts[1:] {
		next := &model.Post{
			UserId: mattermostUserID, ChannelId: channelID, Message: part, RootId: rootID,
			CreateAt: event.Timestamp + int64(i+1), RemoteId: &b.remoteID,
			Props: map[string]any{"from_matrix": true},
		}
		if _, appErr := b.API.CreatePost(next); appErr != nil {
			b.logger.LogWarn("Could not post the rest of a long Matrix message", "error", appErr, "matrix_event_id", event.EventID, "part", i+2)
			break
		}
	}

	b.logger.LogDebug("Successfully synced Matrix message to Mattermost", "matrix_event_id", event.EventID, "mattermost_post_id", createdPost.Id, "sender", event.Sender, "channel_id", channelID)

	// Record successful message sync from Matrix
	b.metrics.RecordMessageFromMatrix()

	return nil
}

// handleMatrixMessageEdit handles Matrix message edits by updating the corresponding Mattermost post
func (b *MatrixToMattermostBridge) handleMatrixMessageEdit(event MatrixEvent, channelID string) error {
	// Extract the new content from the edit event
	newContent := b.extractMatrixMessageContent(event)
	// Extract the original event ID being edited
	relatesTo, exists := event.Content["m.relates_to"].(map[string]any)
	if !exists {
		return errors.New("edit event missing m.relates_to")
	}

	originalEventID, exists := relatesTo["event_id"].(string)
	if !exists {
		return errors.New("edit event missing original event_id")
	}

	// Find the Mattermost post corresponding to the original Matrix event
	postID := b.getPostIDFromMatrixEvent(originalEventID, channelID)
	if postID == "" {
		b.logger.LogWarn("Cannot find Mattermost post for Matrix edit", "original_event_id", originalEventID, "edit_event_id", event.EventID)
		return nil // Post not found, maybe it wasn't synced
	}

	// Get the existing post
	post, appErr := b.API.GetPost(postID)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to get post for edit")
	}

	// Update the post content (allow empty content - user may have deleted all text)
	post.Message = truncateForMattermost(b.convertMatrixToMattermost(newContent), maxPostRunes)
	post.EditAt = event.Timestamp

	// Update the post
	updatedPost, appErr := b.API.UpdatePost(post)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to update post")
	}

	b.logger.LogDebug("Successfully updated Mattermost post from Matrix edit", "original_matrix_event_id", originalEventID, "edit_matrix_event_id", event.EventID, "mattermost_post_id", updatedPost.Id)
	return nil
}

// syncMatrixFileToMattermost handles syncing Matrix file attachments to Mattermost
func (b *MatrixToMattermostBridge) syncMatrixFileToMattermost(event MatrixEvent, channelID string) error {
	b.logger.LogDebug("Syncing Matrix file to Mattermost", "event_id", event.EventID, "sender", event.Sender, "channel_id", channelID)

	// Extract file metadata
	body, exists := event.Content["body"].(string)
	if !exists {
		b.logger.LogWarn("Matrix file message missing body field", "event_id", event.EventID)
		return nil
	}

	// Files in encrypted rooms carry their location and key in "file".
	encryptedFile, _ := event.Content["file"].(map[string]any)
	url, exists := event.Content["url"].(string)
	if encryptedFile != nil {
		url, exists = encryptedFile["url"].(string)
	}
	if !exists {
		b.logger.LogWarn("Matrix file message missing url field", "event_id", event.EventID)
		return nil
	}

	// Get or create Mattermost user for the Matrix sender
	mattermostUserID, err := b.getOrCreateMattermostUser(event.Sender, channelID)
	if err != nil {
		return errors.Wrap(err, "failed to get or create Mattermost user for file")
	}

	// A file over the limit is not copied; say so rather than drop it silently.
	if info, ok := event.Content["info"].(map[string]any); ok {
		if size, ok := info["size"].(float64); ok && b.maxFileSize > 0 && int64(size) > b.maxFileSize {
			b.noticeFileNotCopied(channelID, mattermostUserID, body, int64(size))
			return nil
		}
	}

	// Download the file from Matrix
	fileData, err := b.downloadMatrixFile(url)
	if err != nil {
		return errors.Wrap(err, "failed to download Matrix file")
	}
	if encryptedFile != nil {
		fileData, err = decryptAttachment(encryptedFile, fileData)
		if err != nil {
			return errors.Wrap(err, "failed to decrypt Matrix file")
		}
	}

	// Upload file to Mattermost
	uploadedFileInfo, appErr := b.API.UploadFile(fileData, channelID, body)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to upload file to Mattermost")
	}

	// Check if this is a threaded message (reply)
	var rootID string
	if relatesTo, exists := event.Content["m.relates_to"].(map[string]any); exists {
		// Check for Matrix reply structure (m.in_reply_to)
		if inReplyTo, hasInReplyTo := relatesTo["m.in_reply_to"].(map[string]any); hasInReplyTo {
			if parentEventID, hasEventID := inReplyTo["event_id"].(string); hasEventID {
				// Find the Mattermost post ID for this Matrix event
				if mattermostPostID := b.getPostIDFromMatrixEvent(parentEventID, channelID); mattermostPostID != "" {
					rootID = mattermostPostID
					b.logger.LogDebug("Found Matrix file reply, setting root ID", "matrix_event_id", event.EventID, "parent_event_id", parentEventID, "mattermost_root_id", rootID)
				} else {
					b.logger.LogDebug("Matrix file reply parent not found in Mattermost", "matrix_event_id", event.EventID, "parent_event_id", parentEventID)
				}
			}
		}
		// Also check for thread relation (m.thread) as fallback
		if rootID == "" {
			if relType, exists := relatesTo["rel_type"].(string); exists && relType == "m.thread" {
				if parentEventID, exists := relatesTo["event_id"].(string); exists {
					// Find the Mattermost post ID for this Matrix event
					if mattermostPostID := b.getPostIDFromMatrixEvent(parentEventID, channelID); mattermostPostID != "" {
						rootID = mattermostPostID
						b.logger.LogDebug("Found Matrix file thread, setting root ID", "matrix_event_id", event.EventID, "parent_event_id", parentEventID, "mattermost_root_id", rootID)
					}
				}
			}
		}
	}

	// Create Mattermost post with file attachment (no message text needed)
	post := &model.Post{
		UserId:    mattermostUserID,
		ChannelId: channelID,
		Message:   "", // Empty message - like chess, the file attachment speaks for itself
		CreateAt:  event.Timestamp,
		RootId:    rootID,
		RemoteId:  &b.remoteID,
		FileIds:   []string{uploadedFileInfo.Id},
		Props:     make(map[string]any),
	}

	// Store Matrix event ID in post properties for reaction mapping and edit tracking
	config := b.getConfiguration()
	serverDomain := extractServerDomain(b.logger, config.MatrixServerURL)
	propertyKey := "matrix_event_id_" + serverDomain
	post.Props[propertyKey] = event.EventID
	post.Props["from_matrix"] = true

	// Create the post in Mattermost
	createdPost, appErr := b.API.CreatePost(post)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to create Mattermost post with file attachment")
	}

	// Store Matrix event ID to Mattermost post ID mapping for efficient reverse lookups
	b.storeMatrixEventPostMapping(event.EventID, createdPost.Id)

	b.logger.LogDebug("Successfully synced Matrix file to Mattermost", "matrix_event_id", event.EventID, "mattermost_post_id", createdPost.Id, "filename", body, "file_id", uploadedFileInfo.Id)
	return nil
}

// syncMatrixReactionToMattermost handles syncing Matrix reactions to Mattermost
func (b *MatrixToMattermostBridge) syncMatrixReactionToMattermost(event MatrixEvent, channelID string) error {
	b.logger.LogDebug("Syncing Matrix reaction to Mattermost", "event_id", event.EventID, "sender", event.Sender, "channel_id", channelID)

	// Extract reaction key (emoji)
	relatesTo, exists := event.Content["m.relates_to"].(map[string]any)
	if !exists {
		return errors.New("reaction event missing m.relates_to")
	}

	key, exists := relatesTo["key"].(string)
	if !exists {
		return errors.New("reaction event missing key")
	}

	targetEventID, exists := relatesTo["event_id"].(string)
	if !exists {
		return errors.New("reaction event missing target event_id")
	}

	// Find the Mattermost post corresponding to the target Matrix event
	postID := b.getPostIDFromMatrixEvent(targetEventID, channelID)
	if postID == "" {
		// If we can't find a direct match, check if this is a reaction to a file message
		// that's related to a primary message
		postID = b.getPostIDFromRelatedMatrixEvent(targetEventID, channelID)
		if postID == "" {
			b.logger.LogWarn("Cannot find Mattermost post for Matrix reaction", "target_event_id", targetEventID, "reaction_event_id", event.EventID)
			return nil // Post not found, maybe it wasn't synced
		}
		b.logger.LogDebug("Found Mattermost post via related Matrix event", "target_event_id", targetEventID, "mattermost_post_id", postID)
	}

	// Get or create Mattermost user for the reaction sender
	mattermostUserID, err := b.getOrCreateMattermostUser(event.Sender, channelID)
	if err != nil {
		return errors.Wrap(err, "failed to get or create Mattermost user for reaction")
	}

	// Convert Matrix emoji to Mattermost format; an image reaction becomes a
	// custom emoji.
	emojiName := b.convertMatrixEmojiToMattermost(key)
	if strings.HasPrefix(key, "mxc://") {
		shortcode, _ := event.Content["com.beeper.reaction.shortcode"].(string)
		if name, ok := b.emojiForImageReaction(key, shortcode); ok {
			emojiName = name
		}
	}

	// Create Mattermost reaction
	reaction := &model.Reaction{
		UserId:    mattermostUserID,
		PostId:    postID,
		EmojiName: emojiName,
		CreateAt:  event.Timestamp,
		RemoteId:  &b.remoteID, // Attribute to Matrix remote
	}

	// Add the reaction
	_, appErr := b.API.AddReaction(reaction)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to add reaction to Mattermost")
	}

	// Store mapping of Matrix reaction event ID to Mattermost reaction info for deletion purposes
	// We need to store the info to reconstruct the model.Reaction object for deletion
	reactionKey := kvstore.BuildMatrixReactionKey(event.EventID)
	reactionInfo := map[string]string{
		"post_id":    postID,
		"user_id":    mattermostUserID,
		"emoji_name": emojiName,
	}
	reactionInfoBytes, _ := json.Marshal(reactionInfo)
	if err := b.kvstore.Set(reactionKey, reactionInfoBytes); err != nil {
		b.logger.LogWarn("Failed to store Matrix reaction mapping", "error", err, "reaction_event_id", event.EventID)
	} else {
		b.logger.LogDebug("Stored Matrix reaction mapping", "reaction_event_id", event.EventID, "post_id", postID, "emoji", emojiName)
	}

	b.logger.LogDebug("Successfully synced Matrix reaction to Mattermost", "matrix_event_id", event.EventID, "mattermost_post_id", postID, "emoji", emojiName, "sender", event.Sender)
	return nil
}

// syncMatrixRedactionToMattermost handles Matrix message deletions (redactions)
func (b *MatrixToMattermostBridge) syncMatrixRedactionToMattermost(event MatrixEvent, channelID string) error {
	b.logger.LogDebug("Syncing Matrix redaction to Mattermost", "event_id", event.EventID, "sender", event.Sender, "channel_id", channelID)

	// Extract the redacted event ID
	redactsEventID, exists := event.Content["redacts"].(string)
	if !exists {
		return errors.New("redaction event missing redacts field")
	}

	// Get the Matrix room ID to fetch the redacted event details
	matrixRoomIdentifier, err := b.GetMatrixRoomID(channelID)
	if err != nil {
		b.logger.LogWarn("Failed to get Matrix room identifier for redaction", "error", err, "channel_id", channelID)
		return nil
	}

	if matrixRoomIdentifier == "" {
		b.logger.LogWarn("No Matrix room mapped for channel", "channel_id", channelID)
		return nil
	}

	// Resolve room alias to room ID if needed
	matrixRoomID, err := b.matrixClient.ResolveRoomAlias(matrixRoomIdentifier)
	if err != nil {
		b.logger.LogWarn("Failed to resolve Matrix room identifier for redaction", "error", err, "room_identifier", matrixRoomIdentifier)
		return nil
	}

	// Get the redacted event to determine its type
	redactedEvent, err := b.matrixClient.GetEvent(matrixRoomID, redactsEventID)
	if err != nil {
		b.logger.LogWarn("Failed to get redacted Matrix event", "error", err, "redacted_event_id", redactsEventID)
		// If we can't get the event details, try to handle it as a post deletion (fallback)
		return b.handlePostDeletion(redactsEventID, channelID, event.EventID)
	}

	// Check the type of the redacted event
	redactedEventType, ok := redactedEvent["type"].(string)
	if !ok {
		b.logger.LogWarn("Redacted Matrix event has no type field", "redacted_event_id", redactsEventID)
		return nil
	}

	b.logger.LogDebug("Processing redaction", "redacted_event_type", redactedEventType, "redacted_event_id", redactsEventID)

	switch redactedEventType {
	case "m.reaction":
		// This is a reaction removal
		return b.handleReactionDeletion(redactedEvent, channelID, event.EventID)
	case "m.room.message":
		// This is a message deletion
		return b.handlePostDeletion(redactsEventID, channelID, event.EventID)
	default:
		b.logger.LogDebug("Ignoring redaction of unsupported event type", "redacted_event_type", redactedEventType, "redacted_event_id", redactsEventID)
		return nil
	}
}

// handleReactionDeletion handles the removal of a Matrix reaction from Mattermost
func (b *MatrixToMattermostBridge) handleReactionDeletion(redactedReactionEvent map[string]any, channelID, redactionEventID string) error {
	b.logger.LogDebug("Handling Matrix reaction deletion", "redaction_event_id", redactionEventID, "channel_id", channelID)

	// Get the redacted Matrix reaction event ID
	redactedEventID, ok := redactedReactionEvent["event_id"].(string)
	if !ok {
		return errors.New("redacted reaction event missing event_id")
	}

	// Look up the stored Mattermost reaction info using the Matrix reaction event ID
	reactionKey := kvstore.BuildMatrixReactionKey(redactedEventID)
	reactionInfoBytes, err := b.kvstore.Get(reactionKey)
	if err != nil {
		b.logger.LogWarn("Cannot find stored Matrix reaction mapping", "matrix_event_id", redactedEventID, "redaction_event_id", redactionEventID)
		return nil // Reaction mapping not found, maybe it wasn't stored or already deleted
	}

	// Parse the stored reaction info
	var reactionInfo map[string]string
	if err := json.Unmarshal(reactionInfoBytes, &reactionInfo); err != nil {
		b.logger.LogWarn("Failed to parse stored Matrix reaction info", "error", err, "matrix_event_id", redactedEventID)
		// Clean up the corrupted mapping
		_ = b.kvstore.Delete(reactionKey)
		return nil
	}

	postID := reactionInfo["post_id"]
	userID := reactionInfo["user_id"]
	emojiName := reactionInfo["emoji_name"]

	b.logger.LogDebug("Found stored Matrix reaction mapping", "matrix_event_id", redactedEventID, "post_id", postID, "user_id", userID, "emoji", emojiName)

	// Create reaction object for removal
	reaction := &model.Reaction{
		UserId:    userID,
		PostId:    postID,
		EmojiName: emojiName,
	}

	// Remove the reaction
	if appErr := b.API.RemoveReaction(reaction); appErr != nil {
		b.logger.LogWarn("Failed to remove Matrix reaction from Mattermost", "error", appErr, "post_id", postID, "emoji", emojiName)
		return errors.Wrap(appErr, "failed to remove reaction from Mattermost")
	}

	// Clean up the mapping after successful deletion
	if err := b.kvstore.Delete(reactionKey); err != nil {
		b.logger.LogWarn("Failed to clean up Matrix reaction mapping", "error", err, "key", reactionKey)
	}

	b.logger.LogDebug("Successfully removed Matrix reaction from Mattermost", "matrix_event_id", redactedEventID, "post_id", postID, "emoji", emojiName, "redaction_event_id", redactionEventID)
	return nil
}

// handlePostDeletion handles the deletion of a Matrix message from Mattermost
func (b *MatrixToMattermostBridge) handlePostDeletion(redactsEventID, channelID, redactionEventID string) error {
	b.logger.LogDebug("Handling Matrix post deletion", "redacted_event_id", redactsEventID, "redaction_event_id", redactionEventID, "channel_id", channelID)

	// Find the Mattermost post corresponding to the redacted Matrix event
	postID := b.getPostIDFromMatrixEvent(redactsEventID, channelID)
	if postID == "" {
		b.logger.LogWarn("Cannot find Mattermost post for Matrix redaction", "redacted_event_id", redactsEventID, "redaction_event_id", redactionEventID)
		return nil // Post not found, maybe it wasn't synced
	}

	// Delete the post
	if appErr := b.API.DeletePost(postID); appErr != nil {
		return errors.Wrap(appErr, "failed to delete post")
	}

	b.logger.LogDebug("Successfully deleted Mattermost post from Matrix redaction", "redacted_matrix_event_id", redactsEventID, "redaction_matrix_event_id", redactionEventID, "mattermost_post_id", postID)
	return nil
}
