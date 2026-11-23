import type { Schema } from '../api/types'
import { calendarDate, dateError, hourlyAllowed, trafficTotals } from '../lib/traffic'

type State = { nodes: Schema['Node'][] }
export function demoTraffic(state: State, params: URLSearchParams, now: number): Schema['TrafficReport'] {
 const from = params.get('from') || '', to = params.get('to') || ''
 const scope = (['inbounds', 'local', 'outbounds'].includes(params.get('scope') || '') ? params.get('scope') : 'inbounds') as Schema['TrafficReport']['scope']
 const requested = params.get('granularity'), granularity = requested === 'hour' || requested === 'month' ? requested : 'day'
 if (dateError(from, to)) throw new Error(dateError(from, to))
 if (granularity === 'hour' && !hourlyAllowed(from, to)) throw new Error('按小时查看最多选择 7 天')
 const selected = state.nodes.filter(n => !params.get('node') || n.id === params.get('node'))
 const q = (params.get('q') || '').toLowerCase(), protocol = params.get('protocol'), outbound = params.get('outbound'), activity = params.get('activity')
 const factor = from ? 10n : 1n
 const bytes = <T extends { upload_bytes: string; download_bytes: string }>(r: T) => ({ ...r, upload_bytes: (BigInt(r.upload_bytes) / factor).toString(), download_bytes: (BigInt(r.download_bytes) / factor).toString() })
 const nodes = selected.map(n => {
  const inbounds = n.inbounds.filter(i => (!protocol || i.protocol === protocol) && (!q || `${i.tag} ${i.protocol} ${i.port} ${n.name}`.toLowerCase().includes(q)) && (!activity || i.active === (activity === 'active'))).map(bytes)
  const outbounds = n.outbounds.filter(o => (!outbound || o.tag === outbound) && (!q || `${o.tag} ${n.name}`.toLowerCase().includes(q)) && (!activity || o.active === (activity === 'active'))).map(bytes)
  const local_users = n.local_users.filter(u => (!params.get('local_user') || u.name === params.get('local_user')) && (!q || `${u.name} ${n.name}`.toLowerCase().includes(q)) && (!activity || u.active === (activity === 'active'))).map(bytes)
  const inSum = trafficTotals(inbounds), outSum = trafficTotals(outbounds)
  return { ...n, inbounds, outbounds, local_users, inbound_upload_bytes: inSum.upload_bytes, inbound_download_bytes: inSum.download_bytes, outbound_upload_bytes: outSum.upload_bytes, outbound_download_bytes: outSum.download_bytes }
 })
 const yearAgo = new Date(now + 8 * 3600_000); yearAgo.setUTCDate(1); yearAgo.setUTCMonth(yearAgo.getUTCMonth() - 11)
 const trendFrom = from || (granularity === 'month' ? yearAgo.toISOString().slice(0, 10) : calendarDate(now - (granularity === 'hour' ? 1 : 6) * 86400000)), trendTo = to || calendarDate(now)
 const days: string[] = []
 for (let ms = Date.parse(trendFrom); ms <= Date.parse(trendTo); ms += 86400000) days.push(new Date(ms).toISOString().slice(0, 10))
 const historyStart = calendarDate(now - 14 * 86400000), available = days.filter(day => day >= historyStart)
 const rows = scope === 'local' ? nodes.flatMap(n => n.local_users) : scope === 'outbounds' ? nodes.flatMap(n => n.outbounds) : nodes.flatMap(n => n.inbounds)
 const total = trafficTotals(rows)
 const trend: Schema['TrafficDay'][] = days.map(day => {
  if (!nodes.length || !available.includes(day)) return { date: day, upload_bytes: null, download_bytes: null, status: 'unavailable' }
  const divide = (value: string) => {
   const period = BigInt(value) / (from ? 1n : 10n), size = BigInt(available.length)
   return (period / size + (day === available.at(-1) ? period % size : 0n)).toString()
  }
  return { date: day, upload_bytes: divide(total.upload_bytes), download_bytes: divide(total.download_bytes), status: 'partial' }
 })
 // 小时桶把当天的演示用量平均分到已经过去的小时，合计与按日一致。
 const currentHour = new Date(now + 8 * 3600_000).toISOString().slice(0, 13)
 const hours: Schema['TrafficDay'][] = trend.flatMap(day => {
  const labels = Array.from({ length: 24 }, (_, hour) => `${day.date}T${String(hour).padStart(2, '0')}`).filter(label => label <= currentHour)
  return labels.map((label, index) => {
   if (day.upload_bytes === null || day.download_bytes === null) return { date: `${label}:00`, upload_bytes: null, download_bytes: null, status: 'unavailable' as const }
   const share = (value: string) => (BigInt(value) / BigInt(labels.length) + (index === labels.length - 1 ? BigInt(value) % BigInt(labels.length) : 0n)).toString()
   return { date: `${label}:00`, upload_bytes: share(day.upload_bytes), download_bytes: share(day.download_bytes), status: day.status }
  })
 })
 // 月份由每日数据折叠而成：各日状态一致才沿用，否则记为不完整。
 const months: Schema['TrafficDay'][] = []
 for (const day of trend) {
  let month = months.at(-1)
  if (month?.date !== day.date.slice(0, 7)) months.push(month = { date: day.date.slice(0, 7), upload_bytes: null, download_bytes: null, status: day.status })
  if (day.status !== month.status) month.status = 'partial'
  if (day.upload_bytes === null || day.download_bytes === null) continue
  month.upload_bytes = (BigInt(month.upload_bytes ?? '0') + BigInt(day.upload_bytes)).toString()
  month.download_bytes = (BigInt(month.download_bytes ?? '0') + BigInt(day.download_bytes)).toString()
 }
 return { nodes, periods: nodes.map(n => ({ node_id: n.id, history_started_at: n.quality.started_at, status: !from ? 'cumulative' : available.length ? 'partial' : 'unavailable' })), from, to, scope, granularity, trend: granularity === 'hour' ? hours : granularity === 'month' ? months : trend, trend_from: trendFrom, trend_to: trendTo, timezone: 'Asia/Singapore', as_of: new Date(now).toISOString() }
}

export function demoTrafficLive(state: State, params: URLSearchParams, now: number): Schema['LiveTraffic'] {
 const filterNode = params.get('node') || ''
 const selected = state.nodes.filter(n => !filterNode || n.id === filterNode)
 const points: Schema['LivePoint'][] = Array.from({ length: 24 }, (_, idx) => {
  const wave = 0.75 + 0.25 * Math.sin((now / 2000) + idx * 0.45)
  const up = Math.round(selected.reduce((s, n) => s + n.upload_bps, 0) * wave)
  const down = Math.round(selected.reduce((s, n) => s + n.download_bps, 0) * wave)
  const act = selected.reduce((s, n) => s + n.active_connections, 0)
  return { timestamp: new Date(now - (23 - idx) * 2000).toISOString(), upload_bps: up, download_bps: down, active_connections: act }
 })
 const nodes: Schema['LiveNode'][] = selected.map(n => ({
  node_id: n.id,
  sampled_at: new Date(now).toISOString(),
  fresh: n.status === 'online',
  upload_bps: n.upload_bps,
  download_bps: n.download_bps,
  active_connections: n.active_connections,
  inbounds: n.inbounds.map(i => ({ tag: i.tag, upload_bps: Math.round(n.upload_bps / 2), download_bps: Math.round(n.download_bps / 2) })),
  outbounds: n.outbounds.map(o => ({ tag: o.tag, upload_bps: Math.round(n.upload_bps / 2), download_bps: Math.round(n.download_bps / 2) })),
  history: points.slice(-12),
 }))
 const active_items: Schema['TrafficConnection'][] = selected.filter(n => n.status === 'online').slice(0, 6).map((n, idx) => ({
  node_id: n.id,
  connection_id: `live-${n.id}-${idx}`,
  inbound_id: '',
  inbound: 'anytls-in',
  inbound_type: 'anytls',
  network: 'tcp',
  domain: idx % 2 === 0 ? 'stream.example.com' : 'api.github.com',
  destination: `104.18.${idx + 1}.10:443`,
  source: `198.51.100.${10 + idx}:52000`,
  outbound: idx % 2 === 0 ? 'warp-out' : 'direct',
  outbound_type: idx % 2 === 0 ? 'wireguard' : 'direct',
  chain: [idx % 2 === 0 ? 'warp-out' : 'direct'],
  rule: idx % 2 === 0 ? 'geosite:streaming' : 'final',
  upload_bytes: String((idx + 1) * 32768),
  download_bytes: String((idx + 1) * 1048576),
  started_at: new Date(now - (idx + 1) * 15_000).toISOString(),
  closed_at: '',
 }))
 const latest = points.at(-1) ?? { upload_bps: 0, download_bps: 0, active_connections: 0 }
 return { as_of: new Date(now).toISOString(), upload_bps: latest.upload_bps, download_bps: latest.download_bps, active_connections: latest.active_connections, nodes, history: points, active_items }
}

export function demoTrafficConnections(state: State, params: URLSearchParams, now: number): Schema['TrafficConnectionReport'] {
 const node = params.get('node'), protocol = params.get('protocol'), outboundFilter = params.get('outbound'), rankBy = params.get('rank_by') || 'bytes'
 const selected = state.nodes.filter(n => !node || n.id === node)
 const items: Schema['TrafficConnection'][] = selected.flatMap(n => n.inbounds.filter(i => !protocol || i.protocol === protocol).slice(0, 4).map((i, index) => ({
  node_id: n.id, connection_id: `demo-${n.id}-${i.tag}-${index}`, inbound_id: '', inbound: i.tag, inbound_type: i.protocol, network: 'tcp', domain: `example-${index + 1}.com`, destination: `93.184.216.${34 + index}:443`, source: '10.0.0.2:50000', outbound: index % 2 === 0 ? 'warp-out' : 'direct', outbound_type: index % 2 === 0 ? 'wireguard' : 'direct', chain: [index % 2 === 0 ? 'warp-out' : 'direct'], rule: index % 2 === 0 ? 'geosite:streaming' : 'final', upload_bytes: String((index + 1) * 2048), download_bytes: String((4 - index) * 65536), started_at: new Date(now - (index + 1) * 60_000).toISOString(), closed_at: new Date(now - index * 30_000).toISOString(),
 }))).filter(item => !outboundFilter || item.outbound === outboundFilter)
 const summary = (key: keyof Schema['TrafficConnection']) => {
  const counts = new Map<string, Schema['TrafficConnectionSummary']>()
  for (const item of items) { const name = String(item[key] || ''); if (!name) continue; const old = counts.get(name); counts.set(name, { name, connections: (old?.connections ?? 0) + 1, upload_bytes: (BigInt(old?.upload_bytes ?? '0') + BigInt(item.upload_bytes)).toString(), download_bytes: (BigInt(old?.download_bytes ?? '0') + BigInt(item.download_bytes)).toString() }) }
  return [...counts.values()].sort((a, b) => {
   if (rankBy === 'connections' && b.connections !== a.connections) return b.connections - a.connections
   const diff = (BigInt(b.upload_bytes) + BigInt(b.download_bytes)) - (BigInt(a.upload_bytes) + BigInt(a.download_bytes))
   if (diff !== 0n) return diff > 0n ? 1 : -1
   return b.connections - a.connections
  })
 }
 const page = Number(params.get('page') || 1), pageSize = Number(params.get('page_size') || 100)
 return { items: items.slice((page - 1) * pageSize, page * pageSize), total: items.length, page, page_size: pageSize, retention_days: 90, history_gap: false, from: params.get('from') ?? '', to: params.get('to') ?? '', as_of: new Date(now).toISOString(), summary: { domains: summary('domain'), destinations: summary('destination'), rules: summary('rule'), outbounds: summary('outbound') } }
}
