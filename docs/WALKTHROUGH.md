# Walkthrough: setting up the bridge and what it does, on both sides

Every screenshot on this page is produced automatically by
[`e2e/walkthrough_test.go`](../e2e/walkthrough_test.go) against the code in
this repository. The test deploys Synapse, Mattermost and Element Web in
containers, creates demo people on both networks (Ada, Carol and Dave on
Mattermost; Alice and Bob on Matrix, each with a name and avatar), drives
both web UIs with Playwright, and writes the images to
[`docs/walkthrough/`](walkthrough/). The pictures therefore show the current
release, not a hand-made mock-up, and the run fails if any step does not
behave as described here.

Regenerate them with `make walkthrough` (Docker, Node.js and a Playwright
Chromium required), or let the
[walkthrough workflow](../.github/workflows/walkthrough.yml) do it: it runs on
every push to `master`, weekly, and on demand, and opens a pull request when
the images change.

- [Part 1: setup](#part-1-setup)
  - [Mattermost side](#mattermost-side)
  - [Homeserver side](#homeserver-side)
  - [Bridge a channel](#bridge-a-channel)
  - [Matrix user side](#matrix-user-side)
- [Part 2: features, side by side](#part-2-features-side-by-side)
- [Part 3: day-to-day operations](#part-3-day-to-day-operations)
- [How the screenshots are made](#how-the-screenshots-are-made)

## Part 1: setup

### Mattermost side

**1. Install the plugin.** Download the bundle from the releases page and
upload it under **System Console → Plugins → Plugin Management**, then
enable it.

![Plugin Management with the bridge installed](walkthrough/setup-mm-plugin-management.png)

**2. Configure it.** Open **System Console → Plugins → Mattermost bridge for
Matrix**. Enter the homeserver URL as reachable from the Mattermost server
and, if it differs from the URL's host, the Matrix server name. The plugin
generates the two application service tokens for you.

![The plugin settings](walkthrough/setup-mm-plugin-settings.png)

**3. Download the registration file.** Further down the same page the plugin
builds the appservice registration from these settings. Download it; it goes
to the homeserver in the next step.

![The registration download](walkthrough/setup-mm-registration-download.png)

### Homeserver side

**4. Register the appservice.** Place the downloaded file on the homeserver
and reference it from Synapse's configuration, then restart Synapse. This is
the registration the demo homeserver in these screenshots runs with
([`walkthrough/registration.yaml`](walkthrough/registration.yaml), tokens
removed):

```yaml
# homeserver.yaml
app_service_config_files:
  - /data/mattermost-bridge.yaml
```

The registration reserves the `@_mattermost_*` user namespace for ghost
users, names the bridge bot `@_mattermost_bridge`, and asks for ephemeral
events so typing and presence reach Mattermost. Other homeservers (Conduit,
Dendrite, conduwuit) take the same file.

**5. Check the connection.** In any channel, run `/matrix test`. The reply
confirms the homeserver is reachable, the token is accepted, and the
appservice may act in its namespace.

![/matrix test](walkthrough/setup-mm-matrix-test.png)

### Bridge a channel

**6. Create or map a room.** `/matrix create` creates a Matrix room for the
current channel and bridges it in one step; `publish=true` makes it a public
room that any Matrix user can join, otherwise it is invite-only. To bridge an
existing room instead, invite the bridge bot to it and run
`/matrix map !room:server` or `/matrix map #alias:server`. New channels get a
room automatically from then on.

![/matrix create](walkthrough/setup-mm-matrix-create.png)

### Matrix user side

**7. Join from Matrix.** Nothing to install: the bridged room is an ordinary
Matrix room. Alice opens it in Element and joins.

![Room preview in Element](walkthrough/setup-element-room-preview.png)

The member list shows the bridge bot and one ghost user per channel member,
with the Mattermost names and avatars.

![Member list in Element](walkthrough/setup-element-members.png)

Back in Mattermost, Alice has joined the channel as a remote user.

![Matrix user joined the channel](walkthrough/setup-mm-matrix-user-joined.png)

## Part 2: features, side by side

Each feature is shown as it looks in Mattermost and in Element. The
[capability matrix](CAPABILITIES.md), generated from the end-to-end suite,
is the authoritative list of what is bridged and what is still a gap.

### Messages with formatting

| Mattermost | Element |
| --- | --- |
| ![Ada posts a formatted message](walkthrough/feature-mm-message.png) | ![The same message in Element](walkthrough/feature-element-message.png) |

Markdown becomes Matrix HTML and back. Messages from Mattermost are sent by
the author's ghost, with their display name and avatar; messages from Matrix
are posted by a remote user carrying the Matrix display name and avatar.

![Alice's reply in Mattermost](walkthrough/feature-mm-message-from-matrix.png)

### Threads

| Mattermost | Element |
| --- | --- |
| ![A thread in Mattermost](walkthrough/feature-mm-thread.png) | ![The same thread in Element](walkthrough/feature-element-thread.png) |

Mattermost thread replies become Matrix threads, and Matrix thread or reply
events land in the post's thread.

### Edits

| Mattermost | Element |
| --- | --- |
| ![An edit from Matrix in Mattermost](walkthrough/feature-mm-edit.png) | ![An edit from Mattermost in Element](walkthrough/feature-element-edit.png) |

Edits are applied in place on both sides and marked as edited. Deletions
and redactions are mirrored the same way.

### Reactions

| Mattermost | Element |
| --- | --- |
| ![Reactions in Mattermost](walkthrough/feature-mm-reactions.png) | ![Reactions in Element](walkthrough/feature-element-reactions.png) |

Mattermost emoji names are mapped to Unicode for Matrix, and back. Removing
a reaction removes it on the other side too.

### Mentions

| Mattermost | Element |
| --- | --- |
| ![A Matrix mention in Mattermost](walkthrough/feature-mm-mention.png) | ![A Mattermost mention in Element](walkthrough/feature-element-mention.png) |

Mentioning a ghost in Matrix mentions the real Mattermost user; mentioning
a remote user in Mattermost produces a Matrix pill.

### Attachments

| Mattermost | Element |
| --- | --- |
| ![An image from Matrix in Mattermost](walkthrough/feature-mm-attachment.png) | ![An image from Mattermost in Element](walkthrough/feature-element-attachment.png) |

Images, files, audio and video are re-uploaded on the other side, one
Matrix event per attachment.

### Typing indicators

![Typing indicator in Mattermost](walkthrough/feature-mm-typing.png)

Typing in Element is shown in Mattermost through appservice ephemeral
events. Presence is forwarded the same way. The other direction needs a
Mattermost websocket client in the plugin and is on the
[roadmap](ROADMAP.md).

### Room topic and channel header

| Mattermost | Element |
| --- | --- |
| ![The channel header follows the room topic](walkthrough/feature-mm-topic.png) | ![The room topic in Element](walkthrough/feature-element-topic.png) |

Room name and topic changes in Matrix update the channel name and header.

### Membership

| Mattermost | Element |
| --- | --- |
| ![Joins in Mattermost](walkthrough/feature-mm-membership.png) | ![Members in Element](walkthrough/feature-element-membership.png) |

Adding Dave to the channel joins his ghost to the room; Bob joining from
Matrix adds him to the channel as a remote user. Leaving works the same way.

### Direct messages

| Mattermost | Element |
| --- | --- |
| ![The DM in Mattermost](walkthrough/feature-mm-dm.png) | ![The DM in Element](walkthrough/feature-element-dm.png) |

Alice starts a direct chat with Carol's ghost in Element; it becomes a
Mattermost direct message, and Carol's reply flows back.

### One identity on both networks

| Mattermost | Element |
| --- | --- |
| ![/matrix link](walkthrough/feature-mm-matrix-link.png) | ![The bridge bot conversation](walkthrough/feature-element-bot-dm.png) |

Carol runs `/matrix link` in Mattermost and gets a one-time code. In Element
she sends `!mattermost link <code>` to the bridge bot; from then on her
Matrix messages post as her Mattermost account. With
`!mattermost login <access token>` the bridge also sends her Mattermost
messages from her own Matrix account instead of a ghost:

![Double puppeted message in Element](walkthrough/feature-element-double-puppet.png)

This is the stepping stone for moving a community from Mattermost to Matrix
without anyone changing identity.

## Part 3: day-to-day operations

| `/matrix status` | `/matrix list` |
| --- | --- |
| ![/matrix status](walkthrough/ops-mm-matrix-status.png) | ![/matrix list](walkthrough/ops-mm-matrix-list.png) |

`/matrix status` shows the sync state of the current channel and
`/matrix list` every mapping in the team. Administrators also have
`/matrix metrics` for the bridge counters and `/matrix backfill` to bring
history across. See [COMMANDS.md](COMMANDS.md) for the full reference and who
may run what.

## How the screenshots are made

| Piece | Role |
| --- | --- |
| [`e2e/walkthrough_test.go`](../e2e/walkthrough_test.go) | Gated on `E2E_WALKTHROUGH=1`. Deploys the stack with the shared harness, starts an Element Web container pointed at the Complement homeserver, writes the redacted registration file, and spawns the Playwright script with the URLs and credentials in `WALKTHROUGH_SCENARIO`. Fails if any step in the manifest failed. |
| [`e2e/walkthrough/walkthrough.mjs`](../e2e/walkthrough/walkthrough.mjs) | Creates the demo people (rendered initials avatars, names), then drives Mattermost with [`@mattermost/playwright-lib`](https://www.npmjs.com/package/@mattermost/playwright-lib), Mattermost's own Playwright page objects (API login, `ChannelsPage`, `SystemConsolePage`), and captures a screenshot per step. Actions that are not what a picture is about go through the REST APIs so the images stay stable across UI releases. Writes `manifest.json` with the captions. |
| [`e2e/walkthrough/element.mjs`](../e2e/walkthrough/element.mjs) | Element helpers mirrored from [element-web's Playwright suite](https://github.com/element-hq/element-web/tree/develop/apps/web/playwright): credential injection, toast handling, composer, member list and timeline selectors. |
| [`.github/workflows/walkthrough.yml`](../.github/workflows/walkthrough.yml) | Runs the test in CI, uploads the images (and, on failure, per-step debug screenshots) as artifacts, and on `master` opens a pull request with the refreshed images. |

Set `WALKTHROUGH_OUT_DIR` to write the images somewhere else and
`E2E_ELEMENT_IMAGE` to pin a specific Element Web release.
