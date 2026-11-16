#!/bin/sh
set -eu
test "$#" -eq 0
exec /usr/bin/env -i PATH=/usr/bin:/bin SBCTL_NODE_CONFIG=/opt/sbctl/config/node.toml \
  /opt/sbctl/bin/sbctl-node rpc
