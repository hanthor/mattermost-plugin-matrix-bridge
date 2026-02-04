# Documentation Index

Complete documentation for the Mattermost-Matrix Bridge plugin.

## 📚 Documentation Overview

| Document | Description | Audience |
|----------|-------------|----------|
| [Bridge Overview](BRIDGE_OVERVIEW.md) | High-level overview of the bridge, features, and use cases | Everyone |
| [Commands Reference](COMMANDS.md) | Complete guide to all `/matrix` commands | End Users, Admins |
| [Architecture](ARCHITECTURE.md) | Technical architecture and implementation details | Developers, System Architects |
| [Join Command Guide](JOIN_COMMAND.md) | Detailed guide for joining existing Matrix rooms | End Users |

## 🚀 Quick Start

**New to the bridge?** Start here:
1. Read [Bridge Overview](BRIDGE_OVERVIEW.md) to understand what the bridge does
2. Follow [Local Development Guide](../LOCAL_DEVELOPMENT.md) to set up your environment
3. Learn the commands in [Commands Reference](COMMANDS.md)

**Want to join Matrix communities?**
- See [Join Command Guide](JOIN_COMMAND.md)

**Developing or troubleshooting?**
- Review [Architecture](ARCHITECTURE.md) for technical details
- Check [Local Development](../LOCAL_DEVELOPMENT.md) for setup

## 📖 Document Summaries

### [Bridge Overview](BRIDGE_OVERVIEW.md)
Learn what the bridge is, how it works, and what it can do.

**Contents:**
- What is the Mattermost-Matrix Bridge?
- Key features and capabilities
- Architecture diagram
- Component overview
- Data flow (Mattermost ↔ Matrix)
- Ghost users and spaces
- Federation support
- Use cases and examples
- Supported features checklist
- Limitations and known issues

**Best for:** Understanding the big picture before diving into specifics.

---

### [Commands Reference](COMMANDS.md)
Complete reference for all `/matrix` slash commands.

**Contents:**
- Command overview table
- Detailed documentation for each command:
  - `/matrix test` - Test connection
  - `/matrix create` - Create new room
  - `/matrix join` - Join existing room
  - `/matrix map` - Map channel to room
  - `/matrix unmap` - Remove mapping
  - `/matrix list` - List all mappings
  - `/matrix map_user` - Custom user mapping
  - `/matrix backfill` - Sync existing data
  - `/matrix status` - Check status
  - `/matrix migrate` - Fix mappings
- Usage examples
- Troubleshooting guide
- Best practices
- Quick reference card

**Best for:** Day-to-day usage, learning commands, troubleshooting.

---

### [Architecture](ARCHITECTURE.md)
Deep dive into the technical architecture and implementation.

**Contents:**
- System architecture diagram
- Component details (Plugin, Client, Bridge, AS Server, Storage)
- Complete data flow diagrams
- Storage schema and KV store structure
- API integration (Mattermost Plugin API, Matrix CS API, AS API)
- Security model and authentication
- Performance considerations
- Rate limiting implementation
- Monitoring and observability
- Future enhancements

**Best for:** Developers, contributors, system architects, troubleshooting complex issues.

---

### [Join Command Guide](JOIN_COMMAND.md)
Comprehensive guide for the `/matrix join` command.

**Contents:**
- What is the join command?
- Usage and examples
- How it works internally
- Requirements and prerequisites
- Troubleshooting common issues
- Comparison with create and map commands
- Advanced usage (federation, testing)
- Security considerations

**Best for:** Users wanting to connect to existing Matrix communities.

---

## 📝 Related Documentation

### In This Repository

- [Main README](../README.md) - Project overview and introduction
- [Local Development Guide](../LOCAL_DEVELOPMENT.md) - Setup, installation, and testing
- [Plugin JSON](../plugin.json) - Plugin metadata and configuration schema

### External Resources

- [Matrix Specification](https://spec.matrix.org/) - Official Matrix protocol specification
- [Matrix Client-Server API](https://spec.matrix.org/v1.11/client-server-api/) - API for clients
- [Matrix Application Service API](https://spec.matrix.org/v1.11/application-service-api/) - API for bridges/bots
- [Mattermost Plugin Documentation](https://developers.mattermost.com/extend/plugins/) - Official plugin guide
- [Mattermost Plugin API](https://developers.mattermost.com/extend/plugins/server/reference/) - Server plugin API reference

## 🎯 Common Tasks

### For End Users

**I want to bridge a channel to Matrix:**
1. Go to the channel
2. Run `/matrix create` or `/matrix create "Room Name"`
3. Done! Messages will now sync

**I want to join an existing Matrix community:**
1. Find the Matrix room alias (e.g., `#rust:matrix.org`)
2. Run `/matrix join #rust:matrix.org create_channel=true`
3. A new channel will be created and bridged

**I want to see what's bridged:**
```
/matrix list
```

**Messages aren't syncing:**
1. Check if the channel is mapped: `/matrix list`
2. Test the connection: `/matrix test`
3. Try unmapping and remapping: `/matrix unmap` then `/matrix map <room>`

### For Administrators

**Setting up the bridge:**
1. Follow [Local Development Guide](../LOCAL_DEVELOPMENT.md)
2. Generate AS and HS tokens
3. Configure registration file
4. Install and enable plugin
5. Test with `/matrix test`

**Migrating existing data:**
```
/matrix backfill all
```

**Fixing broken mappings:**
```
/matrix migrate
```

**Mapping admin to real Matrix account:**
```
/matrix map_user @admin @admin:matrix.company.com
```

### For Developers

**Understanding the codebase:**
1. Read [Architecture](ARCHITECTURE.md)
2. Review component diagrams
3. Study data flow diagrams
4. Check API integration sections

**Adding a new command:**
1. Add command constants to `command.go`
2. Implement `execute*Command` method
3. Add case to command switch
4. Add to autocomplete registration
5. Document in [Commands Reference](COMMANDS.md)

**Debugging message sync issues:**
1. Check logs for errors
2. Verify mappings in KV store
3. Test Matrix API calls directly
4. Review data flow in [Architecture](ARCHITECTURE.md)

## 🔍 Finding Information

### By Topic

**Authentication & Security:**
- [Architecture - Security Model](ARCHITECTURE.md#security-model)
- [Bridge Overview - Authentication & Security](BRIDGE_OVERVIEW.md#authentication--security)

**Message Syncing:**
- [Architecture - Data Flow](ARCHITECTURE.md#data-flow)
- [Bridge Overview - Data Flow](BRIDGE_OVERVIEW.md#data-flow)

**User Management:**
- [Bridge Overview - Ghost Users](BRIDGE_OVERVIEW.md#ghost-users)
- [Architecture - User Creation Flow](ARCHITECTURE.md#user-creation-flow)
- [Commands - map_user](COMMANDS.md#matrix-map_user)

**Room/Channel Management:**
- [Commands - create](COMMANDS.md#matrix-create)
- [Commands - join](COMMANDS.md#matrix-join)
- [Commands - map](COMMANDS.md#matrix-map)

**Spaces/Teams:**
- [Bridge Overview - Matrix Spaces](BRIDGE_OVERVIEW.md#matrix-spaces)
- [Architecture - Team Mappings](ARCHITECTURE.md#storage-schema)

**Performance:**
- [Architecture - Performance Considerations](ARCHITECTURE.md#performance-considerations)
- [Bridge Overview - Performance & Rate Limiting](BRIDGE_OVERVIEW.md#performance--rate-limiting)

**Storage:**
- [Architecture - Storage Schema](ARCHITECTURE.md#storage-schema)
- [Bridge Overview - Storage & Mappings](BRIDGE_OVERVIEW.md#storage--mappings)

### By Role

**End Users:**
- Start: [Bridge Overview](BRIDGE_OVERVIEW.md)
- Reference: [Commands](COMMANDS.md)
- How-to: [Join Command Guide](JOIN_COMMAND.md)

**Administrators:**
- Setup: [Local Development](../LOCAL_DEVELOPMENT.md)
- Commands: [Commands Reference](COMMANDS.md)
- Troubleshooting: All documents have troubleshooting sections

**Developers:**
- Architecture: [Architecture](ARCHITECTURE.md)
- Overview: [Bridge Overview](BRIDGE_OVERVIEW.md)
- Development: [Local Development](../LOCAL_DEVELOPMENT.md)

## 🤝 Contributing to Documentation

When adding or updating documentation:

1. **Keep it consistent** - Follow the existing structure and style
2. **Update the index** - Add new documents to this README
3. **Cross-reference** - Link to related sections in other documents
4. **Include examples** - Show real command usage and output
5. **Test accuracy** - Verify commands and examples work
6. **Consider the audience** - Write for the document's target readers

## 📜 Documentation Standards

### Formatting

- Use clear headings and sections
- Include code blocks with syntax highlighting
- Add tables for reference information
- Include diagrams where helpful
- Use bullet points for lists
- Add links to related content

### Content

- Start with an overview/introduction
- Include practical examples
- Explain the "why" not just the "how"
- Add troubleshooting sections
- Include both simple and advanced usage
- Keep information up-to-date

### Style

- Write in clear, simple language
- Use active voice
- Be concise but complete
- Define technical terms
- Use consistent terminology
- Include emojis sparingly for visual markers

## 🆘 Getting Help

**Documentation Issues:**
- Check if information is outdated
- Look for typos or errors
- File an issue on GitHub

**Using the Bridge:**
- Read [Commands Reference](COMMANDS.md)
- Check [Troubleshooting sections](BRIDGE_OVERVIEW.md#troubleshooting)
- Test with `/matrix test`

**Development Questions:**
- Review [Architecture](ARCHITECTURE.md)
- Check [Local Development](../LOCAL_DEVELOPMENT.md)
- Look at code comments

**Found a Bug:**
- Check if it's a known limitation
- Look for similar issues on GitHub
- File a detailed bug report

---

## Document Version History

| Date | Document | Change |
|------|----------|--------|
| 2026-02-04 | All | Initial comprehensive documentation created |
| 2026-02-04 | JOIN_COMMAND.md | Created join command guide |
| 2026-02-04 | COMMANDS.md | Added full command reference |
| 2026-02-04 | ARCHITECTURE.md | Added technical architecture documentation |
| 2026-02-04 | BRIDGE_OVERVIEW.md | Added high-level bridge overview |

---

**Last Updated:** February 4, 2026

**Documentation maintained by:** Mattermost-Matrix Bridge Contributors
