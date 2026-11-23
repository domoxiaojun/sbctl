import { ArrowUpRight } from 'lucide-react'
import type { Schema } from '@/api/types'
import { exactBytes, formatBytes, totalBytes } from '@/lib/format'
import { trafficRankings, type TrafficFilters, type TrafficRank } from '@/lib/traffic'

function Ranking({ title, rows, onSelect }: { title: string; rows: TrafficRank[]; onSelect: (filter: TrafficFilters) => void }) {
 const maximum = rows.length ? BigInt(totalBytes(rows[0])) : 0n
 return <section className="panel min-w-0 p-5" aria-label={title}>
  <div className="flex items-center justify-between gap-2 mb-4"><h2>{title}</h2><span className="text-xs text-muted-foreground">前 5 名 · 点击筛选</span></div>
  {rows.length ? <ol className="space-y-1">{rows.slice(0, 5).map((row, index) => <li key={row.id}>
   <button className="w-full min-h-14 rounded-lg px-2 py-3 text-left hover:bg-muted focus-visible:outline-2 focus-visible:outline-ring" aria-label={`按${row.name}筛选`} onClick={() => onSelect(row.filter)}>
    <span className="flex items-center gap-3"><span className="text-xs text-muted-foreground tabular-nums w-3">{index + 1}</span><span className="flex-1 min-w-0 truncate text-sm font-medium">{row.name}</span><strong className="numeric text-sm shrink-0" title={exactBytes(totalBytes(row))}>{formatBytes(totalBytes(row))}</strong><ArrowUpRight size={14} className="shrink-0 text-muted-foreground" /></span>
    <span className="block ml-6 mt-2 h-1 rounded-full bg-muted overflow-hidden"><span className="block h-full rounded-full bg-primary/65" style={{ width: `${maximum ? Number(BigInt(totalBytes(row)) * 10000n / maximum) / 100 : 0}%` }} /></span>
   </button>
  </li>)}</ol> : <p className="py-8 text-sm text-muted-foreground">当前筛选没有可排行的记录。</p>}
 </section>
}
export function TrafficRankings({ report, scope, onSelect }: { report: Schema['TrafficReport']; scope: TrafficFilters['scope']; onSelect: (filter: TrafficFilters) => void }) {
 const rows = trafficRankings(report, scope)
 const secondaryTitle = scope === 'local' ? '本地用户用量排行' : scope === 'outbounds' ? '出站分流用量排行' : '协议用量排行'
 return <div className="grid gap-5 lg:grid-cols-2"><Ranking title="节点用量排行" rows={rows.nodes} onSelect={onSelect} /><Ranking title={secondaryTitle} rows={rows.identities} onSelect={onSelect} /></div>
}
