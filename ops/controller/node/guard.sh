#!/bin/sh
set -eu
manifest=/run/sbctl/manifest
test -r "$manifest"
state=$(sed -n '1p' "$manifest")
format=$(sed -n '2p' "$manifest")
revision=$(sed -n '3p' "$manifest")
test -z "$(sed -n '4,$p' "$manifest")"
test "$state" = allowed
case "$format" in
  2) ;;
  # Compatibility with the original three-line manifest. The old digest is
  # intentionally ignored; the next helper write upgrades it to format 2.
  *)
    test "${#format}" -eq 64
    case "$format" in *[!a-f0-9]*) exit 71;; esac
    ;;
esac
case "$revision" in ''|*[!0-9]*) exit 71;; esac
test -r /etc/sing-box/config.json
# Record what this process loaded, without granting the manifest authority over
# its contents. The host can distinguish a live edit from a completed restart.
loaded=$(sha256sum /etc/sing-box/config.json)
loaded=${loaded%% *}
printf '%s\n%s\n2\n' "$loaded" "$revision" > /var/lib/sing-box/.sbctl-started
mv /var/lib/sing-box/.sbctl-started /var/lib/sing-box/sbctl-started
exec /usr/local/bin/sing-box "$@"
