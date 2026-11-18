#!/bin/bash
# Install the fixed helper before the first web-based node inspection.
# Does not inspect, change or restart any proxy container.
set -euo pipefail
[[ $EUID -eq 0 ]]
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
test -x /opt/sbctl/bin/sbctl-node
install -d -m 0755 /opt/sbctl/config /opt/sbctl/data /opt/sbctl/data/safety /opt/sbctl/data/sing-box /opt/sbctl/bin
install -d -m 0700 /opt/sbctl/data/node
for account in sbctl-publish sbctl-stats; do
  if ! id "$account" >/dev/null 2>&1; then
    useradd --system --user-group --create-home --home-dir "/opt/sbctl/data/$account" --shell /bin/sh "$account"
  fi
  account_home=$(getent passwd "$account" | cut -d: -f6)
  install -d -m 0700 -o "$account" -g "$account" "$account_home/.ssh"
done
install -d -m 0750 -o sbctl-stats -g sbctl-stats /opt/sbctl/data/ledger
install -m 0755 "$root/publish-entry.sh" /opt/sbctl/bin/sbctl-publish
install -m 0755 "$root/stats-entry.sh" /opt/sbctl/bin/sbctl-stats
install -m 0755 "$root/rpc-root.sh" /opt/sbctl/bin/sbctl-node-rpc
install -m 0755 "$root/runtime-root.sh" /opt/sbctl/bin/sbctl-runtime
ln -sfn /opt/sbctl/bin/sbctl-node /usr/local/bin/sbctl-node
ln -sfn /opt/sbctl/bin/sbctl-publish /usr/local/bin/sbctl-publish
ln -sfn /opt/sbctl/bin/sbctl-stats /usr/local/bin/sbctl-stats
ln -sfn /opt/sbctl/bin/sbctl-node-rpc /usr/local/sbin/sbctl-node-rpc
ln -sfn /opt/sbctl/bin/sbctl-runtime /usr/local/sbin/sbctl-runtime
install -m 0644 "$root/guard.sh" /opt/sbctl/data/safety/guard.sh
sudoers=$(mktemp)
trap 'unlink "$sudoers"' EXIT
cat >"$sudoers" <<'SUDO'
sbctl-publish ALL=(root) NOPASSWD: /usr/local/sbin/sbctl-node-rpc ""
sbctl-stats ALL=(root) NOPASSWD: /usr/local/sbin/sbctl-runtime ""
SUDO
visudo -cf "$sudoers"
install -m 0440 "$sudoers" /etc/sudoers.d/sbctl-node
install -m 0644 "$root/sbctl-node.service" /etc/systemd/system/sbctl-node.service
install -m 0644 "$root/sbctl-collector.service" /etc/systemd/system/sbctl-collector.service
systemctl daemon-reload
printf 'Fixed helper installed. Configure authorized_keys and verify the SSH host key, then inspect in the console.\n'
