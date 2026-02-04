# Local Development Guide for Mattermost-Matrix Bridge

This guide walks you through setting up a complete local development environment with Mattermost and Matrix (Synapse) to test the bridge.

## Quick Start

```bash
# One-command setup
./scripts/local-setup.sh
```

This script will:
1. Check prerequisites (Docker, Go, npm)
2. Generate secure tokens
3. Build the plugin
4. Start all Docker services
5. Create admin users on both platforms
6. Install and enable the plugin

## Manual Setup

If you prefer to set things up manually, follow these steps:

### Prerequisites

- Docker and Docker Compose
- Go 1.21+ (for building the plugin)
- Node.js 18+ and npm (for building the webapp)
- A Pro/Enterprise Mattermost license (for Shared Channels feature)

### Step 1: Build the Plugin

```bash
cd mattermost-plugin-matrix-bridge
make dist
```

This creates the plugin bundle in `dist/com.mattermost.plugin-matrix-bridge-*.tar.gz`

### Step 2: Generate Tokens

Generate secure tokens for the Application Service:

```bash
# Application Service Token (used by Mattermost to auth to Matrix)
AS_TOKEN=$(openssl rand -hex 32)
echo "AS Token: $AS_TOKEN"

# Homeserver Token (used by Matrix to send events to Mattermost)  
HS_TOKEN=$(openssl rand -hex 32)
echo "HS Token: $HS_TOKEN"
```

Save these tokens - you'll need them for both the registration file and plugin configuration.

### Step 3: Update Registration File

Edit `docker/mattermost-bridge-registration.yaml` and replace the placeholder tokens:

```yaml
as_token: YOUR_AS_TOKEN_HERE
hs_token: YOUR_HS_TOKEN_HERE
```

### Step 4: Start Docker Services

```bash
docker compose up -d
```

This starts:
- **Mattermost** on http://localhost:8066
- **Matrix/Synapse** on http://localhost:8888
- **Element Web** on http://localhost:8081 (optional Matrix client)
- PostgreSQL databases for both services

### Step 5: Create Admin Users

**Mattermost Admin:**
```bash
docker exec -u mattermost mattermost-plugin-matrix-bridge-mattermost-1 \
    /mattermost/bin/mmctl --local user create \
    --email admin@example.com \
    --username admin \
    --password "Admin123!" \
    --system-admin
```

**Matrix Admin:**
```bash
docker exec mattermost-plugin-matrix-bridge-synapse-1 \
    register_new_matrix_user \
    -c /data/homeserver.yaml \
    -u admin \
    -p admin123 \
    -a \
    http://localhost:8008
```

### Step 6: Create a Team

```bash
docker exec -u mattermost mattermost-plugin-matrix-bridge-mattermost-1 \
    /mattermost/bin/mmctl --local team create \
    --name test-team \
    --display-name "Test Team"

docker exec -u mattermost mattermost-plugin-matrix-bridge-mattermost-1 \
    /mattermost/bin/mmctl --local team users add test-team admin
```

### Step 7: Install the Plugin

1. Log into Mattermost at http://localhost:8066 with `admin` / `Admin123!`
2. Go to **System Console** → **Plugins** → **Plugin Management**
3. Upload the plugin from `dist/com.mattermost.plugin-matrix-bridge-*.tar.gz`
4. Enable the plugin

### Step 8: Configure the Plugin

1. Go to **System Console** → **Plugins** → **Matrix Bridge**
2. Set **Matrix Server URL** to: `http://synapse:8008`
   - Use `synapse` (the Docker service name) because Mattermost runs inside Docker
3. Set the **Application Service Token** to your generated `AS_TOKEN`
4. Set the **Homeserver Token** to your generated `HS_TOKEN`  
5. Enable **Message Sync**
6. Click **Save**

### Step 9: Test the Bridge

1. Go to your test team and create a new channel (e.g., "bridge-test")
2. In the channel, type: `/matrix create "Bridge Test Room"`
3. The command should create a corresponding Matrix room
4. Type `/matrix status` to check the bridge health

**Joining Existing Matrix Rooms:**
You can also join existing Matrix rooms from Mattermost:
- `/matrix join #room:server.com` - Join a room with the bridge bot
- `/matrix join #room:server.com create_channel=true` - Join and auto-create a bridged Mattermost channel

## Testing with Element Web

Element Web provides a nice UI to verify the Matrix side of the bridge:

1. Open http://localhost:8081
2. Click "Sign In"
3. Log in with `admin` / `admin123`
4. Look for bridged rooms in your room list

## Architecture Overview

```
┌─────────────────────┐          ┌─────────────────────┐
│                     │          │                     │
│    Mattermost       │◄────────►│    Matrix/Synapse   │
│    :8066            │  Bridge  │    :8888            │
│                     │  Plugin  │                     │
└─────────────────────┘          └─────────────────────┘
         │                                │
         │                                │
         ▼                                ▼
┌─────────────────────┐          ┌─────────────────────┐
│   mattermost-db     │          │    synapse-db       │
│   PostgreSQL        │          │    PostgreSQL       │
└─────────────────────┘          └─────────────────────┘
```

The bridge works as a Mattermost plugin that also functions as a Matrix Application Service:

1. **Mattermost → Matrix**: When a user posts in a bridged channel, the plugin's hooks catch the message and forward it to Matrix via the Client-Server API.

2. **Matrix → Mattermost**: Matrix sends events to the plugin's Application Service endpoint (`/_matrix/app/v1/transactions/{txnId}`), which then creates posts in Mattermost.

## Troubleshooting

### Plugin won't enable

Check the Mattermost logs:
```bash
docker compose logs mattermost
```

Common issues:
- Missing Pro/Enterprise license (required for Shared Channels)
- Plugin build errors

### Matrix connection fails

1. Verify Synapse is running:
```bash
curl http://localhost:8888/_matrix/client/versions
```

2. Check if registration file is loaded:
```bash
docker compose logs synapse | grep -i "application service"
```

3. Verify the Matrix Server URL uses the Docker service name (`http://synapse:8008`)

### Messages not syncing

1. Check plugin logs in System Console → Plugins → Matrix Bridge
2. Verify tokens match between plugin config and registration file
3. Ensure the channel is configured as a shared channel
4. Check that sync is enabled in plugin settings

### Reset Everything

To start fresh:
```bash
docker compose down -v
rm -f .as_token .hs_token
./scripts/local-setup.sh
```

## Development Workflow

### Rebuild and redeploy plugin

```bash
make deploy
```

This builds the plugin and deploys it to your local Mattermost instance.

### Watch mode for webapp changes

```bash
make watch
```

### View logs

```bash
# All services
docker compose logs -f

# Just Mattermost
docker compose logs -f mattermost

# Just Synapse
docker compose logs -f synapse
```

### Access containers

```bash
# Mattermost CLI
docker exec -it -u mattermost mattermost-plugin-matrix-bridge-mattermost-1 /bin/bash

# Synapse
docker exec -it mattermost-plugin-matrix-bridge-synapse-1 /bin/bash
```

## Next Steps

Once you have single-server bridging working, see [FEDERATION.md](FEDERATION.md) for setting up a second Matrix server to test full federation between:
- Mattermost Server A → Matrix Server A → Matrix Server B

This enables cross-organization communication through the Matrix federation protocol.
