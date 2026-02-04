#!/bin/bash
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Banner
echo -e "${CYAN}"
cat << "EOF"
╔═══════════════════════════════════════════════════════╗
║                                                       ║
║   Mattermost Matrix Bridge - Mirror Mode Setup       ║
║                                                       ║
╚═══════════════════════════════════════════════════════╝
EOF
echo -e "${NC}"

# Configuration directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_DIR="${SCRIPT_DIR}/matrix-config"
OUTPUT_DIR="${SCRIPT_DIR}/mirror-mode-setup"

echo -e "${BLUE}This script will set up Matrix (Synapse) with Mirror Mode enabled.${NC}\n"

# ============================================================================
# Step 1: Gather Information
# ============================================================================

echo -e "${YELLOW}━━━ Step 1: Configuration ━━━${NC}\n"

# Detect or ask for domain
if [ -f "${SCRIPT_DIR}/docker-compose.yml" ]; then
    DETECTED_DOMAIN=$(grep -E "MATTERMOST_SITE_URL|MM_SERVICESETTINGS_SITEURL" "${SCRIPT_DIR}/docker-compose.yml" | head -1 | grep -oP 'https?://\K[^:/]+' || echo "")
    if [ -n "$DETECTED_DOMAIN" ]; then
        echo -e "${GREEN}✓${NC} Detected Mattermost domain: ${CYAN}${DETECTED_DOMAIN}${NC}"
        read -p "Press Enter to use this domain, or type a different one: " INPUT_DOMAIN
        DOMAIN="${INPUT_DOMAIN:-$DETECTED_DOMAIN}"
    fi
fi

if [ -z "$DOMAIN" ]; then
    read -p "Enter your Mattermost domain (e.g., chat.example.com): " DOMAIN
fi

# Extract base domain for Matrix
BASE_DOMAIN=$(echo "$DOMAIN" | sed -E 's/^(chat\.|mm\.|mattermost\.)//g')
MATRIX_DOMAIN="matrix.${BASE_DOMAIN}"
ELEMENT_DOMAIN="element.${BASE_DOMAIN}"

echo -e "\n${BLUE}Configuration:${NC}"
echo -e "  Mattermost:  https://${DOMAIN}"
echo -e "  Matrix API:  https://${MATRIX_DOMAIN}"
echo -e "  Element Web: https://${ELEMENT_DOMAIN}"
echo -e "  Server Name: ${BASE_DOMAIN} ${CYAN}(used in Matrix IDs like @user:${BASE_DOMAIN})${NC}\n"

read -p "Is this correct? (y/n): " CONFIRM
if [ "$CONFIRM" != "y" ]; then
    echo "Please edit the script or run again with correct domain."
    exit 1
fi

# Mattermost connection details
echo -e "\n${YELLOW}Mattermost Connection:${NC}"
read -p "Mattermost URL [http://localhost:8065]: " MM_URL
MM_URL="${MM_URL:-http://localhost:8065}"

echo -e "\n${BLUE}Admin credentials are needed to configure the plugin.${NC}"
read -p "Admin email or username: " ADMIN_USER
read -sp "Admin password: " ADMIN_PASSWORD
echo ""

# ============================================================================
# Step 2: Generate Tokens and Secrets
# ============================================================================

echo -e "\n${YELLOW}━━━ Step 2: Generating Tokens ━━━${NC}\n"

AS_TOKEN=$(openssl rand -hex 32)
HS_TOKEN=$(openssl rand -hex 32)
REGISTRATION_SECRET=$(openssl rand -hex 32)
POSTGRES_PASSWORD=$(openssl rand -hex 16)

echo -e "${GREEN}✓${NC} Generated Application Service token"
echo -e "${GREEN}✓${NC} Generated Homeserver token"
echo -e "${GREEN}✓${NC} Generated registration shared secret"
echo -e "${GREEN}✓${NC} Generated database password"

# ============================================================================
# Step 3: Get Mattermost Admin Token
# ============================================================================

echo -e "\n${YELLOW}━━━ Step 3: Authenticating with Mattermost ━━━${NC}\n"

LOGIN_RESPONSE=$(curl -s -X POST "${MM_URL}/api/v4/users/login" \
  -H "Content-Type: application/json" \
  -d "{\"login_id\":\"${ADMIN_USER}\",\"password\":\"${ADMIN_PASSWORD}\"}")

MM_TOKEN=$(echo "$LOGIN_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin).get('id', ''))" 2>/dev/null || echo "")

if [ -z "$MM_TOKEN" ]; then
    echo -e "${RED}✗ Failed to authenticate with Mattermost${NC}"
    echo "Response: $LOGIN_RESPONSE"
    exit 1
fi

echo -e "${GREEN}✓${NC} Authenticated successfully"

# Create output directory
mkdir -p "$OUTPUT_DIR"
mkdir -p "$CONFIG_DIR"

# ============================================================================
# Step 4: Create Docker Compose Configuration
# ============================================================================

echo -e "\n${YELLOW}━━━ Step 4: Creating Docker Compose Configuration ━━━${NC}\n"

cat > "${OUTPUT_DIR}/docker-compose.matrix.yml" << EOF
# Add this to your existing docker-compose.yml or use as an override:
# docker-compose -f docker-compose.yml -f docker-compose.matrix.yml up -d

version: '3.8'

services:
  synapse:
    image: matrixdotorg/synapse:latest
    container_name: synapse
    restart: unless-stopped
    environment:
      - SYNAPSE_SERVER_NAME=${BASE_DOMAIN}
      - SYNAPSE_REPORT_STATS=no
    volumes:
      - synapse_data:/data
      - ${CONFIG_DIR}:/config:ro
    networks:
      - default
    ports:
      - "8448:8448"  # Federation (expose externally)
      - "127.0.0.1:8008:8008"  # Client API (internal only)
    depends_on:
      - synapse-db
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8008/health"]
      interval: 30s
      timeout: 10s
      retries: 3

  synapse-db:
    image: postgres:15-alpine
    container_name: synapse-db
    restart: unless-stopped
    environment:
      POSTGRES_DB: synapse
      POSTGRES_USER: synapse_user
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
      POSTGRES_INITDB_ARGS: --encoding=UTF-8 --lc-collate=C --lc-ctype=C
    volumes:
      - synapse_db:/var/lib/postgresql/data
    networks:
      - default
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U synapse_user -d synapse"]
      interval: 10s
      timeout: 5s
      retries: 5

  element-web:
    image: vectorim/element-web:latest
    container_name: element-web
    restart: unless-stopped
    volumes:
      - ${OUTPUT_DIR}/element-config.json:/app/config.json:ro
    networks:
      - default
    ports:
      - "127.0.0.1:8081:80"

volumes:
  synapse_data:
    driver: local
  synapse_db:
    driver: local

networks:
  default:
    name: mattermost_network
    external: true
EOF

echo -e "${GREEN}✓${NC} Created docker-compose.matrix.yml"

# ============================================================================
# Step 5: Create Synapse Configuration
# ============================================================================

echo -e "\n${YELLOW}━━━ Step 5: Creating Synapse Configuration ━━━${NC}\n"

cat > "${CONFIG_DIR}/homeserver.yaml" << EOF
# Synapse Homeserver Configuration for Mirror Mode
server_name: "${BASE_DOMAIN}"

# Listen configuration
listeners:
  - port: 8008
    tls: false
    type: http
    x_forwarded: true
    bind_addresses: ['::']
    resources:
      - names: [client, federation]
        compress: false

# Database
database:
  name: psycopg2
  args:
    user: synapse_user
    password: ${POSTGRES_PASSWORD}
    database: synapse
    host: synapse-db
    port: 5432
    cp_min: 5
    cp_max: 10

# Registration
enable_registration: false
enable_registration_without_verification: false
registration_shared_secret: "${REGISTRATION_SECRET}"

# Application Service
app_service_config_files:
  - /data/mattermost-bridge-registration.yaml

# Federation
federation_domain_whitelist: null  # Allow all - edit to restrict
trusted_key_servers:
  - server_name: "matrix.org"

# Room directory - allow bridge bot to publish rooms
room_list_publication_rules:
  - user_id: "@_mattermost_bridge:${BASE_DOMAIN}"
    action: allow
  - user_id: "*"
    action: deny

# Signing key
signing_key_path: "/data/${BASE_DOMAIN}.signing.key"

# Media
max_upload_size: 50M
max_image_pixels: 32M

# Features
use_presence: true
url_preview_enabled: true
url_preview_ip_range_blacklist:
  - '127.0.0.0/8'
  - '10.0.0.0/8'
  - '172.16.0.0/12'
  - '192.168.0.0/16'

# Performance
rc_federation:
  window_size: 1000
  sleep_limit: 10
  sleep_delay: 500
  reject_limit: 50
  concurrent: 3

# Logging
log_config: "/data/${BASE_DOMAIN}.log.config"

# Suppress warnings
suppress_key_server_warning: true
EOF

echo -e "${GREEN}✓${NC} Created homeserver.yaml"

# ============================================================================
# Step 6: Create Application Service Registration
# ============================================================================

echo -e "\n${YELLOW}━━━ Step 6: Creating Application Service Registration ━━━${NC}\n"

cat > "${CONFIG_DIR}/mattermost-bridge-registration.yaml" << EOF
# Mattermost Bridge Application Service Registration
id: mattermost-bridge

# URL where Matrix sends events (use Docker service name)
url: http://mattermost:8065/plugins/com.mattermost.plugin-matrix-bridge

# Tokens
as_token: ${AS_TOKEN}
hs_token: ${HS_TOKEN}

# Bridge bot
sender_localpart: _mattermost_bridge

# Disable rate limiting
rate_limited: false

# Namespaces - Mirror Mode owns all users and aliases
namespaces:
  users:
    - exclusive: true
      regex: "@_mattermost_.*:${BASE_DOMAIN//./\\.}"
    - exclusive: true
      regex: "@.*:${BASE_DOMAIN//./\\.}"
  
  aliases:
    - exclusive: true
      regex: "#_mattermost_.*:${BASE_DOMAIN//./\\.}"
    - exclusive: true
      regex: "#.*:${BASE_DOMAIN//./\\.}"
  
  rooms: []

protocols: []
EOF

echo -e "${GREEN}✓${NC} Created application service registration"

# ============================================================================
# Step 7: Create Element Configuration
# ============================================================================

echo -e "\n${YELLOW}━━━ Step 7: Creating Element Configuration ━━━${NC}\n"

cat > "${OUTPUT_DIR}/element-config.json" << EOF
{
  "default_server_config": {
    "m.homeserver": {
      "base_url": "https://${MATRIX_DOMAIN}",
      "server_name": "${BASE_DOMAIN}"
    }
  },
  "brand": "Element",
  "default_country_code": "US",
  "show_labs_settings": true,
  "default_theme": "light",
  "room_directory": {
    "servers": ["${BASE_DOMAIN}", "matrix.org"]
  },
  "permalink_prefix": "https://${ELEMENT_DOMAIN}",
  "disable_guests": true,
  "disable_login_language_selector": false,
  "disable_3pid_login": false,
  "features": {
    "feature_new_room_decoration_ui": true
  }
}
EOF

echo -e "${GREEN}✓${NC} Created Element configuration"

# ============================================================================
# Step 8: Save Configuration Summary
# ============================================================================

echo -e "\n${YELLOW}━━━ Step 8: Saving Configuration ━━━${NC}\n"

cat > "${OUTPUT_DIR}/config-summary.txt" << EOF
╔═══════════════════════════════════════════════════════╗
║     Mattermost Matrix Bridge Configuration           ║
╚═══════════════════════════════════════════════════════╝

Generated: $(date)

DOMAINS:
--------
Mattermost:     https://${DOMAIN}
Matrix API:     https://${MATRIX_DOMAIN}
Element Web:    https://${ELEMENT_DOMAIN}
Server Name:    ${BASE_DOMAIN}

TOKENS (Secret - Store Securely):
----------------------------------
AS Token:       ${AS_TOKEN}
HS Token:       ${HS_TOKEN}
Reg Secret:     ${REGISTRATION_SECRET}
DB Password:    ${POSTGRES_PASSWORD}

PLUGIN CONFIGURATION:
---------------------
Matrix Server URL:       http://synapse:8008
Matrix Server Domain:    ${BASE_DOMAIN}
Application Service Token: ${AS_TOKEN}
Homeserver Token:        ${HS_TOKEN}
Enable Mirror Mode:      true
Mirror Mode Password:    Matrix123!
Sync User Profiles:      true
Enable Sync:             true

FILES CREATED:
--------------
Docker Compose:      ${OUTPUT_DIR}/docker-compose.matrix.yml
Homeserver Config:   ${CONFIG_DIR}/homeserver.yaml
AS Registration:     ${CONFIG_DIR}/mattermost-bridge-registration.yaml
Element Config:      ${OUTPUT_DIR}/element-config.json
Nginx Config:        ${OUTPUT_DIR}/nginx-matrix.conf
DNS Records:         ${OUTPUT_DIR}/dns-records.txt

NEXT STEPS:
-----------
1. Review DNS records in dns-records.txt
2. Set up reverse proxy (see nginx-matrix.conf)
3. Start Matrix services
4. Configure Mattermost plugin
5. Test the setup

See INSTALLATION.md for detailed instructions.
EOF

echo -e "${GREEN}✓${NC} Saved configuration summary"

# ============================================================================
# Step 9: Create Nginx Configuration
# ============================================================================

echo -e "\n${YELLOW}━━━ Step 9: Creating Nginx Configuration ━━━${NC}\n"

cat > "${OUTPUT_DIR}/nginx-matrix.conf" << 'EOF'
# Matrix Client API and Federation
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name MATRIX_DOMAIN;

    # SSL Configuration (adjust paths as needed)
    ssl_certificate /etc/letsencrypt/live/MATRIX_DOMAIN/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/MATRIX_DOMAIN/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;

    # Matrix client API
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

    # Well-known for client discovery
    location /.well-known/matrix/client {
        return 200 '{"m.homeserver": {"base_url": "https://MATRIX_DOMAIN"}}';
        default_type application/json;
        add_header Access-Control-Allow-Origin *;
    }
}

# Matrix Federation (port 8448)
server {
    listen 8448 ssl http2 default_server;
    listen [::]:8448 ssl http2 default_server;
    server_name MATRIX_DOMAIN;

    # SSL Configuration
    ssl_certificate /etc/letsencrypt/live/MATRIX_DOMAIN/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/MATRIX_DOMAIN/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;

    location / {
        proxy_pass http://localhost:8008;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Host $host;
    }
}

# Element Web Client
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name ELEMENT_DOMAIN;

    # SSL Configuration
    ssl_certificate /etc/letsencrypt/live/ELEMENT_DOMAIN/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/ELEMENT_DOMAIN/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;

    location / {
        proxy_pass http://localhost:8081;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
EOF

# Replace placeholders
sed -i "s/MATRIX_DOMAIN/${MATRIX_DOMAIN}/g" "${OUTPUT_DIR}/nginx-matrix.conf"
sed -i "s/ELEMENT_DOMAIN/${ELEMENT_DOMAIN}/g" "${OUTPUT_DIR}/nginx-matrix.conf"

echo -e "${GREEN}✓${NC} Created Nginx configuration"

# ============================================================================
# Step 10: Create DNS Records File
# ============================================================================

echo -e "\n${YELLOW}━━━ Step 10: Creating DNS Records ━━━${NC}\n"

# Get server IP
SERVER_IP=$(curl -s ifconfig.me 2>/dev/null || echo "YOUR_SERVER_IP")

cat > "${OUTPUT_DIR}/dns-records.txt" << EOF
╔═══════════════════════════════════════════════════════╗
║              DNS RECORDS - COPY & PASTE               ║
╚═══════════════════════════════════════════════════════╝

Add these records to your DNS provider (e.g., Cloudflare):

─────────────────────────────────────────────────────────
1. A RECORD for Matrix Server
─────────────────────────────────────────────────────────
Type:    A
Name:    matrix.${BASE_DOMAIN}  (or just "matrix" if your provider auto-adds the domain)
Content: ${SERVER_IP}
TTL:     Auto (or 3600)
Proxy:   Disabled (orange cloud OFF in Cloudflare)

─────────────────────────────────────────────────────────
2. A RECORD for Element Web
─────────────────────────────────────────────────────────
Type:    A
Name:    element.${BASE_DOMAIN}  (or just "element")
Content: ${SERVER_IP}
TTL:     Auto (or 3600)
Proxy:   Enabled (orange cloud ON in Cloudflare - optional)

─────────────────────────────────────────────────────────
3. SRV RECORD for Federation (REQUIRED!)
─────────────────────────────────────────────────────────
Type:     SRV
Name:     _matrix._tcp.${BASE_DOMAIN}  (or "_matrix._tcp")
Service:  _matrix
Protocol: _tcp
Priority: 10
Weight:   0
Port:     8448
Target:   matrix.${BASE_DOMAIN}
TTL:      Auto (or 3600)

─────────────────────────────────────────────────────────
CLOUDFLARE SPECIFIC FORMAT:
─────────────────────────────────────────────────────────
If using Cloudflare, enter SRV record like this:

Service:  _matrix
Protocol: _tcp
Name:     ${BASE_DOMAIN}
TTL:      Auto
Priority: 10
Weight:   0
Port:     8448
Target:   matrix.${BASE_DOMAIN}

─────────────────────────────────────────────────────────
VERIFICATION COMMANDS:
─────────────────────────────────────────────────────────

After adding DNS records (allow 5-10 minutes for propagation):

# Check A records
dig A matrix.${BASE_DOMAIN}
dig A element.${BASE_DOMAIN}

# Check SRV record (IMPORTANT!)
dig SRV _matrix._tcp.${BASE_DOMAIN}

Expected output should show:
_matrix._tcp.${BASE_DOMAIN}. 3600 IN SRV 10 0 8448 matrix.${BASE_DOMAIN}.

# Test Matrix federation
curl -X GET "https://matrix.${BASE_DOMAIN}/_matrix/federation/v1/version"

─────────────────────────────────────────────────────────
TROUBLESHOOTING:
─────────────────────────────────────────────────────────

If dig commands don't work, try:
  nslookup matrix.${BASE_DOMAIN}
  nslookup -type=SRV _matrix._tcp.${BASE_DOMAIN}

Online checker:
  https://federationtester.matrix.org/
  Enter: ${BASE_DOMAIN}
EOF

echo -e "${GREEN}✓${NC} Created DNS records guide"

# ============================================================================
# Step 11: Configure Mattermost Plugin
# ============================================================================

echo -e "\n${YELLOW}━━━ Step 11: Configuring Mattermost Plugin ━━━${NC}\n"

# Create plugin configuration JSON
cat > "${OUTPUT_DIR}/plugin-config.json" << EOF
{
  "matrix_server_url": "http://synapse:8008",
  "matrix_server_domain": "${BASE_DOMAIN}",
  "matrix_as_token": "${AS_TOKEN}",
  "matrix_hs_token": "${HS_TOKEN}",
  "enable_mirror_mode": true,
  "mirror_mode_password": "Matrix123!",
  "sync_user_profiles": true,
  "enable_sync": true,
  "rate_limiting_mode": "automatic"
}
EOF

# Update Mattermost configuration via API
echo "Updating plugin configuration..."
UPDATE_RESPONSE=$(curl -s -X PUT "${MM_URL}/api/v4/plugins/com.mattermost.plugin-matrix-bridge/configuration" \
  -H "Authorization: Bearer ${MM_TOKEN}" \
  -H "Content-Type: application/json" \
  -d @"${OUTPUT_DIR}/plugin-config.json")

if echo "$UPDATE_RESPONSE" | grep -q "error"; then
    echo -e "${YELLOW}⚠${NC} Could not auto-configure plugin via API"
    echo "You'll need to configure manually in System Console"
else
    echo -e "${GREEN}✓${NC} Plugin configuration updated"
fi

# ============================================================================
# Step 12: Create Installation Guide
# ============================================================================

echo -e "\n${YELLOW}━━━ Step 12: Creating Installation Guide ━━━${NC}\n"

cat > "${OUTPUT_DIR}/INSTALLATION.md" << 'EOF'
# Mirror Mode Installation Guide

## Overview

This guide will help you complete the Mirror Mode setup. Most configuration files have been generated automatically.

## Installation Steps

### 1. Configure DNS Records ⚠️ MANUAL STEP

Open `dns-records.txt` and add the records to your DNS provider.

**Critical**: The SRV record is REQUIRED for federation!

Wait 5-10 minutes for DNS propagation, then verify:
```bash
dig SRV _matrix._tcp.YOUR_DOMAIN
```

### 2. Install SSL Certificates ⚠️ MANUAL STEP

Generate certificates for your domains:

```bash
# Install certbot
sudo apt-get update
sudo apt-get install certbot

# Get certificates
sudo certbot certonly --standalone -d matrix.YOUR_DOMAIN
sudo certbot certonly --standalone -d element.YOUR_DOMAIN

# Or if you have a wildcard cert, use that
```

### 3. Install Nginx Configuration ⚠️ MANUAL STEP

```bash
# Copy nginx config
sudo cp nginx-matrix.conf /etc/nginx/sites-available/matrix

# Edit SSL certificate paths if needed
sudo nano /etc/nginx/sites-available/matrix

# Enable site
sudo ln -s /etc/nginx/sites-available/matrix /etc/nginx/sites-enabled/

# Test configuration
sudo nginx -t

# Reload nginx
sudo systemctl reload nginx
```

### 4. Start Matrix Services ✅ AUTOMATED

```bash
# Start Matrix containers
docker-compose -f docker-compose.yml -f docker-compose.matrix.yml up -d

# Check status
docker-compose ps

# Check logs
docker-compose logs synapse
docker-compose logs synapse-db
```

Wait for health checks to pass (30-60 seconds).

### 5. Install/Configure Mattermost Plugin ✅ MOSTLY AUTOMATED

The plugin configuration has been updated automatically. To verify:

1. Go to System Console → Plugins → Matrix Bridge
2. Verify settings match `config-summary.txt`
3. If needed, manually update:
   - Matrix Server URL: `http://synapse:8008`
   - Matrix Server Domain: (your server_name)
   - Application Service Token: (from config-summary.txt)
   - Homeserver Token: (from config-summary.txt)
   - Enable Mirror Mode: ✓
   - Mirror Mode Password: `Matrix123!`
   - Sync User Profiles: ✓
   - Enable Sync: ✓

4. Click Save

### 6. Restart Services

```bash
# Restart Mattermost to reload plugin
docker-compose restart mattermost

# Or just disable/enable plugin
docker exec mattermost mattermost plugin disable com.mattermost.plugin-matrix-bridge
docker exec mattermost mattermost plugin enable com.mattermost.plugin-matrix-bridge
```

### 7. Backfill Existing Data

In any Mattermost channel, run:
```
/matrix backfill users
/matrix backfill channels
```

This creates Matrix accounts for all users and rooms for all channels.

### 8. Test the Setup

#### Test Connection
In Mattermost:
```
/matrix test
```

Should show all green checkmarks.

#### Test Element Login
1. Go to https://element.YOUR_DOMAIN
2. Click "Edit" on homeserver
3. Enter: https://matrix.YOUR_DOMAIN
4. Login with:
   - Username: (your Mattermost username)
   - Password: Matrix123!

#### Test Message Sync
1. Post a message in Mattermost
2. Check Element - should appear
3. Post in Element
4. Check Mattermost - should appear

#### Test Federation
1. In Element, search: @user:matrix.org
2. Start a conversation
3. Verify messages flow

### 9. Security Hardening ⚠️ IMPORTANT

After testing:

1. **Change Default Password**
   Users should change their Matrix password immediately

2. **Firewall Configuration**
   ```bash
   # Allow federation
   sudo ufw allow 8448/tcp
   
   # Verify
   sudo ufw status
   ```

3. **Review Federation Settings**
   Edit `homeserver.yaml` if you want to restrict federation

4. **Monitor Logs**
   ```bash
   docker-compose logs -f synapse
   ```

## Verification Checklist

- [ ] DNS records added and verified
- [ ] SSL certificates installed
- [ ] Nginx configured and running
- [ ] Matrix containers healthy
- [ ] Plugin configured
- [ ] `/matrix test` passes
- [ ] Can login to Element
- [ ] Messages sync both ways
- [ ] Federation works (tested with matrix.org)
- [ ] Firewall configured
- [ ] Users informed about password

## Troubleshooting

### Plugin Can't Connect to Matrix
**Symptom**: `/matrix test` shows connection refused

**Fix**: Ensure Matrix Server URL is `http://synapse:8008` (not localhost)

### Room Creation Fails
**Symptom**: "M_EXCLUSIVE" error

**Fix**: Registration file is correct and Synapse restarted after adding it

### Federation Not Working
**Symptom**: Can't contact other servers

**Checklist**:
1. SRV record exists: `dig SRV _matrix._tcp.YOUR_DOMAIN`
2. Port 8448 open: `telnet matrix.YOUR_DOMAIN 8448`
3. SSL valid: `openssl s_client -connect matrix.YOUR_DOMAIN:8448`
4. Test: https://federationtester.matrix.org/

### Can't Login to Element
**Symptom**: Invalid username/password

**Solutions**:
1. User must exist (run `/matrix backfill users`)
2. Password is default: Matrix123!
3. Homeserver URL correct in Element

## Support

- Check logs: `docker-compose logs synapse`
- Plugin logs: System Console → Logs
- Matrix documentation: https://matrix-org.github.io/synapse/
- Federation tester: https://federationtester.matrix.org/

## Next Steps

- Set up monitoring (Prometheus + Grafana)
- Configure TURN server for VoIP
- Set up Synapse workers for scale
- Implement SSO/SAML
- Configure media retention
EOF

echo -e "${GREEN}✓${NC} Created installation guide"

# ============================================================================
# Summary and Next Steps
# ============================================================================

echo -e "\n${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${GREEN}                   SETUP COMPLETE!                        ${NC}"
echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n"

echo -e "${BLUE}📁 All files created in: ${CYAN}${OUTPUT_DIR}${NC}\n"

echo -e "${YELLOW}⚠️  MANUAL STEPS REQUIRED:${NC}\n"

echo -e "${RED}1. ADD DNS RECORDS${NC} (CRITICAL for federation)"
echo -e "   Open: ${CYAN}${OUTPUT_DIR}/dns-records.txt${NC}"
echo -e "   Copy the records to your DNS provider (Cloudflare, etc.)"
echo -e "   ${BLUE}→ This file has copy-paste friendly formats${NC}\n"

echo -e "${RED}2. INSTALL SSL CERTIFICATES${NC}"
echo -e "   Run: ${CYAN}sudo certbot certonly --standalone -d ${MATRIX_DOMAIN}${NC}"
echo -e "   Run: ${CYAN}sudo certbot certonly --standalone -d ${ELEMENT_DOMAIN}${NC}\n"

echo -e "${RED}3. INSTALL NGINX CONFIG${NC}"
echo -e "   Run: ${CYAN}sudo cp ${OUTPUT_DIR}/nginx-matrix.conf /etc/nginx/sites-available/matrix${NC}"
echo -e "   Run: ${CYAN}sudo ln -s /etc/nginx/sites-available/matrix /etc/nginx/sites-enabled/${NC}"
echo -e "   Run: ${CYAN}sudo nginx -t && sudo systemctl reload nginx${NC}\n"

echo -e "${YELLOW}✅ AUTOMATED STEPS (just run):${NC}\n"

echo -e "${GREEN}4. START MATRIX SERVICES${NC}"
echo -e "   Run: ${CYAN}cd ${SCRIPT_DIR}${NC}"
echo -e "   Run: ${CYAN}docker-compose -f docker-compose.yml -f ${OUTPUT_DIR}/docker-compose.matrix.yml up -d${NC}\n"

echo -e "${GREEN}5. BACKFILL DATA${NC}"
echo -e "   In Mattermost, run: ${CYAN}/matrix backfill users${NC}"
echo -e "   In Mattermost, run: ${CYAN}/matrix backfill channels${NC}\n"

echo -e "${BLUE}📖 Full instructions: ${CYAN}${OUTPUT_DIR}/INSTALLATION.md${NC}\n"

echo -e "${YELLOW}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${YELLOW}Quick Start Commands:${NC}\n"
echo -e "  cd ${OUTPUT_DIR}"
echo -e "  cat dns-records.txt          # Copy DNS records"
echo -e "  cat INSTALLATION.md          # Read full guide"
echo -e "  cat config-summary.txt       # View all tokens/config"
echo -e "${YELLOW}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n"

# Save execution log
cat > "${OUTPUT_DIR}/setup.log" << EOF
Setup completed: $(date)
Domain: ${DOMAIN}
Matrix: ${MATRIX_DOMAIN}
Element: ${ELEMENT_DOMAIN}
Server Name: ${BASE_DOMAIN}

Files created:
$(ls -1 ${OUTPUT_DIR}/)

Configuration files:
$(ls -1 ${CONFIG_DIR}/)
EOF

echo -e "${GREEN}✓${NC} Setup log saved to: ${OUTPUT_DIR}/setup.log\n"
