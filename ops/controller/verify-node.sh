#!/bin/bash
# Run on ovhus; uses the existing HTTPS site and never prints session secrets.
set -euo pipefail
umask 077
node_id=${1:-yxvmhk}
mode=${2:-check}
report=${3:-/opt/sbctl/data/node-verification.json}
[[ "$node_id" =~ ^[a-z][a-z0-9_-]{2,79}$ ]]
[[ "$mode" == check || "$mode" == configure ]]
scratch=$(mktemp -d)
base=https://sbctl.domob.org/api/v1
csrf=
cleanup() {
  if [[ -n "$csrf" ]]; then
    request DELETE /session >/dev/null 2>&1 || true
  fi
  rm -rf "$scratch"
}
trap cleanup EXIT
request() {
  local method=$1 path=$2 file=${3:-} key=${4:-}
  local args=(--silent --show-error --fail-with-body --max-time 20
    --resolve sbctl.domob.org:443:127.0.0.1
    --cookie "$scratch/cookies" --cookie-jar "$scratch/cookies"
    --header 'Origin: https://sbctl.domob.org' --header 'Content-Type: application/json'
    --header "X-CSRF-Token: $csrf" --request "$method")
  if [[ -n "$key" ]]; then args+=(--header "Idempotency-Key: $key"); fi
  if [[ -n "$file" ]]; then args+=(--data-binary "@$file"); fi
  curl "${args[@]}" "$base$path"
}
jq -n --rawfile password /opt/sbctl/config/admin.initial '{login_name:"admin",password:($password|rtrimstr("\n"))}' > "$scratch/login.json"
request POST /session "$scratch/login.json" > "$scratch/session.json"
csrf=$(jq -er .csrf_token "$scratch/session.json")
request GET "/nodes/$node_id/config" > "$scratch/config.json"
wait_job() {
  local job=$1 output=$2 state
  for ((i=0; i<180; i++)); do
    request GET "/node-jobs/$job" > "$output"
    state=$(jq -r .state "$output")
    if [[ "$state" == completed ]]; then return; fi
    if [[ "$state" == failed ]]; then
      jq '{id,state,error_code,message}' "$output" >&2
      return 1
    fi
    sleep 1
  done
  printf 'node job timed out: %s\n' "$job" >&2
  return 1
}
submit() {
  local body=$1 output=$2 key
  key=$(cat /proc/sys/kernel/random/uuid)
  request POST /node-jobs "$body" "$key" > "$scratch/accepted.json"
  wait_job "$(jq -er .job_id "$scratch/accepted.json")" "$output"
}
if [[ "$mode" == configure ]]; then
  jq '{kind:"inspect",draft:.draft,expected_version:.version,inspection_id:""}' "$scratch/config.json" > "$scratch/input.json"
  submit "$scratch/input.json" "$scratch/inspect.json"
  jq -e '.result.can_adopt == true' "$scratch/inspect.json" >/dev/null
  jq --arg inspection "$(jq -r .id "$scratch/inspect.json")" '{kind:"configure",draft:.draft,expected_version:.version,inspection_id:$inspection,desired_container:"",desired_image:""}' "$scratch/config.json" > "$scratch/input.json"
  submit "$scratch/input.json" "$scratch/configured.json"
  request GET "/nodes/$node_id/config" > "$scratch/config.json"
fi
jq '{kind:"check",draft:.draft,expected_version:.version,inspection_id:""}' "$scratch/config.json" > "$scratch/input.json"
submit "$scratch/input.json" "$scratch/check.json"
jq -e '.result.ready == true and all(.result.checks[]; .ok == true)' "$scratch/check.json" >/dev/null
request GET "/nodes/$node_id" > "$scratch/node.json"
jq -n --slurpfile check "$scratch/check.json" --slurpfile node "$scratch/node.json" '{check:$check[0],node:$node[0]}' > "$report"
if [[ "$mode" == configure ]]; then
  jq --slurpfile inspection "$scratch/inspect.json" --slurpfile configured "$scratch/configured.json" '. + {inspection:$inspection[0],configured:$configured[0]}' "$report" > "$scratch/result.json"
  mv "$scratch/result.json" "$report"
fi
jq '{node_id:.node.id,check_job:.check.id,ready:.check.result.ready,quality:.node.quality,configure_job:.configured.id}' "$report"
