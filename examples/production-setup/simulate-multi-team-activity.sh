#!/bin/bash
set -e

# Simulate realistic activity across all teams
MATTERMOST_URL="https://YOUR_DOMAIN.com"
DEFAULT_PASSWORD="ChangeMe123!"

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

echo -e "${YELLOW}Simulating multi-team activity...${NC}\n"

# Example users
USERS=(
    "alice@example.YOUR_DOMAIN.com"
    "bob@example.YOUR_DOMAIN.com"
    "carol@example.YOUR_DOMAIN.com"
    "dave@example.YOUR_DOMAIN.com"
    "eve@example.YOUR_DOMAIN.com"
    "frank@example.YOUR_DOMAIN.com"
    "grace@example.YOUR_DOMAIN.com"
    "henry@example.YOUR_DOMAIN.com"
    "iris@example.YOUR_DOMAIN.com"
    "jack@example.YOUR_DOMAIN.com"
)

# Function to login and get token
login_user() {
    local email=$1
    TOKEN=$(curl -s -i -X POST "${MATTERMOST_URL}/api/v4/users/login" \
      -H "Content-Type: application/json" \
      -d "{\"login_id\":\"${email}\",\"password\":\"${DEFAULT_PASSWORD}\"}" | grep -i "^token:" | awk '{print $2}' | tr -d '\r')
    echo "$TOKEN"
}

# Function to get channel ID
get_channel_id() {
    local team=$1
    local channel=$2
    local token=$3
    
    RESPONSE=$(curl -s -X GET "${MATTERMOST_URL}/api/v4/teams/name/${team}/channels/name/${channel}" \
      -H "Authorization: Bearer ${token}")
    echo "$RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin).get('id', ''))" 2>/dev/null || echo ""
}

# Function to post message
post_message() {
    local channel_id=$1
    local message=$2
    local token=$3
    
    curl -s -X POST "${MATTERMOST_URL}/api/v4/posts" \
      -H "Authorization: Bearer ${token}" \
      -H "Content-Type: application/json" \
      -d "{\"channel_id\":\"${channel_id}\",\"message\":\"${message}\"}" > /dev/null
}

# Get random user
random_user() {
    echo "${USERS[$RANDOM % ${#USERS[@]}]}"
}

echo -e "${BLUE}Posting to your-team team...${NC}"
# your-team team - general community discussion
for i in {1..5}; do
    user=$(random_user)
    token=$(login_user "$user")
    channel_id=$(get_channel_id "your-team" "general" "$token")
    
    MESSAGES=(
        "Hey everyone! Looking forward to this weekend's meetup 👋"
        "Great community here! Learned so much already"
        "Anyone working on interesting open source projects?"
        "Just joined! Excited to be part of LKO FOSS"
        "Thanks for the warm welcome everyone!"
    )
    msg="${MESSAGES[$i-1]}"
    post_message "$channel_id" "$msg" "$token"
    echo -e "  ${GREEN}✓${NC} $user -> general: $msg"
done

# your-team team - tech discussions
for i in {1..4}; do
    user=$(random_user)
    token=$(login_user "$user")
    channel_id=$(get_channel_id "your-team" "tech-talks" "$token")
    
    MESSAGES=(
        "🐍 Anyone here using Python for web scraping? Looking for library recommendations"
        "Just discovered Rust! The ownership model is fascinating"
        "Docker containers vs VMs - what do you all prefer for local development?"
        "CI/CD pipelines - sharing our GitHub Actions workflow template soon"
    )
    msg="${MESSAGES[$i-1]}"
    post_message "$channel_id" "$msg" "$token"
    echo -e "  ${GREEN}✓${NC} $user -> tech-talks: $msg"
done

echo -e "\n${BLUE}Posting to organizers team...${NC}"
# organizers team - planning
for i in {1..3}; do
    user=$(random_user)
    token=$(login_user "$user")
    channel_id=$(get_channel_id "organizers" "general" "$token")
    
    MESSAGES=(
        "📋 Finalized the venue for next month's workshop"
        "Need 2 volunteers for registration desk at Saturday's event"
        "Speaker lineup is confirmed! 5 great talks scheduled"
    )
    msg="${MESSAGES[$i-1]}"
    post_message "$channel_id" "$msg" "$token"
    echo -e "  ${GREEN}✓${NC} $user -> organizers/general: $msg"
done

echo -e "\n${BLUE}Posting to contributors team...${NC}"
# contributors team - project work
for i in {1..4}; do
    user=$(random_user)
    token=$(login_user "$user")
    channel_id=$(get_channel_id "contributors" "projects" "$token")
    
    MESSAGES=(
        "🔧 PR #42 is ready for review - added dark mode support"
        "Fixed the authentication bug, tests are passing now ✅"
        "Working on the API documentation, will push tonight"
        "Code review needed: refactored the database queries for better performance"
    )
    msg="${MESSAGES[$i-1]}"
    post_message "$channel_id" "$msg" "$token"
    echo -e "  ${GREEN}✓${NC} $user -> contributors/projects: $msg"
done

echo -e "\n${BLUE}Posting to events team...${NC}"
# events team - event coordination
for i in {1..3}; do
    user=$(random_user)
    token=$(login_user "$user")
    channel_id=$(get_channel_id "events" "announcements" "$token")
    
    MESSAGES=(
        "🎉 Next Meetup: Saturday 2 PM at Tech Hub - RSVP at meetup.com"
        "Workshop Registration Open: Git & GitHub for Beginners - 50 seats available"
        "📢 Lightning talks session next week! Submit your topic ideas"
    )
    msg="${MESSAGES[$i-1]}"
    post_message "$channel_id" "$msg" "$token"
    echo -e "  ${GREEN}✓${NC} $user -> events/announcements: $msg"
done

# Random interactions across teams
echo -e "\n${BLUE}Adding some conversations and reactions...${NC}"
sleep 1

# Thread replies
user1=$(random_user)
token1=$(login_user "$user1")
channel_id=$(get_channel_id "your-team" "help" "$token1")
post_message "$channel_id" "Need help setting up a local development environment for a Django project. Any tips?" "$token1"
echo -e "  ${GREEN}✓${NC} Started help thread in your-team/help"

sleep 1
user2=$(random_user)
token2=$(login_user "$user2")
post_message "$channel_id" "I'd recommend using Docker Compose! Makes it super easy to manage dependencies" "$token2"
echo -e "  ${GREEN}✓${NC} Reply added to help thread"

# Introductions
user3=$(random_user)
token3=$(login_user "$user3")
channel_id=$(get_channel_id "your-team" "introductions" "$token3")
post_message "$channel_id" "👋 Hi! I'm new to open source and excited to learn. Background in Java development" "$token3"
echo -e "  ${GREEN}✓${NC} New introduction posted"

# Resources sharing
user4=$(random_user)
token4=$(login_user "$user4")
channel_id=$(get_channel_id "your-team" "resources" "$token4")
post_message "$channel_id" "📚 Great free resource: FreeCodeCamp's open source curriculum - https://www.freecodecamp.org" "$token4"
echo -e "  ${GREEN}✓${NC} Resource shared"

echo -e "\n${GREEN}========================================${NC}"
echo -e "${GREEN}✓ Multi-team activity simulation complete!${NC}"
echo -e "${GREEN}========================================${NC}"
echo -e "\n${YELLOW}Summary:${NC}"
echo "  - Posted to your-team team (general, tech-talks, help, introductions, resources)"
echo "  - Posted to organizers team (general)"
echo "  - Posted to contributors team (projects)"
echo "  - Posted to events team (announcements)"
echo ""
echo -e "${BLUE}Verify at:${NC} ${MATTERMOST_URL}"
echo ""
