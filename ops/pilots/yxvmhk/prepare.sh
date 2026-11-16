#!/bin/bash
set -euo pipefail
[[ $EUID -eq 0 ]]
[[ $(id -u sing-box) == 994 && $(id -g sing-box) == 994 ]]
[[ -f /etc/sing-box/private.key && ! -L /etc/sing-box/private.key ]]
# Keep root ownership; permit only the existing service group to read the key.
chown root:sing-box /etc/sing-box/private.key
chmod 0640 /etc/sing-box/private.key
