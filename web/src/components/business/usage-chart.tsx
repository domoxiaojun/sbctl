import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from 'recharts'
import type { Schema } from '@/api/types'
import { ChartContainer, ChartTooltip, type ChartConfig } from '@/components/ui/chart'
import { exactBytes, formatBytes, totalBytes, usageChartScale } from '@/lib/format'
const config = { upload: { label: '上传', color: 'var(--chart-2)' }, download: { label: '下载', color: 'var(--chart-1)' } } satisfies ChartConfig
export default function UsageChart({ items }: { items: Schema['Usage'][] }) {
  const scale = usageChartScale(items)
  const data = items.map(item => ({ name: item.display_name, bytes: totalBytes(item), uploadBytes: item.upload_bytes, downloadBytes: item.download_bytes, upload: scale.value(item.upload_bytes), download: scale.value(item.download_bytes) }))
  return <div className="usage-chart" role="img" aria-label={`用户已记录累计：${data.map(row => `${row.name} ${formatBytes(row.bytes)}`).join('，')}`}>
    <ChartContainer config={config} className="h-full w-full aspect-auto">
      <BarChart accessibilityLayer data={data} layout="vertical" margin={{ left: 0, right: 16, top: 10, bottom: 0 }} barSize={24}>
        <CartesianGrid horizontal={false} strokeDasharray="3 4" />
        <XAxis type="number" axisLine={false} tickLine={false} tickMargin={12} tick={{ fill: 'var(--muted-foreground)', fontSize: 12 }} tickFormatter={value => `${Number(value.toFixed(2))} ${scale.unit}`} />
        <YAxis type="category" dataKey="name" width={60} axisLine={false} tickLine={false} tickMargin={10} tick={{ fill: 'var(--muted-foreground)', fontSize: 13 }} />
        <ChartTooltip cursor={{ fill: 'var(--muted)' }} content={({ active, payload }) => {
          if (!active || !payload?.length) return null
          const row = payload[0].payload
          return <div className="chart-tooltip"><strong>{row.name}</strong><p><span className="legend-dot upload" />上传<span>{formatBytes(row.uploadBytes)}</span></p><p><span className="legend-dot download" />下载<span>{formatBytes(row.downloadBytes)}</span></p><div className="tooltip-total">累计 <strong>{formatBytes(row.bytes)}</strong></div><small>{exactBytes(row.bytes)}</small></div>
        }} />
        <Bar dataKey="upload" stackId="usage" fill="var(--color-upload)" radius={[5, 0, 0, 5]} isAnimationActive={false} />
        <Bar dataKey="download" stackId="usage" fill="var(--color-download)" radius={[0, 5, 5, 0]} isAnimationActive={false} />
      </BarChart>
    </ChartContainer>
  </div>
}
