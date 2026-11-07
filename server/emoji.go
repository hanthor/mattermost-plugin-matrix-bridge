package main

import (
	"regexp"
	"strconv"
	"strings"
)

// hexToUnicode converts a hex string (with potential -fe0f suffixes) to Unicode character
func hexToUnicode(hexStr string) string {
	// Handle complex emoji sequences (e.g., "1f441-fe0f-200d-1f5e8-fe0f")
	parts := strings.Split(hexStr, "-")
	var result strings.Builder

	for _, part := range parts {
		// Include variation selectors and zero-width joiners for proper emoji rendering
		// fe0f = variation selector-16 (emoji presentation)
		// 200d = zero-width joiner (combines emoji)

		// Convert hex to rune
		if codePoint, err := strconv.ParseInt(part, 16, 32); err == nil && codePoint > 0 {
			result.WriteRune(rune(codePoint))
		}
	}

	return result.String()
}

// shortcodePattern matches Mattermost emoji shortcodes such as :wave: or :+1:.
var shortcodePattern = regexp.MustCompile(`:([a-z0-9_+\-]+):`)

// replaceEmojiShortcodes turns known :shortcodes: into Unicode emoji so they
// render in Matrix clients, which have no shortcode support. Text inside
// code spans and code blocks is left alone, and unknown names (custom emoji)
// are kept as written.
func replaceEmojiShortcodes(text string) string {
	if !strings.Contains(text, ":") {
		return text
	}
	var out strings.Builder
	rest := text
	for rest != "" {
		// Find the next code segment (``` block or ` span) and only replace before it.
		fence := strings.Index(rest, "```")
		tick := strings.Index(rest, "`")
		start, delim := -1, ""
		switch {
		case fence >= 0 && (tick < 0 || fence <= tick):
			start, delim = fence, "```"
		case tick >= 0:
			start, delim = tick, "`"
		}
		if start < 0 {
			out.WriteString(replaceShortcodesInPlainText(rest))
			break
		}
		out.WriteString(replaceShortcodesInPlainText(rest[:start]))
		end := strings.Index(rest[start+len(delim):], delim)
		if end < 0 {
			out.WriteString(rest[start:])
			break
		}
		closeAt := start + len(delim) + end + len(delim)
		out.WriteString(rest[start:closeAt])
		rest = rest[closeAt:]
	}
	return out.String()
}

func replaceShortcodesInPlainText(text string) string {
	return shortcodePattern.ReplaceAllStringFunc(text, func(match string) string {
		name := strings.Trim(match, ":")
		index, ok := emojiNameToIndex[name]
		if !ok {
			return match
		}
		hex, ok := emojiIndexToUnicode[index]
		if !ok {
			return match
		}
		if unicode := hexToUnicode(hex); unicode != "" {
			return unicode
		}
		return match
	})
}
