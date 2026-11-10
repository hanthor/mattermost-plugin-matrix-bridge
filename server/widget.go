package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

// A bridged room carries a widget that shows the Mattermost channel it is
// connected to: its name, team, purpose and size, and a link to open it. The
// page is served by the plugin and reachable only with a signature over the
// channel ID, so what it shows is limited to people who can see the room's
// state (the widget address is part of it).

const (
	widgetStateKey = "com.mattermost.channel"
	widgetPath     = "/plugins/com.mattermost.plugin-matrix-bridge/widget"
)

func (p *Plugin) widgetSignature(channelID string) string {
	mac := hmac.New(sha256.New, []byte("widget|"+secretFor(p.API, p.getConfiguration())))
	_, _ = mac.Write([]byte(channelID))
	return hex.EncodeToString(mac.Sum(nil))
}

func (p *Plugin) siteURL() string {
	if cfg := p.API.GetConfig(); cfg != nil && cfg.ServiceSettings.SiteURL != nil {
		return strings.TrimRight(*cfg.ServiceSettings.SiteURL, "/")
	}
	return ""
}

// widgetContent is the im.vector.modular.widgets state of a bridged room, or
// nil when the server has no site address the widget could be loaded from.
func (p *Plugin) widgetContent(channelID, channelName string) map[string]any {
	site := p.siteURL()
	if site == "" {
		return nil
	}
	q := url.Values{"channel": {channelID}, "sig": {p.widgetSignature(channelID)}}
	return map[string]any{
		"type":          "customwidget",
		"url":           site + widgetPath + "?" + q.Encode(),
		"name":          "Mattermost",
		"id":            widgetStateKey,
		"creatorUserId": p.BridgeBotUserID(),
		"data":          map[string]any{"title": "Mattermost: " + channelName},
	}
}

var widgetPage = template.Must(template.New("widget").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Name}}</title>
<style>body{font:14px/1.5 system-ui,sans-serif;margin:0;padding:16px;color:#3f4350}
h1{font-size:16px;margin:0 0 4px}.muted{opacity:.72}a.btn{display:inline-block;margin-top:12px;padding:6px 14px;border-radius:4px;background:#1c58d9;color:#fff;text-decoration:none;font-weight:600}</style>
</head><body>
<h1>{{.Name}}</h1>
<div class="muted">{{.Team}}{{if .Team}} · {{end}}{{.Members}} {{if eq .Members 1}}member{{else}}members{{end}} in Mattermost</div>
{{if .Purpose}}<p>{{.Purpose}}</p>{{end}}
{{if .Link}}<a class="btn" href="{{.Link}}" target="_blank" rel="noopener noreferrer">Open in Mattermost</a>{{end}}
</body></html>`))

func (p *Plugin) handleWidget(w http.ResponseWriter, r *http.Request) {
	channelID := r.URL.Query().Get("channel")
	sig := r.URL.Query().Get("sig")
	if channelID == "" || !hmac.Equal([]byte(sig), []byte(p.widgetSignature(channelID))) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil || channel == nil || p.roomIDForChannel(channelID) == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	data := struct {
		Name, Team, Purpose, Link string
		Members                   int64
	}{Name: channel.DisplayName, Purpose: channel.Purpose}
	if channel.TeamId != "" {
		if team, appErr := p.API.GetTeam(channel.TeamId); appErr == nil && team != nil {
			data.Team = team.DisplayName
			if site := p.siteURL(); site != "" {
				data.Link = site + "/" + team.Name + "/channels/" + channel.Name
			}
		}
	}
	if stats, appErr := p.API.GetChannelStats(channelID); appErr == nil && stats != nil {
		data.Members = stats.MemberCount
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// Meant to be embedded by Matrix clients on any origin.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors *")
	_ = widgetPage.Execute(w, data)
}
