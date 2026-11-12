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
- [Decisions](#decisions)

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
ChannelHasBeenCreated(c *plugin.Context, channel *model.Channel)
TeamHasBeenCreated(c *plugin.Context, team *model.Team)
UserHasJoinedChannel(c *plugin.Context, channelMember *model.ChannelMember, actor *model.User)
```

**Dependencies:**
- Matrix Client
- KV Store
- Plugin API
- Bridge Services

### 2. Matrix Client (`server/matrix/`)

The client is split by concern: `client.go` (types, constructor, request helpers), `rooms.go`, `accounts.go` (ghost and registered users, profiles), `events.go` (sending and reading events), `media.go`, `server_info.go`.

Handles all communication with the Matrix homeserver via Client-Server API.

**Key Methods:**
```go
// Room Management
CreateRoom(name, topic, serverDomain string, isPublic bool, channelID string, skipPrefix bool, teamName string) (string, error)
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

### 3. Bridge Service (`server/sync_to_matrix*.go`, `server/sync_to_mattermost*.go`)

Each direction is split the same way: the main file holds the post flow, and `_accounts`/`_users`, `_reactions`, `_mentions`, `_compare`, `_members`, `_lookup` and `_convert` files hold the rest.

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

### 6. Command Handler (`server/command/`)

`command.go` dispatches and checks permissions; `types.go` holds the interfaces and report types, and the commands live in `room_commands.go`, `admin_commands.go`, `account_commands.go` and `history_command.go`.

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

## Decisions

Short records of choices that shape the code. Newer entries replace older ones.

### Two delivery paths: hooks are the supported path (#64)

Mattermost → Matrix has two implementations: the plugin hooks
(`MessageHasBeenPosted`, `MessageHasBeenUpdated`, reaction hooks) and Shared
Channels (`OnSharedChannelsSyncMsg` and related), which a licensed server uses
for channels shared through Connected Workspaces.

**Decision.** The hooks path is the supported, tested delivery path. Every
feature (outbox and retries, history, encryption, direct messages, cut-over,
reply fallbacks) is built and verified there. The Shared Channels handlers stay
so existing deployments keep working, but they are **not covered by automated
tests**, because CI has no license, and new features are not required to support
them. `/matrix doctor` flags a shared channel ("Delivery path") so operators
know which path it takes.

**Why not remove it.** Ghost users and posts the bridge created carry the
remote ID from the registration, and removing the registration would orphan
them. **Revisit** when a licensed end-to-end job exists (the harness already
reads `E2E_MM_LICENSE`): then either repeat the core message tests with
channels shared, or remove the handlers.

### Storage: the key-value store, with indices where scans hurt (#62)

Everything the bridge remembers is in the plugin key-value store.

**Decision.** Stay on the key-value store for now. Plugin-owned SQL tables
would give indexed lookups and transactions, but they tie the plugin to the
server's database driver (PostgreSQL and MySQL differ), need their own
migration and backup story, and are not a supported surface for plugins. The
cost today is acceptable: the hot paths (a post, an event) read single keys;
the scans (`ghostRooms`, `bridgedDirectChannels`, `teamMappedChannels`, status
counts) run on commands and background passes, not per message.

**Mitigations already in place.** The inbound journal and outbox
(`docs/OPERATIONS.md`) make writes durable and ordered without a database; the
reconciliation pass is bounded to twenty channels per run.

**Revisit when** a deployment has more than a few thousand bridged channels
and the scans show up in `/matrix doctor` timings, or when encryption must run
on more than one node. The first step then is a secondary index per account
(one key listing its rooms) and incremental encryption persistence; SQL, using
mautrix-go's SQL crypto store, is the step after that. The key-value
migrations in `server/migrations.go` are the migration path.

### Two Matrix clients: keep the split, with a documented boundary (#76)

The plugin has a hand-written Matrix client (`server/matrix/`: requests,
retry, rate limiting, media, room creation, application-service masquerading)
and links mautrix-go for end-to-end encryption (`server/e2ee`).

**Decision.** Keep both, and keep the boundary where it is:

- `server/matrix` is the only client the bridge's features use. It is built
  around the appservice's needs (acting as any ghost with `user_id`, `ts`
  timestamps, per-user rate limiting, the plugin's retry and outbox
  semantics), which mautrix's `Client` does not offer in this shape.
- mautrix-go is used for what it is best at and what is hardest to write
  correctly: Olm/Megolm sessions, device keys and their storage. Nothing else
  imports it, and `server/encryption.go` is the single place that converts
  between the two clients' event types.

**Why not move everything onto mautrix's client.** It would trade a stable,
fully tested client for a rewrite of every call site (and its tests), to gain
typed events we do not currently need. The costs of the split are real but
small and contained to one file.

**Revisit when** a feature needs something mautrix gives for free and the
hand-written client lacks (for example sliding sync or a new room version
handling): move that area onto mautrix then, not the whole client.

### Integration images stay links, not files (#72)

An integration's attachment can carry an `image_url` (a chart, a build badge).
The bridge writes that address into the message text; it does not download the
image and upload it to Matrix as a file.

**Decision.** Do not fetch it. The address is chosen by whoever configures the
integration, and fetching it would make the Mattermost server request any URL
on their behalf, including addresses inside the organisation's network (a
server-side request forgery path). A person's own uploaded files are different:
they live in Mattermost and are copied as files already.

**Revisit when** an administrator-set allow-list of hosts exists to fetch from,
or Mattermost offers a safe proxy for external images that the plugin can call.

## Future Enhancements

### Features (Implementation Status)

#### ✅ Implemented
- **Message edits, deletions, reactions and threads** in both directions through the
  `MessageHasBeenUpdated`, `MessageHasBeenDeleted`/Shared Channels and `ReactionHasBeen*` hooks
  and the appservice transaction handler.
- **Membership**: `UserHasJoinedChannel`/`UserHasLeftChannel` move ghosts in and out of rooms;
  `m.room.member` events add and remove remote users in channels.
- **Ephemeral events** (`m.typing`, `m.presence`) are accepted when the registration sets
  `receive_ephemeral` and are republished as Mattermost typing events and status updates.
- **Room state**: `m.room.name` and `m.room.topic` update the channel display name and header.
- **Appservice API**: `/transactions`, `/users` (lazy ghost provisioning), `/rooms` and `/ping`,
  on both the `/_matrix/app/v1` and legacy paths, with 401/403 semantics from the spec.

#### 📋 Planned
- Read receipts (no Mattermost plugin API to mark channels read for another user)
- Typing indicators Mattermost → Matrix (no plugin hook exposes typing)
- End-to-end encryption (E2EE) - requires significant work, see below

#### ❌ Not Feasible
- **E2EE Support** - Fundamentally incompatible with bridge architecture
  - Would require Olm/Megolm crypto implementation
  - Messages must be decrypted at bridge (defeats E2EE purpose)
  - Stored unencrypted in Mattermost
  - Security/compliance concerns
  - **Recommendation**: Don't bridge E2EE rooms, use unencrypted rooms for bridging

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
