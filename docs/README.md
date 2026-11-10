# Documentation

| Document | What it covers | Audience |
|----------|----------------|----------|
| [../README.md](../README.md) | Feature matrix, quick start for the Mattermost and Matrix sides, configuration reference, troubleshooting | Everyone |
| [CAPABILITIES.md](CAPABILITIES.md) | Generated, test-enforced matrix of what is bridged in each direction, including known gaps | Everyone |
| [ROADMAP.md](ROADMAP.md) | Principles, how to pick something up, and where the plan lives (the issues) | Everyone |
| [COMMANDS.md](COMMANDS.md) | Every `/matrix` slash command, its arguments and who may run it | Users, admins |
| [BRIDGE_OVERVIEW.md](BRIDGE_OVERVIEW.md) | How the bridge works: ghost users, spaces, federation, security model, supported features | Admins, developers |
| [ARCHITECTURE.md](ARCHITECTURE.md) | Components, data flow, KV storage schema, extension points | Developers |
| [BATCH_OPERATIONS.md](BATCH_OPERATIONS.md) | Event queue and batching design | Developers |
| [CALLS_AND_MIRROR_MODE.md](CALLS_AND_MIRROR_MODE.md) | Design: how calls are bridged, and the scenarios mirror mode must handle for a migration | Admins, developers |
| [MIRROR_MODE_SETUP.md](MIRROR_MODE_SETUP.md) | Experimental mirror mode on a dedicated homeserver | Admins |
| [NOTIFICATIONS.md](NOTIFICATIONS.md) | Mentions, keywords and what notifies whom across the bridge | Everyone |
| [PRIVACY.md](PRIVACY.md) | What is copied where, who can read it, retention and erasure gaps | Administrators |
| [OPERATIONS.md](OPERATIONS.md) | Metrics for Prometheus, rate limits, retries, what is per node | Admins |
| [ENCRYPTION.md](ENCRYPTION.md) | End-to-end encrypted rooms: turning it on, what it means, limits | Admins |
| [MAS_SETUP.md](MAS_SETUP.md) | Shared login: Matrix Authentication Service with an OpenID Connect provider | Admins |
| [../LOCAL_DEVELOPMENT.md](../LOCAL_DEVELOPMENT.md) | Docker Compose stack with Mattermost, Synapse and Element | Developers |
| [WALKTHROUGH.md](WALKTHROUGH.md) | Screenshot tour of a bridged channel, generated from the current code | Everyone |
| [../e2e/README.md](../e2e/README.md) | Complement end-to-end suite and how to run it | Developers |

## Where to start

- **Seeing what the bridge does:** skim the [walkthrough](WALKTHROUGH.md).
- **Setting the bridge up:** follow the quick start in the [main README](../README.md),
  then use [COMMANDS.md](COMMANDS.md) to connect channels.
- **Understanding or changing the code:** read [BRIDGE_OVERVIEW.md](BRIDGE_OVERVIEW.md),
  then [ARCHITECTURE.md](ARCHITECTURE.md), and run the [end-to-end suite](../e2e/README.md)
  before opening a pull request.
