#!/bin/bash
set -euo pipefail
root=/opt/sbctl/pilots/yxvmhk
"$root/tools/grpcurl" -plaintext -connect-timeout 3 -max-time 5 \
  -import-path "$root" -proto stats.proto -d '{"reset":false}' \
  127.0.0.1:10085 v2ray.core.app.stats.command.StatsService/QueryStats
