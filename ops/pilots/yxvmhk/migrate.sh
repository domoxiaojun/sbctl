#!/bin/bash
# Run in a transient systemd job; the resulting service uses Compose directly.
set -euo pipefail
umask 077
[[ $EUID -eq 0 ]]
root=/opt/sbctl/pilots/yxvmhk
compose=/etc/sing-box/compose.yaml
exec 9>/run/sbctl-yxvmhk-migrate.lock
flock -n 9
[[ $(systemctl show sing-box.service -p FragmentPath --value) == /lib/systemd/system/sing-box.service ]]
systemctl is-active --quiet sing-box.service
[[ ! -e /etc/systemd/system/sing-box.service && ! -L /etc/systemd/system/sing-box.service ]]
[[ ! -e $compose && ! -e /etc/sing-box/compose.yml ]]
[[ $(sha256sum /etc/sing-box/config.json | cut -d' ' -f1) == d92099ef390c4fd94a6579b605bed48f01056bece49773a3ba3d67522594c12e ]]
[[ $(sha256sum /usr/bin/sing-box | cut -d' ' -f1) == d883cf185b05564674485b64973014955c8060493e597ddb90369a6860bf5441 ]]
configs=(/etc/sing-box/*.json)
[[ ${#configs[@]} == 1 && ${configs[0]} == /etc/sing-box/config.json ]]
[[ -z $(docker ps -aq --filter label=com.docker.compose.project=sbctl-yxvmhk) ]]
[[ -x $root/tools/grpcurl ]]
"$root/check-image.sh"
docker compose -f "$root/compose.yaml" config --quiet
if ss -H -lntup | awk '{p=$5;sub(/^.*:/,"",p);if(p==10085)found=1}END{exit !found}'; then
  printf 'statistics port already occupied\n' >&2
  exit 1
fi
backup=/var/backups/sbctl/yxvmhk/$(date -u +%Y%m%dT%H%M%SZ)
install -d -m 0700 "$backup"
stat -c '%a %u %g' /etc/sing-box/private.key >"$backup/key.metadata"
systemctl is-enabled sing-box.service >"$backup/native-enabled"
cp -a /lib/systemd/system/sing-box.service "$backup/vendor-unit.service"
cp /etc/sing-box/config.json "$backup/original-config.json"
cp "$root/compose.yaml" "$backup/pilot-compose.yaml"
tar -C /etc -cpf "$backup/config.tar" sing-box
sha256sum /usr/bin/sing-box >"$backup/original-binary.sha256"
printf '%s\n' "$backup" >"$root/LAST_BACKUP"
switched=0
finished=0
work=$(mktemp -d /run/sbctl-check.XXXXXX)
cleanup() {
  code=$?
  trap - EXIT
  rm -rf "$work"
  if [[ $finished != 1 ]]; then
    printf 'migration_failed=true\n' >&2
    if [[ $switched == 1 ]]; then
      if ! "$root/rollback.sh" "$backup"; then printf 'automatic_rollback_failed=true\n' >&2; fi
    else
      read -r mode owner group <"$backup/key.metadata"
      chown "$owner:$group" /etc/sing-box/private.key
      chmod "$mode" /etc/sing-box/private.key
    fi
    [[ $code != 0 ]] || code=1
  fi
  exit "$code"
}
trap cleanup EXIT
trap 'exit 143' TERM
trap 'exit 130' INT
jq -f "$root/stats-config.jq" /etc/sing-box/config.json >"$work/candidate.json"
jq '.experimental.v2ray_api' "$work/candidate.json" >"$backup/installed-api.json"
# This pilot adds only the API and the previously absent TUIC tag.
jq -S 'del(.experimental.v2ray_api) | .inbounds |= map(if .type=="tuic" then del(.tag) else . end)' "$work/candidate.json" >"$work/roundtrip.json"
jq -S . "$backup/original-config.json" >"$work/original.json"
cmp -s "$work/roundtrip.json" "$work/original.json"
"$root/prepare.sh"
mkdir "$work/state"
chown 994:994 "$work/state" "$work/candidate.json"
image=$(docker compose -f "$root/compose.yaml" config --format json | jq -er '.services["sing-box"].image')
docker run --rm --pull=never --network none --read-only --user 994:994 --cap-drop ALL --security-opt no-new-privileges:true \
  --mount type=bind,src=/etc/sing-box,dst=/etc/sing-box,readonly \
  --mount "type=bind,src=$work/candidate.json,dst=/candidate.json,readonly" \
  --mount "type=bind,src=$work/state,dst=/var/lib/sing-box" \
  "$image" check -D /var/lib/sing-box -c /candidate.json >"$backup/preflight.log" 2>&1
printf 'candidate_config_valid=true\n'
"$root/probe.sh" false | tee "$backup/baseline-probe.log"
cmp -s /etc/sing-box/config.json "$backup/original-config.json"
switched=1
systemctl disable --now sing-box.service
systemctl mask sing-box.service
tar -C /var/lib -cpf "$backup/state.tar" sing-box
install -m 0640 "$root/compose.yaml" "$compose"
install -m 0644 "$work/candidate.json" /etc/sing-box/.config.sbctl-next
mv /etc/sing-box/.config.sbctl-next /etc/sing-box/config.json
sha256sum /etc/sing-box/config.json >"$backup/installed-config.sha256"
docker compose -f "$compose" up -d --no-build --pull never sing-box
"$root/verify.sh" >"$backup/runtime-before-restart.json"
"$root/probe.sh" true | tee "$backup/container-probe.log"
docker compose -f "$compose" restart -t 30 sing-box
"$root/verify.sh" >"$backup/runtime.json"
jq -e -n --slurpfile before "$backup/runtime-before-restart.json" --slurpfile after "$backup/runtime.json" \
  '$before[0].container_id == $after[0].container_id and $before[0].started_at != $after[0].started_at' >/dev/null
"$root/probe.sh" true | tee "$backup/restart-probe.log"
sha256sum -c "$backup/original-binary.sha256" "$backup/installed-config.sha256" >/dev/null
[[ $(systemctl is-enabled sing-box.service) == masked ]]
finished=1
printf 'migration_completed=true\ncompose_restart_verified=true\nbackup_path=%s\n' "$backup"
