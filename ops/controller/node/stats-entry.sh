#!/bin/sh
set -eu
test -z "${SSH_ORIGINAL_COMMAND:-}"
exec /opt/sbctl/bin/sbctl-node stats-rpc
