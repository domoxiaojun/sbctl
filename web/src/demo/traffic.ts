import type { Schema } from '../api/types'
import { calendarDate, dateError, hourlyAllowed, trafficTotals } from '../lib/traffic'

type State = { nodes: Schema['Node'][] }
export function demoTraffic(state: State, params: URLSearchParams, now: number): Schema['TrafficReport'] {
 const from = params.get('from') || '', to = params.get('to') || ''
 const scope = (params.get('scope') || 'inbounds') as Schema['TrafficReport']['scope']
 const requested = params.get('granularity'), granularity = requested === 'hour' || requested === 'month' ? requested : 'day'
 if (dateError(from, to)) throw new Error(dateError(from, to))
 if (granularity === 'hour' && !hourlyAllowed(from, to)) throw new Error('按小时查看最多选择 7 天')
 const selected = state.nodes.filter(n => !params.get('node') || n.id === params.get('node'))
 const q = (params.get('q') || '').toLowerCase(), protocol = params.get('protocol'), activity = params.get('activity')
 const factor = from ? 10n : 1n
 const bytes = <T extends { upload_bytes: string; download_bytes: string }>(r: T) => ({ ...r, upload_bytes: (BigInt(r.upload_bytes) / factor).toString(), download_bytes: (BigInt(r.download_bytes) / factor).toString() })
 const nodes = selected.map(n => {
  const inbounds = n.inbounds.filter(i => (!protocol || i.protocol === protocol) && (!q || `${i.tag} ${i.protocol} ${i.port} ${n.name}`.toLowerCase().includes(q)) && (!activity || i.active === (activity === 'active'))).map(bytes)
  const local_users = n.local_users.filter(u => (!params.get('local_user') || u.name === params.get('local_user')) && (!q || `${u.name} ${n.name}`.toLowerCase().includes(q)) && (!activity || u.active === (activity === 'active'))).map(bytes)
  const sum = trafficTotals(inbounds)
  return { ...n, inbounds, local_users, inbound_upload_bytes: sum.upload_bytes, inbound_download_bytes: sum.download_bytes }
 })
 const yearAgo = new Date(now + 8 * 3600_000); yearAgo.setUTCDate(1); yearAgo.setUTCMonth(yearAgo.getUTCMonth() - 11)
 const trendFrom = from || (granularity === 'month' ? yearAgo.toISOString().slice(0, 10) : calendarDate(now - (granularity === 'hour' ? 1 : 6) * 86400000)), trendTo = to || calendarDate(now)
 const days: string[] = []
 for (let ms = Date.parse(trendFrom); ms <= Date.parse(trendTo); ms += 86400000) days.push(new Date(ms).toISOString().slice(0, 10))
 const historyStart = calendarDate(now - 14 * 86400000), available = days.filter(day => day >= historyStart)
 const rows = scope === 'local' ? nodes.flatMap(n => n.local_users) : nodes.flatMap(n => n.inbounds)
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

export function demoTrafficConnections(state: State, params: URLSearchParams, now: number): Schema['TrafficConnectionReport'] {
 const node = params.get('node'), protocol = params.get('protocol'), selected = state.nodes.filter(n => !node || n.id === node)
 const items: Schema['TrafficConnection'][] = selected.flatMap(n => n.inbounds.filter(i => !protocol || i.protocol === protocol).slice(0, 4).map((i, index) => ({
  node_id: n.id, connection_id: `demo-${n.id}-${i.tag}`, inbound_id: '', inbound: i.tag, inbound_type: i.protocol, network: 'tcp', domain: `example-${index + 1}.com`, destination: `93.184.216.${34 + index}:443`, source: '10.0.0.2:50000', outbound: 'direct', outbound_type: 'direct', chain: ['direct'], rule: 'final', upload_bytes: '1024', download_bytes: '4096', started_at: new Date(now - (index + 1) * 60_000).toISOString(), closed_at: index % 2 ? '' : new Date(now - index * 30_000).toISOString(),
 })))
 const summary = (key: keyof Schema['TrafficConnection']) => {
  const counts = new Map<string, Schema['TrafficConnectionSummary']>()
  for (const item of items) { const name = String(item[key] || ''); if (!name) continue; const old = counts.get(name); counts.set(name, { name, connections: (old?.connections ?? 0) + 1, upload_bytes: (BigInt(old?.upload_bytes ?? '0') + BigInt(item.upload_bytes)).toString(), download_bytes: (BigInt(old?.download_bytes ?? '0') + BigInt(item.download_bytes)).toString() }) }
  return [...counts.values()].sort((a, b) => b.connections - a.connections)
 }
 const page = Number(params.get('page') || 1), pageSize = Number(params.get('page_size') || 100)
 return { items: items.slice((page - 1) * pageSize, page * pageSize), total: items.length, page, page_size: pageSize, retention_days: 90, history_gap: false, from: params.get('from') ?? '', to: params.get('to') ?? '', as_of: new Date(now).toISOString(), summary: { domains: summary('domain'), destinations: summary('destination'), rules: summary('rule'), outbounds: summary('outbound') } }
}
