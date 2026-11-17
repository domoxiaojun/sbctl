#!/bin/bash
# Linux-only isolated guard acceptance. Does not touch any production container.
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
work=$(mktemp -d /tmp/sbctl-guard-test.XXXXXX)
trap 'rm -rf "$work"' EXIT
chmod 0755 "$work"
printf '{}\n' >"$work/config.json"
chmod 0644 "$work/config.json"
image=ghcr.io/domoxiaojun/sing-box@sha256:d9edd64387dfafad2eb1e15efdcf4bd904a2d0939a577107c678f3f8d033ce94
check() {
  docker run --rm --network none --user 10001:10001 --read-only --cap-drop ALL --security-opt no-new-privileges:true \
    --mount "type=bind,src=$root/guard.sh,dst=/run/sbctl/guard.sh,readonly" \
    --mount "type=bind,src=$work/manifest,dst=/run/sbctl/manifest,readonly" \
    --mount "type=bind,src=$work,dst=/etc/sing-box,readonly" \
    --tmpfs /var/lib/sing-box:size=1m,mode=1777 --entrypoint /bin/sh "$image" /run/sbctl/guard.sh version
}
printf 'blocked\n2\n1\n' >"$work/manifest"
chmod 0644 "$work/manifest"
if check >"$work/blocked.log" 2>&1; then echo 'blocked guard unexpectedly started'; exit 1; fi
printf 'allowed\n2\n7\n' >"$work/manifest"
check >"$work/allowed.log" 2>&1
printf 'allowed\n%s\n7\n' "$(printf '{}' | sha256sum | cut -d' ' -f1)" >"$work/legacy-manifest"
cp "$work/legacy-manifest" "$work/manifest"
check >"$work/legacy.log" 2>&1
printf '{"changed":true}\n' >"$work/config.json"
check >"$work/changed.log" 2>&1
printf 'guard: blocked denied; formatted and changed config started\n'
