#!/bin/bash
# Read-only capability preflight; does not mount production config or state.
set -euo pipefail
root=/opt/sbctl/pilots/yxvmhk
image=$(docker compose -f "$root/compose.yaml" config --format json | jq -er '.services["sing-box"].image')
if [[ ! $image =~ @sha256:[0-9a-f]{64}$ ]]; then
  printf 'candidate image must be pinned by digest\n' >&2
  exit 1
fi
version=$(docker run --rm --pull=never --network none --read-only --user 994:994 --cap-drop ALL --security-opt no-new-privileges:true "$image" version)
tags=$(printf '%s\n' "$version" | sed -n 's/^Tags: //p')
if [[ ,$tags, != *,with_v2ray_api,* ]]; then
  printf 'candidate image lacks with_v2ray_api; production migration has not started\n' >&2
  exit 1
fi
printf 'candidate_v2ray_build_tag=true\n'
# This proves the build flag only. API configuration and real counters still
# require validation before this pilot can be considered integrated.
