# Walkthrough: bridging a channel end to end

This tutorial follows one channel from installation to a linked identity, on
both sides of the bridge. Every screenshot is produced automatically by
[`e2e/walkthrough_test.go`](../e2e/walkthrough_test.go) against the code in
this repository: the test deploys Synapse, Mattermost and Element Web in
containers, drives both web UIs with Playwright, and writes the images to
[`docs/walkthrough/`](walkthrough/). The pictures therefore always show the
current release, not a hand-made mock-up.

Regenerate them locally with `make walkthrough` (needs Docker, Node.js and a
Playwright Chromium), or let the [walkthrough workflow](../.github/workflows/walkthrough.yml)
do it: it runs on every push to `master`, weekly, and on demand, and opens a
pull request when the images change.

## 1. Configure the plugin

Install the plugin bundle from the releases page, then open **System Console →
Plugins → Mattermost bridge for Matrix**. Enter the homeserver URL and server
name; the plugin generates the two appservice tokens for you. Download the
registration file, place it on the homeserver and reference it from Synapse's
`app_service_config_files`, then restart Synapse and enable the plugin.

![Plugin settings in the System Console](walkthrough/01-plugin-settings.png)

## 2. Check the connection

In any channel, run `/matrix test`. The reply confirms that the homeserver is
reachable, that the appservice token is accepted, and which bot and ghost
namespace the bridge owns.

![/matrix test output](walkthrough/02-matrix-test.png)

## 3. Create or map a room

`/matrix create` creates a Matrix room for the current channel and bridges it
in one step; add `publish=true` for a public room listed in the directory that
anyone on Matrix can join, otherwise the room is invite-only. To bridge an
existing room instead, invite the bridge bot and run `/matrix map !room:server`
or `/matrix map #alias:server`. `/matrix list` shows every bridged channel in
the team.

![/matrix create output](walkthrough/03-matrix-create.png)

## 4. Post from Mattermost

Anything posted in the channel is relayed to the room by a ghost Matrix user
carrying the Mattermost display name and avatar. Markdown, mentions, emoji,
attachments, edits and deletions are preserved.

![Message in Mattermost](walkthrough/04-mattermost-message.png)

![The same message in Element](walkthrough/05-element-bridged-message.png)

## 5. Reply from Matrix

Replies from Element arrive in Mattermost as remote users with the Matrix
display name. Threads, replies, reactions, typing notifications and read
receipts flow in this direction too.

![Matrix reply in Mattermost](walkthrough/06-mattermost-bridged-reply.png)

![Matrix reaction in Mattermost](walkthrough/07-mattermost-reaction.png)

## 6. Be one person on both networks

Run `/matrix link` in Mattermost to get a one-time code, then send
`!mattermost link <code>` to the bridge bot in a Matrix DM. From then on your
Matrix messages post as your Mattermost account. Add
`!mattermost login <access token>` and your Mattermost messages appear from
your own Matrix account too, instead of a ghost: this is the stepping stone for
moving a community from Mattermost to Matrix without anyone losing their
identity.

![/matrix link output](walkthrough/08-matrix-link.png)

![Bridge bot conversation in Element](walkthrough/09-element-bot-dm.png)

![Mattermost message sent by the linked Matrix account](walkthrough/10-element-double-puppet.png)

## 7. Keep an eye on things

`/matrix list` shows the mappings, `/matrix status` the sync state of the
current channel, and `/matrix metrics` (admins) the bridge counters. The
[capability matrix](CAPABILITIES.md) lists exactly which features are
bridged today and which are still gaps, as verified by the end-to-end suite.

![/matrix list output](walkthrough/11-matrix-list.png)

## How the screenshots are made

| Piece | Role |
| --- | --- |
| [`e2e/walkthrough_test.go`](../e2e/walkthrough_test.go) | Gated on `E2E_WALKTHROUGH=1`. Deploys the stack with the shared harness, starts an Element Web container pointed at the Complement homeserver, and spawns the Playwright script with the URLs and credentials in `WALKTHROUGH_SCENARIO`. |
| [`e2e/walkthrough/walkthrough.mjs`](../e2e/walkthrough/walkthrough.mjs) | Drives Mattermost with [`@mattermost/playwright-lib`](https://www.npmjs.com/package/@mattermost/playwright-lib), Mattermost's own Playwright page objects (API login, `ChannelsPage`, `SystemConsolePage`), runs the commands above, waits for each bridged event and captures a screenshot per step. Each step is independent, so one UI change does not hide the rest of the tutorial. Writes `manifest.json` with the captions. |
| [`e2e/walkthrough/element.mjs`](../e2e/walkthrough/element.mjs) | Element helpers mirrored from [element-web's Playwright suite](https://github.com/element-hq/element-web/tree/develop/apps/web/playwright): credential injection, toast handling, composer and timeline selectors. |
| [`.github/workflows/walkthrough.yml`](../.github/workflows/walkthrough.yml) | Runs the test in CI, uploads the images as an artifact, and on `master` opens a pull request with the refreshed images. |

Set `WALKTHROUGH_OUT_DIR` to write the images somewhere else and
`E2E_ELEMENT_IMAGE` to pin a specific Element Web release.
