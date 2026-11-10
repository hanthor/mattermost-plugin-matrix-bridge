# Operating the bridge

## Metrics for Prometheus

The plugin serves Prometheus metrics at

```
https://<mattermost>/plugins/com.mattermost.plugin-matrix-bridge/metrics
```

It is guarded with the **homeserver token** (the `hs_token` in the appservice
registration, shown in the plugin settings) as a bearer token:

```yaml
scrape_configs:
  - job_name: matrix-bridge
    metrics_path: /plugins/com.mattermost.plugin-matrix-bridge/metrics
    scheme: https
    authorization:
      credentials: "<hs_token>"
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

## What is still per node

- The websocket-based extras (typing, presence) have no single-consumer
  election yet (#35).
- The record of events the bridge sent itself (used to ignore their echo) is in
  memory on each node.
- Encryption needs a single node (see [ENCRYPTION.md](ENCRYPTION.md)).
