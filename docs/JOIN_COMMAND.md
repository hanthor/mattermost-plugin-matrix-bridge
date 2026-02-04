# Join Command - Connecting to Existing Matrix Rooms

The `/matrix join` command allows you to connect your Mattermost workspace to existing Matrix rooms without creating new ones. This is useful for:

- Joining public Matrix communities
- Connecting to existing federated Matrix rooms
- Bridging to rooms created by other Matrix servers
- Participating in cross-organization Matrix spaces

## Usage

### Basic Join
Join a Matrix room with the bridge bot:
```
/matrix join #room:server.com
```

After joining, you can:
- Use `/matrix map #room:server.com` in any channel to bridge it to the room
- Messages will sync between Mattermost and Matrix

### Auto-Create Channel
Join a room and automatically create a bridged Mattermost channel:
```
/matrix join #community:matrix.org create_channel=true
```

This will:
1. Join the Matrix room with the bridge bot
2. Create a new Mattermost channel (using the room's name)
3. Automatically map the channel to the Matrix room
4. Add you to the new channel

## Examples

### Join a Public Community
```
/matrix join #rust:matrix.org
```

Then in any channel:
```
/matrix map #rust:matrix.org
```

### Join and Create Channel in One Step
```
/matrix join #python:matrix.org create_channel=true
```

This creates a channel called "python" and bridges it automatically.

### Join a Room by Room ID
```
/matrix join !abc123:matrix.org
```

Room IDs (starting with `!`) work the same as room aliases (starting with `#`), but aliases are more human-readable.

## How It Works

1. **Bridge Bot Joins**: The Application Service bot joins the Matrix room on behalf of your Mattermost instance
2. **Ghost User Joins**: Your Mattermost user's Matrix "ghost" is also joined to enable immediate messaging
3. **Channel Creation** (optional): If `create_channel=true`, a new Mattermost channel is created
4. **Bidirectional Mapping**: The room-to-channel mapping is stored for message synchronization

## Requirements

- The Matrix room must be:
  - **Public** (anyone can join), OR
  - The bridge bot must be **invited** to the room first
  
- For private rooms, a Matrix user must invite the bridge bot (`@mattermost_bot:your.server.com`) before you can join

## Troubleshooting

### "Failed to join Matrix room"

**Possible causes:**
- The room doesn't exist
- The room is private and the bridge wasn't invited
- The room identifier is incorrect
- Network connectivity issues

**Solutions:**
1. Verify the room identifier format: `#name:server.com` or `!id:server.com`
2. Check that the room exists (try joining it from Element Web or another Matrix client)
3. If it's a private room, ask a room admin to invite `@mattermost_bot:your.server.com`
4. Check the plugin logs for detailed error messages

### Room joined but messages not syncing

Make sure you've mapped the room to a channel:
```
/matrix map #room:server.com
```

Or join with `create_channel=true` to do this automatically.

## Comparison with Other Commands

| Command | Purpose | Use Case |
|---------|---------|----------|
| `/matrix create` | Create a NEW Matrix room | Starting fresh communication |
| `/matrix join` | Join an EXISTING Matrix room | Connecting to established communities |
| `/matrix map` | Link current channel to a room | Bridging existing channel to existing room |

## Advanced Usage

### Joining Federated Rooms

Join rooms from other Matrix servers:
```
/matrix join #community:matrix.org create_channel=true
/matrix join #element-web:matrix.org create_channel=true
```

This enables your Mattermost team to participate in Matrix's federated ecosystem.

### Team Collaboration

Multiple Mattermost channels can bridge to different rooms in the same Matrix Space (category):
```
/matrix join #team-general:company.com create_channel=true
/matrix join #team-dev:company.com create_channel=true
/matrix join #team-support:company.com create_channel=true
```

### Testing Matrix Federation

Great for testing your bridge with well-known public rooms:
```
/matrix join #matrix:matrix.org create_channel=true
```

## Security Considerations

- **Public Rooms**: Anyone on the Matrix network can see messages in public rooms
- **Private Rooms**: Only invited members can see messages
- **Ghost Users**: Each Mattermost user appears as a separate Matrix user in the room
- **Bridge Bot**: Has the same permissions as any other room member

## Related Commands

- `/matrix create` - Create a new Matrix room
- `/matrix map` - Map current channel to an existing room
- `/matrix status` - Check bridge health
- `/matrix list` - Show all channel-room mappings
