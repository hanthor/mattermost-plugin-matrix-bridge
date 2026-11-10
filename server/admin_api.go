package main

import (
	"net/http"
	"sort"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// The System Console page reads one status document: how the bridge is doing,
// what is bridged to what, and what is waiting. System administrators only.

const adminStatusMappingLimit = 500

type adminHealthCheck struct {
	Status string `json:"status"` // ok, warn or fail
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

type adminMapping struct {
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	Team        string `json:"team,omitempty"`
	Room        string `json:"room"`
}

type adminStatus struct {
	Enabled bool               `json:"enabled"`
	Mode    string             `json:"mode"`
	Health  []adminHealthCheck `json:"health"`
	Worst   string             `json:"worst"`
	// Mappings lists bridged channels (at most adminStatusMappingLimit);
	// MappedChannels is the real number.
	Mappings       []adminMapping `json:"mappings"`
	MappedChannels int            `json:"mapped_channels"`
	Mirror         struct {
		Accounts   int `json:"accounts"`
		Claimed    int `json:"claimed"`
		Collisions int `json:"collisions"`
	} `json:"mirror"`
	DeadLetters int  `json:"dead_letters"`
	Cutover     bool `json:"cutover"`
}

func (p *Plugin) handleUIAdminStatus(w http.ResponseWriter, r *http.Request) {
	if !p.API.HasPermissionTo(userFromContext(r), model.PermissionManageSystem) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only system administrators can see this"})
		return
	}
	cfg := p.getConfiguration()
	status := adminStatus{
		Enabled: cfg.EnableSync,
		Mode:    "ghost",
		Cutover: cfg.CutoverMode,
		Health:  []adminHealthCheck{},
		Worst:   "ok",
	}
	if cfg.EnableMirrorMode {
		status.Mode = "mirror"
	}
	if p.matrixClient != nil && p.kvstore != nil {
		names := map[doctorStatus]string{doctorOK: "ok", doctorWarn: "warn", doctorFail: "fail"}
		worst := doctorOK
		for _, c := range p.doctorChecks("") {
			status.Health = append(status.Health, adminHealthCheck{Status: names[c.Status], Title: c.Title, Detail: c.Detail})
			if c.Status > worst {
				worst = c.Status
			}
		}
		status.Worst = names[worst]
		status.DeadLetters = p.DeadLetters()
		stats := newMirrorStore(p.kvstore).Stats()
		status.Mirror.Accounts, status.Mirror.Claimed, status.Mirror.Collisions = stats.Accounts, stats.Claimed, stats.Collisions
		p.fillAdminMappings(&status)
	}
	writeJSON(w, http.StatusOK, status)
}

func (p *Plugin) fillAdminMappings(status *adminStatus) {
	status.Mappings = []adminMapping{}
	var ids []string
	rooms := map[string]string{}
	for page := 0; ; page++ {
		keys, err := p.kvstore.ListKeysWithPrefix(page, 200, kvstore.KeyPrefixChannelMapping)
		if err != nil {
			break
		}
		for _, k := range keys {
			id := strings.TrimPrefix(k, kvstore.KeyPrefixChannelMapping)
			if data, err := p.kvstore.Get(k); err == nil {
				rooms[id] = string(data)
				ids = append(ids, id)
			}
		}
		if len(keys) < 200 {
			break
		}
	}
	sort.Strings(ids)
	status.MappedChannels = len(ids)
	for _, id := range ids {
		if len(status.Mappings) >= adminStatusMappingLimit {
			break
		}
		m := adminMapping{ChannelID: id, Room: rooms[id], ChannelName: id}
		if ch, appErr := p.API.GetChannel(id); appErr == nil && ch != nil {
			m.ChannelName = ch.DisplayName
			if ch.IsGroupOrDirect() {
				m.ChannelName = "(direct conversation)"
			} else if ch.TeamId != "" {
				if team, appErr := p.API.GetTeam(ch.TeamId); appErr == nil && team != nil {
					m.Team = team.DisplayName
				}
			}
		}
		status.Mappings = append(status.Mappings, m)
	}
}
