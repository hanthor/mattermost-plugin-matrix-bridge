// Package e2e runs the Mattermost Matrix bridge against a real Synapse
// homeserver deployed by Complement and a real Mattermost server, and drives
// both sides through their public APIs.
//
// Requirements:
//   - Docker
//   - COMPLEMENT_BASE_IMAGE pointing at a Complement-compatible homeserver
//     image (for example ghcr.io/element-hq/synapse/complement-synapse:develop)
//   - A built plugin bundle (make dist) or E2E_PLUGIN_BUNDLE pointing at one
//
// See README.md in this directory for details.
package e2e

import (
	"testing"

	"github.com/matrix-org/complement"
)

func TestMain(m *testing.M) {
	complement.TestMain(m, "mmbridge")
}
