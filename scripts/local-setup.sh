#!/bin/bash
# Local Development Setup Script for Mattermost-Matrix Bridge
# This script sets up the complete local development environment

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Move to the project root directory
cd "$SCRIPT_DIR/.."

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

print_step() {
    echo -e "${BLUE}==>${NC} $1"
}

print_success() {
    echo -e "${GREEN}✓${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}!${NC} $1"
}

print_error() {
    echo -e "${RED}✗${NC} $1"
}

# Check prerequisites
check_prerequisites() {
    print_step "Checking prerequisites..."
    
    if ! command -v docker &> /dev/null; then
        print_error "Docker is not installed. Please install Docker first."
        exit 1
    fi
    
    if ! command -v docker compose &> /dev/null && ! docker compose version &> /dev/null; then
        print_error "Docker Compose is not installed. Please install Docker Compose first."
        exit 1
    fi
    
    if ! command -v go &> /dev/null; then
        print_warning "Go is not installed. You won't be able to build the plugin."
    fi
    
    if ! command -v npm &> /dev/null; then
        print_warning "npm is not installed. You won't be able to build the webapp."
    fi
    
    print_success "Prerequisites check complete"
}

# Build the plugin
build_plugin() {
    print_step "Building the Mattermost Matrix Bridge plugin..."
    
    if command -v go &> /dev/null && command -v npm &> /dev/null; then
        make dist
        print_success "Plugin built successfully: dist/com.mattermost.plugin-matrix-bridge-*.tar.gz"
    else
        print_warning "Skipping plugin build - Go or npm not available"
        print_warning "Make sure you have a pre-built plugin in the dist/ directory"
    fi
}

# Generate tokens
generate_tokens() {
    print_step "Generating secure tokens..."
    
    AS_TOKEN=$(openssl rand -hex 32)
    HS_TOKEN=$(openssl rand -hex 32)
    
    echo "$AS_TOKEN" > .as_token
    echo "$HS_TOKEN" > .hs_token
    
    # Export for docker-compose
    export MATRIX_AS_TOKEN="$AS_TOKEN"
    export MATRIX_HS_TOKEN="$HS_TOKEN"
    export MATRIX_SERVER_URL="http://synapse:8008"
    export ENABLE_SYNC="true"
    export RATE_LIMITING_MODE="automatic"
    
    print_success "Tokens generated and saved to .as_token and .hs_token"
    echo ""
    echo "Application Service Token: $AS_TOKEN"
    echo "Homeserver Token: $HS_TOKEN"
    echo ""
}

# Update registration file with tokens
update_registration() {
    print_step "Updating bridge registration file with tokens..."
    
    AS_TOKEN=$(cat .as_token)
    HS_TOKEN=$(cat .hs_token)
    
    # Use sed to update the registration file
    if [[ "$OSTYPE" == "darwin"* ]]; then
        # macOS
        sed -i '' "s/as_token: .*/as_token: $AS_TOKEN/" docker/mattermost-bridge-registration.yaml
        sed -i '' "s/hs_token: .*/hs_token: $HS_TOKEN/" docker/mattermost-bridge-registration.yaml
    else
        # Linux
        sed -i "s/as_token: .*/as_token: $AS_TOKEN/" docker/mattermost-bridge-registration.yaml
        sed -i "s/hs_token: .*/hs_token: $HS_TOKEN/" docker/mattermost-bridge-registration.yaml
    fi
    
    print_success "Registration file updated"
}

# Start Docker services
start_services() {
    print_step "Starting Docker services..."
    
    # Export tokens for docker-compose if they exist
    if [ -f .as_token ] && [ -f .hs_token ]; then
        export MATRIX_AS_TOKEN=$(cat .as_token)
        export MATRIX_HS_TOKEN=$(cat .hs_token)
        export MATRIX_SERVER_URL="http://synapse:8008"
        export ENABLE_SYNC="true"
        export RATE_LIMITING_MODE="automatic"
    fi
    
    docker compose down -v 2>/dev/null || true
    docker compose up -d
    
    print_success "Docker services started"
    print_step "Waiting for services to be healthy..."
    
    # Wait for Mattermost
    echo -n "Waiting for Mattermost..."
    for i in {1..60}; do
        if curl -s http://localhost:8066/api/v4/system/ping > /dev/null 2>&1; then
            echo " Ready!"
            break
        fi
        echo -n "."
        sleep 2
    done
    
    # Wait for Synapse
    echo -n "Waiting for Synapse..."
    for i in {1..60}; do
        if curl -s http://localhost:8888/_matrix/client/versions > /dev/null 2>&1; then
            echo " Ready!"
            break
        fi
        echo -n "."
        sleep 2
    done
    
    print_success "All services are running"
}

# Setup Mattermost
setup_mattermost() {
    print_step "Setting up Mattermost..."
    
    CONTAINER=$(docker compose ps -q mattermost)
    
    # Wait a bit more for Mattermost to be fully ready
    sleep 5
    
    # Create admin user
    print_step "Creating admin user..."
    docker exec -u mattermost "$CONTAINER" /mattermost/bin/mmctl --local user create \
        --email admin@example.com \
        --username admin \
        --password "Admin123!" \
        --system-admin 2>/dev/null || print_warning "Admin user may already exist"
    
    # Create a team
    print_step "Creating test team..."
    docker exec -u mattermost "$CONTAINER" /mattermost/bin/mmctl --local team create \
        --name test-team \
        --display-name "Test Team" 2>/dev/null || print_warning "Team may already exist"
    
    # Add admin to team
    docker exec -u mattermost "$CONTAINER" /mattermost/bin/mmctl --local team users add test-team admin 2>/dev/null || true
    
    print_success "Mattermost setup complete"
}

# Create Matrix admin user
setup_synapse() {
    print_step "Setting up Synapse..."
    
    CONTAINER=$(docker compose ps -q synapse)
    
    # Wait for Synapse to be fully ready
    sleep 5
    
    # Register admin user
    print_step "Creating Matrix admin user..."
    docker exec "$CONTAINER" register_new_matrix_user \
        -c /data/homeserver.yaml \
        -u admin \
        -p admin123 \
        -a \
        http://localhost:8008 2>/dev/null || print_warning "Admin user may already exist"
    
    print_success "Synapse setup complete"
}

# Install plugin
install_plugin() {
    print_step "Installing plugin to Mattermost..."
    
    CONTAINER=$(docker compose ps -q mattermost)
    PLUGIN_FILE=$(ls dist/com.mattermost.plugin-matrix-bridge-*.tar.gz 2>/dev/null | head -n1)
    
    if [ -z "$PLUGIN_FILE" ]; then
        print_error "Plugin file not found in dist/. Run 'make dist' first."
        return 1
    fi
    
    # Copy plugin to container
    docker cp "$PLUGIN_FILE" "$CONTAINER":/tmp/plugin.tar.gz
    
    # Install using mmctl (more reliable than API)
    print_step "Installing plugin using mmctl..."
    if docker exec -u mattermost "$CONTAINER" /mattermost/bin/mmctl --local plugin add /tmp/plugin.tar.gz 2>/dev/null; then
        print_success "Plugin installed"
        
        # Enable plugin
        print_step "Enabling plugin..."
        if docker exec -u mattermost "$CONTAINER" /mattermost/bin/mmctl --local plugin enable com.mattermost.plugin-matrix-bridge 2>/dev/null; then
            print_success "Plugin enabled"
        else
            print_warning "Could not enable plugin. Enable it manually via System Console."
        fi
    else
        print_warning "Could not install plugin. Install it manually via System Console."
    fi
}

# Configure plugin via API
configure_plugin() {
    print_step "Configuring plugin..."
    
    # Wait for Mattermost to be fully ready
    sleep 3
    
    # Get auth token
    TOKEN=$(curl -s -X POST "http://localhost:8066/api/v4/users/login" \
        -H "Content-Type: application/json" \
        -d '{"login_id":"admin","password":"Admin123!"}' \
        -D /tmp/mm_headers.txt 2>/dev/null | jq -r '.id' 2>/dev/null)
    
    AUTH_TOKEN=$(cat /tmp/mm_headers.txt 2>/dev/null | grep -i "token:" | awk '{print $2}' | tr -d '\r\n')
    
    if [ -z "$AUTH_TOKEN" ]; then
        print_warning "Could not get auth token. Configure plugin manually."
        return 1
    fi
    
    # Get current config
    CURRENT_CONFIG=$(curl -s -X GET "http://localhost:8066/api/v4/config" \
        -H "Authorization: Bearer $AUTH_TOKEN" 2>/dev/null)
    
    # Update plugin settings
    UPDATED_CONFIG=$(echo "$CURRENT_CONFIG" | jq \
        --arg server_url "http://synapse:8008" \
        --arg as_token "$AS_TOKEN" \
        --arg hs_token "$HS_TOKEN" \
        '.PluginSettings.Plugins["com.mattermost.plugin-matrix-bridge"] = {
            "matrixserverurl": $server_url,
            "matrixastoken": $as_token,
            "matrixhstoken": $hs_token,
            "enablesync": true,
            "ratelimitingmode": "automatic"
        }')
    
    # Apply configuration
    if curl -s -X PUT "http://localhost:8066/api/v4/config" \
        -H "Authorization: Bearer $AUTH_TOKEN" \
        -H "Content-Type: application/json" \
        -d "$UPDATED_CONFIG" > /dev/null 2>&1; then
        print_success "Plugin configured"
        rm -f /tmp/mm_headers.txt
        return 0
    else
        print_warning "Could not configure plugin. Configure manually via System Console."
        rm -f /tmp/mm_headers.txt
        return 1
    fi
}

# Note: Plugin configuration must be done via System Console UI
# mmctl config set doesn't work for plugin settings

# Print final instructions
print_instructions() {
    echo ""
    echo "=========================================="
    echo -e "${GREEN}Local Development Environment Ready!${NC}"
    echo "=========================================="
    echo ""
    echo "Services running:"
    echo "  - Mattermost:    http://localhost:8066"
    echo "  - Matrix/Synapse: http://localhost:8888"
    echo "  - Element Web:   http://localhost:8081"
    echo ""
    echo "Credentials:"
    echo "  Mattermost: admin / Admin123!"
    echo "  Matrix:     admin / admin123"
    echo ""
    echo "Next steps:"
    echo "  1. Log into Mattermost at http://localhost:8066"
    echo "  2. Plugin is automatically pre-configured with:"
    echo "     ✓ Matrix Server URL: http://synapse:8008"
    echo "     ✓ Tokens: Auto-configured from registration file"
    echo "     ✓ Message Sync: Enabled"
    echo "  3. Create a channel and use /matrix create \"Room Name\""
    echo "  4. Or use /matrix test to verify connectivity"
    echo ""
    echo "To view/modify settings: System Console → Plugins → Matrix Bridge"
    echo ""
    echo "To test Matrix directly:"
    echo "  - Open Element Web at http://localhost:8081"
    echo "  - Log in with the Matrix admin credentials"
    echo ""
    echo "Tokens (also saved in files):"
    echo "  AS Token: $(cat .as_token 2>/dev/null || echo 'Not generated')"
    echo "  HS Token: $(cat .hs_token 2>/dev/null || echo 'Not generated')"
    echo ""
}

# Main execution
main() {
    echo ""
    echo "=========================================="
    echo "Mattermost-Matrix Bridge Local Setup"
    echo "=========================================="
    echo ""
    
    check_prerequisites
    
    # Check if tokens exist, if not generate them
    if [ ! -f .as_token ] || [ ! -f .hs_token ]; then
        generate_tokens
        update_registration
    else
        print_step "Using existing tokens from .as_token and .hs_token"
    fi
    
    # Build if dist doesn't exist
    if [ ! -d dist ] || [ -z "$(ls dist/*.tar.gz 2>/dev/null)" ]; then
        build_plugin
    else
        print_step "Using existing plugin build in dist/"
    fi
    
    start_services
    setup_mattermost
    setup_synapse
    install_plugin
    configure_plugin
    print_instructions
}

# Run main function
main "$@"
