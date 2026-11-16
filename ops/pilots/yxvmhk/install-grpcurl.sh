#!/bin/bash
set -euo pipefail
umask 077
[[ $EUID -eq 0 ]]
root=/opt/sbctl/pilots/yxvmhk
work=$(mktemp -d /run/sbctl-grpcurl.XXXXXX)
trap 'rm -rf "$work"' EXIT
curl --fail --silent --show-error --location --proto '=https' --retry 2 \
  https://github.com/fullstorydev/grpcurl/releases/download/v1.9.4/grpcurl_1.9.4_linux_x86_64.tar.gz \
  --output "$work/grpcurl.tar.gz"
printf '%s  %s\n' 97e13d58d2733a0e62cd2571d1d5f0c02823f0d25282f08bddedf1ad9c5d1736 "$work/grpcurl.tar.gz" | sha256sum -c -
tar -xzf "$work/grpcurl.tar.gz" -C "$work" grpcurl
install -d -m 0755 "$root/tools"
install -m 0755 "$work/grpcurl" "$root/tools/grpcurl"
"$root/tools/grpcurl" -version
