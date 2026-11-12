package main

import (
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// getPostIDFromMatrixEvent finds the Mattermost post ID for a Matrix event ID.
// Uses an optimized lookup strategy: KV store first (fast for Matrix-originated events),
// then Matrix event metadata (for Mattermost-originated events).
func (b *MatrixToMattermostBridge) getPostIDFromMatrixEvent(matrixEventID, channelID string) string {
	// First check KV store for Matrix-originated events (O(1), no network calls)
	mappingKey := kvstore.BuildMatrixEventPostKey(matrixEventID)
	if postIDBytes, err := b.kvstore.Get(mappingKey); err == nil && len(postIDBytes) > 0 {
		postID := string(postIDBytes)
		b.logger.LogDebug("Found Mattermost post ID for Matrix-originated event in KV store", "matrix_event_id", matrixEventID, "mattermost_post_id", postID)
		return postID
	}

	// Fall back to Matrix event metadata for Mattermost-originated events
	return b.getPostIDFromMatrixEventMetadata(matrixEventID, channelID)
}

// storeMatrixEventPostMapping stores the mapping from Matrix event ID to Mattermost post ID
// for efficient reverse lookups. This is used by both message and file sync functions.
func (b *MatrixToMattermostBridge) storeMatrixEventPostMapping(matrixEventID, mattermostPostID string) {
	mappingKey := kvstore.BuildMatrixEventPostKey(matrixEventID)
	if err := b.kvstore.Set(mappingKey, []byte(mattermostPostID)); err != nil {
		b.logger.LogWarn("Failed to store Matrix event to post mapping", "error", err, "matrix_event_id", matrixEventID, "post_id", mattermostPostID)
		// Continue anyway - post was created successfully
	}
}

// getPostIDFromMatrixEventMetadata retrieves post ID from Matrix event content (for Mattermost-originated events)
func (b *MatrixToMattermostBridge) getPostIDFromMatrixEventMetadata(matrixEventID, channelID string) string {
	// Get the Matrix room ID for this channel
	matrixRoomIdentifier, err := b.GetMatrixRoomID(channelID)
	if err != nil {
		b.logger.LogDebug("Failed to get Matrix room identifier", "error", err, "channel_id", channelID, "matrix_event_id", matrixEventID)
		return ""
	}

	if matrixRoomIdentifier == "" {
		b.logger.LogDebug("No Matrix room identifier found for channel", "channel_id", channelID, "matrix_event_id", matrixEventID)
		return ""
	}

	// Resolve room alias to room ID if needed (handles both aliases and room IDs correctly)
	matrixRoomID, err := b.matrixClient.ResolveRoomAlias(matrixRoomIdentifier)
	if err != nil {
		b.logger.LogDebug("Failed to resolve Matrix room identifier", "error", err, "room_identifier", matrixRoomIdentifier, "matrix_event_id", matrixEventID)
		return ""
	}

	// Get the Matrix event to extract Mattermost metadata
	event, err := b.matrixClient.GetEvent(matrixRoomID, matrixEventID)
	if err != nil {
		b.logger.LogDebug("Failed to get Matrix event", "error", err, "event_id", matrixEventID, "room_id", matrixRoomID)
		return ""
	}

	// Extract Mattermost post ID from event content
	content, ok := event["content"].(map[string]any)
	if !ok {
		b.logger.LogDebug("Matrix event has no content field", "event_id", matrixEventID)
		return ""
	}

	if postID, exists := content["mattermost_post_id"].(string); exists && postID != "" {
		b.logger.LogDebug("Found Mattermost post ID in Matrix event metadata", "matrix_event_id", matrixEventID, "mattermost_post_id", postID)
		return postID
	}

	// No Mattermost post found for this Matrix event
	b.logger.LogDebug("No Mattermost post found for Matrix event", "event_id", matrixEventID)
	return ""
}

// getPostIDFromRelatedMatrixEvent finds a Mattermost post ID by checking if the Matrix event
// is related to a primary message that has a Mattermost post ID
func (b *MatrixToMattermostBridge) getPostIDFromRelatedMatrixEvent(matrixEventID, channelID string) string {
	// Get the Matrix room ID for this channel
	matrixRoomIdentifier, err := b.GetMatrixRoomID(channelID)
	if err != nil {
		b.logger.LogWarn("Failed to get Matrix room identifier for related event lookup", "error", err, "channel_id", channelID)
		return ""
	}

	if matrixRoomIdentifier == "" {
		return ""
	}

	// Resolve room alias to room ID if needed
	matrixRoomID, err := b.matrixClient.ResolveRoomAlias(matrixRoomIdentifier)
	if err != nil {
		b.logger.LogWarn("Failed to resolve Matrix room identifier for related event lookup", "error", err, "room_identifier", matrixRoomIdentifier)
		return ""
	}

	// Get the Matrix event to check if it's related to another event
	event, err := b.matrixClient.GetEvent(matrixRoomID, matrixEventID)
	if err != nil {
		b.logger.LogWarn("Failed to get Matrix event for related lookup", "error", err, "event_id", matrixEventID)
		return ""
	}

	// Check if this event is related to another event (file message related to primary message)
	content, ok := event["content"].(map[string]any)
	if !ok {
		return ""
	}

	relatesTo, ok := content["m.relates_to"].(map[string]any)
	if !ok {
		return ""
	}

	// Check if this is a file attachment relation
	relType, ok := relatesTo["rel_type"].(string)
	primaryEventID, hasEventID := relatesTo["event_id"].(string)
	if !ok || relType != "m.mattermost.post" || !hasEventID {
		return ""
	}

	b.logger.LogDebug("Found file message related to primary message", "file_event_id", matrixEventID, "primary_event_id", primaryEventID)

	// Now look up the Mattermost post ID using the primary event ID
	return b.getPostIDFromMatrixEvent(primaryEventID, channelID)
}

// getThreadRootFromPostID finds the actual thread root ID for a given Mattermost post ID.
// If the post is already a thread root, returns the same ID. If it's a thread reply, returns the RootId.
func (b *MatrixToMattermostBridge) getThreadRootFromPostID(postID string) string {
	if postID == "" {
		return ""
	}

	// Get the post to check if it's part of a thread
	post, appErr := b.API.GetPost(postID)
	if appErr != nil {
		b.logger.LogWarn("Failed to get post for thread root lookup", "error", appErr, "post_id", postID)
		return postID // Return original ID as fallback
	}

	// If this post has a RootId, return that (it's a thread reply)
	if post.RootId != "" {
		b.logger.LogDebug("Post is thread reply, returning root ID", "post_id", postID, "root_id", post.RootId)
		return post.RootId
	}

	// Otherwise, this post is either standalone or the thread root itself
	b.logger.LogDebug("Post is thread root or standalone", "post_id", postID)
	return postID
}
