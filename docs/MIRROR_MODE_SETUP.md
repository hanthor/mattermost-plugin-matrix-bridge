# Mirror Mode Setup Guide: Adding Matrix to Existing Mattermost

This guide walks you through adding a Matrix (Synapse) server to an existing Mattermost Docker deployment with Mirror Mode enabled and Matrix federation configured.

## Table of Contents
- [Prerequisites](#prerequisites)
- [Architecture Overview](#architecture-overview)
- [Step 1: Add Synapse to Docker Compose](#step-1-add-synapse-to-docker-compose)
- [Step 2: Configure Synapse](#step-2-configure-synapse)
- [Step 3: Create Application Service Registration](#step-3-create-application-service-registration)
- [Step 4: Install Matrix Bridge Plugin](#step-4-install-matrix-bridge-plugin)
- [Step 5: Configure Mirror Mode](#step-5-configure-mirror-mode)
- [Step 6: Enable Federation](#step-6-enable-federation)
- [Step 7: Testing](#step-7-testing)
- [Troubleshooting](#troubleshooting)

## Prerequisites

- Existing Mattermost server running in Docker
- Docker and Docker Compose installed
- Domain name for your Matrix server (e.g., `matrix.example.com`)
- SSL certificates for your domain
- Access to DNS configuration

## Architecture Overview

**Mirror Mode** means the Matrix server is completely hijacked to mirror Mattermost:
- All Mattermost users automatically get Matrix accounts
- Channels automatically become Matrix rooms
- Messages sync bidirectionally
- Users can access via Element or other Matrix clients with their Mattermost credentials

**Federation** allows your Matrix server to communicate with other Matrix servers (like matrix.org).

## Step 1: Add Synapse to Docker Compose

Add these services to your existing `docker-compose.yml`:

```yaml
services:
  # Your existing Mattermost services...
  
  synapse:
    image: matrixdotorg/synapse:latest
    container_name: synapse
    restart: unless-stopped
    environment:
      - SYNAPSE_SERVER_NAME=example.com  # Your domain
      - SYNAPSE_REPORT_STATS=no
    volumes:
      - synapse_data:/data
      - ./matrix-config:/config:ro
    networks:
      - mattermost_network
    ports:
      - "8448:8448"  # Federation port (expose externally)
      - "8008:8008"  # Client API (internal only)
    depends_on:
      - synapse-db

  synapse-db:
    image: postgres:15-alpine
    container_name: synapse-db
    restart: unless-stopped
    environment:
      POSTGRES_DB: synapse
      POSTGRES_USER: synapse_user
      POSTGRES_PASSWORD: synapse_password
      POSTGRES_INITDB_ARGS: --encoding=UTF-8 --lc-collate=C --lc-ctype=C
    volumes:
      - synapse_db:/var/lib/postgresql/data
    networks:
      - mattermost_network

  element-web:
    image: vectorim/element-web:latest
    container_name: element-web
    restart: unless-stopped
    volumes:
      - ./element-config.json:/app/config.json:ro
    networks:
      - mattermost_network
    ports:
      - "8081:80"

volumes:
  synapse_data:
  synapse_db:

networks:
  mattermost_network:
    driver: bridge
```

## Step 2: Configure Synapse

### 2.1 Generate Initial Configuration

```bash
# Create config directory
mkdir -p matrix-config

# Generate homeserver.yaml
docker run -it --rm \
  -v $(pwd)/matrix-config:/data \
  -e SYNAPSE_SERVER_NAME=example.com \
  -e SYNAPSE_REPORT_STATS=no \
  matrixdotorg/synapse:latest generate
```

### 2.2 Edit homeserver.yaml

Edit `matrix-config/homeserver.yaml`:

```yaml
# Server name - this is your Matrix domain
server_name: "example.com"

# Listen on all interfaces inside container
listeners:
  - port: 8008
    tls: false
    type: http
    x_forwarded: true
    bind_addresses: ['::']
    resources:
      - names: [client, federation]
        compress: false

# Database configuration
database:
  name: psycopg2
  args:
    user: synapse_user
    password: synapse_password
    database: synapse
    host: synapse-db
    cp_min: 5
    cp_max: 10

# Enable registration for bridge (but disable public registration)
enable_registration: false
enable_registration_without_verification: false

# Allow application services to register users
app_service_config_files:
  - /data/mattermost-bridge-registration.yaml

# Federation settings
federation_domain_whitelist: null  # null = allow all
# If you want to restrict federation:
# federation_domain_whitelist:
#   - matrix.org
#   - another-server.com

# Disable rate limiting for app services
rc_federation:
  window_size: 1000
  sleep_limit: 10
  sleep_delay: 500
  reject_limit: 50
  concurrent: 3

# Room directory settings - allow bridge bot to publish rooms
room_list_publication_rules:
  - user_id: "@_mattermost_bridge:example.com"
    action: allow
  - user_id: "*"
    action: deny

# Trust X-Forwarded-For headers (important for federation behind proxy)
trusted_key_servers:
  - server_name: "matrix.org"

# Signing key (generated automatically)
signing_key_path: "/data/example.com.signing.key"

# Registration shared secret (for creating admin users)
registration_shared_secret: "GENERATE_RANDOM_STRING_HERE"

# Suppress key server warnings
suppress_key_server_warning: true

# Media settings
max_upload_size: 50M
max_image_pixels: 32M

# Enable presence
use_presence: true

# URL previews
url_preview_enabled: true
url_preview_ip_range_blacklist:
  - '127.0.0.0/8'
  - '10.0.0.0/8'
  - '172.16.0.0/12'
  - '192.168.0.0/16'
```

### 2.3 Configure Element

Create `element-config.json`:

```json
{
  "default_server_config": {
    "m.homeserver": {
      "base_url": "https://matrix.example.com",
      "server_name": "example.com"
    }
  },
  "brand": "Element",
  "integrations_ui_url": "https://scalar.vector.im/",
  "integrations_rest_url": "https://scalar.vector.im/api",
  "integrations_widgets_urls": [
    "https://scalar.vector.im/_matrix/integrations/v1",
    "https://scalar.vector.im/api",
    "https://scalar-staging.vector.im/_matrix/integrations/v1",
    "https://scalar-staging.vector.im/api",
    "https://scalar-staging.riot.im/scalar/api"
  ],
  "default_country_code": "US",
  "show_labs_settings": true,
  "features": {
    "feature_new_room_decoration_ui": true
  },
  "default_theme": "light",
  "room_directory": {
    "servers": ["example.com", "matrix.org"]
  }
}
```

## Step 3: Create Application Service Registration

Create `matrix-config/mattermost-bridge-registration.yaml`:

```yaml
# Mattermost Bridge Application Service Registration
id: mattermost-bridge

# URL where Matrix will send events
url: http://mattermost:8065/plugins/com.mattermost.plugin-matrix-bridge

# Application Service token (generate with: openssl rand -hex 32)
as_token: YOUR_GENERATED_AS_TOKEN_HERE

# Homeserver token (generate with: openssl rand -hex 32)
hs_token: YOUR_GENERATED_HS_TOKEN_HERE

# Bridge bot user
sender_localpart: _mattermost_bridge

# Disable rate limiting for this AS
rate_limited: false

# Namespaces - what users/aliases the bridge controls
namespaces:
  users:
    - exclusive: true
      regex: "@_mattermost_.*:example\\.com"
    - exclusive: true
      regex: "@.*:example\\.com"  # Mirror Mode: owns ALL users
  
  aliases:
    - exclusive: true
      regex: "#_mattermost_.*:example\\.com"
    - exclusive: true
      regex: "#.*:example\\.com"  # Mirror Mode: owns ALL aliases
  
  rooms: []

protocols: []
```

Generate tokens:
```bash
# Generate AS token
openssl rand -hex 32

# Generate HS token
openssl rand -hex 32
```

## Step 4: Install Matrix Bridge Plugin

### 4.1 Build or Download Plugin

```bash
# If building from source:
cd mattermost-plugin-matrix-bridge
make dist

# The plugin will be at: dist/com.mattermost.plugin-matrix-bridge-*.tar.gz
```

### 4.2 Upload to Mattermost

1. Go to System Console → Plugins → Management
2. Click "Upload Plugin"
3. Select the `.tar.gz` file
4. Enable the plugin

## Step 5: Configure Mirror Mode

### 5.1 Enable Shared Channels

Go to System Console → Experimental → Features:
- Enable "Shared Channels" ✓

### 5.2 Configure Plugin

Go to System Console → Plugins → Matrix Bridge:

**Basic Settings:**
- **Matrix Server URL**: `http://synapse:8008`
- **Matrix Server Domain**: `example.com` (your server_name)
- **Application Service Token**: (copy from registration.yaml)
- **Homeserver Token**: (copy from registration.yaml)

**Mirror Mode Settings:**
- **Enable Mirror Mode**: ✓ (checked)
- **Mirror Mode Default Password**: `Matrix123!` (or your preferred default)
- **Sync User Profiles**: ✓ (checked)
- **Enable Sync**: ✓ (checked)

**Rate Limiting:**
- Set to "Automatic" or "Lenient"

Click **Save**

### 5.3 Restart Plugin

After configuration:
```bash
# Via CLI
docker exec mattermost mattermost plugin disable com.mattermost.plugin-matrix-bridge
docker exec mattermost mattermost plugin enable com.mattermost.plugin-matrix-bridge
```

Or use System Console → Plugins → Management → Restart

## Step 6: Enable Federation

### 6.1 DNS Configuration

Add these DNS records:

```
# A record for Matrix server
matrix.example.com.     A       YOUR_SERVER_IP

# SRV record for federation (required!)
_matrix._tcp.example.com. SRV   10 0 8448 matrix.example.com.
```

Verify:
```bash
dig SRV _matrix._tcp.example.com
dig A matrix.example.com
```

### 6.2 Reverse Proxy Configuration (nginx)

Create `/etc/nginx/sites-available/matrix`:

```nginx
# Matrix client API
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name matrix.example.com;

    ssl_certificate /etc/letsencrypt/live/matrix.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/matrix.example.com/privkey.pem;

    location /_matrix {
        proxy_pass http://localhost:8008;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Host $host;
        
        client_max_body_size 50M;
    }
    
    location /_synapse/client {
        proxy_pass http://localhost:8008;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Host $host;
    }
}

# Matrix federation
server {
    listen 8448 ssl http2;
    listen [::]:8448 ssl http2;
    server_name matrix.example.com;

    ssl_certificate /etc/letsencrypt/live/matrix.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/matrix.example.com/privkey.pem;

    location / {
        proxy_pass http://localhost:8008;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Host $host;
    }
}

# Element Web UI
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name element.example.com;

    ssl_certificate /etc/letsencrypt/live/element.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/element.example.com/privkey.pem;

    root /dev/null;

    location / {
        proxy_pass http://localhost:8081;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
    }
}
```

Enable and test:
```bash
ln -s /etc/nginx/sites-available/matrix /etc/nginx/sites-enabled/
nginx -t
systemctl reload nginx
```

### 6.3 Firewall Configuration

```bash
# Allow federation port
ufw allow 8448/tcp

# Verify
ufw status
```

### 6.4 Test Federation

```bash
# Test federation to matrix.org
curl -X GET "https://matrix.example.com/_matrix/federation/v1/version"

# Test from federation tester
# Visit: https://federationtester.matrix.org/
# Enter: example.com
```

## Step 7: Testing

### 7.1 Backfill Existing Data

In any Mattermost channel, run:
```
/matrix backfill users
/matrix backfill channels
```

This creates Matrix accounts for all Mattermost users and rooms for all channels.

### 7.2 Test Matrix Login

1. Go to `https://element.example.com` (or `http://localhost:8081` for testing)
2. Click "Edit" on homeserver
3. Enter your Matrix server: `https://matrix.example.com`
4. Login with:
   - Username: Your Mattermost username
   - Password: `Matrix123!` (or your configured default)

### 7.3 Test Message Sync

1. Post a message in Mattermost
2. Check Element - message should appear
3. Post a message in Element
4. Check Mattermost - message should appear

### 7.4 Test Federation

1. In Element, search for a user on another server: `@user:matrix.org`
2. Start a DM or invite them to a room
3. Messages should flow between servers

### 7.5 Verify Connection

In Mattermost:
```
/matrix test
```

Should show:
```
✅ Server URL: http://synapse:8008
✅ Matrix Client: Initialized
✅ Connection: Successfully connected to Matrix server
```

## Troubleshooting

### Plugin Can't Connect to Matrix

**Problem**: `dial tcp 127.0.0.1:8008: connect: connection refused`

**Solution**: Use Docker service name instead of localhost
- Change Matrix Server URL from `http://localhost:8008` to `http://synapse:8008`

### Room Creation Fails: M_EXCLUSIVE

**Problem**: `This application service has not reserved this kind of alias`

**Solution**: Update `mattermost-bridge-registration.yaml` to include:
```yaml
aliases:
  - exclusive: true
    regex: "#.*:example\\.com"
```

### Federation Not Working

**Checklist**:
1. DNS SRV record configured? `dig SRV _matrix._tcp.example.com`
2. Port 8448 open? `telnet matrix.example.com 8448`
3. SSL certificate valid? `openssl s_client -connect matrix.example.com:8448`
4. Test at: https://federationtester.matrix.org/

### Users Can't Login to Element

**Problem**: "Invalid username or password"

**Solutions**:
1. Ensure Mirror Mode is enabled in plugin config
2. User must have posted at least one message (triggers user creation)
3. Or run `/matrix backfill users` to create all users
4. Check password is correct (default: `Matrix123!`)

### Messages Not Syncing

**Checklist**:
1. Enable Sync checked in plugin config?
2. Channel is bridged? Run `/matrix list` to see mappings
3. Check logs: `docker logs mattermost | grep matrix`

### Database Connection Failed

**Problem**: Synapse can't connect to database

**Solution**: Ensure synapse-db is on same network:
```yaml
networks:
  - mattermost_network
```

## Security Considerations

1. **Change Default Password**: Users should change their Matrix password after first login
2. **Federation**: Consider using `federation_domain_whitelist` to restrict federation
3. **Registration**: Keep `enable_registration: false` to prevent unauthorized signups
4. **Tokens**: Use strong, unique tokens for AS and HS tokens
5. **SSL**: Always use SSL certificates in production
6. **Firewall**: Only expose necessary ports (8448 for federation)

## Performance Tuning

For large deployments:

### Synapse Configuration
```yaml
# Increase connection pools
database:
  args:
    cp_min: 10
    cp_max: 20

# Increase worker count (requires separate setup)
# See: https://matrix-org.github.io/synapse/latest/workers.html
```

### Plugin Configuration
- Use "Lenient" rate limiting mode
- Consider dedicated Matrix server hardware

## Backup Strategy

### What to Backup
1. Synapse database: `synapse_db` volume
2. Synapse data: `synapse_data` volume
3. Configuration files: `matrix-config/` directory
4. Registration file: `mattermost-bridge-registration.yaml`

### Backup Script
```bash
#!/bin/bash
BACKUP_DIR="/backups/matrix-$(date +%Y%m%d)"
mkdir -p "$BACKUP_DIR"

# Stop synapse
docker compose stop synapse

# Backup database
docker compose exec synapse-db pg_dump -U synapse_user synapse > "$BACKUP_DIR/synapse.sql"

# Backup data volume
docker run --rm -v synapse_data:/data -v "$BACKUP_DIR":/backup alpine tar czf /backup/synapse_data.tar.gz /data

# Backup config
cp -r matrix-config "$BACKUP_DIR/"

# Restart synapse
docker compose start synapse
```

## Next Steps

- Set up monitoring (Prometheus + Grafana)
- Configure TURN server for VoIP calls
- Set up synapse workers for better performance
- Implement SSO/SAML integration
- Configure media retention policies

## Resources

- [Synapse Documentation](https://matrix-org.github.io/synapse/latest/)
- [Matrix Spec](https://spec.matrix.org/)
- [Element Documentation](https://element.io/user-guide)
- [Federation Setup](https://matrix-org.github.io/synapse/latest/federate.html)
- [Matrix Bridge Plugin Repository](https://github.com/mattermost/mattermost-plugin-matrix-bridge)
