package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
)

func TestAttachmentsBecomeTheMessageOfAnIntegrationPost(t *testing.T) {
	post := &model.Post{Message: ""}
	post.AddProp("attachments", []any{
		map[string]any{
			"pretext":    "Build finished",
			"title":      "Job #42",
			"title_link": "https://ci.example.com/42",
			"text":       "All tests passed",
			"fields": []any{
				map[string]any{"title": "Branch", "value": "main", "short": true},
				map[string]any{"title": "Duration", "value": float64(93)},
			},
			"footer": "Jenkins",
		},
	})

	got := postMessageText(post)
	assert.Equal(t, "Build finished\n\n**[Job #42](https://ci.example.com/42)**\n\nAll tests passed\n\n- **Branch:** main\n- **Duration:** 93\n\n_Jenkins_", got)

	plain, html := convertMattermostToMatrix(got)
	assert.Contains(t, plain, "Job #42")
	assert.Contains(t, html, `<a href="https://ci.example.com/42">Job #42</a>`)
}

func TestIntegrationNameIsPrefixedWhenTheSenderIsOverridden(t *testing.T) {
	post := &model.Post{Message: "deploy done"}
	post.AddProp("override_username", "Deploy_Bot")
	assert.Equal(t, `**Deploy\_Bot:** deploy done`, postMessageText(post))
}

func TestOrdinaryPostsAreUnchanged(t *testing.T) {
	assert.Equal(t, "hello **world**", postMessageText(&model.Post{Message: "hello **world**"}))
	assert.Equal(t, "", postMessageText(&model.Post{Message: ""}))
}

func TestAttachmentsWithOnlyAFallbackStillCarryText(t *testing.T) {
	post := &model.Post{}
	post.AddProp("attachments", []any{map[string]any{"fallback": "Alert: disk full"}})
	assert.Equal(t, "Alert: disk full", postMessageText(post))
}

func TestOverriddenSenderCarriesAPerMessageProfile(t *testing.T) {
	post := &model.Post{Message: "deploy done"}
	assert.Nil(t, perMessageProfile(post))

	post.AddProp("override_username", " Deploy Bot ")
	profile := perMessageProfile(post)
	assert.Equal(t, "Deploy Bot", profile["displayname"])
	assert.Equal(t, "mattermost-integration-deploy-bot", profile["id"])
}
