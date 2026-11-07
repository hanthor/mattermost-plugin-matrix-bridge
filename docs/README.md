# Documentation

| Document | What it covers | Audience |
|----------|----------------|----------|
| [../README.md](../README.md) | Feature matrix, quick start for the Mattermost and Matrix sides, configuration reference, troubleshooting | Everyone |
| [CAPABILITIES.md](CAPABILITIES.md) | Generated, test-enforced matrix of what is bridged in each direction, including known gaps | Everyone |
| [ROADMAP.md](ROADMAP.md) | Plan for closing the gaps and integrating natively on both the Mattermost and Matrix side | Everyone |
| [COMMANDS.md](COMMANDS.md) | Every `/matrix` slash command, its arguments and who may run it | Users, admins |
| [BRIDGE_OVERVIEW.md](BRIDGE_OVERVIEW.md) | How the bridge works: ghost users, spaces, federation, security model, supported features | Admins, developers |
| [ARCHITECTURE.md](ARCHITECTURE.md) | Components, data flow, KV storage schema, extension points | Developers |
| [BATCH_OPERATIONS.md](BATCH_OPERATIONS.md) | Event queue and batching design | Developers |
| [MIRROR_MODE_SETUP.md](MIRROR_MODE_SETUP.md) | Experimental mirror mode on a dedicated homeserver | Admins |
| [../LOCAL_DEVELOPMENT.md](../LOCAL_DEVELOPMENT.md) | Docker Compose stack with Mattermost, Synapse and Element | Developers |
| [../e2e/README.md](../e2e/README.md) | Complement end-to-end suite and how to run it | Developers |

## Where to start

- **Setting the bridge up:** follow the quick start in the [main README](../README.md),
  then use [COMMANDS.md](COMMANDS.md) to connect channels.
- **Understanding or changing the code:** read [BRIDGE_OVERVIEW.md](BRIDGE_OVERVIEW.md),
  then [ARCHITECTURE.md](ARCHITECTURE.md), and run the [end-to-end suite](../e2e/README.md)
  before opening a pull request.
