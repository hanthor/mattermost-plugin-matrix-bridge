# Architecture Documentation

This document provides a detailed technical overview of the Mattermost-Matrix Bridge architecture.

## Table of Contents

- [System Architecture](#system-architecture)
- [Component Details](#component-details)
- [Data Flow](#data-flow)
- [Storage Schema](#storage-schema)
- [API Integration](#api-integration)
- [Security Model](#security-model)
- [Performance Considerations](#performance-considerations)

---

## System Architecture

### High-Level Overview

```
┌────────────────────────────────────────────────────────────────────┐
│                         Mattermost Server                          │
│                                                                    │
│  ┌──────────────────────────────────────────────────────────────┐ │
│  │                  Matrix Bridge Plugin                        │ │
│  │                                                              │ │
│  │  ┌───────────────────┐        ┌──────────────────────────┐ │ │
│  │  │   Plugin Core     │        │   HTTP Server (AS)       │ │ │
│  │  │  - Hooks          │◄──────►│  - /_matrix/app/v1/*     │ │ │
│  │  │  - Commands       │        │  - Transaction Handler   │ │ │
│  │  │  - KV Store       │        │  - User/Room Query       │ │ │
│  │  └───────────────────┘        └──────────────────────────┘ │ │
│  │           │                              ▲                  │ │
│  │           │                              │                  │ │
│  │           ▼                              │                  │ │
│  │  ┌───────────────────┐                  │                  │ │
│  │  │  Matrix Client    │──────────────────┘                  │ │
│  │  │  - Client API     │                                     │ │
│  │  │  - Room Mgmt      │                                     │ │
│  │  │  - User Mgmt      │                                     │ │
│  │  │  - Media Upload   │                                     │ │
│  │  └───────────────────┘                                     │ │
│  └──────────────────────────────────────────────────────────────┘ │
│                           ▲                                        │
└───────────────────────────┼────────────────────────────────────────┘
                            │
                   ┌────────┴────────┐
                   │                 │
           Client-Server API   Application Service API
                   │                 │
                   └────────┬────────┘
                            ▼
┌────────────────────────────────────────────────────────────────────┐
│                     Matrix Homeserver                              │
│                  (Synapse, Dendrite, etc.)                         │
│                                                                    │
│  ┌──────────────┐  ┌──────────────┐  ┌───────────────────────┐  │
│  │   Rooms      │  │   Spaces     │  │  Application Service  │  │
│  │   Database   │  │   Hierarchy  │  │  Registration         │  │
│  └──────────────┘  └──────────────┘  └───────────────────────┘  │
└────────────────────────────────────────────────────────────────────┘
                            │
                            ▼
                   Matrix Federation
                            │
                            ▼
                  ┌──────────────────┐
                  │  Other Matrix    │
                  │  Homeservers     │
                  └──────────────────┘
```

### Component Layers

1. **Mattermost Layer**
   - Plugin hooks intercept events
   - Slash commands provide user interface
   - KV store persists mappings
   - Webhook system for posting messages

2. **Bridge Layer**
   - Bidirectional event translation
   - User identity management (ghost users)
   - Room/channel mapping
   - Media handling

3. **Matrix Layer**
   - Homeserver maintains rooms and users
   - Application Service provides bridge privileges
   - Federation enables cross-server communication

---

## Component Details

### 1. Plugin Core (`server/plugin.go`)

The main plugin entry point that integrates with Mattermost.

**Key Responsibilities:**
- Plugin lifecycle management (OnActivate, OnDeactivate)
- Hook registration
- Configuration management
- Service initialization

**Key Hooks:**
```go
MessageHasBeenPosted(c *plugin.Context, post *model.Post)
UserHasBeenCreated(c *plugin.Context, user *model.User)
UserHasUpdated(c *plugin.Context, user *model.User)
ChannelHasBeenCreated(c *plugin.Context, channel *model.Channel)
TeamHasBeenCreated(c *plugin.Context, team *model.Team)
UserHasJoinedChannel(c *plugin.Context, channelMember *model.ChannelMember, actor *model.User)
```

**Dependencies:**
- Matrix Client
- KV Store
- Plugin API
- Bridge Services

### 2. Matrix Client (`server/matrix/client.go`)

Handles all communication with the Matrix homeserver via Client-Server API.

**Key Methods:**
```go
// Room Management
CreateRoom(name, topic, serverDomain string, isPublic bool, channelID string) (string, error)
CreateSpace(name, topic, aliasLocalPart string) (string, error)
JoinRoom(roomIdentifier string) error
JoinRoomAsUser(roomIdentifier, userID string) error

// User Management
RegisterUser(localpart string) error
EnsureUserExists(matrixUserID string) error
SetDisplayName(userID, displayName string) error
SetAvatarURL(userID, avatarURL string) error

// Messaging
SendMessage(roomID, userID, message string) error
SendFormattedMessage(roomID, userID, plaintext, formatted string) error
UploadMedia(data []byte, filename, contentType string) (string, error)

// Room Operations
InviteUserToRoom(roomID, userID string) error
AddSpaceChild(spaceID, childRoomID string) error
AddRoomAlias(roomID, alias string) error
ResolveRoomAlias(alias string) (string, error)
```

**Features:**
- Rate limiting (configurable)
- Retry logic
- Error handling
- Connection pooling
- Request logging

### 3. Bridge Service (`server/sync_to_matrix.go`, `server/sync_from_matrix.go`)

Translates events between Mattermost and Matrix formats.

**Mattermost → Matrix (`sync_to_matrix.go`):**
```go
type MattermostToMatrixBridge struct {
    plugin       *Plugin
    matrixClient *matrix.Client
    logger       Logger
}

// Core sync methods
SyncMessageToMatrix(post *model.Post) error
SyncUserToMatrix(user *model.User) error
CreateOrGetGhostUser(mattermostUserID string) (string, error)
SyncUserProfile(user *model.User, matrixUserID string) error
```

**Matrix → Mattermost (`sync_from_matrix.go`):**
```go
type MatrixToMattermostBridge struct {
    plugin       *Plugin
    logger       Logger
}

// Core sync methods
HandleMatrixEvent(event *MatrixEvent) error
SyncMatrixMessage(event *MatrixEvent) error
EnsureRemoteUser(matrixUserID string) (string, error)
```

### 4. Application Service Server (`server/matrix/server.go`)

HTTP server that receives Matrix events and queries.

**Endpoints:**
```go
// Transaction endpoint - receives room events
PUT /_matrix/app/v1/transactions/{txnId}

// User query - validates ghost users
GET /_matrix/app/v1/users/{userID}

// Room query - validates room aliases
GET /_matrix/app/v1/rooms/{roomAlias}
```

**Transaction Processing:**
```go
type Transaction struct {
    Events []MatrixEvent `json:"events"`
}

type MatrixEvent struct {
    Type      string          `json:"type"`
    EventID   string          `json:"event_id"`
    RoomID    string          `json:"room_id"`
    Sender    string          `json:"sender"`
    Content   json.RawMessage `json:"content"`
    Timestamp int64           `json:"origin_server_ts"`
}
```

### 5. Storage Layer (`server/store/kvstore/`)

Manages persistent mappings in Mattermost's KV store.

**Key Functions:**
```go
type KVStore interface {
    Set(key string, value []byte) error
    Get(key string) ([]byte, error)
    Delete(key string) error
}

// Key builders
BuildChannelMappingKey(channelID string) string       // "channel_mapping_<id>"
BuildRoomMappingKey(roomID string) string             // "room_mapping_<id>"
BuildTeamMappingKey(teamID string) string             // "team_mapping_<id>"
BuildUserMappingKey(userID string) string             // "user_mapping_<id>"
BuildMatrixUserMappingKey(matrixID string) string     // "matrix_user_mapping_<id>"
BuildRemoteUserKey(matrixID string) string            // "remote_user_<id>"
```

### 6. Command Handler (`server/command/command.go`)

Processes slash commands and provides user interface.

**Structure:**
```go
type Handler struct {
    plugin    PluginAccessor
    client    *pluginapi.Client
    kvstore   kvstore.KVStore
    pluginAPI plugin.API
}

// Command execution methods
executeTestCommand(args *model.CommandArgs) *model.CommandResponse
executeCreateRoomCommand(args *model.CommandArgs, roomName string, publish bool) *model.CommandResponse
executeJoinCommand(args *model.CommandArgs, roomIdentifier string, createChannel bool) *model.CommandResponse
executeMapCommand(args *model.CommandArgs, roomIdentifier string) *model.CommandResponse
executeUnmapCommand(args *model.CommandArgs) *model.CommandResponse
executeListMappingsCommand(args *model.CommandArgs) *model.CommandResponse
executeMapUserCommand(args *model.CommandArgs, username, matrixUserID string) *model.CommandResponse
executeBackfillCommand(args *model.CommandArgs, mode string) *model.CommandResponse
executeMigrateCommand(args *model.CommandArgs) *model.CommandResponse
```

---

## Data Flow

### Message Flow: Mattermost → Matrix

```
1. User posts message in Mattermost
   │
   ▼
2. Hook: MessageHasBeenPosted(post)
   │
   ├─► Check if channel is mapped to room
   │   (lookup in KV store)
   │
   ├─► Get or create ghost user for poster
   │   (lookup/create Matrix user)
   │
   ├─► Translate post to Matrix event
   │   • Text → body/formatted_body
   │   • Attachments → media upload + message
   │   • Mentions → Matrix mentions
   │
   ▼
3. MatrixClient.SendMessage(roomID, ghostUserID, content)
   │
   ├─► Apply rate limiting
   │
   ├─► HTTP POST to Matrix CS API
   │   /_matrix/client/v3/rooms/{roomID}/send/m.room.message
   │
   ▼
4. Matrix homeserver processes event
   │
   ├─► Persists to database
   │
   ├─► Notifies room members
   │
   └─► Federates to other servers (if federated room)
```

### Message Flow: Matrix → Mattermost

```
1. User posts message in Matrix
   │
   ▼
2. Matrix homeserver processes event
   │
   ├─► Persists to database
   │
   ├─► Identifies bridge as Application Service
   │
   ▼
3. HTTP PUT to AS transaction endpoint
   /_matrix/app/v1/transactions/{txnId}
   │
   ▼
4. HandleMatrixTransaction(events)
   │
   ├─► For each event in transaction:
   │   │
   │   ├─► Check event type (m.room.message, m.room.member, etc.)
   │   │
   │   ├─► Lookup channel mapping for room
   │   │   (check KV store: room_mapping_<roomID>)
   │   │
   │   ├─► Get or create remote user for sender
   │   │   (Matrix user → Mattermost remote user)
   │   │
   │   ├─► Translate Matrix event to Mattermost post
   │   │   • body/formatted_body → Text
   │   │   • Media → File attachments
   │   │   • Mentions → @mentions
   │   │
   │   └─► Post to Mattermost channel as remote user
   │
   └─► Return 200 OK (acknowledge transaction)
```

### User Creation Flow

```
1. Mattermost user created
   │
   ▼
2. Hook: UserHasBeenCreated(user)
   │
   ├─► Generate Matrix user ID
   │   @mattermost_<username>:<server>
   │
   ├─► Register user via AS
   │   POST /_matrix/client/v3/register?kind=user_id
   │
   ├─► Set display name (user's Mattermost name)
   │   PUT /_matrix/client/v3/profile/{userID}/displayname
   │
   ├─► Upload and set avatar (if exists)
   │   POST /_matrix/media/v3/upload
   │   PUT /_matrix/client/v3/profile/{userID}/avatar_url
   │
   └─► Store mapping
       user_mapping_<mattermostID> → matrixUserID
```

### Channel/Room Creation Flow

```
1. Mattermost channel created
   │
   ▼
2. Hook: ChannelHasBeenCreated(channel)
   │
   ├─► Determine room properties
   │   • Public channel → Public room
   │   • Private channel → Private room
   │
   ├─► Generate room alias
   │   #_mattermost_<channel-name>:<server>
   │
   ├─► Create Matrix room
   │   POST /_matrix/client/v3/createRoom
   │   {
   │     "name": "<channel display name>",
   │     "topic": "<channel purpose>",
   │     "room_alias_name": "_mattermost_<name>",
   │     "preset": "public_chat" | "private_chat",
   │     "visibility": "public" | "private"
   │   }
   │
   ├─► Join AS bot to room
   │
   ├─► Store bidirectional mapping
   │   channel_mapping_<channelID> → roomID
   │   room_mapping_<roomID> → channelID
   │
   └─► Sync all channel members
       For each member:
         • Create/get ghost user
         • Join ghost user to room
```

---

## Storage Schema

### KV Store Keys

```
# Channel Mappings
channel_mapping_<channelID>          → roomID or alias
room_mapping_<roomID>                → channelID
room_mapping_<roomAlias>             → channelID

# Team Mappings
team_mapping_<teamID>                → spaceID

# User Mappings (Ghost Users)
user_mapping_<mattermostUserID>      → matrixUserID
matrix_user_mapping_<matrixUserID>   → mattermostUserID

# Remote Users (Matrix → Mattermost)
remote_user_<matrixUserID>           → mattermostRemoteUserID

# Custom User Mappings
user_mapping_<mattermostUserID>      → customMatrixUserID (override)
```

### Example Data

```yaml
# Channel to Room
channel_mapping_abc123:
  value: "#_mattermost_general:matrix.company.com"

room_mapping_#_mattermost_general:matrix.company.com:
  value: "abc123"

room_mapping_!xyz789:matrix.company.com:
  value: "abc123"

# Team to Space
team_mapping_team456:
  value: "!space123:matrix.company.com"

# User Mapping
user_mapping_user789:
  value: "@mattermost_alice:matrix.company.com"

matrix_user_mapping_@mattermost_alice:matrix.company.com:
  value: "user789"

# Remote User
remote_user_@bob:matrix.org:
  value: "remoteuser_abc"
```

---

## API Integration

### Mattermost Plugin API

**Hooks Used:**
```go
MessageHasBeenPosted(*plugin.Context, *model.Post)
UserHasBeenCreated(*plugin.Context, *model.User)
UserHasUpdated(*plugin.Context, *model.User)
ChannelHasBeenCreated(*plugin.Context, *model.Channel)
TeamHasBeenCreated(*plugin.Context, *model.Team)
UserHasJoinedChannel(*plugin.Context, *model.ChannelMember, *model.User)
```

**API Methods Used:**
```go
// User operations
API.GetUser(userID) (*model.User, *model.AppError)
API.GetUserByUsername(username) (*model.User, *model.AppError)
API.CreateUser(user) (*model.User, *model.AppError)

// Channel operations
API.GetChannel(channelID) (*model.Channel, *model.AppError)
API.GetChannelMembers(channelID, offset, limit) ([]*model.ChannelMember, *model.AppError)
API.CreateChannel(channel) (*model.Channel, *model.AppError)

// Post operations
API.CreatePost(post) (*model.Post, *model.AppError)
API.GetPost(postID) (*model.Post, *model.AppError)

// File operations
API.GetFile(fileID) ([]byte, *model.AppError)
API.UploadFile(data, channelID, filename) (*model.FileInfo, *model.AppError)

// KV Store
API.KVGet(key) ([]byte, *model.AppError)
API.KVSet(key, value) *model.AppError
API.KVDelete(key) *model.AppError
```

### Matrix Client-Server API

**Endpoints Used:**

**Room Operations:**
```
POST   /_matrix/client/v3/createRoom
POST   /_matrix/client/v3/join/{roomIdOrAlias}
PUT    /_matrix/client/v3/rooms/{roomId}/send/{eventType}/{txnId}
GET    /_matrix/client/v3/rooms/{roomId}/state/{eventType}/{stateKey}
PUT    /_matrix/client/v3/rooms/{roomId}/state/{eventType}/{stateKey}
POST   /_matrix/client/v3/rooms/{roomId}/invite
PUT    /_matrix/client/v3/directory/room/{roomAlias}
GET    /_matrix/client/v3/directory/room/{roomAlias}
```

**User Operations:**
```
POST   /_matrix/client/v3/register
PUT    /_matrix/client/v3/profile/{userId}/displayname
PUT    /_matrix/client/v3/profile/{userId}/avatar_url
GET    /_matrix/client/v3/profile/{userId}
```

**Media Operations:**
```
POST   /_matrix/media/v3/upload
GET    /_matrix/media/v3/download/{serverName}/{mediaId}
GET    /_matrix/media/v3/thumbnail/{serverName}/{mediaId}
```

**Server Operations:**
```
GET    /_matrix/client/versions
GET    /_matrix/client/v3/capabilities
```

### Matrix Application Service API

**Endpoints Implemented:**

```
PUT /_matrix/app/v1/transactions/{txnId}
  • Receives events from homeserver
  • Processes m.room.message, m.room.member, etc.
  • Returns 200 to acknowledge

GET /_matrix/app/v1/users/{userID}
  • Validates ghost user existence
  • Returns 200 if bridge manages this user
  • Returns 404 otherwise

GET /_matrix/app/v1/rooms/{roomAlias}
  • Validates room alias
  • Returns 200 if bridge manages this alias
  • Returns 404 otherwise
```

**Registration File:**
```yaml
id: mattermost_bridge
url: http://mattermost:8065/_matrix/app/v1
as_token: <APPLICATION_SERVICE_TOKEN>
hs_token: <HOMESERVER_TOKEN>
sender_localpart: mattermost_bot
namespaces:
  users:
    - exclusive: true
      regex: "@mattermost_.*"
  aliases:
    - exclusive: true
      regex: "#_mattermost_.*"
rate_limited: false
```

---

## Security Model

### Authentication

**AS Token (Application Service Token):**
- Used by bridge to authenticate to Matrix
- Included in Authorization header: `Bearer <AS_TOKEN>`
- Allows user impersonation (ghost users)
- Must be kept secret

**HS Token (Homeserver Token):**
- Used by Matrix to authenticate to bridge
- Verified on incoming AS requests
- Included in query parameter: `?access_token=<HS_TOKEN>`
- Must be kept secret

### User Namespaces

Bridge has exclusive control over:
- Users matching `@mattermost_.*`
- Room aliases matching `#_mattermost_.*`

No other AS or users can create identities in these namespaces.

### Permissions

**Ghost Users:**
- Created and controlled by bridge
- Cannot be used by real users
- Automatically joined to rooms
- Display names/avatars managed by bridge

**Room Permissions:**
- Bridge bot has admin privileges in created rooms
- Ghost users are regular members
- Room power levels are configurable

### Data Privacy

**Message Content:**
- All messages stored on both platforms
- Matrix federation exposes messages to other servers
- Private channels → Private Matrix rooms (invite-only)
- Public channels → Public Matrix rooms (joinable)

**User Data:**
- Ghost users mirror Mattermost profiles
- Avatar images uploaded to Matrix
- Display names synced from Mattermost

---

## Performance Considerations

### Rate Limiting

**Client-Side (Bridge → Matrix):**
```go
type RateLimitConfig struct {
    Enabled            bool
    RoomCreationRate   float64  // rooms per second (0 = unlimited)
    RoomCreationBurst  int      // burst capacity
    MessageRate        float64  // messages per second
    MessageBurst       int      // burst capacity
}
```

**Server-Side (Matrix → Bridge):**
- Matrix homeserver enforces its own limits
- Bridge must respect backpressure
- Transaction processing should be fast

### Caching

**Current Implementation:**
- No explicit caching layer
- KV store accessed directly for mappings
- Matrix client may cache connection

**Potential Optimizations:**
- In-memory cache for mappings
- User profile cache
- Room metadata cache

### Scalability

**Bottlenecks:**
1. **KV Store Access** - Every message requires mapping lookup
2. **Matrix API Calls** - Network latency for each operation
3. **Ghost User Creation** - First message from new user creates user
4. **Media Upload** - Large files take time to transfer

**Mitigation Strategies:**
1. Caching layer for mappings
2. Connection pooling
3. Batch operations where possible
4. Async processing for non-critical operations

### Resource Usage

**Memory:**
- Plugin runs in Mattermost process
- Minimal memory overhead
- Ghost user mappings in KV store
- HTTP client connections

**CPU:**
- Event processing is CPU-light
- JSON parsing for Matrix events
- Message translation logic

**Network:**
- HTTP requests to Matrix homeserver
- Webhook delivery from Matrix
- Media upload/download bandwidth

---

## Monitoring & Observability

### Logging

**Log Levels:**
```go
logger.LogDebug()   // Detailed operation info
logger.LogInfo()    // Important events
logger.LogWarn()    // Recoverable errors
logger.LogError()   // Critical failures
```

**Key Events Logged:**
- Room/user creation
- Message sync operations
- Mapping updates
- API errors
- Rate limit hits

### Metrics (Potential)

Possible metrics to track:
- Messages synced per direction
- Ghost users created
- Rooms/channels mapped
- API call latency
- Error rates
- Rate limit hits

### Health Checks

`/matrix test` command provides basic health check:
- Matrix server reachability
- Authentication validity
- Basic API functionality

---

## Future Enhancements

### Planned Features
- Message edit/delete sync
- Reaction bridging
- Typing indicators
- Read receipts
- Threaded conversations
- Presence sync

### Technical Improvements
- Caching layer
- Metrics/observability
- Connection pooling
- Batch operations
- Event queuing
- Retry mechanisms

---

## See Also

- [Bridge Overview](BRIDGE_OVERVIEW.md)
- [Commands Reference](COMMANDS.md)
- [Local Development](../LOCAL_DEVELOPMENT.md)
- [Matrix Specification](https://spec.matrix.org/)
- [Mattermost Plugin Docs](https://developers.mattermost.com/extend/plugins/)
