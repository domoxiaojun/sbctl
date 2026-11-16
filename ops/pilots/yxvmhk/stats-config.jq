if .experimental.v2ray_api != null then error("statistics already configured") else . end |
if ([.inbounds[] | select(.type == "anytls")] | length) != 1 or
   ([.inbounds[] | select(.type == "tuic")] | length) != 1
then error("unexpected pilot protocol layout") else . end |
if any(.inbounds[]; .tag == "tuic-in") then error("candidate TUIC tag already used") else . end |
if any(.inbounds[] | select(.type == "anytls" or .type == "tuic") | .users[]; (.name // "") == "")
then error("pilot users must already have names") else . end |
.inbounds |= map(if .type == "tuic" and (.tag // "") == "" then .tag = "tuic-in" else . end) |
.experimental.v2ray_api = {
  listen: "127.0.0.1:10085",
  stats: {
    enabled: true,
    inbounds: [.inbounds[] | select(.type == "anytls" or .type == "tuic") | .tag],
    users: [.inbounds[] | select(.type == "anytls" or .type == "tuic") | .users[].name] | unique
  }
}
