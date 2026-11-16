import type { Schema } from '@/api/types'
import { totalBytes } from './format'

export type TrafficFilters = {
 q?: string; node?: string; protocol?: string; activity?: string;
 from?: string; to?: string; scope?: 'inbounds' | 'local' | 'managed'; sort?: 'name' | 'bytes' | 'created'
}
export function trafficSearch(raw: Record<string, unknown>): TrafficFilters {
 const text = (key: string) => typeof raw[key] === 'string' ? raw[key].slice(0, 100) : undefined
 return { node: text('node'), protocol: text('protocol'), activity: ['active', 'removed'].includes(String(raw.activity)) ? String(raw.activity) : undefined, from: text('from'), to: text('to'), scope: ['inbounds', 'local', 'managed'].includes(String(raw.scope)) ? raw.scope as TrafficFilters['scope'] : undefined }
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
