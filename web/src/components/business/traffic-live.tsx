import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Activity, Radio } from 'lucide-react'
import { api, isDemo } from '@/api/client'
import type { Schema } from '@/api/types'
import { Button } from '@/components/ui/button'
import { formatBytes, formatRate, formatTime } from '@/lib/format'

function Sparkline({ points }: { points: Schema['LivePoint'][] }) {
  if (points.length < 2) {
    return <div className="h-24 flex items-center justify-center text-xs text-muted-foreground rounded-lg bg-muted/30">正在收集实时采样点…</div>
  }
  const width = 600, height = 96, pad = 6
  const maxRate = Math.max(1024, ...points.map(p => Math.max(p.download_bps, p.upload_bps)))
  const coords = (pick: (p: Schema['LivePoint']) => number) =>
    points.map((p, i) => {
      const x = pad + (i / (points.length - 1)) * (width - pad * 2)
      const y = height - pad - (pick(p) / maxRate) * (height - pad * 2)
      return `${x.toFixed(1)},${y.toFixed(1)}`
    }).join(' ')
  const downPoints = coords(p => p.download_bps)
  const upPoints = coords(p => p.upload_bps)
  const areaDown = `${pad},${height - pad} ${downPoints} ${width - pad},${height - pad}`
  return (
    <svg viewBox={`0 0 ${width} ${height}`} className="w-full h-24 overflow-visible" role="img" aria-label="实时上下行速率脉冲曲线">
      <polygon points={areaDown} className="fill-primary/12" />
      <polyline fill="none" stroke="currentColor" strokeWidth="2" points={downPoints} className="text-primary" />
      <polyline fill="none" stroke="currentColor" strokeWidth="1.5" strokeDasharray="4 3" points={upPoints} className="text-muted-foreground" />
    </svg>
  )
}

export function TrafficLivePanel({ node }: { node?: string }) {
  const [streamData, setStreamData] = useState<Schema['LiveTraffic'] | null>(null)
  const [streaming, setStreaming] = useState(false)
  const [showActive, setShowActive] = useState(false)

  useEffect(() => {
    if (isDemo || typeof EventSource === 'undefined') return
    const url = `/api/v1/traffic/live/stream${node ? `?node=${encodeURIComponent(node)}` : ''}`
    const es = new EventSource(url, { withCredentials: true })
    es.onmessage = event => {
      try {
        const parsed = JSON.parse(event.data) as Schema['LiveTraffic']
        setStreamData(parsed)
        setStreaming(true)
      } catch {
        // ignore malformed frame
      }
    }
    es.onerror = () => {
      setStreaming(false)
    }
    return () => {
      es.close()
      setStreaming(false)
    }
  }, [node])

  const fallback = useQuery({
    queryKey: ['traffic-live', node ?? ''],
    queryFn: ({ signal }) => api.trafficLive(node, signal),
    enabled: isDemo || !streaming,
    refetchInterval: 2_500,
  })

  const live = (streaming && streamData) ? streamData : fallback.data
  if (!live) return null

  return (
    <section className="panel p-4 sm:p-5 space-y-4" aria-label="实时速率与活跃连接">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <Radio size={16} className="text-primary animate-pulse motion-reduce:animate-none" />
          <h2 className="text-sm font-semibold">实时速率脉冲</h2>
          <span className="text-xs text-muted-foreground">{streaming ? 'SSE 1.5s 实时流' : '轮询同步'} · 采样 {formatTime(live.as_of, true)}</span>
        </div>
        <div className="flex flex-wrap items-center gap-4 text-sm tabular-nums">
          <span>下载 <strong className="text-primary">{formatRate(live.download_bps)}</strong></span>
          <span>上传 <strong>{formatRate(live.upload_bps)}</strong></span>
          <Button type="button" variant={showActive ? 'secondary' : 'outline'} size="sm" onClick={() => setShowActive(v => !v)}>
            <Activity size={14} />{live.active_connections} 活跃连接
          </Button>
        </div>
      </div>

      <Sparkline points={live.history} />

      {showActive && (
        <div className="border-t pt-3 space-y-2">
          <p className="text-xs text-muted-foreground">当前实时进行中的连接（按流量排序，最多显示前 100 条）：</p>
          <div className="overflow-x-auto">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b text-left text-muted-foreground">
                  <th className="py-2 pr-3">节点 / 入口</th>
                  <th className="py-2 pr-3">目标域名或地址</th>
                  <th className="py-2 pr-3">出站 / 规则</th>
                  <th className="py-2 pr-3">已传输流量</th>
                  <th className="py-2">建立时间</th>
                </tr>
              </thead>
              <tbody>
                {live.active_items.map(item => (
                  <tr className="border-b last:border-0" key={`${item.node_id}/${item.connection_id}`}>
                    <td className="py-2 pr-3"><span className="text-muted-foreground">{item.node_id}</span> · <strong>{item.inbound || '—'}</strong></td>
                    <td className="py-2 pr-3 max-w-60 truncate" title={item.domain || item.destination}>{item.domain || item.destination || '—'}</td>
                    <td className="py-2 pr-3">{item.outbound || '—'} <span className="text-muted-foreground">({item.rule || 'final'})</span></td>
                    <td className="py-2 pr-3 tabular-nums whitespace-nowrap">↑ {formatBytes(item.upload_bytes)} · ↓ {formatBytes(item.download_bytes)}</td>
                    <td className="py-2 whitespace-nowrap text-muted-foreground">{formatTime(item.started_at, true)}</td>
                  </tr>
                ))}
                {!live.active_items.length && (
                  <tr><td colSpan={5} className="py-4 text-center text-muted-foreground">当前没有活跃传输连接。</td></tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </section>
  )
}
