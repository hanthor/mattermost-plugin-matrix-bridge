#!/bin/bash
set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

echo -e "${CYAN}"
cat << "EOF"
╔═══════════════════════════════════════════════════════╗
║                                                       ║
║   Production Mattermost Setup - lkofoss.club         ║
║                                                       ║
╚═══════════════════════════════════════════════════════╝
EOF
echo -e "${NC}"

# Configuration
CHAT_DOMAIN="chat.lkofoss.club"
BASE_DOMAIN="lkofoss.club"
SERVER_IP="77.42.94.83"
MATTERMOST_VERSION="latest"
POSTGRES_VERSION="15-alpine"

# Directories
SETUP_DIR="$(pwd)/production-setup"
mkdir -p "$SETUP_DIR"

echo -e "${BLUE}This will set up a production Mattermost server on:${NC}"
echo -e "  Mattermost: ${CYAN}https://${CHAT_DOMAIN}${NC}"
echo -e "  Base:       ${CYAN}${BASE_DOMAIN}${NC}"
echo -e "  Server:     ${CYAN}${SERVER_IP}${NC}"
echo -e "  Wildcard:   ${CYAN}*.${BASE_DOMAIN}${NC}\n"

read -p "Continue? (y/n): " CONFIRM
if [ "$CONFIRM" != "y" ]; then
    exit 0
fi

# Generate passwords
echo -e "\n${YELLOW}━━━ Generating Secrets ━━━${NC}\n"
POSTGRES_PASSWORD=$(openssl rand -hex 16)
MATTERMOST_SECRET=$(openssl rand -hex 32)
echo -e "${GREEN}✓${NC} Generated database password"
echo -e "${GREEN}✓${NC} Generated Mattermost secret"

# ============================================================================
# Create Production Docker Compose
# ============================================================================

echo -e "\n${YELLOW}━━━ Creating Docker Compose Configuration ━━━${NC}\n"

cat > "${SETUP_DIR}/docker-compose.yml" << EOF
version: '3.8'

services:
  mattermost-db:
    image: postgres:${POSTGRES_VERSION}
    container_name: mattermost-db
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    pids_limit: 100
    read_only: true
    tmpfs:
      - /tmp
      - /var/run/postgresql
    environment:
      - TZ=UTC
      - POSTGRES_USER=mmuser
      - POSTGRES_PASSWORD=${POSTGRES_PASSWORD}
      - POSTGRES_DB=mattermost
    volumes:
      - mattermost_db:/var/lib/postgresql/data
    networks:
      - mattermost

  mattermost:
    image: mattermost/mattermost-enterprise-edition:${MATTERMOST_VERSION}
    container_name: mattermost
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    pids_limit: 200
    read_only: false
    tmpfs:
      - /tmp
    environment:
      - TZ=UTC
      - MM_SQLSETTINGS_DRIVERNAME=postgres
      - MM_SQLSETTINGS_DATASOURCE=postgres://mmuser:${POSTGRES_PASSWORD}@mattermost-db:5432/mattermost?sslmode=disable&connect_timeout=10
      - MM_BLEVESETTINGS_INDEXDIR=/mattermost/bleve-indexes
      - MM_SERVICESETTINGS_SITEURL=https://${CHAT_DOMAIN}
      - MM_SERVICESETTINGS_ENABLELOCALMODE=true
      - MM_SERVICESETTINGS_ENABLEDEVELOPER=false
      - MM_LOGSETTINGS_ENABLECONSOLE=true
      - MM_LOGSETTINGS_CONSOLELEVEL=INFO
      - MM_PLUGINSETTINGS_ENABLEUPLOADS=true
      - MM_PLUGINSETTINGS_AUTOMATICPREPACKAGEDPLUGINS=true
      # Shared Channels (required for Matrix bridge)
      - MM_EXPERIMENTALSETTINGS_ENABLESHAREDCHANNELS=true
      - MM_EXPERIMENTALSETTINGS_ENABLEREMOTECLUSTERSERVICE=true
    volumes:
      - mattermost_config:/mattermost/config
      - mattermost_data:/mattermost/data
      - mattermost_logs:/mattermost/logs
      - mattermost_plugins:/mattermost/plugins
      - mattermost_client_plugins:/mattermost/client/plugins
      - mattermost_bleve:/mattermost/bleve-indexes
    ports:
      - "127.0.0.1:8065:8065"
    networks:
      - mattermost
    depends_on:
      - mattermost-db
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8065/api/v4/system/ping"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 90s

volumes:
  mattermost_db:
    driver: local
  mattermost_config:
    driver: local
  mattermost_data:
    driver: local
  mattermost_logs:
    driver: local
  mattermost_plugins:
    driver: local
  mattermost_client_plugins:
    driver: local
  mattermost_bleve:
    driver: local

networks:
  mattermost:
    driver: bridge
EOF

echo -e "${GREEN}✓${NC} Created docker-compose.yml"

# ============================================================================
# Create Nginx Configuration
# ============================================================================

echo -e "\n${YELLOW}━━━ Creating Nginx Configuration ━━━${NC}\n"

cat > "${SETUP_DIR}/nginx-mattermost.conf" << 'EOF'
upstream mattermost {
    server 127.0.0.1:8065;
    keepalive 32;
}

proxy_cache_path /var/cache/nginx/mattermost levels=1:2 keys_zone=mattermost_cache:10m max_size=3g inactive=120m use_temp_path=off;

server {
    listen 80;
    listen [::]:80;
    server_name CHAT_DOMAIN;
    
    location /.well-known/acme-challenge/ {
        root /var/www/certbot;
    }
    
    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name CHAT_DOMAIN;

    ssl_certificate /etc/letsencrypt/live/CHAT_DOMAIN/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/CHAT_DOMAIN/privkey.pem;
    ssl_session_timeout 1d;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;
    ssl_prefer_server_ciphers off;
    ssl_session_cache shared:SSL:50m;
    ssl_stapling on;
    ssl_stapling_verify on;

    client_max_body_size 50M;

    location ~ /api/v[0-9]+/(users/)?websocket$ {
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Frame-Options SAMEORIGIN;
        proxy_buffers 256 16k;
        proxy_buffer_size 16k;
        client_body_timeout 60;
        send_timeout 300;
        lingering_timeout 5;
        proxy_connect_timeout 90;
        proxy_send_timeout 300;
        proxy_read_timeout 90s;
        proxy_http_version 1.1;
        proxy_pass http://mattermost;
    }

    location / {
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Frame-Options SAMEORIGIN;
        proxy_buffers 256 16k;
        proxy_buffer_size 16k;
        proxy_read_timeout 600s;
        proxy_cache mattermost_cache;
        proxy_cache_revalidate on;
        proxy_cache_min_uses 2;
        proxy_cache_use_stale timeout;
        proxy_cache_lock on;
        proxy_pass http://mattermost;
    }
}
EOF

sed -i "s/CHAT_DOMAIN/${CHAT_DOMAIN}/g" "${SETUP_DIR}/nginx-mattermost.conf"

echo -e "${GREEN}✓${NC} Created nginx configuration"

# ============================================================================
# Create User Population Script
# ============================================================================

echo -e "\n${YELLOW}━━━ Creating User Population Script ━━━${NC}\n"

cat > "${SETUP_DIR}/populate-users.sh" << 'POPULATESCRIPT'
#!/bin/bash
set -e

# Configuration
MATTERMOST_URL="https://CHAT_DOMAIN"
ADMIN_EMAIL="admin@BASE_DOMAIN"
ADMIN_PASSWORD="admin123"

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${YELLOW}Populating Mattermost with users and channels...${NC}\n"

# Login as admin
echo "Logging in..."
LOGIN_RESPONSE=$(curl -s -X POST "${MATTERMOST_URL}/api/v4/users/login" \
  -H "Content-Type: application/json" \
  -d "{\"login_id\":\"${ADMIN_EMAIL}\",\"password\":\"${ADMIN_PASSWORD}\"}")

TOKEN=$(echo "$LOGIN_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin).get('id', ''))" 2>/dev/null || echo "")

if [ -z "$TOKEN" ]; then
    echo "Failed to login. Create admin user first:"
    echo "  docker exec mattermost mattermost user create --email ${ADMIN_EMAIL} --username admin --password ${ADMIN_PASSWORD} --system-admin"
    exit 1
fi

echo -e "${GREEN}✓ Logged in${NC}"

# Create teams
echo -e "\nCreating teams..."
TEAMS=("engineering" "sales" "marketing" "support")
TEAM_IDS=()

for team in "${TEAMS[@]}"; do
    RESPONSE=$(curl -s -X POST "${MATTERMOST_URL}/api/v4/teams" \
      -H "Authorization: Bearer ${TOKEN}" \
      -H "Content-Type: application/json" \
      -d "{\"name\":\"${team}\",\"display_name\":\"${team^}\",\"type\":\"O\"}")
    
    TEAM_ID=$(echo "$RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin).get('id', ''))" 2>/dev/null || echo "")
    if [ -n "$TEAM_ID" ]; then
        TEAM_IDS+=("$TEAM_ID")
        echo -e "${GREEN}✓${NC} Created team: ${team}"
    fi
done

# Create users
echo -e "\nCreating users..."
USERS=(
    "alice:alice@DOMAIN:Alice Smith"
    "bob:bob@DOMAIN:Bob Jones"
    "charlie:charlie@DOMAIN:Charlie Brown"
    "diana:diana@DOMAIN:Diana Prince"
    "eve:eve@DOMAIN:Eve Adams"
    "frank:frank@DOMAIN:Frank Castle"
    "grace:grace@DOMAIN:Grace Hopper"
    "henry:henry@DOMAIN:Henry Ford"
)

USER_IDS=()
for user in "${USERS[@]}"; do
    IFS=':' read -r username email fullname <<< "$user"
    
    RESPONSE=$(curl -s -X POST "${MATTERMOST_URL}/api/v4/users" \
      -H "Authorization: Bearer ${TOKEN}" \
      -H "Content-Type: application/json" \
      -d "{\"email\":\"${email}\",\"username\":\"${username}\",\"password\":\"password123\",\"first_name\":\"${fullname%% *}\",\"last_name\":\"${fullname#* }\"}")
    
    USER_ID=$(echo "$RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin).get('id', ''))" 2>/dev/null || echo "")
    if [ -n "$USER_ID" ]; then
        USER_IDS+=("$USER_ID")
        echo -e "${GREEN}✓${NC} Created user: ${username} (${email})"
        
        # Add user to first team
        if [ ${#TEAM_IDS[@]} -gt 0 ]; then
            curl -s -X POST "${MATTERMOST_URL}/api/v4/teams/${TEAM_IDS[0]}/members" \
              -H "Authorization: Bearer ${TOKEN}" \
              -H "Content-Type: application/json" \
              -d "{\"team_id\":\"${TEAM_IDS[0]}\",\"user_id\":\"${USER_ID}\"}" > /dev/null
        fi
    fi
done

# Create channels
echo -e "\nCreating channels..."
if [ ${#TEAM_IDS[@]} -gt 0 ]; then
    CHANNELS=("general" "random" "dev-updates" "water-cooler" "announcements" "project-alpha" "project-beta")
    
    for channel in "${CHANNELS[@]}"; do
        RESPONSE=$(curl -s -X POST "${MATTERMOST_URL}/api/v4/channels" \
          -H "Authorization: Bearer ${TOKEN}" \
          -H "Content-Type: application/json" \
          -d "{\"team_id\":\"${TEAM_IDS[0]}\",\"name\":\"${channel}\",\"display_name\":\"${channel^}\",\"type\":\"O\"}")
        
        CHANNEL_ID=$(echo "$RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin).get('id', ''))" 2>/dev/null || echo "")
        if [ -n "$CHANNEL_ID" ]; then
            echo -e "${GREEN}✓${NC} Created channel: ${channel}"
            
            # Add some users to channel
            for user_id in "${USER_IDS[@]:0:5}"; do
                curl -s -X POST "${MATTERMOST_URL}/api/v4/channels/${CHANNEL_ID}/members" \
                  -H "Authorization: Bearer ${TOKEN}" \
                  -H "Content-Type: application/json" \
                  -d "{\"user_id\":\"${user_id}\"}" > /dev/null
            done
        fi
    done
fi

echo -e "\n${GREEN}✓ Population complete!${NC}"
echo -e "\nTest accounts created:"
echo "  admin@DOMAIN / admin123 (System Admin)"
for user in "${USERS[@]}"; do
    IFS=':' read -r username email _ <<< "$user"
    echo "  ${email} / password123"
done
POPULATESCRIPT

sed -i "s/CHAT_DOMAIN/${CHAT_DOMAIN}/g" "${SETUP_DIR}/populate-users.sh"
sed -i "s/BASE_DOMAIN/${BASE_DOMAIN}/g" "${SETUP_DIR}/populate-users.sh"
chmod +x "${SETUP_DIR}/populate-users.sh"

echo -e "${GREEN}✓${NC} Created user population script"

# ============================================================================
# Create Installation Script
# ============================================================================

echo -e "\n${YELLOW}━━━ Creating Installation Script ━━━${NC}\n"

cat > "${SETUP_DIR}/install.sh" << 'INSTALLSCRIPT'
#!/bin/bash
set -e

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${YELLOW}Installing Mattermost on chat.lkofoss.club${NC}\n"

# Check if running as root
if [ "$EUID" -ne 0 ]; then 
    echo -e "${RED}Please run as root (sudo)${NC}"
    exit 1
fi

# Detect OS
if [ -f /etc/almalinux-release ]; then
    PKG_MANAGER="dnf"
elif [ -f /etc/redhat-release ]; then
    PKG_MANAGER="yum"
elif [ -f /etc/debian_version ]; then
    PKG_MANAGER="apt-get"
else
    PKG_MANAGER="dnf"
fi

# Update system
echo "Updating system..."
if [ "$PKG_MANAGER" = "apt-get" ]; then
    apt-get update
    apt-get upgrade -y
else
    $PKG_MANAGER update -y
    $PKG_MANAGER upgrade -y
fi

# Install Docker
if ! command -v docker &> /dev/null; then
    echo "Installing Docker..."
    curl -fsSL https://get.docker.com -o get-docker.sh
    sh get-docker.sh
    rm get-docker.sh
    systemctl enable docker
    systemctl start docker
fi

# Install Docker Compose
if ! command -v docker-compose &> /dev/null; then
    echo "Installing Docker Compose..."
    curl -L "https://github.com/docker/compose/releases/latest/download/docker-compose-$(uname -s)-$(uname -m)" -o /usr/local/bin/docker-compose
    chmod +x /usr/local/bin/docker-compose
fi

# Install Nginx
if ! command -v nginx &> /dev/null; then
    echo "Installing Nginx..."
    if [ "$PKG_MANAGER" = "apt-get" ]; then
        apt-get install -y nginx
    else
        $PKG_MANAGER install -y nginx
    fi
    systemctl enable nginx
    systemctl start nginx
fi

# Install Certbot
if ! command -v certbot &> /dev/null; then
    echo "Installing Certbot..."
    if [ "$PKG_MANAGER" = "apt-get" ]; then
        apt-get install -y certbot python3-certbot-nginx
    else
        $PKG_MANAGER install -y certbot python3-certbot-nginx
    fi
fi

echo -e "${GREEN}✓ Prerequisites installed${NC}\n"

# Get SSL certificate
echo "Getting SSL certificate..."
certbot certonly --nginx -d chat.lkofoss.club --non-interactive --agree-tos --email admin@lkofoss.club || true

# Install Nginx config
echo "Installing Nginx configuration..."
cp nginx-mattermost.conf /etc/nginx/sites-available/mattermost
ln -sf /etc/nginx/sites-available/mattermost /etc/nginx/sites-enabled/
rm -f /etc/nginx/sites-enabled/default
nginx -t
systemctl reload nginx

# Start Mattermost
echo "Starting Mattermost..."
docker-compose up -d

# Wait for Mattermost to be healthy
echo "Waiting for Mattermost to start..."
sleep 30

# Create admin user
echo -e "\nCreating admin user..."
docker exec mattermost mattermost user create \
  --email admin@lkofoss.club \
  --username admin \
  --password admin123 \
  --system-admin || echo "Admin user may already exist"

echo -e "\n${GREEN}✓ Installation complete!${NC}\n"
echo "Access Mattermost at: https://chat.lkofoss.club"
echo "Login with: admin@lkofoss.club / admin123"
echo -e "\nNext steps:"
echo "  1. Login and complete setup wizard"
echo "  2. Run: ./populate-users.sh"
echo "  3. Configure OAuth with Auth0 (see auth0-setup.md)"
INSTALLSCRIPT

chmod +x "${SETUP_DIR}/install.sh"

echo -e "${GREEN}✓${NC} Created installation script"

# ============================================================================
# Create Auth0 Setup Guide
# ============================================================================

echo -e "\n${YELLOW}━━━ Creating Auth0 Setup Guide ━━━${NC}\n"

cat > "${SETUP_DIR}/auth0-setup.md" << 'EOF'
# Auth0 OAuth Setup for Mattermost

## Step 1: Create Auth0 Application

1. Go to [Auth0 Dashboard](https://manage.auth0.com/)
2. Click **Applications** → **Create Application**
3. Name: `Mattermost lkofoss.club`
4. Type: **Regular Web Application**
5. Click **Create**

## Step 2: Configure Application Settings

In the application settings:

### Basic Information
- **Name**: Mattermost lkofoss.club
- **Domain**: (note this, e.g., `your-tenant.auth0.com`)

### Application URIs
- **Allowed Callback URLs**:
  ```
  https://chat.lkofoss.club/signup/gitlab/complete
  ```

- **Allowed Logout URLs**:
  ```
  https://chat.lkofoss.club
  ```

- **Allowed Web Origins**:
  ```
  https://chat.lkofoss.club
  ```

### Advanced Settings → OAuth
- **JsonWebToken Signature Algorithm**: RS256
- **OIDC Conformant**: Enabled

Click **Save Changes**

## Step 3: Get Credentials

From the application settings, copy:
- **Client ID**: `YOUR_CLIENT_ID`
- **Client Secret**: `YOUR_CLIENT_SECRET`
- **Domain**: `YOUR_DOMAIN.auth0.com`

## Step 4: Configure Mattermost

### Via System Console

1. Go to **System Console** → **Authentication** → **OAuth 2.0**
2. Select **GitLab** (we'll use GitLab provider for Auth0)
3. Configure:
   - **Enable authentication**: true
   - **Application ID**: (Your Auth0 Client ID)
   - **Application Secret Key**: (Your Auth0 Client Secret)
   - **Auth Endpoint**: `https://YOUR_DOMAIN.auth0.com/authorize`
   - **Token Endpoint**: `https://YOUR_DOMAIN.auth0.com/oauth/token`
   - **User API Endpoint**: `https://YOUR_DOMAIN.auth0.com/userinfo`

### Via Environment Variables

Add to `docker-compose.yml`:

```yaml
environment:
  - MM_GITLABSETTINGS_ENABLE=true
  - MM_GITLABSETTINGS_ID=YOUR_CLIENT_ID
  - MM_GITLABSETTINGS_SECRET=YOUR_CLIENT_SECRET
  - MM_GITLABSETTINGS_AUTHENDPOINT=https://YOUR_DOMAIN.auth0.com/authorize
  - MM_GITLABSETTINGS_TOKENENDPOINT=https://YOUR_DOMAIN.auth0.com/oauth/token
  - MM_GITLABSETTINGS_USERAPIENDPOINT=https://YOUR_DOMAIN.auth0.com/userinfo
```

Restart Mattermost:
```bash
docker-compose restart mattermost
```

## Step 5: Test Login

1. Go to https://lkofoss.club
2. Click **GitLab** button (shows as Auth0)
3. Login with Auth0 credentials
4. Should redirect back to Mattermost

## Step 6: User Management in Auth0

### Create Test Users

1. Go to **User Management** → **Users**
2. Click **Create User**
3. Add users with emails matching your domain

### Configure User Metadata

To sync display names:
1. Go to **Auth Profile** → **Rules**
2. Create rule to add user metadata:

```javascript
function (user, context, callback) {
  user.app_metadata = user.app_metadata || {};
  user.app_metadata.mattermost_username = user.nickname || user.email.split('@')[0];
  
  callback(null, user, context);
}
```

## Alternative: OpenID Connect (Better)

For better integration, use OpenID Connect instead of OAuth:

### Mattermost Configuration

1. Go to **System Console** → **Authentication** → **OpenID Connect**
2. Configure:
   - **Enable**: true
   - **Button Name**: Auth0
   - **Button Color**: #EB5424
   - **Discovery Endpoint**: `https://YOUR_DOMAIN.auth0.com/.well-known/openid-configuration`
   - **Client ID**: YOUR_CLIENT_ID
   - **Client Secret**: YOUR_CLIENT_SECRET

### Environment Variables

```yaml
environment:
  - MM_OPENIDSETTINGS_ENABLE=true
  - MM_OPENIDSETTINGS_BUTTONNAME=Auth0
  - MM_OPENIDSETTINGS_BUTTONCOLOR=#EB5424
  - MM_OPENIDSETTINGS_DISCOVERYENDPOINT=https://YOUR_DOMAIN.auth0.com/.well-known/openid-configuration
  - MM_OPENIDSETTINGS_ID=YOUR_CLIENT_ID
  - MM_OPENIDSETTINGS_SECRET=YOUR_CLIENT_SECRET
```

## Troubleshooting

### "OAuth Error: invalid_request"
- Check callback URL matches exactly
- Ensure application is enabled in Auth0

### "User not found"
- Enable auto-create accounts in Mattermost
- System Console → Authentication → Email → Enable Account Creation: false
- System Console → Authentication → OAuth 2.0 → Automatic Account Creation: true

### Users can't login
- Check Auth0 application is not blocked
- Verify user has correct email domain
- Check Mattermost logs: `docker logs mattermost`

## Migration to Matrix Authentication Service

Once Matrix is set up with mirror mode, you can migrate to Matrix Authentication Service (MAS):

1. Users will use Matrix credentials (@user:lkofoss.club)
2. Single sign-on across Mattermost and Matrix
3. No need for separate Auth0 integration

See: https://github.com/matrix-org/matrix-authentication-service
EOF

echo -e "${GREEN}✓${NC} Created Auth0 setup guide"

# ============================================================================
# Create Deployment README
# ============================================================================

echo -e "\n${YELLOW}━━━ Creating Deployment Guide ━━━${NC}\n"

cat > "${SETUP_DIR}/README.md" << 'EOF'
# Production Mattermost Deployment - lkofoss.club

## Quick Start

### 1. Upload Files to Server

```bash
# From your local machine
scp -r production-setup root@77.42.94.83:/root/

# SSH to server
ssh root@77.42.94.83
cd /root/production-setup
```

### 2. Run Installation

```bash
sudo ./install.sh
```

This will:
- ✅ Install Docker & Docker Compose
- ✅ Install Nginx
- ✅ Get SSL certificate for lkofoss.club and *.lkofoss.club
- ✅ Start Mattermost
- ✅ Create admin user

### 3. Complete Setup

1. Access: https://lkofoss.club
2. Login: admin@lkofoss.club / admin123
3. Complete setup wizard

### 4. Populate Test Data

```bash
./populate-users.sh
```

This creates:
- 4 teams (engineering, sales, marketing, support)
- 8 test users
- 7 channels with members

### 5. Configure Auth0 (Optional)

Follow instructions in `auth0-setup.md`

## DNS Configuration

Ensure these DNS records exist:

```
lkofoss.club         A      77.42.94.83
*.lkofoss.club       A      77.42.94.83
```

Verify:
```bash
dig lkofoss.club
dig matrix.lkofoss.club
```

## Architecture

```
┌─────────────────────────────────────────────┐
│  Internet (https://lkofoss.club)            │
└─────────────────┬───────────────────────────┘
                  │
┌─────────────────▼───────────────────────────┐
│  Nginx (Port 443)                           │
│  - SSL Termination                          │
│  - Reverse Proxy                            │
│  - WebSocket Support                        │
└─────────────────┬───────────────────────────┘
                  │
┌─────────────────▼───────────────────────────┐
│  Mattermost (Port 8065)                     │
│  - Application Server                       │
│  - Plugin System                            │
│  - Shared Channels Enabled                  │
└─────────────────┬───────────────────────────┘
                  │
┌─────────────────▼───────────────────────────┐
│  PostgreSQL (Port 5432)                     │
│  - User Data                                │
│  - Messages                                 │
│  - Configuration                            │
└─────────────────────────────────────────────┘
```

## File Structure

```
production-setup/
├── docker-compose.yml          # Docker services
├── nginx-mattermost.conf       # Nginx configuration
├── install.sh                  # Installation script
├── populate-users.sh           # User population script
├── auth0-setup.md             # Auth0 OAuth guide
└── README.md                  # This file
```

## Next Steps: Adding Matrix

Once Mattermost is running smoothly:

1. **Test Mirror Mode Setup Script**
   ```bash
   cd /path/to/mattermost-plugin-matrix-bridge
   ./scripts/setup-mirror-mode.sh
   ```

2. **Follow Generated Instructions**
   - DNS records for matrix.lkofoss.club
   - SSL certificates
   - Matrix services start

3. **Document Issues**
   - Any configuration problems
   - Missing steps
   - Unclear instructions

4. **Improve Scripts**
   - Fix bugs found
   - Add better error handling
   - Update documentation

## Useful Commands

### View Logs
```bash
docker-compose logs -f mattermost
docker-compose logs -f mattermost-db
```

### Restart Services
```bash
docker-compose restart mattermost
docker-compose restart
```

### Backup Database
```bash
docker exec mattermost-db pg_dump -U mmuser mattermost > backup.sql
```

### CLI Access
```bash
docker exec -it mattermost mattermost --help
docker exec -it mattermost mattermost user list
docker exec -it mattermost mattermost team list
```

### Reset Admin Password
```bash
docker exec mattermost mattermost user resetpassword admin@lkofoss.club admin123
```

## Monitoring

### Health Check
```bash
curl https://lkofoss.club/api/v4/system/ping
```

### System Status
```bash
docker ps
docker stats
```

### Disk Usage
```bash
docker system df
```

## Troubleshooting

### Can't Access Website
- Check DNS: `dig lkofoss.club`
- Check firewall: `ufw status`
- Check nginx: `systemctl status nginx`
- Check Docker: `docker ps`

### SSL Certificate Issues
```bash
certbot certificates
certbot renew --dry-run
```

### Database Connection Failed
```bash
docker logs mattermost-db
docker exec mattermost-db psql -U mmuser -d mattermost -c "SELECT 1"
```

### Plugin Upload Fails
- Check file size limit in nginx config
- Ensure shared channels enabled
- Check permissions: `docker exec mattermost ls -la /mattermost/plugins`

## Security Checklist

- [ ] Change default admin password
- [ ] Configure firewall (ufw)
- [ ] Set up automatic SSL renewal
- [ ] Enable rate limiting in Mattermost
- [ ] Configure backup schedule
- [ ] Review Nginx security headers
- [ ] Enable audit logging
- [ ] Configure session timeouts

## Support

- Mattermost Docs: https://docs.mattermost.com/
- Docker Docs: https://docs.docker.com/
- Nginx Docs: https://nginx.org/en/docs/
- Auth0 Docs: https://auth0.com/docs
EOF

echo -e "${GREEN}✓${NC} Created deployment guide"

# ============================================================================
# Save Configuration
# ============================================================================

cat > "${SETUP_DIR}/config.env" << EOF
# Production Configuration
CHAT_DOMAIN=${CHAT_DOMAIN}
BASE_DOMAIN=${BASE_DOMAIN}
SERVER_IP=${SERVER_IP}
POSTGRES_PASSWORD=${POSTGRES_PASSWORD}
MATTERMOST_SECRET=${MATTERMOST_SECRET}
MATTERMOST_VERSION=${MATTERMOST_VERSION}

# Generated: $(date)
EOF

echo -e "${GREEN}✓${NC} Saved configuration"

# ============================================================================
# Summary
# ============================================================================

echo -e "\n${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${GREEN}         Production Setup Files Created!                  ${NC}"
echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n"

echo -e "${BLUE}📁 Files created in: ${CYAN}${SETUP_DIR}${NC}\n"

echo -e "${YELLOW}📋 Deployment Steps:${NC}\n"

echo -e "${GREEN}1. Upload to Server${NC}"
echo -e "   ${CYAN}scp -r ${SETUP_DIR} root@${SERVER_IP}:/root/${NC}\n"

echo -e "${GREEN}2. SSH to Server${NC}"
echo -e "   ${CYAN}ssh root@${SERVER_IP}${NC}\n"

echo -e "${GREEN}3. Run Installation${NC}"
echo -e "   ${CYAN}cd /root/production-setup${NC}"
echo -e "   ${CYAN}sudo ./install.sh${NC}\n"

echo -e "${GREEN}4. Populate Users${NC}"
echo -e "   ${CYAN}./populate-users.sh${NC}\n"

echo -e "${GREEN}5. Configure Auth0${NC}"
echo -e "   ${CYAN}Follow auth0-setup.md${NC}\n"

echo -e "${GREEN}6. Test Mirror Mode Setup${NC}"
echo -e "   ${CYAN}Run setup-mirror-mode.sh on this server${NC}\n"

echo -e "${YELLOW}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${YELLOW}Files:${NC}"
echo -e "  docker-compose.yml       - Mattermost + PostgreSQL"
echo -e "  nginx-mattermost.conf    - Nginx configuration"
echo -e "  install.sh               - Automated installation"
echo -e "  populate-users.sh        - Create test users/channels"
echo -e "  auth0-setup.md          - OAuth setup guide"
echo -e "  README.md               - Full documentation"
echo -e "${YELLOW}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n"

echo -e "${CYAN}After deployment, access at:${NC}"
echo -e "  https://${CHAT_DOMAIN}"
echo -e "  Login: admin@${BASE_DOMAIN} / admin123\n"
