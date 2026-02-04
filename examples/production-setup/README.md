# Production Mattermost Setup Templates

This directory contains templates and scripts for deploying a production Mattermost instance with populated test data.

## Quick Start

1. Copy this entire directory to your server
2. Customize the configuration files for your domain
3. Run the setup scripts

## Files Overview

### Setup Scripts

- **`install.sh`** - Infrastructure deployment (Docker, Nginx, SSL, PostgreSQL, Mattermost)
- **`create-example-users.sh`** - Creates example test users across multiple teams
- **`simulate-multi-team-activity.sh`** - Generates realistic conversations and activity

### Templates

- **`auth0-setup.md.template`** - Guide for Auth0 OAuth integration
- **`users.json.template`** - Example format for bulk user creation

## Usage

### 1. Basic Installation

```bash
# On your server
cd production-setup
./install.sh
```

This installs:
- Docker & Docker Compose
- Nginx with SSL termination
- PostgreSQL 15
- Mattermost Enterprise Edition

### 2. Create Example Users

Edit the configuration in `create-example-users.sh`:
- Update domain references (search for `example.com`)
- Modify team names if needed
- Adjust channel list

```bash
./create-example-users.sh
```

Creates:
- 12 example users
- 4 teams (customize in script)
- Multiple channels per team
- Adds users to all teams and channels

### 3. Generate Activity

Edit `simulate-multi-team-activity.sh`:
- Update `MATTERMOST_URL` to your domain
- Customize user emails
- Modify message content

```bash
./simulate-multi-team-activity.sh
```

Generates:
- Messages across multiple channels
- Threaded conversations
- Help requests and responses
- Introductions and resources

## Customization

### Domain Configuration

Replace these placeholders throughout:
- `YOUR_DOMAIN.com` → Your actual domain
- `YOUR_SERVER_IP` → Your server IP
- `admin@YOUR_DOMAIN.com` → Your admin email

### User Data

For real users:
1. Create a `users.json` file (see `users.json.template`)
2. Format:
```json
[
  {
    "email": "user@example.com",
    "name": "Full Name",
    "email_verified": true
  }
]
```

### Teams and Channels

Edit in the scripts:
```bash
TEAMS=("team1" "team2" "team3")
CHANNELS=("general:General:General discussion" ...)
```

## Auth0 Integration (Optional)

See `auth0-setup.md.template` for complete OAuth setup guide.

Key steps:
1. Create Regular Web Application in Auth0
2. Configure callback URLs
3. Update Mattermost System Console
4. Users can then sign in via Auth0

## Security Notes

⚠️ **Important**:
- Change default password `ChangeMe123!` in scripts
- Never commit real user data to git
- Keep auth credentials in gitignored files
- Use environment variables for secrets

## Architecture

```
Internet (HTTPS)
    ↓
Nginx (Port 443)
    ↓
Mattermost (Port 8065)
    ↓
PostgreSQL (Port 5432)
```

## Verification

Check deployment status:
```bash
# Health check
curl https://YOUR_DOMAIN/api/v4/system/ping

# View logs
docker logs mattermost

# Check database
docker exec -it mattermost-db psql -U mmuser -d mattermost
```

## Troubleshooting

### SSL Certificate Issues
```bash
certbot certificates
certbot renew --dry-run
```

### Mattermost Not Starting
```bash
docker logs mattermost
docker restart mattermost
```

### Database Connection Issues
```bash
docker exec mattermost-db psql -U mmuser -d mattermost -c "SELECT 1;"
```

## Next Steps

After basic setup:
1. Configure Matrix bridge (see `/docs/MIRROR_MODE_SETUP.md`)
2. Set up federation
3. Configure backups
4. Set up monitoring

## Support

For issues specific to Mattermost-Matrix bridge integration, see the main repository documentation.
