#!/bin/bash
# Start Tuwunel for Complement: sign a certificate for SERVER_NAME with
# Complement's CA when it is present, then run the server.
set -e
exec /complement/provision_complement.sh tuwunel "-Oserver_name=\"${SERVER_NAME}\""
