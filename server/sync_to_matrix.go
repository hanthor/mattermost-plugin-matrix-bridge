package main

import (
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/matrix"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

// FileTracker interface for dependency injection
type FileTracker interface {
	GetFiles(postID string) []*PendingFile
	AddFile(postID string, file *PendingFile)
	RemoveFile(postID, fileID string) bool
}

// PostTrackerInterface provides dependency injection for post tracking operations
type PostTrackerInterface interface {
	Get(postID string) (int64, bool)
	Put(postID string, updateAt int64) error
	Delete(postID string)
}

// MattermostToMatrixBridge handles syncing FROM Mattermost TO Matrix
type MattermostToMatrixBridge struct {
	*BridgeUtils
	fileTracker FileTracker
	postTracker PostTrackerInterface
}

// NewMattermostToMatrixBridge creates a new MattermostToMatrixBridge instance
func NewMattermostToMatrixBridge(utils *BridgeUtils, fileTracker FileTracker, postTracker PostTrackerInterface) *MattermostToMatrixBridge {
	return &MattermostToMatrixBridge{
		BridgeUtils: utils,
		fileTracker: fileTracker,
		postTracker: postTracker,
	}
}

// SyncPostsToMatrix syncs multiple posts to Matrix for a single channel, optimizing room resolution
func (b *MattermostToMatrixBridge) SyncPostsToMatrix(posts []*model.Post, channelID string) error {
	if len(posts) == 0 {
		return nil
	}

	// Resolve room ID once for all posts
	matrixRoomIdentifier, err := b.GetMatrixRoomID(channelID)
	if err != nil {
		return errors.Wrap(err, "failed to get Matrix room identifier")
	}

	if matrixRoomIdentifier == "" {
		// Might be a DM channel, handle individually or skip if not found
		// For simplicity, if it's potentially a DM, we'll fall back to individual sync
		for _, post := range posts {
			if err := b.SyncPostToMatrix(post, channelID); err != nil {
				b.logger.LogError("Failed to sync post in batch", "error", err, "post_id", post.Id)
			}
		}
		return nil
	}

	// Resolve room alias to room ID once
	matrixRoomID, err := b.matrixClient.ResolveRoomAlias(matrixRoomIdentifier)
	if err != nil {
		return errors.Wrap(err, "failed to resolve Matrix room identifier")
	}

	for _, post := range posts {
		// For each post, we still need to check deletion and other per-post logic
		// But we pass the resolved matrixRoomID to avoid re-resolving
		if err := b.syncPostToMatrixWithResolvedRoom(post, channelID, matrixRoomID); err != nil {
			b.logger.LogError("Failed to sync post in batch with resolved room", "error", err, "post_id", post.Id)
		}
	}

	return nil
}

func (b *MattermostToMatrixBridge) syncPostToMatrixWithResolvedRoom(post *model.Post, channelID, matrixRoomID string) error {
	// Check if this is a post deletion
	if post.DeleteAt != 0 {
		return b.deletePostFromMatrix(post, channelID)
	}

	// Calls are announced rather than relayed as messages.
	switch post.Type {
	case callPostType:
		return b.syncCallPost(post, matrixRoomID)
	case matrixCallPostType:
		return nil // the card for a call that came from Matrix
	}

	user, appErr := b.API.GetUser(post.UserId)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to get user")
	}
	b.syncProfileIfChanged(user)

	// Check if this post already has a Matrix event ID (indicating it's an edit)
	config := b.getConfiguration()
	serverDomain := extractServerDomain(b.logger, config.MatrixServerURL)
	propertyKey := "matrix_event_id_" + serverDomain

	var existingEventID string
	if post.Props != nil {
		if eventID, ok := post.Props[propertyKey].(string); ok {
			existingEventID = eventID
		}
	}

	if existingEventID != "" {
		// Check if this is a redundant edit from adding the Matrix event ID property
		if storedUpdateAt, exists := b.postTracker.Get(post.Id); exists {
			if post.UpdateAt == storedUpdateAt {
				// This post's UpdateAt matches the timestamp we stored when adding Matrix event ID
				// This is the redundant edit from adding the Matrix event ID property
				b.postTracker.Delete(post.Id)
				b.logger.LogDebug("Skipping redundant edit after post creation", "post_id", post.Id, "matrix_event_id", existingEventID, "stored_update_at", storedUpdateAt, "current_update_at", post.UpdateAt)
				return nil
			}
			// This is a genuine edit that happened after we added the Matrix event ID
			// Remove the tracking entry since we're processing a real edit now
			b.postTracker.Delete(post.Id)
			b.logger.LogDebug("Processing genuine edit after post creation", "post_id", post.Id, "matrix_event_id", existingEventID, "stored_update_at", storedUpdateAt, "current_update_at", post.UpdateAt)
		}

		// This is a genuine post edit - update the existing Matrix message
		err := b.updatePostInMatrix(post, matrixRoomID, existingEventID, user)
		if err != nil {
			return errors.Wrap(err, "failed to update post in Matrix")
		}
		b.logger.LogDebug("Successfully updated post in Matrix", "post_id", post.Id, "matrix_event_id", existingEventID)
	} else {
		// This is a new post - create new Matrix message
		err := b.createPostInMatrix(post, matrixRoomID, user, propertyKey, 0)
		if err != nil {
			return errors.Wrap(err, "failed to create post in Matrix")
		}
		b.logger.LogDebug("Successfully created new post in Matrix", "post_id", post.Id)
	}
	return nil
}

// SyncPostToMatrix syncs a single post (create, edit or delete) to the Matrix room mapped to channelID.
func (b *MattermostToMatrixBridge) SyncPostToMatrix(post *model.Post, channelID string) error {
	// Check if this is a post deletion
	if post.DeleteAt != 0 {
		return b.deletePostFromMatrix(post, channelID)
	}

	// First check if this is a regular channel with existing mapping
	matrixRoomIdentifier, err := b.GetMatrixRoomID(channelID)
	if err != nil {
		return errors.Wrap(err, "failed to get Matrix room identifier")
	}

	// If no regular room mapping, check if this is a DM channel
	if matrixRoomIdentifier == "" {
		isDM, userIDs, err := b.isDirectChannel(channelID)
		if err != nil {
			return errors.Wrap(err, "failed to check if channel is DM")
		}

		if isDM {
			// Private conversations are only bridged when everyone in them agreed.
			if !b.directMessagesAllowed(userIDs) {
				return nil
			}
			// Handle DM channel - get or create Matrix DM room
			matrixRoomIdentifier, err = b.getOrCreateDMRoom(channelID, userIDs, post.UserId)
			if err != nil {
				return errors.Wrap(err, "failed to get or create DM room")
			}
		} else {
			// Not a DM and no regular mapping - skip
			b.logger.LogWarn("No Matrix room mapped for channel", "channel_id", channelID)
			return nil
		}
	}

	// Resolve room alias to room ID if needed
	matrixRoomID, err := b.matrixClient.ResolveRoomAlias(matrixRoomIdentifier)
	if err != nil {
		return errors.Wrap(err, "failed to resolve Matrix room identifier")
	}

	return b.syncPostToMatrixWithResolvedRoom(post, channelID, matrixRoomID)
}

// createPostInMatrix creates a new post in Matrix and stores the event ID
// timestampMs, when not zero, is the time the Matrix event should carry (history import).
func (b *MattermostToMatrixBridge) createPostInMatrix(post *model.Post, matrixRoomID string, user *model.User, propertyKey string, timestampMs int64) error {
	// Skip creating ghost users for Matrix-originated users to prevent loops
	if user.IsRemote() {
		b.logger.LogDebug("Skipping Matrix sync for remote user", "user_id", user.Id, "username", user.Username)
		return nil
	}

	// Get the sending identity: a linked Matrix account, or a ghost
	matrixUserID, sender, _, err := b.resolveSender(user.Id)
	if err != nil {
		return errors.Wrap(err, "failed to get Matrix user ID")
	}

	// Ensure the sender is joined to the room
	err = b.ensureSenderInRoom(sender, matrixUserID, matrixRoomID, user.Id)
	if err != nil {
		return errors.Wrap(err, "failed to ensure user is in room")
	}

	// Process mentions first on the original text
	mentionData := b.extractMattermostMentions(post)

	// Convert post content to Matrix format
	plainText, htmlContent := convertMattermostToMatrix(b.rewriteLinksToMatrix(postMessageText(post), post.ChannelId))

	// Create Matrix message content structure
	messageContent := map[string]any{
		"msgtype": "m.text",
		"body":    plainText,
	}

	// Add formatted content if HTML is present
	if htmlContent != "" {
		messageContent["format"] = "org.matrix.custom.html"
		messageContent["formatted_body"] = htmlContent
	}
	fitMatrixMessage(messageContent)
	var extra map[string]any
	if profile := perMessageProfile(post); profile != nil {
		extra = map[string]any{"com.beeper.per_message_profile": profile}
	}

	// Add Matrix mentions if any @mentions exist in the post
	b.addMatrixMentionsWithData(messageContent, post, mentionData)

	// Check if this is a threaded post (reply to another post)
	var threadEventID string
	if post.RootId != "" {
		// This is a reply - find the Matrix event ID of the root post
		rootPost, appErr := b.API.GetPost(post.RootId)
		if appErr != nil {
			b.logger.LogWarn("Failed to get root post for thread", "error", appErr, "post_id", post.Id, "root_id", post.RootId)
			// Continue without threading - send as regular message
		} else {
			// Get Matrix event ID from root post properties
			if rootPost.Props != nil {
				if eventID, ok := rootPost.Props[propertyKey].(string); ok {
					threadEventID = eventID
				}
			}
			if threadEventID == "" {
				// A call announced by the bridge has no event ID on its post (the
				// Calls plugin owns that post); its recordings and transcripts
				// thread under the announcement.
				threadEventID = b.callNoticeEventID(post.RootId)
			}
			if threadEventID == "" {
				b.logger.LogWarn("Root post has no Matrix event ID for threading", "post_id", post.Id, "root_id", post.RootId)
				// Continue without threading - send as regular message
			}
		}
	}

	// Check for pending file attachments for this post
	pendingFiles := b.fileTracker.GetFiles(post.Id)

	// Prepare file attachments if any
	var fileAttachments []matrix.FileAttachment
	for _, file := range pendingFiles {
		fileAttachments = append(fileAttachments, matrix.FileAttachment{
			Filename: file.Filename,
			MxcURI:   file.MxcURI,
			MimeType: file.MimeType,
			Size:     file.Size,
			Width:    file.Width,
			Height:   file.Height,
			File:     file.File,
		})
	}

	// Extract content from message structure
	finalPlainText := messageContent["body"].(string)
	finalHTMLContent, _ := messageContent["formatted_body"].(string)
	finalMentions, _ := messageContent["m.mentions"].(map[string]any)

	// Send message using consolidated method
	messageRequest := matrix.MessageRequest{
		RoomID:        matrixRoomID,
		GhostUserID:   matrixUserID, // This can be either ghost or real user
		Message:       finalPlainText,
		HTMLMessage:   finalHTMLContent,
		ThreadEventID: threadEventID,
		PostID:        post.Id,
		Files:         fileAttachments,
		Mentions:      finalMentions,
		ExtraContent:  extra,
		Timestamp:     timestampMs,
	}

	sendResponse, err := sender.SendMessage(messageRequest)
	if err != nil {
		return errors.Wrap(err, "failed to send message to Matrix")
	}

	if len(pendingFiles) > 0 {
		b.logger.LogDebug("Posted message with file attachments to Matrix", "post_id", post.Id, "file_count", len(pendingFiles))
	}

	// Store the Matrix event ID as a post property for reaction mapping.
	// System messages cannot be updated through the API, so skip them.
	if sendResponse != nil && sendResponse.EventID != "" && !post.IsSystemMessage() {
		if post.Props == nil {
			post.Props = make(map[string]any)
		}
		post.Props[propertyKey] = sendResponse.EventID

		updatedPost, appErr := b.API.UpdatePost(post)
		if appErr != nil {
			b.logger.LogWarn("Failed to update post with Matrix event ID", "error", appErr, "post_id", post.Id, "event_id", sendResponse.EventID)
			// Continue anyway, the message was sent successfully
		} else {
			// Store the UpdateAt timestamp in memory to detect redundant edits
			err = b.postTracker.Put(post.Id, updatedPost.UpdateAt)
			if err != nil {
				b.logger.LogWarn("Failed to store post tracking for redundant edit detection", "error", err, "post_id", post.Id, "update_at", updatedPost.UpdateAt)
				// Continue anyway - this is just an optimization to avoid redundant edits
			} else {
				b.logger.LogDebug("Stored post tracking for redundant edit detection", "post_id", post.Id, "update_at", updatedPost.UpdateAt)
			}
		}
	}

	b.logger.LogDebug("Successfully created post in Matrix", "post_id", post.Id, "matrix_user_id", matrixUserID, "event_id", sendResponse.EventID)

	// Record successful message sync
	b.metrics.RecordMessageToMatrix()

	return nil
}

// updatePostInMatrix updates an existing post in Matrix
func (b *MattermostToMatrixBridge) updatePostInMatrix(post *model.Post, matrixRoomID string, eventID string, user *model.User) error {
	// Skip updating posts for Matrix-originated users to prevent loops
	if user.IsRemote() {
		b.logger.LogDebug("Skipping post update for remote user", "user_id", user.Id, "username", user.Username)
		return nil
	}

	// Resolve the sending identity (linked account or ghost)
	ghostUserID, sender, _, err := b.resolveSender(user.Id)
	if err != nil {
		return errors.Wrap(err, "failed to create or get ghost user")
	}

	// Ensure the sender is still in the room
	err = b.ensureSenderInRoom(sender, ghostUserID, matrixRoomID, user.Id)
	if err != nil {
		return errors.Wrap(err, "failed to ensure ghost user is in room")
	}

	// Process mentions first on the original text
	mentionData := b.extractMattermostMentions(post)

	// Convert post content to Matrix format
	plainText, htmlContent := convertMattermostToMatrix(b.rewriteLinksToMatrix(postMessageText(post), post.ChannelId))

	// Create Matrix message content structure
	messageContent := map[string]any{
		"msgtype": "m.text",
		"body":    plainText,
	}

	// Add formatted content if HTML is present
	if htmlContent != "" {
		messageContent["format"] = "org.matrix.custom.html"
		messageContent["formatted_body"] = htmlContent
	}
	fitMatrixMessage(messageContent)

	// Add Matrix mentions if any @mentions exist in the post
	b.addMatrixMentionsWithData(messageContent, post, mentionData)

	// Get file attachments for this post (for comparison purposes)
	var currentFiles []matrix.FileAttachment

	// First check pending files (for new posts that haven't been sent yet)
	pendingFiles := b.fileTracker.GetFiles(post.Id)
	for _, file := range pendingFiles {
		currentFiles = append(currentFiles, matrix.FileAttachment{
			Filename: file.Filename,
			MxcURI:   file.MxcURI,
			MimeType: file.MimeType,
			Size:     file.Size,
		})
	}

	// If no pending files, get file attachments from the post itself (for existing posts)
	if len(currentFiles) == 0 && len(post.FileIds) > 0 {
		for _, fileID := range post.FileIds {
			fileInfo, appErr := b.API.GetFileInfo(fileID)
			if appErr != nil {
				b.logger.LogWarn("Failed to get file info for comparison", "error", appErr, "file_id", fileID, "post_id", post.Id)
				continue
			}
			// We don't have the MXC URI for existing files, but we have the filename which is what we need for comparison
			currentFiles = append(currentFiles, matrix.FileAttachment{
				Filename: fileInfo.Name,
				MxcURI:   "", // Not available for existing files, but not needed for text comparison
				MimeType: fileInfo.MimeType,
				Size:     fileInfo.Size,
			})
		}
	}

	// Extract content from message structure
	finalPlainText := messageContent["body"].(string)
	finalHTMLContent, _ := messageContent["formatted_body"].(string)

	b.logger.LogDebug("Preparing to compare Matrix content", "post_id", post.Id, "new_plain_text", finalPlainText, "new_html_content", finalHTMLContent, "file_count", len(currentFiles))
	if len(currentFiles) > 0 {
		b.logger.LogDebug("Files for comparison", "post_id", post.Id, "filenames", func() []string {
			var names []string
			for _, f := range currentFiles {
				names = append(names, f.Filename)
			}
			return names
		}())
	}

	// Fetch the current Matrix event content to compare
	currentEvent, err := b.matrixClient.GetEvent(matrixRoomID, eventID)
	if err != nil {
		b.logger.LogWarn("Failed to fetch current Matrix event for comparison", "error", err, "event_id", eventID)
		// Continue with update if we can't fetch current content
	} else {
		// Compare content and file attachments to see if anything actually changed
		if b.isMatrixContentIdentical(currentEvent, finalPlainText, finalHTMLContent, matrixRoomID, eventID, currentFiles) {
			b.logger.LogDebug("Matrix message content and attachments unchanged, skipping edit", "post_id", post.Id, "matrix_event_id", eventID)
			return nil
		}
	}

	// Send edit as ghost user with proper HTML formatting support
	_, err = sender.EditMessageAsGhost(matrixRoomID, eventID, finalPlainText, finalHTMLContent, ghostUserID)
	if err != nil {
		return errors.Wrap(err, "failed to edit message as ghost user")
	}

	b.logger.LogDebug("Successfully updated post in Matrix", "post_id", post.Id, "ghost_user_id", ghostUserID, "matrix_event_id", eventID)
	return nil
}

// deletePostFromMatrix handles deleting a post from Matrix by redacting the Matrix message
func (b *MattermostToMatrixBridge) deletePostFromMatrix(post *model.Post, channelID string) error {
	// Get Matrix room identifier
	matrixRoomIdentifier, err := b.GetMatrixRoomID(channelID)
	if err != nil {
		return errors.Wrap(err, "failed to get Matrix room identifier for post deletion")
	}

	if matrixRoomIdentifier == "" {
		b.logger.LogWarn("No Matrix room mapped for channel", "channel_id", channelID)
		return nil
	}

	// Resolve room alias to room ID if needed
	matrixRoomID, err := b.matrixClient.ResolveRoomAlias(matrixRoomIdentifier)
	if err != nil {
		return errors.Wrap(err, "failed to resolve Matrix room identifier for post deletion")
	}

	// Get Matrix event ID from post properties
	config := b.getConfiguration()
	serverDomain := extractServerDomain(b.logger, config.MatrixServerURL)
	propertyKey := "matrix_event_id_" + serverDomain

	var matrixEventID string
	if post.Props != nil {
		if eventID, ok := post.Props[propertyKey].(string); ok {
			matrixEventID = eventID
		}
	}

	if matrixEventID == "" {
		b.logger.LogWarn("No Matrix event ID found for post deletion", "post_id", post.Id, "property_key", propertyKey)
		return nil // Can't delete a message that wasn't synced to Matrix
	}

	// Get user for ghost user lookup
	user, appErr := b.API.GetUser(post.UserId)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to get user for post deletion")
	}

	// Check if ghost user exists (needed for redaction)
	ghostUserID, exists := b.getGhostUser(user.Id)
	if !exists {
		b.logger.LogWarn("No ghost user found for post deletion", "user_id", post.UserId, "post_id", post.Id)
		return nil // Can't delete a message from a user that doesn't have a ghost user
	}

	// First, find and delete any file attachment replies to this message
	err = b.deleteAllFileReplies(matrixRoomID, matrixEventID, ghostUserID)
	if err != nil {
		b.logger.LogWarn("Failed to delete file attachment replies", "error", err, "post_id", post.Id, "matrix_event_id", matrixEventID)
		// Continue anyway - we'll still delete the main message
	}

	// Redact the main message event
	_, err = b.matrixClient.RedactEventAsGhost(matrixRoomID, matrixEventID, ghostUserID)
	if err != nil {
		return errors.Wrap(err, "failed to redact post in Matrix")
	}

	b.logger.LogDebug("Successfully deleted post and file attachments from Matrix", "post_id", post.Id, "ghost_user_id", ghostUserID, "matrix_event_id", matrixEventID)
	return nil
}
