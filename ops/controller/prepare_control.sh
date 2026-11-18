#!/bin/bash
# Fresh controller initialization on ovhus with an installed Go binary.
set -euo pipefail
[[ $EUID -eq 0 ]]
test -x /opt/sbctl/bin/sbctl
test -f /opt/sbctl/config/server.toml
test ! -e /opt/sbctl/data/control/control.db
test ! -e /opt/sbctl/config/admin.initial
install -d -m 0755 /opt/sbctl/config /opt/sbctl/data
install -d -m 0700 /opt/sbctl/config/ssh
install -d -m 0700 -o 10001 -g 10001 /opt/sbctl/data/control
install -d -m 0755 -o 10001 -g 10001 /opt/sbctl/data/publication /opt/sbctl/data/publication/subscriptions /opt/sbctl/data/inventory
install -d -m 0750 -o 10002 -g 10002 /opt/sbctl/data/traffic
install -d -m 0755 -o 10002 -g 10002 /opt/sbctl/data/telemetry
install -d -m 0755 /opt/sbctl/data/discovery
for role in publish stats; do
  key="/opt/sbctl/config/ssh/${role}_ed25519"
  if [[ ! -e "$key" ]]; then
    ssh-keygen -q -t ed25519 -N '' -C "sbctl-$role@ovhus" -f "$key"
  fi
done
chown 10001:10001 /opt/sbctl/config/ssh/publish_ed25519
chown 10002:10002 /opt/sbctl/config/ssh/stats_ed25519
chmod 0600 /opt/sbctl/config/ssh/*_ed25519
install -m 0600 -o 10001 -g 10001 /opt/sbctl/config/ssh/stats_ed25519 /opt/sbctl/config/ssh/worker_stats_ed25519
SBCTL_CONFIG=/opt/sbctl/config/server.host.toml /opt/sbctl/bin/sbctl initialize --password-file /opt/sbctl/config/admin.initial
chown 10001:10001 /opt/sbctl/config/master.key /opt/sbctl/data/control/*
printf 'control prepared; initial password stored in /opt/sbctl/config/admin.initial\n'
