package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Events that are not plain text but carry something worth showing: stickers
// (images), locations (a map link) and polls (the question and its options).

const (
	pollStartStable   = "m.poll.start"
	pollStartUnstable = "org.matrix.msc3381.poll.start"
)

// isPollStart reports whether an event type starts a poll.
func isPollStart(eventType string) bool {
	return eventType == pollStartStable || eventType == pollStartUnstable
}

// stickerAsImage turns an m.sticker event into the image message it is.
func stickerAsImage(ev MatrixEvent) MatrixEvent {
	out := ev
	out.Type = "m.room.message"
	content := map[string]any{}
	for k, v := range ev.Content {
		content[k] = v
	}
	content["msgtype"] = "m.image"
	if body, _ := content["body"].(string); body == "" {
		content["body"] = "sticker.png"
	}
	out.Content = content
	return out
}

// locationText writes an m.location message as a line with a map link.
// geo_uri is "geo:LAT,LON[;u=uncertainty]".
func locationText(content map[string]any) (string, bool) {
	if mt, _ := content["msgtype"].(string); mt != "m.location" {
		return "", false
	}
	geo, _ := content["geo_uri"].(string)
	body, _ := content["body"].(string)
	coords := strings.TrimPrefix(geo, "geo:")
	if i := strings.IndexByte(coords, ';'); i >= 0 {
		coords = coords[:i]
	}
	parts := strings.Split(coords, ",")
	if len(parts) < 2 {
		return "", false
	}
	lat, err1 := strconv.ParseFloat(parts[0], 64)
	lon, err2 := strconv.ParseFloat(parts[1], 64)
	if err1 != nil || err2 != nil {
		return "", false
	}
	link := fmt.Sprintf("https://www.openstreetmap.org/?mlat=%s&mlon=%s#map=16/%s/%s",
		url.QueryEscape(parts[0]), url.QueryEscape(parts[1]), strconv.FormatFloat(lat, 'f', -1, 64), strconv.FormatFloat(lon, 'f', -1, 64))
	label := strings.TrimSpace(body)
	if label == "" || strings.HasPrefix(label, "Location geo:") {
		label = "Shared a location"
	}
	return fmt.Sprintf("📍 [%s](%s)", label, link), true
}

// pollText writes a poll start event as its question and numbered options.
func pollText(content map[string]any) (string, bool) {
	poll, _ := content[pollStartUnstable].(map[string]any)
	if poll == nil {
		poll, _ = content[pollStartStable].(map[string]any)
	}
	if poll == nil {
		return "", false
	}
	text := func(m map[string]any) string {
		for _, key := range []string{"org.matrix.msc1767.text", "m.text", "body"} {
			if s, ok := m[key].(string); ok && s != "" {
				return s
			}
			if arr, ok := m[key].([]any); ok && len(arr) > 0 {
				if first, ok := arr[0].(map[string]any); ok {
					if s, ok := first["body"].(string); ok {
						return s
					}
				}
			}
		}
		return ""
	}
	question, _ := poll["question"].(map[string]any)
	q := text(question)
	if q == "" {
		return "", false
	}
	var sb strings.Builder
	sb.WriteString("📊 **" + q + "**")
	answers, _ := poll["answers"].([]any)
	for i, a := range answers {
		if am, ok := a.(map[string]any); ok {
			if label := text(am); label != "" {
				sb.WriteString(fmt.Sprintf("\n%d. %s", i+1, label))
			}
		}
	}
	sb.WriteString("\n_Vote in Matrix; votes are not shown here._")
	return sb.String(), true
}
