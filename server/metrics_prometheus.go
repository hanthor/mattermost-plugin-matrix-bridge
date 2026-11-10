package main

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// promSample is one metric in the Prometheus text exposition format.
type promSample struct {
	name, help, kind string
	value            float64
	labels           map[string]string
}

func (s promSample) line() string {
	if len(s.labels) == 0 {
		return fmt.Sprintf("%s %v\n", s.name, s.value)
	}
	keys := make([]string, 0, len(s.labels))
	for k := range s.labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, s.labels[k]))
	}
	return fmt.Sprintf("%s{%s} %v\n", s.name, strings.Join(parts, ","), s.value)
}

// renderPrometheus writes samples grouped by metric name with one HELP and
// TYPE line each.
func renderPrometheus(samples []promSample) string {
	var sb strings.Builder
	seen := map[string]bool{}
	for _, s := range samples {
		if !seen[s.name] {
			seen[s.name] = true
			sb.WriteString(fmt.Sprintf("# HELP %s %s\n# TYPE %s %s\n", s.name, s.help, s.name, s.kind))
		}
		sb.WriteString(s.line())
	}
	return sb.String()
}

// prometheusSamples turns the bridge's counters and state into samples.
func (p *Plugin) prometheusSamples() []promSample {
	var out []promSample
	counter := func(name, help string, v uint64) {
		out = append(out, promSample{name: "matrix_bridge_" + name + "_total", help: help, kind: "counter", value: float64(v)})
	}
	gauge := func(name, help string, v float64) {
		out = append(out, promSample{name: "matrix_bridge_" + name, help: help, kind: "gauge", value: v})
	}

	if p.metrics != nil {
		m := p.metrics.GetSummary()
		counter("messages_to_matrix", "Messages sent from Mattermost to Matrix.", m.MessagesToMatrix)
		counter("messages_from_matrix", "Messages posted in Mattermost from Matrix.", m.MessagesFromMatrix)
		counter("message_sync_errors", "Messages that failed to sync.", m.MessageSyncErrors)
		counter("message_edits_synced", "Message edits synced.", m.MessageEditsSynced)
		counter("reactions_added", "Reactions added across the bridge.", m.ReactionsAdded)
		counter("reactions_removed", "Reactions removed across the bridge.", m.ReactionsRemoved)
		counter("accounts_created", "Matrix accounts (ghosts or mirror accounts) created.", m.GhostUsersCreated)
		counter("rooms_created", "Matrix rooms created.", m.RoomsCreated)
		counter("channels_created", "Mattermost channels created.", m.ChannelsCreated)
		counter("users_synced", "User profiles synced.", m.UsersSynced)
		counter("matrix_api_calls", "Calls made to the Matrix homeserver.", m.MatrixAPICalls)
		counter("matrix_api_errors", "Matrix homeserver calls that failed.", m.MatrixAPIErrors)
		counter("matrix_api_retries", "Matrix homeserver calls that were retried.", m.MatrixAPIRetries)
		gauge("message_latency_avg_ms", "Average time to sync a message, in milliseconds.", m.MessageLatencyAvgMs)
		gauge("message_latency_max_ms", "Longest time to sync a message, in milliseconds.", float64(m.MessageLatencyMaxMs))
		gauge("api_latency_avg_ms", "Average Matrix call time, in milliseconds.", m.APILatencyAvgMs)
		gauge("api_latency_max_ms", "Longest Matrix call time, in milliseconds.", float64(m.APILatencyMaxMs))
		gauge("uptime_seconds", "Seconds since the plugin started.", m.UptimeSeconds)
		types := make([]string, 0, len(m.ErrorsByType))
		for k := range m.ErrorsByType {
			types = append(types, k)
		}
		sort.Strings(types)
		for _, k := range types {
			out = append(out, promSample{name: "matrix_bridge_errors_total", help: "Errors by type.", kind: "counter", value: float64(m.ErrorsByType[k]), labels: map[string]string{"type": k}})
		}
	}
	if p.eventQueue != nil {
		gauge("queue_depth", "Events waiting for a worker.", float64(p.eventQueue.Depth()))
		gauge("queue_capacity", "How many events can wait.", float64(p.eventQueue.Capacity()))
	}
	if p.kvstore != nil {
		gauge("inbox_pending", "Matrix events journaled and not yet handled.", float64(p.pendingInbound()))
		gauge("dead_letters", "Matrix events set aside after every retry failed.", float64(p.inboundDeadLetters()))
		gauge("outbox_pending", "Mattermost changes waiting to reach Matrix.", float64(p.OutboundPending()))
		gauge("outbox_failed", "Mattermost changes set aside after a day without reaching Matrix.", float64(p.OutboundFailed()))
		st := p.MirrorStatus()
		gauge("mirror_accounts", "Mirror accounts.", float64(st.Accounts))
		gauge("mirror_accounts_claimed", "Mirror accounts their people have claimed.", float64(st.Claimed))
	}
	if p.matrixClient != nil {
		counter("rate_limit_responses", "429 responses from the homeserver.", p.matrixClient.RateLimitHits())
		gauge("rate_limit_factor", "Share of the configured request rate in use (1 unless the homeserver pushed back).", p.matrixClient.RateLimitFactor())
	}
	enc := 0.0
	if p.EncryptionSupported() {
		enc = 1
	}
	gauge("encryption_ready", "1 when the bridge bot's encryption device is running.", enc)
	return out
}

// handlePrometheusMetrics serves GET /metrics.
func (p *Plugin) handlePrometheusMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(renderPrometheus(p.prometheusSamples())))
}
