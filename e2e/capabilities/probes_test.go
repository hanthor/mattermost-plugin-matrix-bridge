package capabilities

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The matrix is only worth reading if every row is backed by a test. This
// scans the end-to-end tests for capabilities.Verify calls and fails for any
// row nobody probes, so a feature cannot be marked supported (or a gap
// recorded) without a test that checks it.
var verifyCall = regexp.MustCompile(`capabilities\.Verify\(t, "([^"]+)"`)

// notProbed lists the rows that cannot be checked by a test, with the reason.
// Each must be a recorded gap: a feature is never supported without a probe.
var notProbed = map[string]string{
	"calls.media":      "audio and video relay is not planned; there is no behavior to observe",
	"calls.recordings": "needs the Mattermost Calls plugin and a recorded call, which the stack does not run",
	"identity.sso":     "automatic linking by verified email in ghost mode is not built; shared login is mirror.sso_provider",
}

func TestEveryCapabilityIsProbedByATest(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "*_test.go"))
	require.NoError(t, err)
	require.NotEmpty(t, files)

	probed := map[string]bool{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, m := range verifyCall.FindAllStringSubmatch(string(data), -1) {
			probed[m[1]] = true
		}
	}

	var unprobed, unknown []string
	for _, c := range All {
		if reason, exempt := notProbed[c.ID]; exempt {
			assert.Equal(t, Unsupported, c.Status, "%s is exempt (%s), so it must be a recorded gap", c.ID, reason)
			continue
		}
		if c.Status != NotApplicable && !probed[c.ID] {
			unprobed = append(unprobed, c.ID)
		}
	}
	for id := range probed {
		if !exists(id) {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(unprobed)
	sort.Strings(unknown)
	assert.Empty(t, unprobed, "capabilities no end-to-end test verifies: add capabilities.Verify(t, \"<id>\", works) to the test that exercises each")
	assert.Empty(t, unknown, "tests verify capabilities that are not in the matrix")
}

func exists(id string) bool {
	for _, c := range All {
		if c.ID == id {
			return true
		}
	}
	return false
}
