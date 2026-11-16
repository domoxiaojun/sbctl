#!/bin/sh
set -eu
test -z "${SSH_ORIGINAL_COMMAND:-}"
exec /usr/bin/sudo -n /usr/local/sbin/sbctl-node-rpc
