package main

import (
	"sync"
	"sync/atomic"
	"time"
)

// Metrics provides telemetry data for the bridge
type Metrics struct {
	// Message counters
	messagesToMatrix        atomic.Uint64
	messagesFromMatrix      atomic.Uint64
	messageSyncErrors       atomic.Uint64
	messageEditsSynced      atomic.Uint64
	reactionsAdded          atomic.Uint64
	reactionsRemoved        atomic.Uint64

	// User/Room counters
	ghostUsersCreated       atomic.Uint64
	roomsCreated            atomic.Uint64
	channelsCreated         atomic.Uint64
	usersSynced             atomic.Uint64

	// API call counters
	matrixAPICalls          atomic.Uint64
	matrixAPIErrors         atomic.Uint64
	matrixAPIRetries        atomic.Uint64

	// Latency tracking (milliseconds)
	messageLatencyMutex     sync.RWMutex
	messageLatencySum       uint64
	messageLatencyCount     uint64
	messageLatencyMax       uint64

	apiLatencyMutex         sync.RWMutex
	apiLatencySum           uint64
	apiLatencyCount         uint64
	apiLatencyMax           uint64

	// Error counters by type
	errorsMutex             sync.RWMutex
	errorsByType            map[string]uint64

	// Start time for uptime calculation
	startTime               time.Time
}

// NewMetrics creates a new metrics instance
func NewMetrics() *Metrics {
	return &Metrics{
		errorsByType: make(map[string]uint64),
		startTime:    time.Now(),
	}
}

// Message sync metrics
func (m *Metrics) RecordMessageToMatrix() {
	m.messagesToMatrix.Add(1)
}

func (m *Metrics) RecordMessageFromMatrix() {
	m.messagesFromMatrix.Add(1)
}

func (m *Metrics) RecordMessageSyncError() {
	m.messageSyncErrors.Add(1)
}

func (m *Metrics) RecordMessageEditSynced() {
	m.messageEditsSynced.Add(1)
}

func (m *Metrics) RecordReactionAdded() {
	m.reactionsAdded.Add(1)
}

func (m *Metrics) RecordReactionRemoved() {
	m.reactionsRemoved.Add(1)
}

// User/Room metrics
func (m *Metrics) RecordGhostUserCreated() {
	m.ghostUsersCreated.Add(1)
}

func (m *Metrics) RecordRoomCreated() {
	m.roomsCreated.Add(1)
}

func (m *Metrics) RecordChannelCreated() {
	m.channelsCreated.Add(1)
}

func (m *Metrics) RecordUserSynced() {
	m.usersSynced.Add(1)
}

// API call metrics
func (m *Metrics) RecordMatrixAPICall() {
	m.matrixAPICalls.Add(1)
}

func (m *Metrics) RecordMatrixAPIError() {
	m.matrixAPIErrors.Add(1)
}

func (m *Metrics) RecordMatrixAPIRetry() {
	m.matrixAPIRetries.Add(1)
}

// Latency tracking
func (m *Metrics) RecordMessageLatency(duration time.Duration) {
	ms := uint64(duration.Milliseconds())
	
	m.messageLatencyMutex.Lock()
	defer m.messageLatencyMutex.Unlock()
	
	m.messageLatencySum += ms
	m.messageLatencyCount++
	if ms > m.messageLatencyMax {
		m.messageLatencyMax = ms
	}
}

func (m *Metrics) RecordAPILatency(duration time.Duration) {
	ms := uint64(duration.Milliseconds())
	
	m.apiLatencyMutex.Lock()
	defer m.apiLatencyMutex.Unlock()
	
	m.apiLatencySum += ms
	m.apiLatencyCount++
	if ms > m.apiLatencyMax {
		m.apiLatencyMax = ms
	}
}

// Error tracking
func (m *Metrics) RecordError(errorType string) {
	m.errorsMutex.Lock()
	defer m.errorsMutex.Unlock()
	
	m.errorsByType[errorType]++
}

// Getters for metrics values

func (m *Metrics) GetMessagesToMatrix() uint64 {
	return m.messagesToMatrix.Load()
}

func (m *Metrics) GetMessagesFromMatrix() uint64 {
	return m.messagesFromMatrix.Load()
}

func (m *Metrics) GetMessageSyncErrors() uint64 {
	return m.messageSyncErrors.Load()
}

func (m *Metrics) GetMessageEditsSynced() uint64 {
	return m.messageEditsSynced.Load()
}

func (m *Metrics) GetReactionsAdded() uint64 {
	return m.reactionsAdded.Load()
}

func (m *Metrics) GetReactionsRemoved() uint64 {
	return m.reactionsRemoved.Load()
}

func (m *Metrics) GetGhostUsersCreated() uint64 {
	return m.ghostUsersCreated.Load()
}

func (m *Metrics) GetRoomsCreated() uint64 {
	return m.roomsCreated.Load()
}

func (m *Metrics) GetChannelsCreated() uint64 {
	return m.channelsCreated.Load()
}

func (m *Metrics) GetUsersSynced() uint64 {
	return m.usersSynced.Load()
}

func (m *Metrics) GetMatrixAPICalls() uint64 {
	return m.matrixAPICalls.Load()
}

func (m *Metrics) GetMatrixAPIErrors() uint64 {
	return m.matrixAPIErrors.Load()
}

func (m *Metrics) GetMatrixAPIRetries() uint64 {
	return m.matrixAPIRetries.Load()
}

// GetMessageLatencyAvg returns average message latency in milliseconds
func (m *Metrics) GetMessageLatencyAvg() float64 {
	m.messageLatencyMutex.RLock()
	defer m.messageLatencyMutex.RUnlock()
	
	if m.messageLatencyCount == 0 {
		return 0
	}
	return float64(m.messageLatencySum) / float64(m.messageLatencyCount)
}

// GetMessageLatencyMax returns maximum message latency in milliseconds
func (m *Metrics) GetMessageLatencyMax() uint64 {
	m.messageLatencyMutex.RLock()
	defer m.messageLatencyMutex.RUnlock()
	
	return m.messageLatencyMax
}

// GetAPILatencyAvg returns average API call latency in milliseconds
func (m *Metrics) GetAPILatencyAvg() float64 {
	m.apiLatencyMutex.RLock()
	defer m.apiLatencyMutex.RUnlock()
	
	if m.apiLatencyCount == 0 {
		return 0
	}
	return float64(m.apiLatencySum) / float64(m.apiLatencyCount)
}

// GetAPILatencyMax returns maximum API call latency in milliseconds
func (m *Metrics) GetAPILatencyMax() uint64 {
	m.apiLatencyMutex.RLock()
	defer m.apiLatencyMutex.RUnlock()
	
	return m.apiLatencyMax
}

// GetErrorsByType returns a copy of error counts by type
func (m *Metrics) GetErrorsByType() map[string]uint64 {
	m.errorsMutex.RLock()
	defer m.errorsMutex.RUnlock()
	
	result := make(map[string]uint64, len(m.errorsByType))
	for k, v := range m.errorsByType {
		result[k] = v
	}
	return result
}

// GetUptime returns how long the plugin has been running
func (m *Metrics) GetUptime() time.Duration {
	return time.Since(m.startTime)
}

// MetricsSummary provides a snapshot of all metrics
type MetricsSummary struct {
	// Message metrics
	MessagesToMatrix        uint64            `json:"messages_to_matrix"`
	MessagesFromMatrix      uint64            `json:"messages_from_matrix"`
	MessageSyncErrors       uint64            `json:"message_sync_errors"`
	MessageEditsSynced      uint64            `json:"message_edits_synced"`
	ReactionsAdded          uint64            `json:"reactions_added"`
	ReactionsRemoved        uint64            `json:"reactions_removed"`

	// User/Room metrics
	GhostUsersCreated       uint64            `json:"ghost_users_created"`
	RoomsCreated            uint64            `json:"rooms_created"`
	ChannelsCreated         uint64            `json:"channels_created"`
	UsersSynced             uint64            `json:"users_synced"`

	// API metrics
	MatrixAPICalls          uint64            `json:"matrix_api_calls"`
	MatrixAPIErrors         uint64            `json:"matrix_api_errors"`
	MatrixAPIRetries        uint64            `json:"matrix_api_retries"`

	// Latency metrics
	MessageLatencyAvgMs     float64           `json:"message_latency_avg_ms"`
	MessageLatencyMaxMs     uint64            `json:"message_latency_max_ms"`
	APILatencyAvgMs         float64           `json:"api_latency_avg_ms"`
	APILatencyMaxMs         uint64            `json:"api_latency_max_ms"`

	// Error metrics
	ErrorsByType            map[string]uint64 `json:"errors_by_type"`

	// System metrics
	UptimeSeconds           float64           `json:"uptime_seconds"`
}

// GetSummary returns a snapshot of all metrics
func (m *Metrics) GetSummary() MetricsSummary {
	return MetricsSummary{
		MessagesToMatrix:        m.GetMessagesToMatrix(),
		MessagesFromMatrix:      m.GetMessagesFromMatrix(),
		MessageSyncErrors:       m.GetMessageSyncErrors(),
		MessageEditsSynced:      m.GetMessageEditsSynced(),
		ReactionsAdded:          m.GetReactionsAdded(),
		ReactionsRemoved:        m.GetReactionsRemoved(),
		GhostUsersCreated:       m.GetGhostUsersCreated(),
		RoomsCreated:            m.GetRoomsCreated(),
		ChannelsCreated:         m.GetChannelsCreated(),
		UsersSynced:             m.GetUsersSynced(),
		MatrixAPICalls:          m.GetMatrixAPICalls(),
		MatrixAPIErrors:         m.GetMatrixAPIErrors(),
		MatrixAPIRetries:        m.GetMatrixAPIRetries(),
		MessageLatencyAvgMs:     m.GetMessageLatencyAvg(),
		MessageLatencyMaxMs:     m.GetMessageLatencyMax(),
		APILatencyAvgMs:         m.GetAPILatencyAvg(),
		APILatencyMaxMs:         m.GetAPILatencyMax(),
		ErrorsByType:            m.GetErrorsByType(),
		UptimeSeconds:           m.GetUptime().Seconds(),
	}
}

// Reset resets all metrics (useful for testing)
func (m *Metrics) Reset() {
	m.messagesToMatrix.Store(0)
	m.messagesFromMatrix.Store(0)
	m.messageSyncErrors.Store(0)
	m.messageEditsSynced.Store(0)
	m.reactionsAdded.Store(0)
	m.reactionsRemoved.Store(0)
	m.ghostUsersCreated.Store(0)
	m.roomsCreated.Store(0)
	m.channelsCreated.Store(0)
	m.usersSynced.Store(0)
	m.matrixAPICalls.Store(0)
	m.matrixAPIErrors.Store(0)
	m.matrixAPIRetries.Store(0)

	m.messageLatencyMutex.Lock()
	m.messageLatencySum = 0
	m.messageLatencyCount = 0
	m.messageLatencyMax = 0
	m.messageLatencyMutex.Unlock()

	m.apiLatencyMutex.Lock()
	m.apiLatencySum = 0
	m.apiLatencyCount = 0
	m.apiLatencyMax = 0
	m.apiLatencyMutex.Unlock()

	m.errorsMutex.Lock()
	m.errorsByType = make(map[string]uint64)
	m.errorsMutex.Unlock()

	m.startTime = time.Now()
}
