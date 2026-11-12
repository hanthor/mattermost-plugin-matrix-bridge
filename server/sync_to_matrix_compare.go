package main

import (
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/matrix"
	"github.com/pkg/errors"
)

func (b *MattermostToMatrixBridge) deleteAllFileReplies(matrixRoomID, matrixEventID, ghostUserID string) error {
	// Find all file events that are replies to this main event
	fileEventIDs, err := b.getFileEventIDsFromMetadata(matrixRoomID, matrixEventID, ghostUserID)
	if err != nil {
		return errors.Wrap(err, "failed to find file events to delete")
	}

	// Delete each file event
	for _, fileEventID := range fileEventIDs {
		_, err := b.matrixClient.RedactEventAsGhost(matrixRoomID, fileEventID, ghostUserID)
		if err != nil {
			b.logger.LogWarn("Failed to delete file event", "error", err, "file_event_id", fileEventID, "main_event_id", matrixEventID)
			// Continue with other files
		} else {
			b.logger.LogDebug("Deleted file event", "file_event_id", fileEventID, "main_event_id", matrixEventID)
		}
	}

	return nil
}

func (b *MattermostToMatrixBridge) getFileEventIDsFromMetadata(matrixRoomID, postEventID, ghostUserID string) ([]string, error) {
	// Get related events for the main post event
	relatedEvents, err := b.matrixClient.GetEventRelationsAsUser(matrixRoomID, postEventID, ghostUserID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get related events")
	}

	var fileEventIDs []string
	for _, event := range relatedEvents {
		// Check if this is a message event
		eventType, ok := event["type"].(string)
		if !ok || eventType != "m.room.message" {
			continue
		}

		// Check if this event is from our ghost user
		sender, ok := event["sender"].(string)
		if !ok || sender != ghostUserID {
			continue
		}

		// Check if this is a file message
		content, ok := event["content"].(map[string]any)
		if !ok {
			continue
		}

		msgType, ok := content["msgtype"].(string)
		if !ok {
			continue
		}

		// File messages have msgtype of m.file, m.image, m.video, or m.audio
		if msgType == "m.file" || msgType == "m.image" || msgType == "m.video" || msgType == "m.audio" {
			eventID, ok := event["event_id"].(string)
			if ok {
				fileEventIDs = append(fileEventIDs, eventID)
			}
		}
	}

	return fileEventIDs, nil
}

// isMatrixContentIdentical compares current Matrix event content with new content to detect if update is needed
func (b *MattermostToMatrixBridge) isMatrixContentIdentical(currentEvent map[string]any, newPlainText, newHTMLContent, matrixRoomID, eventID string, newFiles []matrix.FileAttachment) bool {
	// First check text content
	if !b.compareTextContent(currentEvent, newPlainText, newHTMLContent, newFiles) {
		return false
	}

	// For file-only posts where text content matches (empty new text + filename match),
	// skip file attachment comparison since the text comparison already handled the equivalence
	if newPlainText == "" && len(newFiles) > 0 {
		// Extract current content to check if this is a file-only post
		if content, ok := currentEvent["content"].(map[string]any); ok {
			if currentBody, hasBody := content["body"].(string); hasBody && currentBody != "" {
				// Check if current body matches any filename (file-only post scenario)
				for _, file := range newFiles {
					if currentBody == file.Filename {
						b.logger.LogDebug("File-only post detected with matching text content, skipping file attachment comparison", "current_body", currentBody, "filename", file.Filename)
						// Text comparison already verified equivalence, so content is identical
						b.logger.LogDebug("Matrix message content and attachments are identical, no update needed")
						return true
					}
				}
			}
		}
	}

	// For non-file-only posts, compare file attachments by checking related events
	if !b.areFileAttachmentsIdentical(matrixRoomID, eventID, newFiles) {
		b.logger.LogDebug("Matrix message file attachments differ")
		return false
	}

	// Content and attachments are identical
	b.logger.LogDebug("Matrix message content and attachments are identical, no update needed")
	return true
}

// compareTextContent compares text and HTML content between current and new message content
func (b *MattermostToMatrixBridge) compareTextContent(currentEvent map[string]any, newPlainText, newHTMLContent string, newFiles []matrix.FileAttachment) bool {
	// Extract current content from Matrix event
	content, ok := currentEvent["content"].(map[string]any)
	if !ok {
		b.logger.LogDebug("Current Matrix event has no content field")
		return false
	}

	// Compare plain text body
	currentBody, hasBody := content["body"].(string)
	if !hasBody || currentBody != newPlainText {
		// Special case: file-only posts
		// If new content is empty and we have files, check if current content is just a filename
		if newPlainText == "" && len(newFiles) > 0 && hasBody {
			// Check if current body matches any of the new filenames (file-only post scenario)
			filenameMatch := false
			for _, file := range newFiles {
				if currentBody == file.Filename {
					b.logger.LogDebug("File-only post: current Matrix body matches filename, treating as identical", "current", currentBody, "filename", file.Filename)
					filenameMatch = true
					break
				}
			}
			// If current body doesn't match any filename, content differs
			if !filenameMatch {
				b.logger.LogDebug("Matrix message body differs (not a filename match)", "current", currentBody, "new", newPlainText)
				return false
			}
			// If we found a filename match, continue to check HTML content below
		} else {
			b.logger.LogDebug("Matrix message body differs", "current", currentBody, "new", newPlainText)
			return false
		}
	}

	// Compare HTML formatted content if present
	currentFormattedBody, hasFormatted := content["formatted_body"].(string)
	if newHTMLContent != "" {
		// New content has HTML, check if current content matches
		if !hasFormatted || currentFormattedBody != newHTMLContent {
			b.logger.LogDebug("Matrix message formatted_body differs", "current", currentFormattedBody, "new", newHTMLContent)
			return false
		}
	} else {
		// New content has no HTML, current should also have no formatted content
		if hasFormatted && currentFormattedBody != "" {
			b.logger.LogDebug("Matrix message formatted_body differs (new has none, current has some)", "current", currentFormattedBody)
			return false
		}
	}

	return true
}

// areFileAttachmentsIdentical compares current Matrix file attachments with new file attachments
func (b *MattermostToMatrixBridge) areFileAttachmentsIdentical(matrixRoomID, eventID string, newFiles []matrix.FileAttachment) bool {
	// Get current file attachments by looking at related events
	currentFiles, err := b.getCurrentMatrixFileAttachments(matrixRoomID, eventID)
	if err != nil {
		b.logger.LogWarn("Failed to get current Matrix file attachments for comparison", "error", err, "event_id", eventID)
		// If we can't get current files, assume they're different to be safe
		return false
	}

	// Compare counts first
	if len(currentFiles) != len(newFiles) {
		b.logger.LogDebug("File attachment count differs", "current_count", len(currentFiles), "new_count", len(newFiles))
		return false
	}

	// Compare each file attachment
	for i, newFile := range newFiles {
		if i >= len(currentFiles) {
			b.logger.LogDebug("New file attachment not found in current attachments", "filename", newFile.Filename)
			return false
		}

		currentFile := currentFiles[i]
		if currentFile.Filename != newFile.Filename ||
			currentFile.MxcURI != newFile.MxcURI ||
			currentFile.MimeType != newFile.MimeType ||
			currentFile.Size != newFile.Size {
			b.logger.LogDebug("File attachment differs", "current", currentFile, "new", newFile)
			return false
		}
	}

	return true
}

// getCurrentMatrixFileAttachments retrieves current file attachments for a Matrix event
func (b *MattermostToMatrixBridge) getCurrentMatrixFileAttachments(matrixRoomID, eventID string) ([]matrix.FileAttachment, error) {
	// Get related events (file attachments are sent as separate messages related to the main message)
	relations, err := b.matrixClient.GetEventRelationsAsUser(matrixRoomID, eventID, "")
	if err != nil {
		return nil, errors.Wrap(err, "failed to get event relations")
	}

	var files []matrix.FileAttachment
	for _, event := range relations {
		// Check if this is a file message related to our main message
		eventType, ok := event["type"].(string)
		if !ok || eventType != "m.room.message" {
			continue
		}

		content, ok := event["content"].(map[string]any)
		if !ok {
			continue
		}

		// Check if this is a file message
		msgType, ok := content["msgtype"].(string)
		if !ok {
			continue
		}

		// File messages have msgtype of m.file, m.image, m.video, or m.audio
		if msgType != "m.file" && msgType != "m.image" && msgType != "m.video" && msgType != "m.audio" {
			continue
		}

		// Check if this is related to our main message
		relatesTo, ok := content["m.relates_to"].(map[string]any)
		if !ok {
			continue
		}

		relType, ok := relatesTo["rel_type"].(string)
		relEventID, hasEventID := relatesTo["event_id"].(string)
		if !ok || relType != "m.mattermost.post" || !hasEventID || relEventID != eventID {
			continue
		}

		// Extract file information
		filename, hasFilename := content["body"].(string)
		mxcURI, hasMxcURI := content["url"].(string)

		if !hasFilename || !hasMxcURI {
			continue
		}

		// Extract file info
		var mimeType string
		var size int64
		if info, hasInfo := content["info"].(map[string]any); hasInfo {
			if mt, hasMT := info["mimetype"].(string); hasMT {
				mimeType = mt
			}
			if s, hasSize := info["size"]; hasSize {
				if sizeFloat, ok := s.(float64); ok {
					size = int64(sizeFloat)
				} else if sizeInt, ok := s.(int64); ok {
					size = sizeInt
				}
			}
		}

		files = append(files, matrix.FileAttachment{
			Filename: filename,
			MxcURI:   mxcURI,
			MimeType: mimeType,
			Size:     size,
		})
	}

	return files, nil
}
