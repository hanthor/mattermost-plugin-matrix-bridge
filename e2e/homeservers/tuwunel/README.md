# Tuwunel Complement image

Builds `local/complement-tuwunel`: the upstream Tuwunel binary (taken from
`ghcr.io/matrix-construct/tuwunel`) in a Debian image with the layout Complement
expects (`/complement/appservice`, `/complement/ca`, a health check, and a
certificate signed with Complement's CA at start).

`complement.toml` and `provision_complement.sh` follow the Complement
configuration shipped with [Tuwunel](https://github.com/matrix-construct/tuwunel),
which is Apache-2.0 licensed like this repository.

```bash
make e2e-tuwunel
```

See [docs/HOMESERVERS.md](../../../docs/HOMESERVERS.md) for results and known gaps.
