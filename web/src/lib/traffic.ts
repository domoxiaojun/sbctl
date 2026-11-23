import type { Schema } from '@/api/types'
import { totalBytes } from './format'

export type TrafficFilters = {
 q?: string; node?: string; protocol?: string; outbound?: string; activity?: string; local_user?: string;
 from?: string; to?: string; scope?: 'inbounds' | 'local' | 'outbounds'; sort?: 'name' | 'bytes' | 'created'; granularity?: 'hour' | 'month'
}
export function trafficSearch(raw: Record<string, unknown>): TrafficFilters {
 const text = (key: string) => typeof raw[key] === 'string' ? raw[key].slice(0, 100) : undefined
 return {
  local_user: text('local_user'),
  outbound: text('outbound'),
  node: text('node'),
  protocol: text('protocol'),
  activity: ['active', 'removed'].includes(String(raw.activity)) ? String(raw.activity) : undefined,
  from: text('from'),
  to: text('to'),
  scope: ['inbounds', 'local', 'outbounds'].includes(String(raw.scope)) ? raw.scope as TrafficFilters['scope'] : undefined,
  granularity: raw.granularity === 'hour' || raw.granularity === 'month' ? raw.granularity : undefined,
 }
}
export function calendarDate(value = Date.now()) { return new Date(value + 8 * 3600_000).toISOString().slice(0, 10) }
export function presetDates(preset: string, now = Date.now()) {
 const today = calendarDate(now)
 if (preset === 'all') return { from: '', to: '' }
 if (preset === 'month') return { from: today.slice(0, 7) + '-01', to: today }
 const days = preset === 'week' ? 6 : preset === 'yesterday' ? 1 : 0
 return { from: calendarDate(now - days * 86400_000), to: preset === 'yesterday' ? calendarDate(now - 86400_000) : today }
}
export function dateError(from?: string, to?: string) {
 if (!from && !to) return ''
 if (!from || !to) return '请选择开始日期和结束日期。'
 const valid = (day: string) => /^\d{4}-\d{2}-\d{2}$/.test(day) && Number.isFinite(Date.parse(day)) && new Date(day).toISOString().slice(0, 10) === day
 if (!valid(from) || !valid(to)) return '请输入有效日期。'
 if (from > to) return '结束日期不能早于开始日期。'
 if (to > calendarDate()) return '结束日期不能晚于今天。'
 if (Date.parse(to) - Date.parse(from) > 365 * 86400_000) return '一次最多查询 366 天。'
 return ''
}
// 按小时趋势一次最多 7 天；未选日期时服务端返回最近两天。
export function hourlyAllowed(from?: string, to?: string) {
 return !from || !to || Date.parse(to) - Date.parse(from) <= 6 * 86400_000
}
// 某个月在统计时区内的起止日期，结束日期不超过今天。
export function monthRange(month: string, now = Date.now()) {
 const [year, index] = month.split('-').map(Number), last = new Date(Date.UTC(year, index, 0)).toISOString().slice(0, 10), today = calendarDate(now)
 return { from: `${month}-01`, to: last > today ? today : last }
}
export function sortTraffic<T extends { upload_bytes: string; download_bytes: string }>(rows: T[], sort: string | undefined, name: (row: T) => string) {
 return [...rows].sort((a,b) => { if (sort !== 'name') { const diff = BigInt(totalBytes(b)) - BigInt(totalBytes(a)); if (diff !== 0n) return diff > 0n ? 1 : -1 } return name(a).localeCompare(name(b)) })
}
export function trafficTotals(rows: { upload_bytes: string; download_bytes: string }[]) {
 return rows.reduce((sum,row) => ({upload_bytes:(BigInt(sum.upload_bytes)+BigInt(row.upload_bytes)).toString(),download_bytes:(BigInt(sum.download_bytes)+BigInt(row.download_bytes)).toString()}), {upload_bytes:'0',download_bytes:'0'})
}
export function knownTrafficNodes(report: Schema['TrafficReport']) {
 const unavailable = new Set(report.periods.filter(p => p.status === 'unavailable').map(p => p.node_id))
 return report.nodes.filter(n => !unavailable.has(n.id))
}

export type TrafficRank = { id: string; name: string; upload_bytes: string; download_bytes: string; filter: TrafficFilters }
export function trafficRankings(report: Schema['TrafficReport'], scope: TrafficFilters['scope'] = 'inbounds') {
 const nodes = knownTrafficNodes(report)
 const byNode = new Map<string, TrafficRank>(), byIdentity = new Map<string, TrafficRank>()
 const add = (map: Map<string, TrafficRank>, id: string, name: string, row: { upload_bytes: string; download_bytes: string }, filter: TrafficFilters) => {
  const old = map.get(id)
  map.set(id, { id, name, ...trafficTotals(old ? [old, row] : [row]), filter })
 }
 for (const node of nodes) {
  const rows = scope === 'local' ? node.local_users : scope === 'outbounds' ? node.outbounds : node.inbounds
  add(byNode, node.id, node.name, trafficTotals(rows), { node: node.id })
  if (scope === 'inbounds') for (const row of node.inbounds) add(byIdentity, row.protocol, row.protocol.toUpperCase(), row, { protocol: row.protocol })
  else if (scope === 'outbounds') for (const row of node.outbounds) add(byIdentity, row.tag, row.tag, row, { outbound: row.tag })
  else for (const row of node.local_users) add(byIdentity, JSON.stringify([node.id, row.name]), `${row.name} · ${node.name}`, row, { node: node.id, local_user: row.name })
 }
 return { nodes: sortTraffic([...byNode.values()], 'bytes', r => r.name), identities: sortTraffic([...byIdentity.values()], 'bytes', r => r.name) }
}

// 以 = + - @ 开头的文本会被表格软件当作公式，导出时加前缀使其保持为文本。
function csvCell(value: string | number) {
 const text = /^[=+\-@\t\r]/.test(String(value)) ? `'${value}` : String(value)
 return /[",\r\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text
}
export function trafficCsv(report: Schema['TrafficReport'], scope: TrafficFilters['scope'] = 'inbounds') {
 const range = report.from && report.to ? `${report.from} 至 ${report.to}` : '全部累计'
 const state = (active: boolean) => active ? '当前' : '已移除'
 const nodes = knownTrafficNodes(report)
 const table = scope === 'local'
  ? [['节点', '节点 ID', '本地用户', '记录状态', '上传字节', '下载字节', '合计字节', '首次纳入采集', '统计范围'], ...nodes.flatMap(n => n.local_users.map(u => [n.name, n.id, u.name, state(u.active), u.upload_bytes, u.download_bytes, totalBytes(u), u.started_at, range]))]
  : scope === 'outbounds'
  ? [['节点', '节点 ID', '出站标签', '记录状态', '上传字节', '下载字节', '合计字节', '首次纳入采集', '统计范围'], ...nodes.flatMap(n => n.outbounds.map(o => [n.name, n.id, o.tag, state(o.active), o.upload_bytes, o.download_bytes, totalBytes(o), o.started_at, range]))]
  : [['节点', '节点 ID', '入口', '协议', '端口', '记录状态', '上传字节', '下载字节', '合计字节', '首次纳入采集', '统计范围'], ...nodes.flatMap(n => n.inbounds.map(i => [n.name, n.id, i.tag, i.protocol, i.port, state(i.active), i.upload_bytes, i.download_bytes, totalBytes(i), i.started_at, range]))]
 return '\uFEFF' + table.map(row => row.map(csvCell).join(',')).join('\r\n') + '\r\n'
}
