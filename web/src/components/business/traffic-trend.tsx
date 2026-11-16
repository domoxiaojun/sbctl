import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from 'recharts'
import type { Schema } from '@/api/types'
import { ChartContainer, ChartTooltip, type ChartConfig } from '@/components/ui/chart'
import { exactBytes, formatBytes, usageChartScale } from '@/lib/format'
const config = { upload: { label: '上传', color: 'var(--chart-2)' }, download: { label: '下载', color: 'var(--chart-1)' } } satisfies ChartConfig
const statuses = { complete: '完整', partial: '记录不完整', unavailable: '无日期明细' }
export default function TrafficTrend({ report, onSelect }: { report: Schema['TrafficReport']; onSelect: (date: string) => void }) {
 const points = report.trend
 const scale = usageChartScale(points.map(p => ({ upload_bytes: p.upload_bytes ?? '0', download_bytes: p.download_bytes ?? '0' })))
 const data = points.map(p => ({ ...p, upload: p.upload_bytes === null ? null : scale.value(p.upload_bytes), download: p.download_bytes === null ? null : scale.value(p.download_bytes) }))
 const missing = points.filter(p => p.status === 'unavailable').length, partial = points.filter(p => p.status === 'partial').length
 return <section className="panel overflow-hidden" aria-label="日流量趋势">
  <div className="p-5 flex flex-wrap justify-between gap-3"><div><h2>每日流量</h2><p className="text-xs text-muted-foreground mt-1">{report.trend_from} 至 {report.trend_to} · UTC+8{!report.from && ' · 累计视图下展示最近 7 天'}</p></div><div className="flex items-center gap-4 text-xs"><span className="flex items-center gap-2"><i className="legend-dot upload" />上传</span><span className="flex items-center gap-2"><i className="legend-dot download" />下载</span></div></div>
  {missing === points.length ? <div className="h-52 flex flex-col justify-center items-center px-5 text-sm text-muted-foreground"><p>这段时间还没有日期明细</p><p className="text-xs mt-2">累计用量仍保留，缺失日期不按零流量绘制。</p></div> : <div className="px-2 sm:px-4" role="img" aria-label={`每日流量趋势，${points.length} 天，${partial} 天记录不完整，${missing} 天无明细`}>
   <ChartContainer config={config} className="h-60 sm:h-72 w-full aspect-auto"><BarChart accessibilityLayer data={data} margin={{ left: 0, right: 12, top: 10, bottom: 4 }}>
    <CartesianGrid vertical={false} strokeDasharray="3 4" /><XAxis dataKey="date" tickFormatter={date => date.slice(5).replace('-', '/')} axisLine={false} tickLine={false} minTickGap={24} /><YAxis width={58} axisLine={false} tickLine={false} tickFormatter={n => `${Number(n.toFixed(1))} ${scale.unit}`} />
    <ChartTooltip content={({ active, payload }) => { if (!active || !payload?.length) return null; const point = payload[0].payload as Schema['TrafficDay']; return <div className="chart-tooltip"><strong>{point.date}</strong><p>上传 {point.upload_bytes === null ? '未知' : formatBytes(point.upload_bytes)}</p><p>下载 {point.download_bytes === null ? '未知' : formatBytes(point.download_bytes)}</p><small>{statuses[point.status]} · 点击查看当天明细</small></div> }} />
    <Bar cursor="pointer" onClick={(_, index) => { const point = points[index]; if (point && point.status !== 'unavailable') onSelect(point.date) }} dataKey="upload" stackId="daily" fill="var(--color-upload)" maxBarSize={36} isAnimationActive={false} /><Bar cursor="pointer" onClick={(_, index) => { const point = points[index]; if (point && point.status !== 'unavailable') onSelect(point.date) }} dataKey="download" stackId="daily" fill="var(--color-download)" radius={[4, 4, 0, 0]} maxBarSize={36} isAnimationActive={false} />
   </BarChart></ChartContainer>
  </div>}
  <div className="px-5 pb-4 text-xs text-muted-foreground">{partial > 0 && `${partial} 天记录不完整。`}{missing > 0 && `${missing} 天无明细，图表留空。`}点击柱形或下方日期可查看当天用量。</div>
  <details className="border-t"><summary className="min-h-11 cursor-pointer px-5 py-3 text-sm">查看每日数据</summary><div className="overflow-auto max-h-72 px-5 pb-4"><table className="w-full text-left text-sm"><thead><tr><th className="py-2">日期</th><th>上传</th><th>下载</th><th>记录</th></tr></thead><tbody>{points.map(p => <tr className="border-t" key={p.date}><td><button className="min-h-11 text-primary hover:underline" onClick={() => onSelect(p.date)} aria-label={`查看 ${p.date} 流量`}>{p.date}</button></td><td title={p.upload_bytes === null ? undefined : exactBytes(p.upload_bytes)}>{p.upload_bytes === null ? '—' : formatBytes(p.upload_bytes)}</td><td title={p.download_bytes === null ? undefined : exactBytes(p.download_bytes)}>{p.download_bytes === null ? '—' : formatBytes(p.download_bytes)}</td><td className="text-xs text-muted-foreground">{statuses[p.status]}</td></tr>)}</tbody></table></div></details>
 </section>
}
