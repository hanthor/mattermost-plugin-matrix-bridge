package main

import (
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
)

// maxPostRunes is the longest message Mattermost accepts.
const maxPostRunes = model.PostMessageMaxRunesV2

const (
	continuedMarker = "\n\n_(continued)_"
	truncatedMarker = "\n\n_… the rest of this message was cut off because it is longer than Mattermost allows._"
)

// splitLongMessage cuts text into parts of at most limit characters, at
// paragraph breaks where possible, then at line breaks, then at spaces. A text
// within the limit is returned as it is.
func splitLongMessage(text string, limit int) []string {
	if utf8.RuneCountInString(text) <= limit {
		return []string{text}
	}
	// Leave room for the marker added to every part but the last.
	budget := limit - utf8.RuneCountInString(continuedMarker)
	var parts []string
	rest := text
	for utf8.RuneCountInString(rest) > limit {
		runes := []rune(rest)
		cut := budget
		window := string(runes[:budget])
		for _, sep := range []string{"\n\n", "\n", " "} {
			if i := strings.LastIndex(window, sep); i > budget/2 {
				cut = utf8.RuneCountInString(window[:i])
				break
			}
		}
		parts = append(parts, strings.TrimRight(string(runes[:cut]), " \n")+continuedMarker)
		rest = strings.TrimLeft(string(runes[cut:]), " \n")
	}
	return append(parts, rest)
}

// truncateForMattermost shortens text that cannot be split (an edit replaces
// one post) and says so.
func truncateForMattermost(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	keep := limit - utf8.RuneCountInString(truncatedMarker)
	return strings.TrimRight(string([]rune(text)[:keep]), " \n") + truncatedMarker
}

// maxMatrixEventBytes is kept below the 65,536 bytes Matrix allows for a whole
// event, which also holds the sender, room and relation fields.
const maxMatrixEventBytes = 56 * 1024

// fitMatrixMessage makes a message content fit a Matrix event: the HTML copy is
// dropped first (the plain text stays complete), and a body that is still too
// large is cut and marked.
func fitMatrixMessage(content map[string]any) {
	size := func() int {
		n := 0
		for _, key := range []string{"body", "formatted_body"} {
			if s, ok := content[key].(string); ok {
				n += len(s)
			}
		}
		return n
	}
	if size() <= maxMatrixEventBytes {
		return
	}
	delete(content, "formatted_body")
	delete(content, "format")
	if body, ok := content["body"].(string); ok && len(body) > maxMatrixEventBytes {
		cut := maxMatrixEventBytes - len(matrixTruncatedMarker)
		for cut > 0 && !utf8.RuneStart(body[cut]) {
			cut--
		}
		content["body"] = body[:cut] + matrixTruncatedMarker
	}
}

const matrixTruncatedMarker = "\n\n… (cut off: the message is longer than Matrix allows)"
