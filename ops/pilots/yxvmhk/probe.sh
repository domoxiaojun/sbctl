#!/bin/bash
# Run on the selected VPS. Credentials never leave the root-only temporary path.
set -euo pipefail
umask 077
[[ $EUID -eq 0 ]]
root=/opt/sbctl/pilots/yxvmhk
with_stats=${1:-false}
[[ $with_stats == true || $with_stats == false ]]
image=ghcr.io/domoxiaojun/sing-box@sha256:d9edd64387dfafad2eb1e15efdcf4bd904a2d0939a577107c678f3f8d033ce94
name=sbctl-yxvmhk-probe
if docker container inspect "$name" >/dev/null 2>&1; then
  printf 'probe container already exists; inspect it before retrying\n' >&2
  exit 1
fi
if ss -H -lntup | awk '{p=$5;sub(/^.*:/,"",p);if(p==31830||p==31831||p==53183||p==53184)found=1}END{exit !found}'; then
  printf 'probe ports are occupied\n' >&2
  exit 1
fi
work=$(mktemp -d /run/sbctl-probe.XXXXXX)
started=0
# shellcheck disable=SC2329 # Invoked by the EXIT trap below.
cleanup() {
  if [[ $started == 1 ]]; then docker rm -f "$name" >/dev/null 2>&1 || true; fi
  rm -rf "$work"
}
trap cleanup EXIT
capture_counts() {
  local protocol=$1 destination=$2 user tag
  user=$(jq -er --arg protocol "$protocol" '.inbounds[]|select(.type==$protocol)|.users[0].name' /etc/sing-box/config.json)
  tag=$(jq -er --arg protocol "$protocol" '.inbounds[]|select(.type==$protocol)|.tag' /etc/sing-box/config.json)
  "$root/read-stats.sh" >"$work/raw-stats.json"
  jq --arg user "$user" --arg tag "$tag" '
    [(.stat//[])[]|{key:.name,value:(.value|tonumber)}]|from_entries|
    {user_up:(.["user>>>"+$user+">>>traffic>>>uplink"]//0),
     user_down:(.["user>>>"+$user+">>>traffic>>>downlink"]//0),
     inbound_up:(.["inbound>>>"+$tag+">>>traffic>>>uplink"]//0),
     inbound_down:(.["inbound>>>"+$tag+">>>traffic>>>downlink"]//0)}
  ' "$work/raw-stats.json" >"$destination"
}
check_growth() {
  local protocol=$1 transport=$2
  for _ in {1..10}; do
    capture_counts "$protocol" "$work/after.json"
    jq -n --slurpfile b "$work/before.json" --slurpfile a "$work/after.json" \
      '$a[0] | with_entries(.value = (.value - $b[0][.key]))' >"$work/delta.json"
    if jq -e 'all(.[]; .>0)' "$work/delta.json" >/dev/null; then
      printf '%s_%s_stats=passed delta=%s\n' "$protocol" "$transport" "$(jq -c . "$work/delta.json")"
      return 0
    fi
    sleep 0.2
  done
  printf '%s_%s_stats=failed\n' "$protocol" "$transport" >&2
  return 1
}
server_name=$(openssl x509 -in /etc/sing-box/fullchain.pem -noout -ext subjectAltName | grep -o 'DNS:[^,[:space:]]*' | head -n 1 | cut -c5-)
[[ -n $server_name && $server_name != *'*'* ]]
jq --arg sni "$server_name" '
  (.inbounds|map(select(.type=="anytls"))|first) as $a |
  (.inbounds|map(select(.type=="tuic"))|first) as $t |
  if ($a.users[0].password|type)!="string" or ($t.users[0].uuid|type)!="string" then error("missing probe identity") else
  {log:{disabled:true},inbounds:[
    {type:"socks",tag:"socks-a",listen:"127.0.0.1",listen_port:31830},
    {type:"socks",tag:"socks-t",listen:"127.0.0.1",listen_port:31831},
    {type:"direct",tag:"dns-a",listen:"127.0.0.1",listen_port:53183,network:"udp",override_address:"1.1.1.1",override_port:53},
    {type:"direct",tag:"dns-t",listen:"127.0.0.1",listen_port:53184,network:"udp",override_address:"1.1.1.1",override_port:53}
  ],outbounds:[
    {type:"anytls",tag:"a",server:"::1",server_port:$a.listen_port,password:$a.users[0].password,tls:{enabled:true,server_name:$sni}},
    {type:"tuic",tag:"t",server:"::1",server_port:$t.listen_port,uuid:$t.users[0].uuid,password:$t.users[0].password,tls:{enabled:true,server_name:$sni,alpn:($t.tls.alpn//["h3"])}}
  ],route:{rules:[{inbound:["socks-a","dns-a"],outbound:"a"},{inbound:["socks-t","dns-t"],outbound:"t"}],final:"a"}} end
' /etc/sing-box/config.json >"$work/client.json"
chown 994:994 "$work/client.json"
chmod 0600 "$work/client.json"
docker run -d --name "$name" --label io.sbctl.temporary=probe --network host --user 994:994 --read-only --cap-drop ALL --security-opt no-new-privileges:true --mount "type=bind,src=$work/client.json,dst=/probe/client.json,readonly" "$image" -c /probe/client.json run >/dev/null
started=1
for _ in {1..20}; do
  if ss -H -lnt | awk '{p=$4;sub(/^.*:/,"",p);if(p==31831)found=1}END{exit !found}'; then break; fi
  sleep 0.25
done
failed=0
for entry in anytls:31830:53183 tuic:31831:53184; do
  IFS=: read -r protocol port udp_port <<<"$entry"
  if [[ $with_stats == true ]]; then capture_counts "$protocol" "$work/before.json"; fi
  code=$(curl --silent --show-error --max-time 20 --proxy "socks5h://127.0.0.1:$port" --output /dev/null --write-out '%{http_code}' https://www.gstatic.com/generate_204 2>"$work/curl-error") || code=000
  printf '%s_https_status=%s\n' "$protocol" "$code"
  if [[ $code != 204 ]]; then failed=1; fi
  if [[ $with_stats == true ]]; then
    check_growth "$protocol" tcp || failed=1
    capture_counts "$protocol" "$work/before.json"
  fi
  dig @127.0.0.1 -p "$udp_port" example.com A +time=5 +tries=1 >"$work/dns-answer" 2>/dev/null || true
  if grep -q 'status: NOERROR' "$work/dns-answer" && grep -Eq 'ANSWER: [1-9]' "$work/dns-answer"; then
    printf '%s_udp_dns=passed\n' "$protocol"
  else
    printf '%s_udp_dns=failed\n' "$protocol"
    failed=1
  fi
  if [[ $with_stats == true ]]; then
    check_growth "$protocol" udp || failed=1
    for target in ipv4 domain; do
      if timeout 8 "$root/check-boundary.sh" "$port" "$target"; then
        printf '%s_statistics_proxy_boundary_%s=passed\n' "$protocol" "$target"
      else
        printf '%s_statistics_proxy_boundary_%s=failed\n' "$protocol" "$target" >&2
        failed=1
      fi
    done
  fi
done
exit "$failed"
