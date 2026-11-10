package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/complement/helpers"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestTeamMembershipFollowsIntoTheSpace checks that joining and leaving a team
// puts a person's account in and out of the team's space.
func TestTeamMembershipFollowsIntoTheSpace(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()
	carol, _ := h.CreateMattermostUser("carol")
	// She is added to the test team on creation; start outside of it.
	_, err := h.Admin.RemoveTeamMember(ctx, h.Team.Id, carol.Id)
	require.NoError(t, err)

	// Spaces are made with the structure backfill.
	resp, _, err := h.Admin.ExecuteCommand(ctx, h.TownSquare().Id, "/matrix backfill all")
	require.NoError(t, err)
	require.NotContains(t, resp.Text, "❌", resp.Text)

	var spaceID string
	WaitFor(t, bridgeTimeout, "the team's space", func() bool {
		raw := h.Mattermost.QuerySQL(t, "SELECT convert_from(pvalue, 'UTF8') FROM pluginkeyvaluestore WHERE pluginid = '"+PluginID+"' AND pkey = 'team_mapping_"+h.Team.Id+"'")
		// The query stream starts with a few framing bytes; the ID starts at "!".
		if i := strings.Index(raw, "!"); i >= 0 {
			spaceID = strings.Fields(raw[i:])[0]
		}
		return strings.HasPrefix(spaceID, "!")
	})

	// A homeserver admin can read the space's member list.
	admin := h.Deployment.Register(t, HSName, helpers.RegistrationOpts{LocalpartSuffix: "spaceadmin", IsAdmin: true})
	inSpace := func() bool {
		res := admin.Do(t, "GET", []string{"_synapse", "admin", "v1", "rooms", spaceID, "members"})
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != 200 {
			return false
		}
		for _, m := range gjson.GetBytes(client.ParseJSON(t, res), "members").Array() {
			if m.Str == h.GhostUserID(carol.Id) {
				return true
			}
		}
		return false
	}

	_, _, err = h.Admin.AddTeamMember(ctx, h.Team.Id, carol.Id)
	require.NoError(t, err)
	WaitFor(t, bridgeTimeout, "Carol's account to join the space", inSpace)

	_, err = h.Admin.RemoveTeamMember(ctx, h.Team.Id, carol.Id)
	require.NoError(t, err)
	WaitFor(t, bridgeTimeout, "Carol's account to leave the space", func() bool { return !inSpace() })
	capabilities.Verify(t, "spaces.team", true)
}
