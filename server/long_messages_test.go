package main

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestShortMessagesAreNotSplit(t *testing.T) {
	assert.Equal(t, []string{"hello"}, splitLongMessage("hello", 100))
}

func TestLongMessagesSplitAtParagraphsAndLoseNothing(t *testing.T) {
	paragraph := strings.Repeat("word ", 60) // 300 characters
	var paragraphs []string
	for i := 0; i < 10; i++ {
		paragraphs = append(paragraphs, strings.TrimSpace(paragraph))
	}
	text := strings.Join(paragraphs, "\n\n")

	parts := splitLongMessage(text, 1000)
	require.Greater(t, len(parts), 1)
	for i, part := range parts {
		assert.LessOrEqual(t, utf8.RuneCountInString(part), 1000, "part %d", i)
		if i < len(parts)-1 {
			assert.True(t, strings.HasSuffix(part, continuedMarker))
		}
	}

	var rejoined []string
	for _, part := range parts {
		rejoined = append(rejoined, strings.TrimSuffix(part, continuedMarker))
	}
	words := func(s string) int { return len(strings.Fields(s)) }
	assert.Equal(t, words(text), words(strings.Join(rejoined, " ")), "every word survives")
	assert.True(t, strings.HasPrefix(parts[1], "word"), "parts start at a paragraph boundary, not in the middle of text")
}

func TestAnUnbrokenStringIsStillCut(t *testing.T) {
	parts := splitLongMessage(strings.Repeat("x", 5000), 1000)
	require.Greater(t, len(parts), 1)
	for _, part := range parts {
		assert.LessOrEqual(t, utf8.RuneCountInString(part), 1000)
	}
}

func TestTruncateForMattermost(t *testing.T) {
	assert.Equal(t, "short", truncateForMattermost("short", 100))
	out := truncateForMattermost(strings.Repeat("y", 500), 200)
	assert.LessOrEqual(t, utf8.RuneCountInString(out), 200)
	assert.Contains(t, out, "cut off")
}

func TestFitMatrixMessage(t *testing.T) {
	small := map[string]any{"body": "hi", "format": "org.matrix.custom.html", "formatted_body": "<p>hi</p>"}
	fitMatrixMessage(small)
	assert.Contains(t, small, "formatted_body")

	// Too large with the HTML copy: the plain text is kept whole.
	mid := map[string]any{"body": strings.Repeat("a", 30*1024), "format": "org.matrix.custom.html", "formatted_body": strings.Repeat("b", 30*1024)}
	fitMatrixMessage(mid)
	assert.NotContains(t, mid, "formatted_body")
	assert.Len(t, mid["body"], 30*1024)

	// Too large even as plain text: cut, on a character boundary.
	huge := map[string]any{"body": strings.Repeat("é", 40*1024)}
	fitMatrixMessage(huge)
	body := huge["body"].(string)
	assert.LessOrEqual(t, len(body), maxMatrixEventBytes)
	assert.True(t, utf8.ValidString(body))
	assert.Contains(t, body, "cut off")
}

func TestHumanSize(t *testing.T) {
	assert.Equal(t, "512 B", humanSize(512))
	assert.Equal(t, "1.5 KB", humanSize(1536))
	assert.Equal(t, "48.0 MB", humanSize(48*1024*1024))
	assert.Equal(t, "2.0 GB", humanSize(2*1024*1024*1024))
}

func TestFileNotSentText(t *testing.T) {
	assert.Equal(t, "📎 video.mp4 (80.0 MB) was not sent to Matrix: it is larger than the 50.0 MB limit.", fileNotSentText("video.mp4", 80<<20, 50<<20, true))
	assert.Contains(t, fileNotSentText("a.zip", 80<<20, 50<<20, false), "not sent to Mattermost")
}

func TestOversizedMatrixFileIsAnnouncedInTheChannel(t *testing.T) {
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	var posted []string
	api.On("CreatePost", mock.MatchedBy(func(post *model.Post) bool {
		posted = append(posted, post.Message)
		return true
	})).Return(&model.Post{Id: "p1"}, nil)
	p.matrixToMattermostBridge.maxFileSize = 10 << 20

	p.matrixToMattermostBridge.noticeFileNotCopied("ch1", "mm-ada", "film.mkv", 30<<20)
	require.Len(t, posted, 1)
	assert.Contains(t, posted[0], "film.mkv")
	assert.Contains(t, posted[0], "30.0 MB")
}
