import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import type { Schema } from '@/api/types'
import { exactBytes, formatBytes, totalBytes } from '@/lib/format'
export default function UsageChart({ items }: { items: Schema['Usage'][] }) {
  const data = items.map(item => ({ name: item.display_name, bytes: totalBytes(item), value: Number(BigInt(totalBytes(item)) / 1048576n) / 1024 }))
  return <div className="usage-chart" role="img" aria-label={`用户已记录累计：${data.map(d => `${d.name} ${formatBytes(d.bytes)}`).join('，')}`}>
    <ResponsiveContainer width="100%" height="100%"><BarChart data={data} layout="vertical" margin={{ left: 0, right: 20, top: 8, bottom: 0 }} barSize={22}><CartesianGrid horizontal={false} stroke="var(--border)" strokeDasharray="3 4" /><XAxis type="number" axisLine={false} tickLine={false} tick={{ fill: 'var(--muted-foreground)', fontSize: 11 }} tickFormatter={value => `${value} GiB`} /><YAxis type="category" dataKey="name" width={60} axisLine={false} tickLine={false} tick={{ fill: 'var(--muted-foreground)', fontSize: 12 }} /><Tooltip cursor={{ fill: 'var(--muted)' }} content={({ active, payload }) => active && payload?.length ? <div className="chart-tooltip"><strong>{payload[0].payload.name}</strong><p>{formatBytes(payload[0].payload.bytes)}</p><small>{exactBytes(payload[0].payload.bytes)}</small></div> : null} /><Bar dataKey="value" fill="var(--primary)" radius={[0, 5, 5, 0]} isAnimationActive={false} /></BarChart></ResponsiveContainer>
  </div>
}
