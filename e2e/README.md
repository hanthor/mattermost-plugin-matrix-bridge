# End-to-end tests (Complement)

This module runs the bridge against real servers and drives both networks
through their public APIs:

- **Matrix:** a Synapse homeserver deployed by
  [Complement](https://github.com/matrix-org/complement), with the bridge
  registered as an Application Service. Tests use Complement's `client.CSAPI`
  to act as Matrix users.
- **Mattermost:** a Mattermost server (plus Postgres) started with
  testcontainers on the same Docker network, with the plugin bundle installed
  and configured through the REST API.

Synapse reaches the plugin at `http://mattermost:8065/plugins/<id>`, and the
plugin reaches Synapse at `http://hs1:8008`, exactly as a real deployment does.
Nothing is mocked.

## Running

Requirements: Docker, Go (see `go.mod`), Node.js for the webapp build.

```bash
make e2e
```

`make e2e` builds the plugin bundle (`make dist`) and runs the suite with
`COMPLEMENT_BASE_IMAGE=ghcr.io/element-hq/synapse/complement-synapse:develop`.

Useful variables:

| Variable | Purpose |
|----------|---------|
| `COMPLEMENT_BASE_IMAGE` | Homeserver image under test (any Complement-compatible image). |
| `E2E_PLUGIN_BUNDLE` | Path to a plugin `.tar.gz`; defaults to the newest file in `../dist`. |
| `E2E_MATTERMOST_IMAGE` | Mattermost image, default `mattermost/mattermost-enterprise-edition:12.1.0`. |
| `E2E_POSTGRES_IMAGE` | Postgres image, default `postgres:16-alpine`. |
| `E2E_MM_LICENSE` | Optional Mattermost license; enables Shared Channels (Connected Workspaces). |
| `COMPLEMENT_ALWAYS_PRINT_SERVER_LOGS` | Print homeserver logs after each test. |

Run a single test:

```bash
cd e2e && COMPLEMENT_BASE_IMAGE=ghcr.io/element-hq/synapse/complement-synapse:develop \
  go test -count=1 -v -run 'TestMatrixToMattermost' ./...
```

## What is covered

`TestMattermostToMatrix`

- messages sent as the author's ghost user, markdown to HTML
- edits (`m.replace`), deletions (redaction), thread replies (`m.thread`)
- mentions (`m.mentions`), reactions add/remove, file attachments
- channel membership (join and leave) mirrored to the room
- profile (display name) changes
- automatic room creation for new channels
- no echo back into Mattermost

`TestMatrixToMattermost`

- messages create a remote Mattermost user and a post, HTML to markdown
- edits, thread replies, reactions add/remove, redactions
- image uploads become file attachments
- join/leave membership, display name sync
- room name and topic changes update the channel
- typing notifications (ephemeral events) reach Mattermost clients
- no echo back into Matrix

`TestAppServiceEndpoint`

- homeserver authentication (401 without token, 403 with a wrong one)
- appservice ping and user query (lazy ghost provisioning)
- `/matrix test`, `/matrix create`, `/matrix list`, `/matrix unmap`
- permission checks on administrative commands

`TestCapabilityGaps`

- probes every row of `e2e/capabilities` that is documented as a gap or
  partial (room mentions, pins, custom emoji, stickers, locations, polls,
  typing/presence/receipts from Mattermost, deactivation, power levels,
  room-initiated channels, channel rename/archive, widgets, bot commands)
- a gap that unexpectedly works fails the run until the matrix is promoted; a
  supported row that breaks fails the run. `docs/CAPABILITIES.md` is generated
  from the same list (`make capabilities`).

## Walkthrough screenshots

`TestWalkthrough` (skipped unless `E2E_WALKTHROUGH=1`) additionally starts
Element Web and drives both web UIs with Playwright from
[`walkthrough/walkthrough.mjs`](walkthrough/walkthrough.mjs), using
Mattermost's `@mattermost/playwright-lib` page objects and helpers mirrored
from element-web's Playwright suite, writing the images behind
[docs/WALKTHROUGH.md](../docs/WALKTHROUGH.md). Run it with
`make walkthrough` from the repository root.

## Notes

- Each top-level test deploys its own Synapse + Mattermost pair, which takes a
  minute or two. Subtests within a test run in order and share state.
- Complement generates the appservice tokens; the harness reads them back from
  `/complement/appservice/mattermost-bridge.yaml` inside the homeserver
  container and configures the plugin with them.
- Without a license Mattermost runs with the Team Edition feature set. The
  bridge then works in hooks-only mode, which is what the suite exercises by
  default.
