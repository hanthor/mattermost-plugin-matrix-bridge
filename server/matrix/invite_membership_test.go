package matrix

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inviteClient talks to a homeserver that refuses every invite with the given
// wording, and reports the given membership for the invited person.
func inviteClient(t *testing.T, refusal, membershipStatus, membership string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/invite"):
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errcode":"M_FORBIDDEN","error":"` + refusal + `"}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/state/m.room.member/"):
			if membershipStatus == "404" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"errcode":"M_NOT_FOUND"}`))
				return
			}
			_, _ = w.Write([]byte(`{"membership":"` + membership + `"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return NewClientWithLoggerAndRateLimit(srv.URL, "token", "remote", NewTestLogger(t), TestRateLimitConfig())
}

func TestInvitingSomeoneAlreadyInTheRoomIsNotAnErrorWhateverTheWording(t *testing.T) {
	for _, wording := range []string{
		"@a:hs is already in the room.",                                  // Synapse
		"Auth check failed: cannot invite user that is joined or banned", // Tuwunel
		"some wording nobody has matched before",
	} {
		t.Run("joined: "+wording, func(t *testing.T) {
			require.NoError(t, inviteClient(t, wording, "200", "join").InviteUserToRoom("!room:hs", "@a:hs"))
		})
	}
	t.Run("an invitation already pending counts as done", func(t *testing.T) {
		require.NoError(t, inviteClient(t, "Auth check failed", "200", "invite").InviteUserToRoom("!room:hs", "@a:hs"))
	})
}

func TestARefusedInviteIsStillAnErrorWhenThePersonIsNotInTheRoom(t *testing.T) {
	t.Run("banned", func(t *testing.T) {
		assert.Error(t, inviteClient(t, "Auth check failed", "200", "ban").InviteUserToRoom("!room:hs", "@a:hs"))
	})
	t.Run("left", func(t *testing.T) {
		assert.Error(t, inviteClient(t, "Auth check failed", "200", "leave").InviteUserToRoom("!room:hs", "@a:hs"))
	})
	t.Run("no membership at all", func(t *testing.T) {
		assert.Error(t, inviteClient(t, "Auth check failed", "404", "").InviteUserToRoom("!room:hs", "@a:hs"))
	})
}
