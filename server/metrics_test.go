package main

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewMetrics verifies metrics initialization
func TestNewMetrics(t *testing.T) {
	m := NewMetrics()
	require.NotNil(t, m)
	
	// Verify all counters start at zero
	assert.Equal(t, uint64(0), m.GetMessagesToMatrix())
	assert.Equal(t, uint64(0), m.GetMessagesFromMatrix())
	assert.Equal(t, uint64(0), m.GetMessageSyncErrors())
	assert.Equal(t, uint64(0), m.GetGhostUsersCreated())
	
	// Verify uptime is tracked
	assert.Greater(t, m.GetUptime(), time.Duration(0))
}

// TestMessageMetrics tests message-related metrics
func TestMessageMetrics(t *testing.T) {
	m := NewMetrics()
	
	// Test message to Matrix
	m.RecordMessageToMatrix()
	m.RecordMessageToMatrix()
	m.RecordMessageToMatrix()
	assert.Equal(t, uint64(3), m.GetMessagesToMatrix())
	
	// Test message from Matrix
	m.RecordMessageFromMatrix()
	m.RecordMessageFromMatrix()
	assert.Equal(t, uint64(2), m.GetMessagesFromMatrix())
	
	// Test message sync errors
	m.RecordMessageSyncError()
	assert.Equal(t, uint64(1), m.GetMessageSyncErrors())
	
	// Test message edits
	m.RecordMessageEditSynced()
	m.RecordMessageEditSynced()
	assert.Equal(t, uint64(2), m.GetMessageEditsSynced())
	
	// Test reactions
	m.RecordReactionAdded()
	m.RecordReactionAdded()
	m.RecordReactionAdded()
	assert.Equal(t, uint64(3), m.GetReactionsAdded())
	
	m.RecordReactionRemoved()
	assert.Equal(t, uint64(1), m.GetReactionsRemoved())
}

// TestUserRoomMetrics tests user and room creation metrics
func TestUserRoomMetrics(t *testing.T) {
	m := NewMetrics()
	
	m.RecordGhostUserCreated()
	m.RecordGhostUserCreated()
	assert.Equal(t, uint64(2), m.GetGhostUsersCreated())
	
	m.RecordRoomCreated()
	assert.Equal(t, uint64(1), m.GetRoomsCreated())
	
	m.RecordChannelCreated()
	m.RecordChannelCreated()
	assert.Equal(t, uint64(2), m.GetChannelsCreated())
	
	m.RecordUserSynced()
	m.RecordUserSynced()
	m.RecordUserSynced()
	assert.Equal(t, uint64(3), m.GetUsersSynced())
}

// TestAPIMetrics tests Matrix API call metrics
func TestAPIMetrics(t *testing.T) {
	m := NewMetrics()
	
	m.RecordMatrixAPICall()
	m.RecordMatrixAPICall()
	m.RecordMatrixAPICall()
	assert.Equal(t, uint64(3), m.GetMatrixAPICalls())
	
	m.RecordMatrixAPIError()
	assert.Equal(t, uint64(1), m.GetMatrixAPIErrors())
	
	m.RecordMatrixAPIRetry()
	m.RecordMatrixAPIRetry()
	assert.Equal(t, uint64(2), m.GetMatrixAPIRetries())
}

// TestLatencyTracking tests latency measurement
func TestLatencyTracking(t *testing.T) {
	m := NewMetrics()
	
	// Record some message latencies
	m.RecordMessageLatency(100 * time.Millisecond)
	m.RecordMessageLatency(200 * time.Millisecond)
	m.RecordMessageLatency(300 * time.Millisecond)
	
	// Check average (100 + 200 + 300) / 3 = 200ms
	avgMs := m.GetMessageLatencyAvg()
	assert.Equal(t, 200.0, avgMs)
	
	// Check max
	maxMs := m.GetMessageLatencyMax()
	assert.Equal(t, uint64(300), maxMs)
	
	// Record some API latencies
	m.RecordAPILatency(50 * time.Millisecond)
	m.RecordAPILatency(150 * time.Millisecond)
	
	// Check average (50 + 150) / 2 = 100ms
	apiAvgMs := m.GetAPILatencyAvg()
	assert.Equal(t, 100.0, apiAvgMs)
	
	// Check max
	apiMaxMs := m.GetAPILatencyMax()
	assert.Equal(t, uint64(150), apiMaxMs)
}

// TestLatencyWithNoData tests latency calculations with no data
func TestLatencyWithNoData(t *testing.T) {
	m := NewMetrics()
	
	assert.Equal(t, 0.0, m.GetMessageLatencyAvg())
	assert.Equal(t, uint64(0), m.GetMessageLatencyMax())
	assert.Equal(t, 0.0, m.GetAPILatencyAvg())
	assert.Equal(t, uint64(0), m.GetAPILatencyMax())
}

// TestErrorTracking tests error categorization
func TestErrorTracking(t *testing.T) {
	m := NewMetrics()
	
	m.RecordError("network_timeout")
	m.RecordError("network_timeout")
	m.RecordError("network_timeout")
	m.RecordError("rate_limit")
	m.RecordError("rate_limit")
	m.RecordError("auth_failed")
	
	errors := m.GetErrorsByType()
	assert.Equal(t, uint64(3), errors["network_timeout"])
	assert.Equal(t, uint64(2), errors["rate_limit"])
	assert.Equal(t, uint64(1), errors["auth_failed"])
}

// TestConcurrentAccess tests thread-safety of metrics
func TestConcurrentAccess(t *testing.T) {
	m := NewMetrics()
	
	var wg sync.WaitGroup
	iterations := 1000
	goroutines := 10
	
	// Concurrently increment message counter
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				m.RecordMessageToMatrix()
			}
		}()
	}
	
	// Concurrently record latencies
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				m.RecordMessageLatency(time.Duration(j) * time.Millisecond)
			}
		}()
	}
	
	// Concurrently record errors
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				m.RecordError("error_type_" + string(rune('A'+id)))
			}
		}(i)
	}
	
	wg.Wait()
	
	// Verify counts
	assert.Equal(t, uint64(goroutines*iterations), m.GetMessagesToMatrix())
	
	// Verify we can safely read latency data
	avgLatency := m.GetMessageLatencyAvg()
	assert.Greater(t, avgLatency, 0.0)
	
	// Verify we can safely read error data
	errors := m.GetErrorsByType()
	assert.NotEmpty(t, errors)
}

// TestGetSummary tests the summary snapshot functionality
func TestGetSummary(t *testing.T) {
	m := NewMetrics()
	
	// Record some metrics
	m.RecordMessageToMatrix()
	m.RecordMessageFromMatrix()
	m.RecordGhostUserCreated()
	m.RecordMatrixAPICall()
	m.RecordMessageLatency(100 * time.Millisecond)
	m.RecordError("test_error")
	
	// Get summary
	summary := m.GetSummary()
	
	assert.Equal(t, uint64(1), summary.MessagesToMatrix)
	assert.Equal(t, uint64(1), summary.MessagesFromMatrix)
	assert.Equal(t, uint64(1), summary.GhostUsersCreated)
	assert.Equal(t, uint64(1), summary.MatrixAPICalls)
	assert.Equal(t, 100.0, summary.MessageLatencyAvgMs)
	assert.Equal(t, uint64(100), summary.MessageLatencyMaxMs)
	assert.Equal(t, uint64(1), summary.ErrorsByType["test_error"])
	assert.Greater(t, summary.UptimeSeconds, 0.0)
}

// TestReset tests metrics reset functionality
func TestReset(t *testing.T) {
	m := NewMetrics()
	
	// Record some metrics
	m.RecordMessageToMatrix()
	m.RecordMessageFromMatrix()
	m.RecordGhostUserCreated()
	m.RecordMessageLatency(100 * time.Millisecond)
	m.RecordError("test_error")
	
	// Verify metrics are non-zero
	assert.Greater(t, m.GetMessagesToMatrix(), uint64(0))
	assert.Greater(t, m.GetMessageLatencyAvg(), 0.0)
	
	// Reset
	m.Reset()
	
	// Verify all metrics are reset
	assert.Equal(t, uint64(0), m.GetMessagesToMatrix())
	assert.Equal(t, uint64(0), m.GetMessagesFromMatrix())
	assert.Equal(t, uint64(0), m.GetGhostUsersCreated())
	assert.Equal(t, 0.0, m.GetMessageLatencyAvg())
	assert.Equal(t, uint64(0), m.GetMessageLatencyMax())
	assert.Empty(t, m.GetErrorsByType())
}

// TestUptimeTracking tests uptime calculation
func TestUptimeTracking(t *testing.T) {
	m := NewMetrics()
	
	// Wait a bit
	time.Sleep(100 * time.Millisecond)
	
	uptime := m.GetUptime()
	assert.GreaterOrEqual(t, uptime, 100*time.Millisecond)
	assert.Less(t, uptime, 1*time.Second) // Should be less than a second
}

// TestMetricsIndependence tests that multiple metrics instances are independent
func TestMetricsIndependence(t *testing.T) {
	m1 := NewMetrics()
	m2 := NewMetrics()
	
	m1.RecordMessageToMatrix()
	m1.RecordMessageToMatrix()
	
	m2.RecordMessageToMatrix()
	
	assert.Equal(t, uint64(2), m1.GetMessagesToMatrix())
	assert.Equal(t, uint64(1), m2.GetMessagesToMatrix())
}
