import type { Schema } from '../api/types'
import { calendarDate, dateError, hourlyAllowed, trafficTotals } from '../lib/traffic'

type State = { nodes: Schema['Node'][]; users: Schema['User'][]; groups: Schema['Group'][]; authorizedNodes?: Record<string, string[]> }
export function demoTraffic(state: State, params: URLSearchParams, now: number): Schema['TrafficReport'] {
 const from = params.get('from') || '', to = params.get('to') || ''
 const scope = (params.get('scope') || 'inbounds') as Schema['TrafficReport']['scope']
 const granularity = params.get('granularity') === 'hour' ? 'hour' : 'day'
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
 const users: Schema['TrafficUser'][] = state.users.flatMap(u => {
  if (params.get('user') && u.id !== params.get('user')) return []
  const ids = state.authorizedNodes?.[u.id] ?? [...new Set(state.groups.filter(g => u.group_ids.includes(g.id)).flatMap(g => g.node_ids))]
  return nodes.filter(n => ids.includes(n.id) && (!q || `${u.display_name} ${u.id} ${n.name}`.toLowerCase().includes(q))).map(n => {
   const divide = (value: string) => (BigInt(value) / BigInt(ids.length) + (ids[0] === n.id ? BigInt(value) % BigInt(ids.length) : 0n)).toString()
   return { user_id: u.id, display_name: u.display_name, node_id: n.id, node_name: n.name, quality: n.quality, ...bytes({ upload_bytes: divide(u.upload_bytes), download_bytes: divide(u.download_bytes) }) }
  })
 })
 const trendFrom = from || calendarDate(now - (granularity === 'hour' ? 1 : 6) * 86400000), trendTo = to || calendarDate(now)
 const days: string[] = []
 for (let ms = Date.parse(trendFrom); ms <= Date.parse(trendTo); ms += 86400000) days.push(new Date(ms).toISOString().slice(0, 10))
 const historyStart = calendarDate(now - 14 * 86400000), available = days.filter(day => day >= historyStart)
 const rows = scope === 'managed' ? users : scope === 'local' ? nodes.flatMap(n => n.local_users) : nodes.flatMap(n => n.inbounds)
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
 return { nodes, users, periods: nodes.map(n => ({ node_id: n.id, history_started_at: n.quality.started_at, status: !from ? 'cumulative' : available.length ? 'partial' : 'unavailable' })), from, to, scope, granularity, trend: granularity === 'hour' ? hours : trend, trend_from: trendFrom, trend_to: trendTo, timezone: 'Asia/Singapore', as_of: new Date(now).toISOString() }
}
