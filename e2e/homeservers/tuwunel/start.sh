#!/bin/bash
# Start Tuwunel for Complement: sign a certificate for SERVER_NAME with
# Complement's CA when it is present, then run the server.
set -e

# Complement writes appservice registrations with the older
# push_ephemeral / de.sorunome.msc2409.push_ephemeral fields. Tuwunel reads the
# spec's receive_ephemeral (the plugin's downloadable registration has it), so
# mirror the value; without it typing and presence never reach the bridge.
for f in /complement/appservice/*.yaml; do
  [ -e "$f" ] || continue
  if grep -q '^push_ephemeral: true' "$f" && ! grep -q '^receive_ephemeral:' "$f"; then
    printf 'receive_ephemeral: true\n' >> "$f"
  fi
done

exec /complement/provision_complement.sh tuwunel "-Oserver_name=\"${SERVER_NAME}\""
