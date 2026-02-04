#!/bin/bash
set -e

# Create example test users across all teams
DEFAULT_PASSWORD="ChangeMe123!"

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

echo -e "${YELLOW}Creating example test users across all teams...${NC}\n"

# Example users (not real people)
EXAMPLE_USERS=(
    "alice.developer:Alice Developer:alice@example.YOUR_DOMAIN.com"
    "bob.designer:Bob Designer:bob@example.YOUR_DOMAIN.com"
    "carol.organizer:Carol Martinez:carol@example.YOUR_DOMAIN.com"
    "dave.student:Dave Kumar:dave@example.YOUR_DOMAIN.com"
    "eve.mentor:Eve Thompson:eve@example.YOUR_DOMAIN.com"
    "frank.contributor:Frank Chen:frank@example.YOUR_DOMAIN.com"
    "grace.admin:Grace Patel:grace@example.YOUR_DOMAIN.com"
    "henry.newbie:Henry Wilson:henry@example.YOUR_DOMAIN.com"
    "iris.speaker:Iris Rodriguez:iris@example.YOUR_DOMAIN.com"
    "jack.volunteer:Jack Anderson:jack@example.YOUR_DOMAIN.com"
    "kate.writer:Kate Brown:kate@example.YOUR_DOMAIN.com"
    "leo.tester:Leo Garcia:leo@example.YOUR_DOMAIN.com"
)

TEAMS=("your-team" "organizers" "contributors" "events")

echo -e "${BLUE}Creating users...${NC}"
for user_data in "${EXAMPLE_USERS[@]}"; do
    IFS=':' read -r username fullname email <<< "$user_data"
    IFS=' ' read -r firstname lastname <<< "$fullname"
    
    docker exec mattermost mmctl --local user create \
      --email "$email" \
      --username "$username" \
      --password "$DEFAULT_PASSWORD" \
      --firstname "$firstname" \
      --lastname "$lastname" \
      --email-verified 2>/dev/null || echo "  (User $username may already exist)"
    
    echo -e "  ${GREEN}✓${NC} $fullname ($username)"
done

echo -e "\n${BLUE}Adding users to teams...${NC}"
for team in "${TEAMS[@]}"; do
    echo "  Team: $team"
    for user_data in "${EXAMPLE_USERS[@]}"; do
        IFS=':' read -r username fullname email <<< "$user_data"
        docker exec mattermost mmctl --local team users add "$team" "$email" 2>/dev/null || true
    done
    echo -e "  ${GREEN}✓${NC} Added ${#EXAMPLE_USERS[@]} users to $team"
done

echo -e "\n${BLUE}Adding users to channels...${NC}"
CHANNELS=("general" "random" "announcements" "events" "projects" "help" "introductions" "resources" "off-topic" "tech-talks")

for team in "${TEAMS[@]}"; do
    for channel in "${CHANNELS[@]}"; do
        for user_data in "${EXAMPLE_USERS[@]}"; do
            IFS=':' read -r username fullname email <<< "$user_data"
            docker exec mattermost mmctl --local channel users add "${team}:${channel}" "$email" 2>/dev/null || true
        done
    done
    echo -e "  ${GREEN}✓${NC} Added users to channels in team: $team"
done

echo -e "\n${GREEN}========================================${NC}"
echo -e "${GREEN}✓ Created ${#EXAMPLE_USERS[@]} example users${NC}"
echo -e "${GREEN}========================================${NC}"
echo -e "\n${YELLOW}Users can login with:${NC}"
echo "  Email: <username>@example.YOUR_DOMAIN.com"
echo "  Password: $DEFAULT_PASSWORD"
echo ""
echo "  Examples:"
echo "    alice@example.YOUR_DOMAIN.com / $DEFAULT_PASSWORD"
echo "    bob@example.YOUR_DOMAIN.com / $DEFAULT_PASSWORD"
echo ""
