# Mattermost-Matrix Bridge Overview

The Mattermost-Matrix Bridge is a plugin that enables bidirectional communication between Mattermost and Matrix (Synapse, Dendrite, etc.). It allows teams to bridge channels to Matrix rooms, enabling cross-platform collaboration and federation.

## What is This Bridge?

This bridge connects Mattermost (an open-source team collaboration platform) with Matrix (a decentralized, federated communication protocol). Users on either platform can communicate seamlessly as if they were on the same platform.

### Key Features

- **Bidirectional Message Sync**: Messages flow in both directions between Mattermost and Matrix
- **User Puppeting**: Mattermost users appear as "ghost" users in Matrix with proper names and avatars
- **Automatic Room/Channel Creation**: New channels automatically create Matrix rooms
- **Team Spaces**: Mattermost teams map to Matrix Spaces for organization
- **Join Existing Rooms**: Connect to existing Matrix communities and federated rooms
- **Backfill Support**: Sync existing data to Matrix
- **File/Media Sharing**: Attachments and media are bridged between platforms
- **User Profile Sync**: Display names and avatars are synchronized

## Architecture

### How It Works

The bridge operates as both:
1. **Mattermost Plugin**: Hooks into Mattermost events (messages, user creation, channel creation)
2. **Matrix Application Service**: Registers with Matrix homeserver to receive events and manage ghost users

```
┌─────────────────────────────────────────────────────────────┐
│                      Mattermost Server                      │
│  ┌───────────────────────────────────────────────────────┐  │
│  │            Matrix Bridge Plugin                       │  │
│  │  ┌─────────────────────┐  ┌──────────────────────┐   │  │
│  │  │   Plugin Hooks      │  │   AS HTTP Server     │   │  │
│  │  │  - MessagePosted    │  │  /_matrix/app/v1/*   │   │  │
│  │  │  - UserCreated      │  │                      │   │  │
│  │  │  - ChannelCreated   │  │  (Receives Matrix    │   │  │
│  │  │  - TeamCreated      │  │   events)            │   │  │
│  │  └─────────────────────┘  └──────────────────────┘   │  │
│  │                                                        │  │
│  │  ┌─────────────────────────────────────────────────┐  │  │
│  │  │         Matrix Client (Client-Server API)       │  │  │
│  │  │  - Create/Join Rooms                            │  │  │
│  │  │  - Send Messages                                │  │  │
│  │  │  - Manage Users                                 │  │  │
│  │  │  - Create Spaces                                │  │  │
│  │  └─────────────────────────────────────────────────┘  │  │
│  └───────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
                              ↕
                    Matrix Client-Server API
                    Application Service API
                              ↕
┌─────────────────────────────────────────────────────────────┐
│                    Matrix Homeserver                        │
│                   (Synapse, Dendrite, etc.)                 │
│                                                             │
│  ┌──────────────┐  ┌──────────────┐  ┌─────────────────┐  │
│  │   Rooms      │  │   Spaces     │  │  Ghost Users    │  │
│  │              │  │              │  │  @mattermost_*  │  │
│  └──────────────┘  └──────────────┘  └─────────────────┘  │
└─────────────────────────────────────────────────────────────┘
                              ↕
                       Matrix Federation
                              ↕
                    ┌──────────────────┐
                    │  Other Matrix    │
                    │  Servers         │
                    └──────────────────┘
```

### Component Overview

#### 1. Plugin Hooks (Mattermost → Matrix)
The bridge listens to Mattermost events and forwards them to Matrix:

- **`MessageHasBeenPosted`**: Sends Mattermost messages to Matrix rooms
- **`UserHasBeenCreated`**: Creates corresponding Matrix ghost users
- **`UserHasUpdated`**: Syncs profile changes (name, avatar) to Matrix
- **`ChannelHasBeenCreated`**: Creates Matrix rooms for new channels
- **`TeamHasBeenCreated`**: Creates Matrix Spaces for teams
- **`UserHasJoinedChannel`**: Joins ghost users to Matrix rooms

#### 2. Application Service (Matrix → Mattermost)
The bridge runs an HTTP server that receives Matrix events:

- **Transaction Endpoint** (`/_matrix/app/v1/transactions/{txnId}`): Receives room events
- **User Query** (`/_matrix/app/v1/users/{userID}`): Validates ghost users
- **Room Query** (`/_matrix/app/v1/rooms/{roomAlias}`): Validates room aliases

#### 3. Matrix Client
The bridge uses the Matrix Client-Server API to:

- Create and manage rooms
- Send messages
- Register and manage ghost users
- Create spaces and manage space hierarchy
- Upload media
- Manage room aliases

#### 4. Storage (KV Store)
Bidirectional mappings are stored in Mattermost's KV store:

- `channel_mapping_<channelID>` → `roomID`
- `room_mapping_<roomID>` → `channelID`
- `team_mapping_<teamID>` → `spaceID`
- `user_mapping_<mattermostUserID>` → `matrixUserID`

## Data Flow

### Mattermost → Matrix

1. User posts message in Mattermost channel
2. Plugin hook `MessageHasBeenPosted` is triggered
3. Bridge looks up mapped Matrix room
4. Bridge creates/gets ghost user for the Mattermost user
5. Bridge sends message to Matrix room as the ghost user
6. Matrix distributes message to all room members (including federated servers)

### Matrix → Mattermost

1. User posts message in Matrix room
2. Matrix homeserver forwards event to bridge's AS endpoint
3. Bridge receives transaction with room event
4. Bridge looks up mapped Mattermost channel
5. Bridge creates remote user in Mattermost (if needed)
6. Bridge posts message to Mattermost channel as the remote user

## Ghost Users

"Ghost users" are virtual Matrix users that represent Mattermost users. Each Mattermost user gets a corresponding Matrix user ID:

- Format: `@mattermost_<username>:<server.domain>`
- Example: `@mattermost_alice:matrix.company.com`
- Display names and avatars are synced from Mattermost
- Managed by the Application Service (bridge controls them)

## Matrix Spaces

Mattermost teams are represented as Matrix Spaces:

- **Space**: A special Matrix room that contains other rooms
- **Hierarchy**: Channels within a team appear as child rooms in the space
- **Discovery**: Users can browse the space to see all bridged channels
- **Organization**: Mirrors Mattermost's team structure in Matrix

## Federation

The bridge enables Mattermost to participate in Matrix's federated ecosystem:

```
Mattermost Server A
        ↕
  Matrix Server A (Bridge)
        ↕
Matrix Federation Protocol
        ↕
  Matrix Server B
        ↕
Element, FluffyChat, or other Matrix client
```

Users on completely different Matrix servers can communicate with your Mattermost team.

## Authentication & Security

### Application Service Authentication

- **AS Token**: Bridge authenticates to Matrix using this token
- **HS Token**: Matrix authenticates to the bridge using this token
- **User Impersonation**: AS token allows creating and controlling ghost users

### User Namespace

The bridge registers a namespace for ghost users:
```yaml
namespaces:
  users:
    - exclusive: true
      regex: "@mattermost_.*"
```

This gives the bridge exclusive control over all users matching `@mattermost_*`.

### Room Namespace

The bridge registers a namespace for room aliases:
```yaml
namespaces:
  aliases:
    - exclusive: true
      regex: "#_mattermost_.*"
```

This allows the bridge to create room aliases like `#_mattermost_general:server.com`.

## Performance & Rate Limiting

The bridge includes built-in rate limiting to prevent overwhelming the Matrix server:

- **Room Creation**: Configurable rate limit (default: 6.6s between rooms)
- **Message Sending**: Configurable burst and sustained rate
- **User Registration**: Rate limited to prevent spam
- **Join Operations**: Rate limited to prevent abuse

Configuration in plugin settings:
```json
{
  "RateLimitConfig": {
    "Enabled": true,
    "RoomCreationRate": 0,
    "RoomCreationBurst": 0,
    "MessageRate": 9.99,
    "MessageBurst": 15
  }
}
```

## Storage & Mappings

All mappings are stored in Mattermost's KV store for persistence:

### Channel Mappings
```
channel_mapping_<channelID> → roomID or alias
room_mapping_<roomID> → channelID
room_mapping_<roomAlias> → channelID
```

### Team Mappings
```
team_mapping_<teamID> → spaceID
```

### User Mappings
```
user_mapping_<mattermostUserID> → matrixUserID
matrix_user_mapping_<matrixUserID> → mattermostUserID
```

### Remote Users
```
remote_user_<matrixUserID> → mattermostUserID
```

## Supported Features

### ✅ Implemented

- [x] Bidirectional message sync
- [x] User puppeting (ghost users)
- [x] Profile sync (names, avatars)
- [x] Channel/Room creation and mapping
- [x] Team/Space hierarchy
- [x] Join existing Matrix rooms
- [x] File and media attachments
- [x] Custom user mapping
- [x] Backfill existing data
- [x] Public and private channels/rooms
- [x] Matrix federation support
- [x] Rate limiting
- [x] Room aliases

### 🚧 Planned/Partial

- [ ] Reactions/Emojis
- [ ] Message edits
- [ ] Message deletion sync
- [ ] Typing indicators
- [ ] Read receipts
- [ ] Presence/Status sync
- [ ] Threaded conversations
- [ ] End-to-end encryption (E2EE)
- [ ] Voice/Video bridging

## Use Cases

### 1. Internal Team Communication
Bridge your Mattermost teams to Matrix for redundancy or migration:
```
/matrix create
```

### 2. External Collaboration
Bridge specific channels to Matrix for collaboration with external partners:
```
/matrix create "Partner Collaboration" publish=true
```

### 3. Federated Communities
Join existing Matrix communities:
```
/matrix join #opensource:matrix.org create_channel=true
```

### 4. Cross-Organization Communication
Enable teams on different Mattermost instances to communicate via Matrix federation:
```
Mattermost A → Matrix Server A ⟷ Matrix Federation ⟷ Matrix Server B → Mattermost B
```

### 5. Migration Path
Use as a transition tool when migrating from Mattermost to Matrix or vice versa.

## Limitations & Known Issues

### Current Limitations

1. **No E2EE Support**: End-to-end encrypted rooms are not supported
2. **No Edit/Delete Sync**: Message edits and deletions don't sync (yet)
3. **Single Homeserver**: Bridge connects to one Matrix homeserver
4. **No Reactions**: Emoji reactions don't bridge (yet)
5. **No Threading**: Threaded conversations appear as flat messages

### Performance Considerations

1. **Rate Limiting**: Large teams may hit rate limits when backfilling
2. **Ghost User Creation**: Each Mattermost user creates a Matrix user
3. **File Storage**: Media is stored on both platforms
4. **Message Volume**: High-traffic channels may experience delays

## Getting Started

See the following guides:

- **[Setup Guide](../LOCAL_DEVELOPMENT.md)**: Install and configure the bridge
- **[Commands Reference](COMMANDS.md)**: Complete list of slash commands
- **[Join Command Guide](JOIN_COMMAND.md)**: Join existing Matrix rooms

## Troubleshooting

### Common Issues

**Messages Not Syncing**
- Check that channel is shared (plugin needs access)
- Verify room mapping exists: `/matrix list`
- Check plugin logs for errors

**Matrix Connection Failed**
- Verify Matrix server URL in settings
- Check AS token and HS token match registration file
- Ensure registration file is loaded by homeserver

**Ghost Users Not Created**
- Check Application Service is registered correctly
- Verify user namespace in registration file
- Check Matrix homeserver logs

## Support & Contributing

- **Issues**: [GitHub Issues](https://github.com/mattermost/mattermost-plugin-matrix-bridge/issues)
- **Documentation**: [docs/](.)
- **Development**: See [LOCAL_DEVELOPMENT.md](../LOCAL_DEVELOPMENT.md)

## Related Documentation

- [Commands Reference](COMMANDS.md) - All slash commands
- [Join Command](JOIN_COMMAND.md) - Detailed join command guide
- [Local Development](../LOCAL_DEVELOPMENT.md) - Setup and development guide
- [Matrix Spec](https://spec.matrix.org/) - Matrix protocol specification
- [Mattermost Plugin API](https://developers.mattermost.com/extend/plugins/) - Plugin development
