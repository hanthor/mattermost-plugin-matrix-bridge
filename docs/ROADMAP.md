# Roadmap: a bridge that is native on both sides

Most Matrix bridges connect to a closed network and can only do what that
network's API allows. This bridge connects two free-software networks, and it
runs *inside* Mattermost as a plugin while acting as a full Application
Service on the Matrix side. That lets us integrate deeply on both ends: plugin
UI, slash commands and hooks in Mattermost; widgets, bot commands, state events
and MSCs in Matrix.

**What works today** is in [CAPABILITIES.md](CAPABILITIES.md), generated from
`e2e/capabilities/capabilities.go` and enforced: the end-to-end suite probes
every row, and fails the build if a documented gap silently starts working (so
the matrix is promoted) or a supported capability breaks. **What is planned** is
in the GitHub issues, not here; this page keeps the principles and how to pick
something up, so it cannot drift from the issues.

## Principles

1. **Test first, in both directions.** A capability is added by promoting its
   row in `capabilities.go`; the row's probe is then a regression test, run
   against real servers (Complement) rather than mocks.
2. **Users should not notice the bridge.** Mattermost users see accounts with
   real names, avatars, presence and typing; Matrix users see the same.
3. **No licence requirements.** Everything works on Team Edition through plugin
   hooks; Shared Channels is an optional path (see the decision in
   [ARCHITECTURE.md](ARCHITECTURE.md)).
4. **Spec first, MSC second, custom last.** Prefer stable spec features, then
   widely deployed MSCs (MSC2409 ephemeral, MSC3381 polls, MSC4027 image
   reactions, MSC2545 emoji packs, MSC4144 per-message profiles), and only then
   bridge-specific events in the `com.mattermost.bridge.*` namespace.

## What the plan became

The work was organised as epics, each now a set of issues with their own
history: one identity on both sides, conversation fidelity, ephemeral state
through a websocket client, native UI, the migration toolkit, and operations
(delivery guarantees, security, encryption, privacy, federation tests). The
design of calls and of mirror mode is in
[CALLS_AND_MIRROR_MODE.md](CALLS_AND_MIRROR_MODE.md); setup and operation are in
[MIRROR_MODE_SETUP.md](MIRROR_MODE_SETUP.md), [MAS_SETUP.md](MAS_SETUP.md) and
[OPERATIONS.md](OPERATIONS.md).

## How to pick something up

1. Find the row in `e2e/capabilities/capabilities.go`, or the open issue.
2. Run the probe or write the test first (`cd e2e && go test -run <name> ./...`;
   needs Docker and `COMPLEMENT_BASE_IMAGE`).
3. Implement the feature; flip or add the row; run `make capabilities`.
4. The test is now a regression test.
