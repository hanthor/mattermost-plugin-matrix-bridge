package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

func doctorFixture(t *testing.T) (*Plugin, *fakeSynapse) {
	t.Helper()
	hs := newFakeSynapse(t)
	hs.whoami = "@_mattermost_bridge:example.com"
	p, _ := newMirrorTestBridge(t, hs)
	p.configuration.EnableSync = true
	p.configuration.MatrixASToken = "as"
	p.configuration.MatrixHSToken = "hs"
	p.configuration.EnableMirrorMode = false
	p.configuration.ElementURL = "https://chat.example.com"
	return p, hs
}

func titles(checks []doctorCheck, status doctorStatus) []string {
	var out []string
	for _, c := range checks {
		if c.Status == status {
			out = append(out, c.Title)
		}
	}
	return out
}

func TestDoctorPassesOnAWorkingSetup(t *testing.T) {
	p, _ := doctorFixture(t)
	checks := p.doctorChecks("")
	assert.Empty(t, titles(checks, doctorFail), "%v", checks)
	assert.Empty(t, titles(checks, doctorWarn), "%v", checks)
	assert.Contains(t, p.Doctor(""), "Everything checked is in order")
}

func TestDoctorSaysWhichSettingsAreMissing(t *testing.T) {
	p, _ := doctorFixture(t)
	p.configuration.MatrixHSToken = ""
	p.configuration.MatrixServerDomain = ""

	report := p.Doctor("")
	assert.Contains(t, report, "homeserver token, Matrix server name")
	assert.Contains(t, report, "Fix the ❌ items first")
}

func TestDoctorFlagsAWrongServerName(t *testing.T) {
	p, hs := doctorFixture(t)
	hs.whoami = "@_mattermost_bridge:matrix.example.org"

	checks := p.doctorChecks("")
	assert.Contains(t, titles(checks, doctorFail), "Matrix server name")
}

func TestDoctorFlagsAnAppserviceTokenTheHomeserverRejects(t *testing.T) {
	p, hs := doctorFixture(t)
	hs.whoami = "" // the fake answers 200 with no user; a real homeserver answers 401
	checks := p.doctorChecks("")
	assert.NotEmpty(t, titles(checks, doctorFail))
}

func TestDoctorWarnsWhenTheHomeserverCannotCallThePluginBack(t *testing.T) {
	p, hs := doctorFixture(t)
	hs.pingFails = true

	report := p.Doctor("")
	assert.Contains(t, report, "⚠️ **Homeserver can reach the plugin**")
	assert.Contains(t, report, "M_URL_NOT_REACHABLE")
	assert.Contains(t, report, "`url:` in the registration")
}

func TestDoctorChecksEncryptionOnlyWhenItIsSwitchedOn(t *testing.T) {
	p, _ := doctorFixture(t)
	assert.NotContains(t, p.Doctor(""), "Encrypted rooms")

	p.configuration.EnableEncryption = true
	checks := p.doctorChecks("")
	assert.Contains(t, titles(checks, doctorFail), "Encrypted rooms")
	assert.Contains(t, p.Doctor(""), "org.matrix.msc3202")
}

func TestDoctorReportsStuckDelivery(t *testing.T) {
	p, _ := doctorFixture(t)
	p.deadLetter(MatrixEvent{EventID: "$x", RoomID: "!r:example.com", Type: "m.room.message"}, assert.AnError)

	assert.Contains(t, titles(p.doctorChecks(""), doctorFail), "Delivery")
}

func TestDoctorChecksTheCurrentChannel(t *testing.T) {
	p, hs := doctorFixture(t)

	// Not bridged.
	assert.Contains(t, titles(p.doctorChecks("chan-1"), doctorWarn), "This channel")

	// Bridged, room mapped both ways, the bot in it with little power.
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("chan-1"), []byte("!room:example.com")))
	require.NoError(t, p.kvstore.Set(kvstore.BuildRoomMappingKey("!room:example.com"), []byte("chan-1")))
	hs.joinedRooms = []string{"!room:example.com"}
	hs.botPower = 0
	checks := p.doctorChecks("chan-1")
	assert.Contains(t, titles(checks, doctorWarn), "Bridge bot power level")
	assert.Empty(t, titles(checks, doctorFail), "%v", checks)

	// The bot is not in the room.
	hs.joinedRooms = nil
	assert.Contains(t, titles(p.doctorChecks("chan-1"), doctorFail), "Bridge bot in the room")

	// The room does not point back.
	require.NoError(t, p.kvstore.Delete(kvstore.BuildRoomMappingKey("!room:example.com")))
	assert.Contains(t, titles(p.doctorChecks("chan-1"), doctorFail), "This channel's mapping")
}

func TestDoctorSaysWhenAChannelIsReadOnly(t *testing.T) {
	p, hs := doctorFixture(t)
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("chan-1"), []byte("!room:example.com")))
	require.NoError(t, p.kvstore.Set(kvstore.BuildRoomMappingKey("!room:example.com"), []byte("chan-1")))
	hs.joinedRooms = []string{"!room:example.com"}
	hs.botPower = 100
	require.NoError(t, p.kvstore.Set(cutoverReadOnlyKeyPrefix+"chan-1", []byte("1")))

	assert.True(t, strings.Contains(p.Doctor("chan-1"), "This channel is read-only"))
}
