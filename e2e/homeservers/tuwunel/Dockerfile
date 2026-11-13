# Complement-ready Tuwunel: Debian with a shell and openssl, the prebuilt
# Tuwunel binary, and Tuwunel's own Complement configuration.
# Pinned by digest; bump it on purpose.
ARG TUWUNEL_IMAGE=ghcr.io/matrix-construct/tuwunel@sha256:3dde1e9b3503c68218f95415ec3ebff522bd945065ba76019d408543540d1730
FROM ${TUWUNEL_IMAGE} AS tuwunel
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends bash openssl ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
COPY --from=tuwunel /usr/bin/tuwunel /usr/bin/tuwunel
RUN mkdir -p /database /complement/appservice
COPY complement.toml /complement/complement.toml
COPY provision_complement.sh /complement/provision_complement.sh
# A certificate for when the image runs outside Complement; Complement's own CA
# replaces it at start (provision_complement.sh).
RUN openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj "/CN=hs1" \
      -keyout /complement/private_key.pem -out /complement/certificate.crt \
    && chmod a+x /complement/provision_complement.sh
ENV TUWUNEL_CONFIG=/complement/complement.toml
EXPOSE 8008 8448
HEALTHCHECK --interval=1s --timeout=3s --retries=60 CMD curl -fsS http://localhost:8008/_matrix/client/versions || exit 1
COPY start.sh /complement/start.sh
RUN chmod a+x /complement/start.sh
ENTRYPOINT ["/complement/start.sh"]
