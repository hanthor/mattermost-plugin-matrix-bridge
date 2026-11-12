# Operating the bridge

## Metrics for Prometheus

The plugin serves Prometheus metrics at

```
https://<mattermost>/plugins/com.mattermost.plugin-matrix-bridge/metrics
```

It is guarded with a bearer token. Set a dedicated **Metrics Token** in the
plugin settings and use it for Prometheus; it opens `/metrics` and nothing else.
Without one the **homeserver token** (`hs_token`) is accepted, as in earlier
versions, which gives whoever runs Prometheus the credential the homeserver
uses to post transactions to the bridge. Once a metrics token is set the
homeserver token is no longer accepted on `/metrics`.

```yaml
scrape_configs:
  - job_name: matrix-bridge
    metrics_path: /plugins/com.mattermost.plugin-matrix-bridge/metrics
    scheme: https
    authorization:
      credentials: "<metrics token>"
    static_configs:
      - targets: ["mattermost.example.com"]
```

Without the token the route answers 401, with a wrong one 403. It answers 503
while **Enable Message Sync** is off. The same numbers are available in
Mattermost with `/matrix metrics`.

| Metric | Meaning |
|---|---|
| `matrix_bridge_messages_to_matrix_total`, `..._from_matrix_total` | Messages synced in each direction |
| `matrix_bridge_message_sync_errors_total`, `matrix_bridge_errors_total{type}` | Failures |
| `matrix_bridge_matrix_api_calls_total`, `..._errors_total`, `..._retries_total` | Calls to the homeserver |
| `matrix_bridge_message_latency_{avg,max}_ms`, `matrix_bridge_api_latency_{avg,max}_ms` | Time to sync a message / to make a call |
| `matrix_bridge_queue_depth`, `matrix_bridge_queue_capacity` | Events waiting for a worker |
| `matrix_bridge_mirror_accounts`, `..._claimed` | Mirror mode accounts |
| `matrix_bridge_rate_limit_responses_total`, `matrix_bridge_rate_limit_factor` | 429s received, and the share of the configured request rate in use |
| `matrix_bridge_encryption_ready` | 1 when the bot's encryption device is running |

## Homeserver rate limits

The rate limiting mode in the settings is a guess at the homeserver's limits.
The bridge also listens to the homeserver: when it answers `429
M_LIMIT_EXCEEDED` with `retry_after_ms`, every request pauses until that time,
the refused request is repeated (up to three times), and the bridge's own
limiters slow to half their rate for each 429 (never below a tenth). After a
run of successful requests they speed back up. Watch
`matrix_bridge_rate_limit_factor`: if it stays below 1, raise the homeserver's
limits for the appservice (`rate_limited: false` in the registration, which the
downloadable file sets) or choose a slower mode.

## Delivery guarantees

Nothing the bridge has accepted is lost to a restart or an outage on either side.

**Matrix → Mattermost.** Each event is written to the key-value store before
the homeserver is told its transaction arrived, and removed when its handler
has finished. A crash leaves the entry behind and it is replayed when the
plugin starts (and every half minute if the queue was full). Events of one
room are handled by one worker, in order; rooms run in parallel. A handler that
fails is tried three times (half a second, then two seconds apart); after that
the event is set aside. If the bridge is too busy to accept an event at all, it
answers the transaction with 503 and the homeserver sends it again.

**Mattermost → Matrix.** Posts, edits, deletes and reactions are written to the
key-value store before they are sent and removed when Matrix has them. They go
through the same queue, one lane per channel, so a channel's changes arrive in
the order they were made. While the homeserver is down an entry waits and is
tried again with a growing delay (5 s, 10 s … up to 5 minutes) by a pass that
runs every 15 seconds; entries older than a day are set aside. A message that
had to wait carries the time it was finally sent.

**Set-aside changes.** `/matrix status` shows how many there are, `/matrix
retry` (system administrators) queues them again, and the metrics
`matrix_bridge_dead_letters`, `matrix_bridge_outbox_pending`,
`matrix_bridge_outbox_failed` and `matrix_bridge_inbox_pending` track them.

A replayed event is delivered at least once, not exactly once: a Matrix
message that already has a post is skipped; an edit or reaction replayed twice
has the same result twice.

## Retried transactions

The homeserver retries a transaction until the bridge acknowledges it. Handled
transaction IDs are kept in Mattermost's key-value store for an hour, so a retry
that reaches another node of a cluster, or follows a plugin restart, is
recognised and not handled twice. Two nodes receiving the same transaction at
the very same instant are not guarded against; a homeserver sends one
transaction at a time to an appservice, so that does not happen in practice.

## Typing, status and read receipts

Off by default; switch on **Send Typing, Presence and Read Receipts** in the
plugin settings. Mattermost has no plugin hook for these, so:

- **Typing** is read from the server's own event stream. Typing events only
  reach channel members, so a small bot account, `matrix-bridge-events`, joins
  every bridged channel (its join appears once in each channel, and the bot also
  joins the channel's team). Its access token is kept sealed like the other
  secrets.
- **Status, custom status and read state** are checked every 30 seconds
  (**Status Check Interval** changes that; the minimum is 2). Online, away and
  do-not-disturb become online, unavailable and unavailable. A custom status
  becomes the status message. A person who viewed a recently active channel
  after its latest bridged message gets a read receipt on that message.
- **In a cluster** one server holds a cluster lock and does all of this; if it
  stops, another takes over.
- **Matrix → Mattermost read receipts** cannot be done: Mattermost has no plugin
  API to mark a channel viewed for another person.

## Running on several nodes

Shared between nodes (key-value store or cluster events):

- Transaction IDs, the inbound journal and the outbox.
- The record of events the bridge sent, used to recognise their echo, so an
  echo that reaches another node is still ignored.
- The history-import lock: two nodes cannot import the same channel at once.
- Mapping changes: `/matrix map`, `unmap`, `join`, `create` and DM changes tell
  the other nodes to drop their cached mapping for the channel at once.

Still per node:

- Other caches (ghost accounts, room membership) expire on their own after
  their time to live; a change on one node can take up to that long to show on
  another.
- The websocket-based extras (typing, presence) have no single-consumer
  election yet (#35).
- The event queue and the reaction batcher are in memory; what they hold is
  journaled, so a restart replays it.
- Encryption needs a single node (see [ENCRYPTION.md](ENCRYPTION.md)).
- A Mattermost server configuration change that does not touch the bridge's
  settings no longer rebuilds the Matrix client, the bridges or the encryption
  device.

Not covered by an automated test: a real two-node Mattermost needs a license.
The behaviours above are covered by unit tests that simulate a second node.

## Secrets and what protects them

| Secret | Where it lives | Protection |
|---|---|---|
| Personal Matrix access tokens (`!mattermost login`) | Key-value store | Sealed with AES-256-GCM; the key is derived from the appservice token in the plugin settings, which is kept apart from the key-value store. Tokens from earlier versions are sealed the next time they are read. |
| Encryption key for the bot's Olm/Megolm state | Key-value store | Sealed in the same way. The state itself is encrypted with that key. |
| Appservice, homeserver, metrics, admin and MAS secrets | Plugin settings | Declared secret, so the System Console masks them. |

A copy of the key-value store alone (a database dump of that table, a support
packet) therefore does not reveal tokens or encryption keys. Someone who also
holds the plugin settings does, and so does anyone with the appservice token.

**Rotating the appservice token** makes the sealed values unreadable. People
with a personal token then fall back to a ghost until they send
`!mattermost login` again, and the encryption state cannot be opened: the bridge
refuses to start encryption rather than create a new device silently. Plan a
rotation for a quiet time, and see [ENCRYPTION.md](ENCRYPTION.md) before
rotating when encrypted rooms are bridged.

## Public routes

- `/sso/mattermost/token` is only served when **Matrix Authentication Service
  URL** is set, and allows 30 requests a minute per client address.
- A Matrix transaction larger than 8 MB is refused; real transactions are a few
  kilobytes, because files travel as media links.
- `!mattermost login <token>` messages are removed from the room by the bot
  where it has the power to do so; otherwise the bot asks the person to delete
  the message and to sign out of that session to get a new token.

## Linking people by verified email

Turn on **Link people by verified email** (needs the **Matrix Admin Access
Token**) when the same people use both systems and their email addresses are
verified in both. The first time a Matrix person appears in a bridged room, by
joining or writing, the bridge reads the verified email addresses of their
Matrix account from the homeserver and, if a Mattermost account with the same
verified address exists, links the two: their Matrix messages then post as the
real Mattermost account instead of as a separate remote user.
When the homeserver signs people in through Matrix Authentication Service and the
MAS admin client is configured (docs/MAS_SETUP.md), the bridge also asks MAS for
a session of the person's own account at that moment, so their Mattermost
messages appear from it too, with no command. If MAS cannot provide one, the
link still holds and the person can run `!mattermost login`.

It is deliberately cautious: both addresses must be verified, the Mattermost
account must be active and not already linked to another Matrix account, and
people who already have a remote account are left as they are (link them with
`/matrix link`). It does not obtain the person's Matrix token, so Mattermost
messages still come from their ghost until they run `!mattermost login` or sign
in through shared login.
