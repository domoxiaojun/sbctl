#!/bin/bash
# SOCKS CONNECT may be acknowledged before the remote route is evaluated.
# Compare a real HTTP/2 response with the same request sent through the proxy.
set -euo pipefail
port=$1
target=$2
[[ $port == 31830 || $port == 31831 ]]
direct=$(curl --silent --noproxy '*' --http2-prior-knowledge --connect-timeout 2 --max-time 3 \
  --output /dev/null --write-out '%{http_code}' http://127.0.0.1:10085/) || exit 1
[[ $direct =~ ^[1-5][0-9]{2}$ ]]
case "$target" in
  ipv4) host=127.0.0.1 ;;
  domain) host=localhost ;;
  *) exit 1 ;;
esac
result=0
proxied=$(curl --silent --noproxy '' --proxy "socks5h://127.0.0.1:$port" \
  --http2-prior-knowledge --connect-timeout 2 --max-time 3 \
  --output /dev/null --write-out '%{http_code}' "http://$host:10085/") || result=$?
[[ $result != 0 && $proxied == 000 ]]
