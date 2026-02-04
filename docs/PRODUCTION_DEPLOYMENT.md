# Production Deployment Guide - lkofoss.club

## Overview

This guide walks through deploying a production Mattermost server on **77.42.94.83** with domain **lkofoss.club**, then adding Matrix mirror mode capabilities.

**Goals:**
- Production-ready Mattermost deployment
- Pre-populated with realistic test data
- Auth0 OAuth integration
- Matrix bridge with mirror mode
- Future migration to Matrix Authentication Service
- Validate and improve setup scripts/documentation

## Architecture

```
                          Internet
                             |
                    [lkofoss.club:443]
                             |
┌────────────────────────────┴─────────────────────────────┐
│                                                           │
│  Server: 77.42.94.83                                     │
│                                                           │
│  ┌─────────────────────────────────────────────────┐    │
│  │  Nginx (SSL Termination)                        │    │
│  │  - lkofoss.club → Mattermost                    │    │
│  │  - matrix.lkofoss.club → Synapse               │    │
│  │  - element.lkofoss.club → Element              │    │
│  └─────────────────┬───────────────────────────────┘    │
│                    │                                      │
│  ┌────────────────┴────────────┬────────────────────┐   │
│  │                              │                     │   │
│  │  Mattermost:8065            │  Matrix Stack       │   │
│  │  - REST API                 │  - Synapse:8008     │   │
│  │  - WebSockets               │  - Element          │   │
│  │  - Matrix Plugin            │  - PostgreSQL       │   │
│  │                              │                     │   │
│  │  PostgreSQL:5432            │                     │   │
│  └──────────────────────────────┴────────────────────┘   │
│                                                           │
└───────────────────────────────────────────────────────────┘
```

## Phase 1: Production Mattermost Setup

### Prerequisites

- Server: AlmaLinux 9+ with root access
- DNS: `lkofoss.club` and `*.lkofoss.club` pointing to 77.42.94.83
- Ports: 80, 443, 8448 open
- Email: admin@lkofoss.club for SSL certificates

### Step 1: Generate Setup Files

From your local machine:

```bash
cd mattermost-plugin-matrix-bridge
./scripts/setup-production-mattermost.sh
```

This creates `production-setup/` directory with:
- `docker-compose.yml` - Mattermost + PostgreSQL services
- `nginx-mattermost.conf` - Nginx reverse proxy config
- `install.sh` - Automated installation script
- `populate-users.sh` - Creates test users and channels
- `auth0-setup.md` - OAuth integration guide
- `README.md` - Complete documentation

### Step 2: Upload to Server

```bash
# Upload files
scp -r production-setup root@77.42.94.83:/root/

# Connect to server
ssh root@77.42.94.83
cd /root/production-setup
```

### Step 3: Install Mattermost

```bash
sudo ./install.sh
```

**What this does:**
- ✅ Installs Docker & Docker Compose
- ✅ Installs Nginx
- ✅ Gets Let's Encrypt SSL certificates
- ✅ Configures Nginx reverse proxy
- ✅ Starts Mattermost and PostgreSQL
- ✅ Creates admin account

**Time: ~5-10 minutes**

### Step 4: Initial Configuration

1. Access https://chat.lkofoss.club
2. Login: `admin@lkofoss.club` / `admin123`
3. Complete setup wizard:
   - Organization name
   - Site URL (already set to https://lkofoss.club)
   - Enable email notifications
   - Configure SMTP (optional)

⚠️ **Important**: Change admin password immediately!

### Step 5: Populate Test Data

```bash
./populate-users.sh
```

**Creates:**
- **4 Teams**: Engineering, Sales, Marketing, Support
- **8 Users**: 
  - alice@lkofoss.club / password123
  - bob@lkofoss.club / password123
  - charlie@lkofoss.club / password123
  - diana@lkofoss.club / password123
  - eve@lkofoss.club / password123
  - frank@lkofoss.club / password123
  - grace@lkofoss.club / password123
  - henry@lkofoss.club / password123
- **7 Channels**: general, random, dev-updates, water-cooler, announcements, project-alpha, project-beta

### Step 6: Verify Installation

```bash
# Check services
docker ps

# View logs
docker-compose logs -f mattermost

# Health check
curl https://lkofoss.club/api/v4/system/ping

# Should return: {"status":"OK"}
```

## Phase 2: Auth0 OAuth Integration

### Why Auth0?

- Production-grade authentication
- Social login providers
- User management dashboard
- Good test case for enterprise SSO
- Later: Compare with Matrix Authentication Service

### Setup Steps

Follow detailed instructions in `production-setup/auth0-setup.md`:

1. **Create Auth0 Application**
   - Type: Regular Web Application
   - Name: Mattermost lkofoss.club

2. **Configure Callbacks**
   - Allowed Callback: `https://chat.lkofoss.club/signup/gitlab/complete`
   - Allowed Logout: `https://chat.lkofoss.club`

3. **Get Credentials**
   - Client ID
   - Client Secret
   - Domain (e.g., `your-tenant.auth0.com`)

4. **Configure Mattermost**
   - System Console → Authentication → OAuth 2.0
   - Use GitLab provider for Auth0
   - Or use OpenID Connect (recommended)

5. **Test Login**
   - Logout from admin
   - Click OAuth button
   - Login with Auth0

### Alternative: OpenID Connect

For better integration:

```yaml
# Add to docker-compose.yml environment
- MM_OPENIDSETTINGS_ENABLE=true
- MM_OPENIDSETTINGS_BUTTONNAME=Auth0
- MM_OPENIDSETTINGS_DISCOVERYENDPOINT=https://YOUR_DOMAIN.auth0.com/.well-known/openid-configuration
- MM_OPENIDSETTINGS_ID=YOUR_CLIENT_ID
- MM_OPENIDSETTINGS_SECRET=YOUR_CLIENT_SECRET
```

## Phase 3: Matrix Bridge Setup

### Prerequisites

- ✅ Mattermost running successfully
- ✅ Users and channels populated
- ✅ Auth0 configured (optional)
- ✅ Ready to test mirror mode

### Step 1: Prepare DNS

Add DNS records for Matrix:

```
matrix.lkofoss.club         A      77.42.94.83
element.lkofoss.club        A      77.42.94.83
lkofoss.club                SRV    10 0 8448 matrix.lkofoss.club
```

Verify:
```bash
dig matrix.lkofoss.club
dig element.lkofoss.club
dig _matrix._tcp.lkofoss.club SRV
```

### Step 2: Run Mirror Mode Setup

```bash
cd /root/mattermost-plugin-matrix-bridge
./scripts/setup-mirror-mode.sh
```

**Input Required:**
- Mattermost domain: `lkofoss.club`
- Admin username: `admin`
- Admin password: (your password)

**What this does:**
- 🔐 Generates all tokens (AS, HS, registration secret)
- 📝 Creates Matrix configuration files
- 🐳 Generates docker-compose for Matrix services
- 🌐 Creates Element web client config
- 🔧 Updates Mattermost plugin configuration via API
- 📋 Generates DNS records (if not done)
- 📄 Creates installation guide

**Output Directory:** `mirror-mode-setup/`

### Step 3: Install Matrix Services

```bash
cd mirror-mode-setup

# Get SSL certificates for Matrix domains
sudo certbot certonly --nginx -d matrix.lkofoss.club -d element.lkofoss.club

# Install Nginx configs
sudo cp nginx-matrix.conf /etc/nginx/sites-available/matrix
sudo ln -sf /etc/nginx/sites-available/matrix /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx

# Start Matrix services
docker-compose -f docker-compose.matrix.yml up -d

# Wait for Synapse to start
sleep 30

# Check status
docker-compose -f docker-compose.matrix.yml ps
```

### Step 4: Configure Matrix Plugin

**Auto-configured by script:**
- Enable Mirror Mode: ✅
- Matrix Server URL: https://matrix.lkofoss.club
- Matrix Server Domain: lkofoss.club
- Matrix Bot User: @mattermost:lkofoss.club
- Application Service tokens

**Verify in Mattermost:**
1. System Console → Plugins → Matrix Bridge
2. Check all settings match `config-summary.txt`
3. Click "Test Connection" (once available)

### Step 5: Upload Plugin

Build and upload the Matrix plugin:

```bash
# On your local machine
cd mattermost-plugin-matrix-bridge
make dist

# Upload to server
scp dist/com.mattermost.matrix-bridge-*.tar.gz root@77.42.94.83:/tmp/

# Install via CLI
ssh root@77.42.94.83
docker exec mattermost mattermost plugin add /tmp/com.mattermost.matrix-bridge-*.tar.gz
docker exec mattermost mattermost plugin enable com.mattermost.matrix-bridge
```

### Step 6: Test Mirror Mode

1. **Create a new channel** in Mattermost
   - Should auto-create Matrix room
   - Alias: `#engineering-test-channel:lkofoss.club`

2. **Verify on Matrix side:**
   ```bash
   # Check room was created
   docker exec synapse curl -s http://localhost:8008/_matrix/client/v3/directory/room/%23engineering-test-channel:lkofoss.club
   ```

3. **Login to Element:**
   - Go to https://element.lkofoss.club
   - Login with Mattermost credentials
   - Username: `alice` (becomes @alice:lkofoss.club)
   - Password: (configured in Mirror Mode, default: "password")

4. **Send messages both ways:**
   - Post in Mattermost → appears in Matrix
   - Post in Matrix → appears in Mattermost

### Step 7: Test Federation

Test federation with other Matrix servers:

```bash
# Check federation is working
curl https://federationtester.matrix.org/api/report?server_name=lkofoss.club

# Join a room on another server
# As Element user: Search for #test:matrix.org
```

## Phase 4: Document Issues and Improvements

### Testing Checklist

As you use the system, document:

- [ ] Installation issues encountered
- [ ] Missing steps in documentation
- [ ] Configuration errors
- [ ] Plugin behavior in production
- [ ] Mirror mode synchronization issues
- [ ] Federation problems
- [ ] Performance under load
- [ ] Auth flow problems
- [ ] User confusion points
- [ ] Missing error messages

### Feedback Template

For each issue found:

```markdown
## Issue: [Short Description]

**Context:** What were you trying to do?

**Expected:** What should have happened?

**Actual:** What actually happened?

**Logs:** Relevant error messages

**Fix:** What solved it (if resolved)?

**Documentation:** What should be added/changed?
```

### Common Issues to Watch For

1. **Mirror Mode Sync Delays**
   - Messages not appearing immediately
   - User presence not syncing
   - Profile updates delayed

2. **Room Alias Collisions**
   - Multiple teams with same channel name
   - Special characters in channel names
   - Length limits on aliases

3. **Authentication Confusion**
   - Which password to use (Mattermost vs Matrix)
   - OAuth vs native accounts
   - Password reset flows

4. **Federation Issues**
   - SRV record not resolving
   - SSL certificate mismatches
   - Port 8448 blocked
   - Key exchange failures

5. **Performance**
   - Slow room creation
   - Message lag
   - High CPU/memory usage
   - Database growth

## Phase 5: Matrix Authentication Service

### Future Migration

Once stable, migrate from Auth0 to Matrix Authentication Service (MAS):

**Benefits:**
- Unified identity across Mattermost and Matrix
- Single sign-on experience
- No external OAuth provider needed
- Open source solution
- Full control over auth flow

**Resources:**
- MAS Docs: https://github.com/matrix-org/matrix-authentication-service
- Integration Guide: https://element-hq.github.io/matrix-authentication-service/

**Migration Steps** (Future):
1. Deploy MAS alongside Synapse
2. Configure MAS as OIDC provider
3. Update Mattermost OAuth settings
4. Test login flows
5. Migrate users
6. Deprecate Auth0

## Monitoring and Maintenance

### Health Checks

```bash
# Mattermost
curl -s https://chat.lkofoss.club/api/v4/system/ping

# Matrix
curl -s https://matrix.lkofoss.club/_matrix/client/versions

# Element
curl -s https://element.lkofoss.club/config.json
```

### Log Monitoring

```bash
# Mattermost logs
docker logs -f mattermost

# Matrix logs
docker logs -f synapse

# Nginx logs
tail -f /var/log/nginx/access.log
tail -f /var/log/nginx/error.log
```

### Database Backups

```bash
# Mattermost database
docker exec mattermost-db pg_dump -U mmuser mattermost > mattermost-backup-$(date +%Y%m%d).sql

# Matrix database
docker exec matrix-db pg_dump -U synapse synapse > matrix-backup-$(date +%Y%m%d).sql
```

### Update Services

```bash
# Update Mattermost
cd /root/production-setup
docker-compose pull mattermost
docker-compose up -d mattermost

# Update Matrix
cd /root/mirror-mode-setup
docker-compose -f docker-compose.matrix.yml pull
docker-compose -f docker-compose.matrix.yml up -d
```

## Useful Commands

### Mattermost CLI

```bash
# List users
docker exec mattermost mattermost user list

# Create user
docker exec mattermost mattermost user create --email user@lkofoss.club --username user --password pass123

# List teams
docker exec mattermost mattermost team list

# List channels
docker exec mattermost mattermost channel list engineering

# Reset password
docker exec mattermost mattermost user resetpassword admin@lkofoss.club newpassword
```

### Matrix CLI

```bash
# Register user
docker exec synapse register_new_matrix_user -c /data/homeserver.yaml -u alice -p password123 -a http://localhost:8008

# List rooms
docker exec synapse psql -U synapse synapse -c "SELECT room_id, name FROM room_aliases;"

# Check federation
curl -s "https://matrix.lkofoss.club/_matrix/federation/v1/version"
```

### Docker Management

```bash
# View all containers
docker ps -a

# Check resource usage
docker stats

# Disk usage
docker system df

# Clean up
docker system prune -a
```

## Troubleshooting

### Can't Access Mattermost

1. Check DNS:
   ```bash
   dig chat.lkofoss.club
   ```

2. Check Nginx:
   ```bash
   sudo systemctl status nginx
   sudo nginx -t
   ```

3. Check Docker:
   ```bash
   docker ps
   docker logs mattermost
   ```

4. Check firewall:
   ```bash
   sudo ufw status
   sudo ufw allow 80/tcp
   sudo ufw allow 443/tcp
   ```

### Matrix Federation Not Working

1. Check SRV record:
   ```bash
   dig _matrix._tcp.lkofoss.club SRV
   ```

2. Check port 8448:
   ```bash
   sudo ufw allow 8448/tcp
   curl https://matrix.lkofoss.club:8448/_matrix/federation/v1/version
   ```

3. Test federation:
   ```bash
   curl https://federationtester.matrix.org/api/report?server_name=lkofoss.club
   ```

### Plugin Not Working

1. Check plugin installed:
   ```bash
   docker exec mattermost ls -la /mattermost/plugins
   ```

2. Check plugin enabled:
   ```bash
   docker exec mattermost mattermost plugin list
   ```

3. Check plugin logs:
   ```bash
   docker logs mattermost | grep matrix
   ```

4. Check plugin configuration:
   - System Console → Plugins → Matrix Bridge
   - Compare with `config-summary.txt`

### Database Connection Issues

```bash
# Mattermost database
docker exec mattermost-db psql -U mmuser -d mattermost -c "SELECT 1;"

# Matrix database
docker exec matrix-db psql -U synapse -d synapse -c "SELECT 1;"
```

## Security Hardening

### Firewall Setup

```bash
sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow ssh
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw allow 8448/tcp  # Matrix federation
sudo ufw enable
```

### SSL Certificate Renewal

```bash
# Test renewal
sudo certbot renew --dry-run

# Auto-renewal (already setup by certbot)
sudo systemctl status certbot.timer
```

### Rate Limiting

Add to nginx config:

```nginx
limit_req_zone $binary_remote_addr zone=mattermost:10m rate=10r/s;
limit_req zone=mattermost burst=20 nodelay;
```

### Fail2ban (Optional)

```bash
sudo dnf install -y fail2ban
# Configure for Nginx and SSH
```

## Performance Optimization

### Nginx Caching

Already configured in nginx-mattermost.conf:
- Static assets cached
- API responses not cached
- WebSocket connections optimized

### Database Tuning

Edit `docker-compose.yml`:

```yaml
mattermost-db:
  command: postgres -c shared_buffers=256MB -c max_connections=200
```

### Mattermost Tuning

Add to environment:

```yaml
- MM_SQLSETTINGS_MAXIDLECONNS=20
- MM_SQLSETTINGS_MAXOPENCONNS=300
- MM_FILESETTINGS_MAXFILESIZE=52428800
```

## Next Steps

1. **Deploy to Production**
   - Run setup scripts
   - Populate test data
   - Configure Auth0

2. **Test Mirror Mode**
   - Create channels
   - Send messages
   - Test federation

3. **Document Everything**
   - Installation issues
   - Configuration gotchas
   - User feedback

4. **Improve Scripts**
   - Fix bugs found
   - Add error handling
   - Better validation

5. **Update Documentation**
   - Add real-world examples
   - Common pitfalls
   - Best practices

6. **Share Learnings**
   - Blog posts
   - Documentation PRs
   - Community feedback

## Resources

- **Mattermost**: https://docs.mattermost.com/
- **Matrix**: https://matrix.org/docs/
- **Synapse**: https://matrix-org.github.io/synapse/
- **Element**: https://element.io/user-guide
- **Auth0**: https://auth0.com/docs
- **Matrix Auth Service**: https://github.com/matrix-org/matrix-authentication-service
- **Federation Tester**: https://federationtester.matrix.org/

## Support

For issues or questions:
- Mattermost Community: https://community.mattermost.com/
- Matrix Community: https://matrix.to/#/#matrix:matrix.org
- GitHub Issues: (your repository)
