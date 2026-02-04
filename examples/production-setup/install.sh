#!/bin/bash
set -e

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${YELLOW}Installing Mattermost on YOUR_DOMAIN${NC}\n"

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
certbot certonly --nginx -d YOUR_DOMAIN --non-interactive --agree-tos --email admin@YOUR_DOMAIN || true

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
  --email admin@YOUR_DOMAIN \
  --username admin \
  --password admin123 \
  --system-admin || echo "Admin user may already exist"

echo -e "\n${GREEN}✓ Installation complete!${NC}\n"
echo "Access Mattermost at: https://YOUR_DOMAIN"
echo "Login with: admin@YOUR_DOMAIN / admin123"
echo -e "\nNext steps:"
echo "  1. Login and complete setup wizard"
echo "  2. Run: ./populate-users.sh"
echo "  3. Configure OAuth with Auth0 (see auth0-setup.md)"
