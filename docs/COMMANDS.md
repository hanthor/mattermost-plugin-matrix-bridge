# Matrix Bridge Commands Reference

Complete reference for all `/matrix` slash commands available in the Mattermost-Matrix Bridge plugin.

## Command Overview

| Command | Description | Example |
|---------|-------------|---------|
| [`/matrix test`](#matrix-test) | Test Matrix connection | `/matrix test` |
| [`/matrix create`](#matrix-create) | Create new Matrix room | `/matrix create "My Room"` |
| [`/matrix join`](#matrix-join) | Join existing Matrix room | `/matrix join #room:server.com` |
| [`/matrix map`](#matrix-map) | Map channel to Matrix room | `/matrix map #room:server.com` |
| [`/matrix unmap`](#matrix-unmap) | Remove channel mapping | `/matrix unmap` |
| [`/matrix list`](#matrix-list) | List all mappings | `/matrix list` |
| [`/matrix map_user`](#matrix-map_user) | Map user to Matrix ID | `/matrix map_user @alice @alice:matrix.org` |
| [`/matrix backfill`](#matrix-backfill) | Backfill existing data | `/matrix backfill all` |
| [`/matrix history`](#matrix-history) | Import a channel's past messages | `/matrix history confirm` |
| [`/matrix status`](#matrix-status) | Check bridge status | `/matrix status` |
| [`/matrix migrate-team`](#matrix-migrate-team) | Cut a team over to Matrix: report, read-only channels, archive | `/matrix migrate-team report` |
| [`/matrix migrate`](#matrix-migrate) | Re-run KV migrations | `/matrix migrate` |
| [`/matrix call`](#calls-matrix-call) | Start a call both sides can join | `/matrix call Weekly sync` |
| [`/matrix claim`](#mirror-mode-matrix-claim) | Mirror mode: take over your Matrix account | `/matrix claim` |

---

## Command Details

### `/matrix test`

Tests the connection to the Matrix homeserver and validates the bridge configuration.

**Usage:**
```
/matrix test
```

**What it checks:**
- Matrix server URL is accessible
- Application Service token is valid
- Bridge bot can authenticate
- Basic API functionality

**Example Output:**
```
✅ Matrix Bridge Test Results

Configuration:
• Matrix Server: http://synapse:8008
• Bridge Status: Connected
• AS Token: Configured ✓

Connection Test:
• Homeserver reachable: ✓
• API version check: v1.11 ✓
• Authentication: Success ✓

📋 Next Steps:
   • Use `/matrix create "Room Name"` to create a Matrix room
   • The channel will be automatically configured for syncing
```

**Troubleshooting:**
- If test fails, check System Console → Plugins → Matrix Bridge settings
- Verify Matrix Server URL uses correct Docker service name if running in containers
- Ensure AS Token and HS Token match the registration file

---

### `/matrix create`

Creates a new Matrix room and maps it to the current Mattermost channel.

**Usage:**
```
/matrix create [room_name] [publish=true|false]
```

**Parameters:**
- `room_name` (optional): Name for the Matrix room. Defaults to channel name if not provided.
- `publish` (optional): Whether to publish room to Matrix public directory. Defaults to `false`.

**Examples:**

Create room with channel name:
```
/matrix create
```

Create room with custom name:
```
/matrix create "Engineering Discussion"
```

Create and publish to public directory:
```
/matrix create "Public Support" publish=true
```

**What happens:**
1. Creates a new Matrix room with the specified name
2. Automatically maps the room to the current channel
3. Joins the bridge bot to the room
4. Joins your ghost user to the room
5. Syncs all channel members to the room (creates ghost users)
6. Optionally publishes to Matrix public directory

**Room Properties:**
- **Public channels** → Public Matrix rooms (visible in directory if published)
- **Private channels** → Private Matrix rooms (invite-only)
- **Room alias**: `#_mattermost_<channel-name>:<server>`
- **Bridge alias**: `#mattermost-bridge-<channel-name>:<server>`

**Permissions:**
- Requires permission to post in the channel
- Requires Matrix bridge to be configured

---

### `/matrix join`

Joins an existing Matrix room with the bridge bot. Optionally creates a Mattermost channel and maps it automatically.

**Usage:**
```
/matrix join [room_alias|room_id] [create_channel=true|false]
```

**Parameters:**
- `room_alias|room_id` (required): Matrix room identifier
  - Room alias: `#room:server.com` (preferred, human-readable)
  - Room ID: `!abc123:server.com` (permanent ID)
- `create_channel` (optional): Auto-create and map a Mattermost channel. Defaults to `false`.

**Examples:**

Join room (then manually map):
```
/matrix join #rust:matrix.org
```

Join and auto-create bridged channel:
```
/matrix join #rust:matrix.org create_channel=true
```

Join by room ID:
```
/matrix join !abc123:matrix.org
```

**What happens:**
1. Bridge bot joins the Matrix room
2. Your ghost user joins the room (for immediate messaging)
3. If `create_channel=true`:
   - Creates a new Mattermost channel (named after the room)
   - Maps the channel to the Matrix room
   - Adds you to the new channel

**Requirements:**
- The Matrix room must be **public**, OR
- The bridge bot must be **invited** to private rooms first

**Use Cases:**
- Join public Matrix communities
- Connect to federated rooms on other Matrix servers
- Bridge to rooms created by other Matrix users
- Participate in cross-organization Matrix spaces


---

### `/matrix map`

Maps the current Mattermost channel to an existing Matrix room. Use this when you want to bridge an existing channel to an existing room without creating a new one.

**Usage:**
```
/matrix map [room_alias|room_id]
```

**Parameters:**
- `room_alias|room_id` (required): Matrix room identifier
  - Room alias: `#room:server.com` (preferred)
  - Room ID: `!abc123:server.com`

**Examples:**

Map to room alias:
```
/matrix map #general:matrix.company.com
```

Map to room ID:
```
/matrix map !xyz789:matrix.company.com
```

**What happens:**
1. Attempts to join the Matrix room with the bridge bot
2. Joins your ghost user to the room
3. Creates bidirectional mapping (channel ↔ room)
4. Syncs all channel members to the Matrix room
5. Messages now sync in both directions

**Requirements:**
- Must be run from the channel you want to map
- Room must exist and be joinable
- Only one room can be mapped per channel

**Difference from `/matrix create`:**
- `create` makes a NEW Matrix room
- `map` connects to an EXISTING room
- `join` joins a room (then you map it later, or use `create_channel=true`)

---

### `/matrix unmap`

Removes the mapping between the current channel and its Matrix room. Stops message synchronization.

**Usage:**
```
/matrix unmap
```

**What happens:**
1. Removes channel → room mapping
2. Removes room → channel mapping
3. Stops bidirectional message sync
4. Uninvites the plugin from the shared channel (if applicable)

**Note:**
- Does NOT delete the Matrix room
- Does NOT remove ghost users from the room
- Does NOT delete the Mattermost channel
- Only stops the synchronization

**To resume syncing:** Use `/matrix map [room]` again

---

### `/matrix list`

Lists all channel-to-room mappings in the workspace.

**Usage:**
```
/matrix list
```

**Example Output:**
```
**Matrix Bridge Mappings**

Current Channel:
• general → `#_mattermost_general:synapse-mydomain.com` ✅

All Mappings (3 total):
• General → `#_mattermost_general:synapse-mydomain.com`
• Engineering → `#_mattermost_engineering:synapse-mydomain.com`
• Support → `#support:matrix.company.com` *(current)*

Commands:
• `/matrix map [room_alias|room_id]` - Map current channel to Matrix room
• `/matrix create` - Create new Matrix room using channel name
• `/matrix create [room_name]` - Create new Matrix room with custom name
• `/matrix status` - Check bridge status
```

**Use Cases:**
- Verify which channels are bridged
- Find the room ID for a specific channel
- Audit all bridge connections

---

### `/matrix map_user`

Manually maps a Mattermost user to a specific Matrix user ID. Useful for admins who want to use their existing Matrix accounts instead of ghost users.

**Usage:**
```
/matrix map_user [mattermost_username] [matrix_user_id]
```

**Parameters:**
- `mattermost_username` (required): Mattermost username (with or without @)
- `matrix_user_id` (required): Full Matrix user ID (must include server)

**Examples:**

Map admin to existing Matrix account:
```
/matrix map_user @alice @alice:matrix.company.com
```

Map without @ prefix:
```
/matrix map_user bob @bob:matrix.org
```

**What happens:**
1. Validates the Matrix user ID format
2. Gets the Mattermost user by username
3. Creates mapping: `mattermostUserID` → `matrixUserID`
4. Creates reverse mapping: `matrixUserID` → `mattermostUserID`
5. Future messages from this Mattermost user will appear from the specified Matrix account

**Requirements:**
- Must be system admin or have appropriate permissions
- Matrix user ID must be in valid format: `@user:server.com`
- Mattermost user must exist

**Use Cases:**
- Admins who already have Matrix accounts
- VIP users who want consistent identity
- Testing and development
- Users who want to use their real Matrix account instead of ghost user

**Important:**
- This overrides the default ghost user mapping
- The user must already exist on the Matrix server
- The bridge cannot control this user's profile (no avatar/name sync)

---

### `/matrix backfill`

Backfills existing Mattermost data to Matrix. Creates Matrix resources (users, spaces, rooms) for data that existed before the bridge was installed.

**Usage:**
```
/matrix backfill [mode]
```

**Modes:**
- `users` - Create ghost users for all existing Mattermost users
- `teams` - Create Matrix spaces for all existing teams
- `channels` - Create Matrix rooms for all existing channels
- `all` - Backfill everything (users + teams + channels)

**Examples:**

Backfill all data:
```
/matrix backfill all
```

Backfill only users:
```
/matrix backfill users
```

Backfill teams:
```
/matrix backfill teams
```

**What happens:**

**Users Mode:**
- Iterates through all Mattermost users
- Creates Matrix ghost user for each
- Syncs profile data (name, avatar)

**Teams Mode:**
- Iterates through all Mattermost teams
- Creates Matrix space for each team
- Stores team → space mapping

**Channels Mode:**
- Iterates through all public channels
- Creates Matrix room for each channel
- Maps channel ↔ room
- Syncs channel members to room

**All Mode:**
- Runs users, then teams, then channels in sequence

**Performance Notes:**
- Large instances may take considerable time
- Rate limiting applies (6-7 seconds per room)
- Runs asynchronously - check server logs for progress
- Consider running during off-peak hours

**Example:**
```
🔄 Backfill started for: all. This may take a while. Check server logs for progress.
```

**Use Cases:**
- Initial bridge setup for existing Mattermost instance
- Migrating to Matrix while preserving history
- Re-creating Matrix rooms after homeserver issues

---

### `/matrix status`

Displays the current status of the Matrix bridge.

**Usage:**
```
/matrix status
```

**Example Output:**
```
Matrix Bridge Status:
- Plugin: Active
- Configuration: Check System Console → Plugins → Matrix Bridge
- Logs: Check plugin logs for connection status
```

**What it shows:**
- Plugin activation status
- Quick health check
- Where to find more detailed information

**For detailed diagnostics, use:** `/matrix test`

---

### `/matrix migrate`

Re-runs KV store migrations to fix missing or corrupted mappings. This can help recover from storage issues or bridge configuration problems.

**Usage:**
```
/matrix migrate
```

**What happens:**
1. Scans all existing mappings
2. Identifies missing reverse mappings
3. Creates any missing bidirectional links
4. Reports number of mappings created/fixed

**Example Output:**
```
✅ Migration completed successfully!

Results:
• User mappings created: 15
• Channel mappings created: 8
• Room mappings created: 8
• DM mappings created: 3
• Reverse DM mappings created: 3

Total: 37 mappings processed
```

**When to use:**
- After upgrading the plugin
- If mappings seem inconsistent
- If rooms aren't syncing properly
- After recovering from storage issues
- If `/matrix list` shows incomplete mappings

**Safe to run multiple times** - it only creates missing mappings, doesn't modify existing ones.

---

## Command Comparison

### Creating vs. Joining vs. Mapping

| Scenario | Command to Use |
|----------|----------------|
| Create a NEW Matrix room for this channel | `/matrix create` |
| Join an EXISTING Matrix room (explore first) | `/matrix join #room:server.com` |
| Join a room AND create a Mattermost channel for it | `/matrix join #room:server.com create_channel=true` |
| Connect existing channel to existing room | `/matrix map #room:server.com` |

### Workflow Examples

**Starting Fresh:**
```
1. Create channel in Mattermost
2. /matrix create "My Room"
3. Done! Channel is now bridged
```

**Joining a Community:**
```
1. Find Matrix room (e.g., #rust:matrix.org)
2. /matrix join #rust:matrix.org create_channel=true
3. Done! Channel created and bridged
```

**Bridging Existing Resources:**
```
1. Have existing channel and existing Matrix room
2. Go to the channel
3. /matrix map #existing-room:server.com
4. Done! They're now bridged
```

---

## Identity: `/matrix link` and `/matrix unlink`

`/matrix link` issues a one-time code (valid 15 minutes). In your Matrix client,
open a direct message with the bridge bot (`@_mattermost_bridge:<server>`) and
send `!mattermost link <code>`. From then on your Matrix messages in bridged
rooms post as your Mattermost account, and your Mattermost profile is left
untouched.

To also have your Mattermost messages appear from your own Matrix account
(double puppeting), send the bot `!mattermost login <access_token>` (Element:
Settings → Help & About → Access Token). The token is verified against the
homeserver and must belong to you. `!mattermost logout` removes it,
`!mattermost whoami` shows the current state, and `/matrix unlink` (or
`!mattermost unlink`) removes the link.

Bridge-bot commands available in Matrix: `help`, `whoami`, `link <code>`,
`login <token>`, `logout`, `unlink`.

## Calls: `/matrix call`

Audio and video are not relayed between Mattermost Calls and Matrix: both
sides run their own media servers. What the bridge does is announce calls on
both sides (see the Calls setting in the plugin configuration):

- A call started with the Mattermost Calls plugin appears in the Matrix room
  as a notice with a join link, which is edited when the call ends.
- A call in the Matrix room (Element Call, any MatrixRTC client) appears in
  the channel as a card with the participants, the duration and a **Join in
  Element** button.

With **Calls** set to *Presence and Element Call* and an **Element Call URL**
configured, `/matrix call [title]` starts a call that both sides can join: the
bridge adds a widget to the room, announces the call there, and posts a card in
the channel with the same join address. Only one call runs per room at a time;
asking again returns the running one. The URL may contain `{room_id}`,
`{room_name}`, `{display_name}` and `{user_id}`.

## `/matrix history`

Administrators only. Brings the messages a channel already has into its
bridged room, for moving a community to Matrix. `/matrix history` on its own is
a preview: how many messages would be imported and over what dates. Nothing is
sent. `/matrix history confirm` starts the import in the background, and
`/matrix history confirm limit=500` imports only the oldest 500.

Messages go in oldest first, sent by their authors (their ghost or mirror
account), carrying the time they were written, with threads kept. Run it again
at any time: it skips what is already in the room, so it can finish an import
that stopped (three failures in a row stop a run) or add messages written since.
While it runs, `/matrix history` shows progress; afterwards it shows what the
last run did.

Not carried across yet: attachments (the preview counts them), reactions,
earlier versions of edited messages and direct messages. People who use their
own Matrix token for the bridge (double puppeting) show the import time rather
than the original one, because only the bridge itself may set a message's time.

## Mirror mode: `/matrix claim`

In mirror mode every Mattermost user has a Matrix account of their own, created
without credentials. `/matrix claim` makes the account yours. With a Synapse
admin token configured it sets a one-time password, shown once, to change at
first login; otherwise sign in to Matrix through shared login. Afterwards your
own Matrix client posts in Mattermost as you, and the bridge keeps sending your
Mattermost messages from the same account. `/matrix status` shows how many
accounts exist and how many are claimed. See
[MIRROR_MODE_SETUP.md](MIRROR_MODE_SETUP.md#54-claiming-accounts).

## `/matrix migrate-team`

The last steps of moving a team to Matrix, for system administrators, run in
any channel of the team. Only channels with a Matrix room are affected, and
every step that changes something needs `confirm`; without it the command says
what would happen.

| Command | What it does |
|---|---|
| `/matrix migrate-team report` | Per channel: read-only or not, members on both sides, and how many messages are not in the room yet (✅ when none). |
| `/matrix migrate-team readonly confirm` | Rejects new posts from people, posts and pins a message with the room link. What arrives from Matrix still lands. |
| `/matrix migrate-team writable confirm` | Undoes read-only. The pinned notices stay. |
| `/matrix migrate-team archive confirm` | Archives the read-only channels (posts stay and can be restored). |

Import the remaining messages with [`/matrix history`](#matrix-history) before
making a channel read-only. Matrix member counts include the bridge's own accounts.

### Moving to mirror mode: `/matrix migrate ghosts-to-mirror`

Administrators only. Turning mirror mode on after ghost users exist does not
change anything by itself: the mode is pinned by the first account, and
`/matrix status` says so. `/matrix migrate ghosts-to-mirror` reports what it
would do (a dry run); `/matrix migrate ghosts-to-mirror confirm` creates each
user's mirror account, joins it to the rooms the ghost was in, retires the
ghost and finally switches the mode. People who post as their own linked
account are left alone. If any user fails, nothing is switched and the command
can simply be run again.

When a community has finished moving, the **Cut-over Mode** setting freezes
the bridge: no new accounts, rooms or mappings are created, and everything that
exists keeps syncing.

## Permissions

### User Permissions

| Commands | Who may run them |
|----------|------------------|
| `test`, `list`, `status`, `link`, `unlink`, `claim`, `call` | Any channel member |
| `create`, `map`, `unmap`, `join` | Users allowed to manage the channel's properties (channel admins and team/system admins) |
| `map_user`, `backfill`, `migrate`, `metrics` | System administrators only |

Everyone else receives an ephemeral "permission denied" reply and nothing changes.

### Plugin Permissions

The plugin requires:
- Optionally the **Shared Channels** feature (Mattermost Professional/Enterprise) for attachment
  and profile-image sync through Connected Workspaces; without it the bridge runs in hooks-only mode
- Access to KV store (for mappings)
- Webhook/bot permissions (for posting messages)

---

## Autocomplete

All commands support Mattermost's autocomplete feature:
- Type `/matrix` and press Tab
- See available subcommands
- Get hints for required parameters
- View command descriptions inline

---

## Error Handling

### Common Errors

**"Matrix client not configured"**
- Solution: Configure Matrix settings in System Console → Plugins → Matrix Bridge

**"Invalid room identifier format"**
- Solution: Use `#alias:server.com` or `!id:server.com` format

**"Failed to join Matrix room"**
- Solution: Check room exists, is public, or invite the bridge bot

**"Channel not mapped"**
- Solution: Use `/matrix map` or `/matrix create` first

**"Failed to save mapping"**
- Solution: Check plugin has KV store access, check logs

---

## Best Practices

### Room/Channel Naming

- Use descriptive names for Matrix rooms
- Keep names consistent between Mattermost and Matrix
- Use `publish=true` only for truly public rooms

### User Management

- Let the bridge create ghost users automatically
- Use `map_user` sparingly for admins/VIPs only
- Sync profiles regularly with backfill

### Performance

- Backfill during off-peak hours
- Monitor rate limits for large teams
- Use `/matrix list` to audit mappings periodically

### Security

- Don't publish sensitive channels (`publish=false`)
- Use private channels for confidential discussions
- Review Matrix room permissions regularly
- Audit user mappings with `/matrix list`

---

## Troubleshooting Commands

| Problem | Command | Why |
|---------|---------|-----|
| Not sure if bridge works | `/matrix test` | Tests connection |
| Messages not syncing | `/matrix list` | Check if mapping exists |
| Need to re-establish connection | `/matrix unmap` then `/matrix map` | Resets mapping |
| Mappings corrupted | `/matrix migrate` | Fixes missing mappings |
| Need to audit bridges | `/matrix list` | See all connections |

---

## Advanced Usage

### Batch Operations

For batch operations, consider using the backfill command:
```bash
# Create all rooms at once
/matrix backfill channels

# Sync all users
/matrix backfill users
```

### Federation Testing

Test federation with well-known Matrix rooms:
```
/matrix join #matrix:matrix.org create_channel=true
/matrix join #element-web:matrix.org create_channel=true
```

### Custom User Mapping Workflow

1. Admin creates ghost user normally
2. Admin gets their Matrix ID: `@admin:matrix.org`
3. Admin maps to real account: `/matrix map_user @admin @admin:matrix.org`
4. Admin now uses their real Matrix account in bridged rooms

---

## See Also

- [Bridge Overview](BRIDGE_OVERVIEW.md) - Architecture and how it works
- [Local Development](../LOCAL_DEVELOPMENT.md) - Setup and configuration
- [README](../README.md) - Plugin overview

---

## Quick Reference Card

```
ESSENTIAL COMMANDS
------------------
/matrix test                           Test connection
/matrix create                         Create room (use channel name)
/matrix create "Room Name"             Create room (custom name)
/matrix join #room:server.com          Join existing room
/matrix map #room:server.com           Map to existing room
/matrix list                           Show all mappings
/matrix status                         Check status

ADVANCED COMMANDS
-----------------
/matrix join #room:server.com true     Join + create channel
/matrix map_user @user @id:server      Custom user mapping
/matrix backfill all                   Sync existing data
/matrix unmap                          Remove mapping
/matrix migrate                        Fix mappings
```
