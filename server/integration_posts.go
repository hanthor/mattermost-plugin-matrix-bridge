package main

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// Integrations (webhooks, bots, slash command responses) put their content in
// Slack-style attachments in the post's props and often leave the message
// empty. Matrix has no such structure, so the attachments are written out as
// Markdown and sent as the message.

// postMessageText is the text to send to Matrix for a post: its message,
// followed by its attachments, prefixed with the integration's name when one
// overrides the sender.
func postMessageText(post *model.Post) string {
	var parts []string
	if msg := strings.TrimSpace(post.Message); msg != "" {
		parts = append(parts, msg)
	}
	if rendered := renderAttachments(post.GetProp("attachments")); rendered != "" {
		parts = append(parts, rendered)
	}
	text := strings.Join(parts, "\n\n")
	if text == "" {
		return post.Message
	}
	// Without a per-message profile the integration's name goes in the text,
	// so "Jenkins" does not look like the person who created the webhook.
	if name, _ := post.GetProp("override_username").(string); name != "" {
		text = "**" + escapeMarkdown(name) + ":** " + text
	}
	return text
}

// renderAttachments writes message attachments out as Markdown.
func renderAttachments(raw any) string {
	list, ok := raw.([]any)
	if !ok {
		if typed, ok := raw.([]map[string]any); ok {
			for _, m := range typed {
				list = append(list, m)
			}
		}
	}
	var blocks []string
	for _, item := range list {
		att, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if block := renderAttachment(att); block != "" {
			blocks = append(blocks, block)
		}
	}
	return strings.Join(blocks, "\n\n")
}

func renderAttachment(att map[string]any) string {
	str := func(key string) string {
		s, _ := att[key].(string)
		return strings.TrimSpace(s)
	}
	var lines []string
	if v := str("pretext"); v != "" {
		lines = append(lines, v)
	}
	if title := str("title"); title != "" {
		if link := str("title_link"); link != "" {
			lines = append(lines, fmt.Sprintf("**[%s](%s)**", escapeMarkdown(title), link))
		} else {
			lines = append(lines, "**"+escapeMarkdown(title)+"**")
		}
	}
	if v := str("text"); v != "" {
		lines = append(lines, v)
	}
	if fields, ok := att["fields"].([]any); ok {
		var items []string
		for _, f := range fields {
			field, ok := f.(map[string]any)
			if !ok {
				continue
			}
			title, _ := field["title"].(string)
			value := fmt.Sprint(field["value"])
			if field["value"] == nil {
				value = ""
			}
			switch {
			case title != "" && value != "":
				items = append(items, fmt.Sprintf("- **%s:** %s", escapeMarkdown(title), value))
			case value != "":
				items = append(items, "- "+value)
			}
		}
		if len(items) > 0 {
			lines = append(lines, strings.Join(items, "\n"))
		}
	}
	if v := str("image_url"); v != "" {
		lines = append(lines, v)
	}
	if v := str("footer"); v != "" {
		lines = append(lines, "_"+escapeMarkdown(v)+"_")
	}
	if len(lines) == 0 {
		// An attachment with nothing else still has a plain-text summary.
		return str("fallback")
	}
	return strings.Join(lines, "\n\n")
}

// escapeMarkdown keeps names and titles from being read as formatting.
func escapeMarkdown(s string) string {
	return strings.NewReplacer("*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`, "`", "\\`").Replace(s)
}
