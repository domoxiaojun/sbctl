#!/bin/bash
# Retain current credentials; remove only this pilot's statistics API.
set -euo pipefail
umask 077
[[ $EUID -eq 0 ]]
if [[ ! -x /usr/bin/sing-box ]]; then
  printf 'native sing-box is uninstalled; this migration-only rollback is retired\n' >&2
  exit 1
fi
root=/opt/sbctl/pilots/yxvmhk
compose=/etc/sing-box/compose.yaml
backup=${1:-$(cat "$root/LAST_BACKUP")}
backup=$(realpath "$backup")
[[ $backup == /var/backups/sbctl/yxvmhk/* && -f $backup/key.metadata && -f $backup/installed-api.json ]]
name=sbctl-yxvmhk-sing-box
if docker container inspect "$name" >/dev/null 2>&1; then
  [[ $(docker inspect -f '{{index .Config.Labels "com.docker.compose.project"}}' "$name") == sbctl-yxvmhk ]]
  docker stop -t 30 "$name" >/dev/null
fi
next=$(mktemp /etc/sing-box/.rollback.XXXXXX)
compare=$(mktemp -d /run/sbctl-rollback-compare.XXXXXX)
trap 'rm -f "$next"; rm -rf "$compare"' EXIT
jq --slurpfile api "$backup/installed-api.json" '
  if .experimental.v2ray_api == null then .
  elif .experimental.v2ray_api == $api[0] then del(.experimental.v2ray_api)
  else error("statistics configuration changed after this pilot; refusing to overwrite") end
' /etc/sing-box/config.json >"$next"
# Restore original bytes only when all current semantics are still the same.
# If credentials or other settings changed, keep them and only remove our API.
jq -S . "$backup/original-config.json" >"$compare/original.json"
jq -S '.inbounds |= map(if .type=="tuic" and .tag=="tuic-in" then del(.tag) else . end)' "$next" >"$compare/current.json"
if cmp -s "$compare/current.json" "$compare/original.json"; then cp "$backup/original-config.json" "$next"; fi
chmod --reference=/etc/sing-box/config.json "$next"
chown --reference=/etc/sing-box/config.json "$next"
mv "$next" /etc/sing-box/config.json
if [[ -f $compose ]] && cmp -s "$compose" "$backup/pilot-compose.yaml"; then rm "$compose"; fi
if [[ -L /etc/systemd/system/sing-box.service ]]; then
  [[ $(readlink /etc/systemd/system/sing-box.service) == /dev/null ]]
  systemctl unmask sing-box.service
fi
read -r mode owner group <"$backup/key.metadata"
chown "$owner:$group" /etc/sing-box/private.key
chmod "$mode" /etc/sing-box/private.key
systemctl daemon-reload
if [[ $(cat "$backup/native-enabled") == enabled ]]; then systemctl enable sing-box.service; fi
/usr/bin/sing-box check -D /var/lib/sing-box -C /etc/sing-box >"$backup/rollback-check.log" 2>&1
systemctl start sing-box.service
systemctl is-active --quiet sing-box.service
printf 'restored_systemd_process=true\ncurrent_credentials_preserved=true\n'
