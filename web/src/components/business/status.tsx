import { AlertCircle, CheckCircle2, Circle, Clock3, Radio, Unplug } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import type { Quality } from '@/api/types'
const labels: Record<string, string> = { online: '在线', degraded: '需关注', offline: '失联', unregistered: '待登记', active: '已启用', disabled: '已禁用', provisioning: '配置中', queued: '已接收', running: '执行中', partial: '部分完成', completed: '已完成', failed: '失败', superseded: '已被后续变更替代', revoke_blocked: '撤销受阻', verified: '已验证', applied: '已应用', unreachable: '待恢复', pending: '待发布', published: '已发布', revoked: '已吊销', revoke_pending: '待吊销', open: '待处理', acknowledged: '已确认', resolved: '已恢复' }
export function StatusBadge({ status }: { status: string }) {
  const good = ['online', 'active', 'completed', 'verified', 'published', 'resolved'].includes(status)
  const bad = ['offline', 'failed', 'unreachable', 'revoke_blocked'].includes(status)
  const quiet = ['disabled', 'revoked', 'acknowledged'].includes(status)
  const Icon = good ? CheckCircle2 : bad ? Unplug : quiet ? Circle : Clock3
  return <Badge variant="outline" className={cn('status-badge h-6 rounded-md px-2 gap-1.5 font-normal', good ? 'border-success/20 bg-success/5 text-success' : bad ? 'border-destructive/20 bg-destructive/5 text-destructive' : quiet ? 'border-border bg-muted text-muted-foreground' : 'border-warning/20 bg-warning/5 text-warning')}><Icon size={12} />{labels[status] ?? status}</Badge>
}
export function DataFreshness({ quality, compact = false }: { quality: Quality; compact?: boolean }) {
  return <div className="flex flex-wrap items-center gap-2 text-xs">
    <span className={cn('inline-flex items-center gap-1.5', quality.freshness === 'current' ? 'text-muted-foreground' : 'text-warning')}><Radio size={13} />{quality.freshness === 'current' ? '数据当前' : quality.freshness === 'stale' ? '采样已过期' : '新鲜度未知'}</span>
    {quality.integrity_state !== 'ok' && <span className="text-destructive">计数异常</span>}
    {quality.has_historical_gap && <span className="text-warning inline-flex items-center gap-1"><AlertCircle size={13} />{compact ? '有缺口' : `历史有 ${quality.gap_count} 处缺口`}</span>}
  </div>
}
