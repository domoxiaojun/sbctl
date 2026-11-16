#!/bin/sh
set -eu
manifest=/run/sbctl/manifest
test -r "$manifest"
state=$(sed -n '1p' "$manifest")
expected=$(sed -n '2p' "$manifest")
revision=$(sed -n '3p' "$manifest")
test "$state" = allowed
test "${#expected}" -eq 64
case "$revision" in ''|*[!0-9]*) exit 71;; esac
actual=$(sha256sum /etc/sing-box/config.json)
actual=${actual%% *}
test "$expected" = "$actual"
printf '%s\n%s\n' "$expected" "$revision" > /var/lib/sing-box/.sbctl-started
mv /var/lib/sing-box/.sbctl-started /var/lib/sing-box/sbctl-started
exec /usr/local/bin/sing-box "$@"
