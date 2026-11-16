#!/bin/bash
set -euo pipefail
umask 077
name=sbctl-yxvmhk-sing-box
work=$(mktemp -d /run/sbctl-verify.XXXXXX)
trap 'rm -rf "$work"' EXIT
jq -r '
  .inbounds[] |
  (.listen_port|tostring) as $port |
  (if .type=="tuic" then ["udp"]
   elif (.type=="direct" or .type=="shadowsocks") then
     (if .network=="tcp" then ["tcp"] elif .network=="udp" then ["udp"] else ["tcp","udp"] end)
   else ["tcp"] end)[] | .+" "+$port
' /etc/sing-box/config.json | sort -u >"$work/expected"
api_port=$(jq -r '.experimental.clash_api.external_controller//""|split(":")|last' /etc/sing-box/config.json)
if [[ $api_port =~ ^[0-9]+$ ]]; then printf 'tcp %s\n' "$api_port" >>"$work/expected"; fi
printf 'tcp 10085\n' >>"$work/expected"
sort -u -o "$work/expected" "$work/expected"
ready=0
for _ in {1..30}; do
  if [[ $(docker inspect -f '{{.State.Running}}' "$name") == true ]]; then
    pid=$(docker inspect -f '{{.State.Pid}}' "$name")
    ss -H -lntup | awk -v pid="$pid" 'index($0,"pid=" pid ",") {p=$5;sub(/^.*:/,"",p);print $1,p}' | sort -u >"$work/actual"
    comm -23 "$work/expected" "$work/actual" >"$work/missing"
    if [[ ! -s "$work/missing" ]]; then ready=1; break; fi
  fi
  sleep 0.5
done
if [[ $ready != 1 ]]; then printf 'missing configured listeners\n' >&2; cat "$work/missing" >&2; exit 1; fi
[[ $(awk '/^Uid:/{print $2}' "/proc/$pid/status") == 994 ]]
[[ $(docker exec "$name" sha256sum /usr/local/bin/sing-box | cut -d' ' -f1) == 620b0ae5fe80372e4e12cdb01e990fc1c056d8f07ba473d074b828c6fbe549c9 ]]
[[ $(docker inspect -f '{{.HostConfig.NetworkMode}} {{.HostConfig.ReadonlyRootfs}} {{.HostConfig.RestartPolicy.Name}}' "$name") == 'host true unless-stopped' ]]
[[ $(jq -r '.experimental.v2ray_api.listen' /etc/sing-box/config.json) == 127.0.0.1:10085 ]]
[[ $(ss -H -lnt | awk '$4 ~ /:10085$/ {print $4}') == 127.0.0.1:10085 ]]
/opt/sbctl/pilots/yxvmhk/read-stats.sh >"$work/stats.json"
jq -e '(.stat // []) | type == "array"' "$work/stats.json" >/dev/null
id=$(docker inspect -f '{{.Id}}' "$name")
started=$(docker inspect -f '{{.State.StartedAt}}' "$name")
image=$(docker inspect -f '{{.Config.Image}}' "$name")
[[ $image == ghcr.io/domoxiaojun/sing-box@sha256:d9edd64387dfafad2eb1e15efdcf4bd904a2d0939a577107c678f3f8d033ce94 ]]
restart_count=$(docker inspect -f '{{.RestartCount}}' "$name")
config_sha=$(sha256sum /etc/sing-box/config.json | cut -d' ' -f1)
tailscale_info='{"connected":null,"ips":[]}'
if command -v tailscale >/dev/null && tailscale status --json >"$work/tailscale.json" 2>/dev/null; then
  tailscale_info=$(jq -c '{connected:(.BackendState=="Running" and .Self.Online==true),ips:(.Self.TailscaleIPs//[])}' "$work/tailscale.json")
fi
jq -n --arg id "$id" --arg started "$started" --arg image "$image" --arg sha "$config_sha" --argjson restarts "$restart_count" --argjson listeners "$(wc -l <"$work/expected")" --argjson ts "$tailscale_info" '{node:"yxvmhk",stage:"stats-compose-pilot",compose_path:"/etc/sing-box/compose.yaml",network_mode:"host",container_id:$id,started_at:$started,source_epoch:($id+"@"+$started),image:$image,running:true,uid:994,restart_count:$restarts,config_sha256:$sha,configured_listeners_verified:$listeners,protocol_validation:"see probe result",v2ray_api_ready:true,statistics_listener:"127.0.0.1:10085",statistics_validation:"see probe result",tailscale_connected:$ts.connected,tailscale_ips:$ts.ips,sbctl_backend_connected:false}'
