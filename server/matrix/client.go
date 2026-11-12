// Package matrix provides Matrix client functionality for the Mattermost bridge.
package matrix

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/pkg/errors"
)

// Error represents a Matrix API error response
type Error struct {
	ErrCode    string `json:"errcode"`
	ErrMsg     string `json:"error"`
	StatusCode int    `json:"-"`
}

// Error implements the error interface
func (e *Error) Error() string {
	return fmt.Sprintf("matrix API error: %d %s - %s", e.StatusCode, e.ErrCode, e.ErrMsg)
}

// IsAlreadyJoined checks if the error indicates the user is already in the room
func (e *Error) IsAlreadyJoined() bool {
	return e.ErrCode == "M_BAD_STATE" ||
		strings.Contains(strings.ToLower(e.ErrMsg), "already joined") ||
		strings.Contains(strings.ToLower(e.ErrMsg), "already in the room") ||
		strings.Contains(e.ErrCode, "ALREADY_JOINED")
}

// parseMatrixError attempts to parse a Matrix error from response body
func parseMatrixError(statusCode int, body []byte) *Error {
	var mErr Error
	mErr.StatusCode = statusCode

	if err := json.Unmarshal(body, &mErr); err != nil {
		// Fallback for non-JSON responses
		mErr.ErrCode = "UNKNOWN"
		mErr.ErrMsg = string(body)
	}

	return &mErr
}

// isForbiddenJoinError checks if an error indicates a forbidden join attempt
func isForbiddenJoinError(err error) bool {
	var matrixErr *Error
	if errors.As(err, &matrixErr) {
		return matrixErr.StatusCode == http.StatusForbidden ||
			matrixErr.ErrCode == "M_FORBIDDEN" ||
			strings.Contains(strings.ToLower(matrixErr.ErrMsg), "not invited")
	}

	// Fallback for legacy error types that aren't structured
	return strings.Contains(err.Error(), "403") ||
		strings.Contains(err.Error(), "M_FORBIDDEN") ||
		strings.Contains(err.Error(), "not invited")
}

// Path traversal validation functions

// ValidatePathComponent checks for path traversal sequences in URL path components
func ValidatePathComponent(component string) error {
	if strings.Contains(component, "..") {
		return errors.Errorf("path traversal detected in component: %s", component)
	}
	return nil
}

// BuildSecureURL constructs URL paths with proper escaping and validation
func BuildSecureURL(baseURL string, pathComponents ...string) (string, error) {
	var urlParts []string

	for _, component := range pathComponents {
		if err := ValidatePathComponent(component); err != nil {
			return "", err
		}
		urlParts = append(urlParts, url.PathEscape(component))
	}

	return baseURL + strings.Join(urlParts, "/"), nil
}

// ValidateMXCComponents validates MXC URI components for path traversal attacks
func ValidateMXCComponents(serverName, mediaID string) error {
	if err := ValidatePathComponent(serverName); err != nil {
		return errors.Wrap(err, "invalid server name in MXC URI")
	}
	if err := ValidatePathComponent(mediaID); err != nil {
		return errors.Wrap(err, "invalid media ID in MXC URI")
	}
	return nil
}

// Logger interface for matrix client logging
type Logger interface {
	LogDebug(message string, keyValuePairs ...any)
	LogInfo(message string, keyValuePairs ...any)
	LogWarn(message string, keyValuePairs ...any)
	LogError(message string, keyValuePairs ...any)
}

// APILogger implements Logger interface using plugin.API
type APILogger struct {
	api plugin.API
}

// NewAPILogger creates a new APILogger
func NewAPILogger(api plugin.API) Logger {
	return &APILogger{api: api}
}

// LogDebug logs a debug message using the plugin API
func (l *APILogger) LogDebug(message string, keyValuePairs ...any) {
	l.api.LogDebug(message, keyValuePairs...)
}

// LogInfo logs an info message using the plugin API
func (l *APILogger) LogInfo(message string, keyValuePairs ...any) {
	l.api.LogInfo(message, keyValuePairs...)
}

// LogWarn logs a warning message using the plugin API
func (l *APILogger) LogWarn(message string, keyValuePairs ...any) {
	l.api.LogWarn(message, keyValuePairs...)
}

// LogError logs an error message using the plugin API
func (l *APILogger) LogError(message string, keyValuePairs ...any) {
	l.api.LogError(message, keyValuePairs...)
}

// TestLogger implements Logger interface for testing
type TestLogger interface {
	Logf(format string, args ...any)
}

// testLogger implements Logger interface using a TestLogger (like testing.T)
type testLogger struct {
	t TestLogger
}

// NewTestLogger creates a new test logger that logs to a TestLogger (like testing.T)
func NewTestLogger(t TestLogger) Logger {
	return &testLogger{t: t}
}

func (l *testLogger) LogDebug(message string, keyValuePairs ...any) {
	if l.t != nil {
		l.t.Logf("[DEBUG] %s %v", message, keyValuePairs)
	}
}

func (l *testLogger) LogInfo(message string, keyValuePairs ...any) {
	if l.t != nil {
		l.t.Logf("[INFO] %s %v", message, keyValuePairs)
	}
}

func (l *testLogger) LogWarn(message string, keyValuePairs ...any) {
	if l.t != nil {
		l.t.Logf("[WARN] %s %v", message, keyValuePairs)
	}
}

func (l *testLogger) LogError(message string, keyValuePairs ...any) {
	if l.t != nil {
		l.t.Logf("[ERROR] %s %v", message, keyValuePairs)
	}
}

// Client represents a Matrix HTTP client for communicating with Matrix servers.
type Client struct {
	// encryptDirectRooms, when set and true, makes new direct rooms start
	// encrypted.
	encryptDirectRooms func() bool
	serverURL          string
	asToken            string // Application Service token for all operations
	remoteID           string // Plugin remote ID for metadata
	httpClient         *http.Client
	logger             Logger
	serverDomain       string      // explicit server domain for testing
	puppetToken        string      // when set, requests act as a real user with this token instead of impersonating via the AS token
	sent               *sentEvents // events this client sent, shared by puppet copies, to recognise their echoes

	// Rate limiting
	rateLimitConfig     RateLimitConfig
	roomCreationLimiter *TokenBucket
	messageLimiter      *TokenBucket
	inviteLimiter       *TokenBucket
	registrationLimiter *TokenBucket
	joinLimiter         *TokenBucket

	// crypto is shared by every copy of the client (see AsPuppet): the optional
	// encryptor for outgoing events and what is known about encrypted rooms.
	crypto *clientCrypto

	// adapter slows requests down when the homeserver answers 429.
	adapter *rateAdapter
}

// waitForRateLimit applies rate limiting for the specified operation
func (c *Client) waitForRateLimit(limiter *TokenBucket, operation string) error {
	if !c.rateLimitConfig.Enabled {
		c.logger.LogDebug("Rate limiting disabled, proceeding without throttling", "operation", operation)
		return nil
	}

	if limiter == nil {
		c.logger.LogWarn("Rate limiter not initialized for operation", "operation", operation)
		return nil
	}

	c.logger.LogDebug("Applying rate limiting", "operation", operation)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startTime := time.Now()
	if err := limiter.Wait(ctx); err != nil {
		c.logger.LogWarn("Rate limiting failed", "operation", operation, "error", err, "waited_duration", time.Since(startTime))
		return errors.Wrap(err, operation+" rate limited")
	}

	waitDuration := time.Since(startTime)
	if waitDuration > 100*time.Millisecond {
		c.logger.LogInfo("Rate limiting applied", "operation", operation, "waited_duration", waitDuration)
	} else {
		c.logger.LogDebug("Rate limiting completed (no wait required)", "operation", operation, "waited_duration", waitDuration)
	}

	return nil
}

// MessageContent represents the content structure for Matrix messages.
type MessageContent struct {
	MsgType       string `json:"msgtype"`
	Body          string `json:"body"`
	Format        string `json:"format,omitempty"`
	FormattedBody string `json:"formatted_body,omitempty"`
}

// FileAttachment represents a file attachment for Matrix messages.
type FileAttachment struct {
	Filename string `json:"filename"`
	MxcURI   string `json:"mxc_uri"`
	MimeType string `json:"mimetype"`
	Size     int64  `json:"size"`
	// Width and Height of an image or video, when known; clients use them to
	// reserve space before the media loads.
	Width  int `json:"w,omitempty"`
	Height int `json:"h,omitempty"`
	// File is set instead of MxcURI semantics when the room is encrypted: the
	// encrypted file description (url, key, iv, hashes) that goes in the
	// message's "file" field.
	File map[string]any `json:"file,omitempty"`
}

// MessageRequest represents a request to send a message as a ghost user with all optional parameters.
type MessageRequest struct {
	RoomID         string           `json:"room_id"`           // Required: Matrix room ID
	GhostUserID    string           `json:"ghost_user_id"`     // Required: Ghost user ID to send as
	Message        string           `json:"message"`           // Optional: Plain text message content
	HTMLMessage    string           `json:"html_message"`      // Optional: HTML formatted message content
	ThreadEventID  string           `json:"thread_event_id"`   // Optional: Event ID to thread/reply to
	PostID         string           `json:"post_id"`           // Optional: Mattermost post ID metadata
	Files          []FileAttachment `json:"files"`             // Optional: File attachments
	ReplyToEventID string           `json:"reply_to_event_id"` // Optional: Event ID to reply to (for files)
	Mentions       map[string]any   `json:"mentions"`          // Optional: Matrix mentions data (m.mentions field)
	// ExtraContent is merged into the event content, for fields such as a
	// per-message profile. It never replaces the fields the client sets itself.
	ExtraContent map[string]any `json:"extra_content,omitempty"`
	// Timestamp is the time in milliseconds the message should carry instead
	// of now, for importing history. Only the application service honours it.
	Timestamp int64 `json:"timestamp,omitempty"`
}

// SendEventResponse represents the response from Matrix when sending events.
type SendEventResponse struct {
	EventID string `json:"event_id"`
}

// NewClientWithRateLimit creates a new Matrix client with custom rate limiting.
func NewClientWithRateLimit(serverURL, asToken, remoteID string, api plugin.API, rateLimitConfig RateLimitConfig) *Client {
	return NewClientWithLoggerAndRateLimit(serverURL, asToken, remoteID, NewAPILogger(api), rateLimitConfig)
}

// NewClientWithLoggerAndRateLimit creates a new Matrix client with custom logger and rate limiting.
func NewClientWithLoggerAndRateLimit(serverURL, asToken, remoteID string, logger Logger, rateLimitConfig RateLimitConfig) *Client {
	// Configure HTTP transport with connection pooling for better performance
	transport := &http.Transport{
		MaxIdleConns:        100,              // Maximum idle connections across all hosts
		MaxIdleConnsPerHost: 10,               // Maximum idle connections per host
		MaxConnsPerHost:     50,               // Maximum connections per host (including active)
		IdleConnTimeout:     90 * time.Second, // How long idle connections stay open
		DisableKeepAlives:   false,            // Enable HTTP keep-alive
		ForceAttemptHTTP2:   true,             // Try HTTP/2 if available
	}

	adapter := newRateAdapter()
	client := &Client{
		adapter:   adapter,
		crypto:    &clientCrypto{},
		serverURL: serverURL,
		asToken:   asToken,
		remoteID:  remoteID,
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &rateAwareTransport{base: transport, adapter: adapter, logger: logger},
		},
		logger:          logger,
		rateLimitConfig: rateLimitConfig,
		sent:            newSentEvents(),
	}

	// Initialize rate limiters if enabled
	if rateLimitConfig.Enabled {
		logger.LogInfo("Initializing Matrix client with rate limiting enabled",
			"room_creation_rate", rateLimitConfig.RoomCreation.Rate,
			"room_creation_burst", rateLimitConfig.RoomCreation.BurstSize,
			"message_rate", rateLimitConfig.Messages.Rate,
			"message_burst", rateLimitConfig.Messages.BurstSize)

		client.roomCreationLimiter = NewTokenBucket(rateLimitConfig.RoomCreation)
		client.messageLimiter = NewTokenBucket(rateLimitConfig.Messages)
		client.inviteLimiter = NewTokenBucket(rateLimitConfig.Invites)
		client.registrationLimiter = NewTokenBucket(rateLimitConfig.Registration)
		client.joinLimiter = NewTokenBucket(rateLimitConfig.Joins)
		adapter.attach(client.roomCreationLimiter, client.messageLimiter, client.inviteLimiter, client.registrationLimiter, client.joinLimiter)
	} else {
		logger.LogWarn("Matrix client initialized with rate limiting DISABLED - may encounter 429 errors")
	}

	return client
}

// SetServerDomain sets an explicit server domain (used for testing)
func (c *Client) SetServerDomain(domain string) {
	c.serverDomain = domain
}

// SetServerURL updates the server URL for the client
func (c *Client) SetServerURL(serverURL string) {
	c.serverURL = serverURL
}

// TestConnection verifies that the Matrix client can connect to the server.
func (c *Client) TestConnection() error {
	if c.serverURL == "" || c.asToken == "" {
		return errors.New("matrix client not configured")
	}

	url := c.serverURL + "/_matrix/client/v3/account/whoami"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create request")
	}

	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send request")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("matrix connection test failed: %d %s", resp.StatusCode, string(body))
	}

	return nil
}

// AsPuppet returns a copy of the client that acts as a real Matrix user with
// the given access token (double puppeting). Requests made through the copy
// send the token instead of the appservice token and omit user_id.
func (c *Client) AsPuppet(token string) *Client {
	cp := *c
	cp.puppetToken = token
	return &cp
}

// IsPuppet reports whether the client acts as a real user.
func (c *Client) IsPuppet() bool {
	return c.puppetToken != ""
}

// authorize sets the Authorization header and, when impersonating through
// the appservice, the user_id query parameter.
func (c *Client) authorize(req *http.Request, userID string) {
	if c.puppetToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.puppetToken)
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.asToken)
	if userID != "" {
		q := req.URL.Query()
		q.Set("user_id", userID)
		req.URL.RawQuery = q.Encode()
	}
}

// WhoAmI returns the user ID that owns an access token.
func (c *Client) WhoAmI(token string) (string, error) {
	req, err := http.NewRequest("GET", c.serverURL+"/_matrix/client/v3/account/whoami", nil)
	if err != nil {
		return "", errors.Wrap(err, "failed to create whoami request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "failed to send whoami request")
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("whoami failed: HTTP %d - %s", resp.StatusCode, string(body))
	}
	var out struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", errors.Wrap(err, "failed to parse whoami response")
	}
	return out.UserID, nil
}

// ServerDomain returns the Matrix server name this client builds user IDs
// with, or "" when it cannot be determined.
func (c *Client) ServerDomain() string {
	domain, err := c.extractServerDomain()
	if err != nil {
		return ""
	}
	return domain
}

// extractServerDomain extracts the hostname from the Matrix server URL
func (c *Client) extractServerDomain() (string, error) {
	// Use explicit server domain if set (for testing)
	if c.serverDomain != "" {
		return c.serverDomain, nil
	}

	if c.serverURL == "" {
		return "", errors.New("server URL not configured")
	}

	parsedURL, err := url.Parse(c.serverURL)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse server URL")
	}

	hostname := parsedURL.Hostname()
	if hostname == "" {
		return "", errors.New("could not extract hostname from server URL")
	}

	return hostname, nil
}

// extractServerDomainForTest extracts domain for testing purposes
func (c *Client) extractServerDomainForTest() string {
	if c.serverURL == "" {
		return "example.com"
	}

	// Use the existing extractServerDomain method
	domain, err := c.extractServerDomain()
	if err != nil {
		return "example.com"
	}

	return domain
}

// makeMatrixRequest makes an HTTP request to Matrix without Application Service authentication
func (c *Client) makeMatrixRequest(method, endpoint string, data any, token string) (any, error) {
	var body io.Reader

	if data != nil {
		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		body = strings.NewReader(string(jsonData))
	}

	requestURL := c.serverURL + endpoint
	req, err := http.NewRequest(method, requestURL, body)
	if err != nil {
		return nil, err
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if data != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("matrix API error: %d %s", resp.StatusCode, string(responseBody))
	}

	if len(responseBody) == 0 {
		return map[string]any{}, nil
	}

	var result any
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return nil, err
	}

	return result, nil
}

// getServerName returns the Matrix server name used to build user IDs. It
// prefers the explicitly configured server domain and otherwise derives the
// hostname from the configured server URL.
func (c *Client) getServerName() string {
	domain, err := c.extractServerDomain()
	if err != nil {
		return ""
	}
	return domain
}
