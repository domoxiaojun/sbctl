import { Check, CloudUpload, KeyRound, LoaderCircle, ShieldCheck, ShieldOff, Unplug, UserPlus } from 'lucide-react'
import { Link } from '@tanstack/react-router'
import type { Operation } from '@/api/types'
import { formatTime, operationNames } from '@/lib/format'
import { StatusBadge } from './status'
export function OperationProgress({ operation, detailed = false }: { operation: Operation; detailed?: boolean }) {
  const complete = operation.targets.filter(t => t.state === 'verified').length
  const ActionIcon = { create_user: UserPlus, disable_user: ShieldOff, enable_user: ShieldCheck, update_grants: ShieldCheck, rotate_credentials: KeyRound, publish: CloudUpload }[operation.kind]
  return <div className={detailed ? 'operation-detail' : 'operation-row'}><div className="operation-summary"><div className="operation-icon"><ActionIcon size={17} /></div><div className="min-w-0 flex-1"><Link to="/operations/$operationId" params={{ operationId: operation.id }} className="font-medium hover:text-primary">{operationNames[operation.kind]} · {operation.subject}</Link><div className="mt-1 text-xs text-muted-foreground">{formatTime(operation.created_at)} <span className="mx-1">·</span> {complete}/{operation.targets.length} 个节点已验证</div></div><StatusBadge status={operation.state} /></div><div className="progress-track" role="progressbar" aria-label="节点验证进度" aria-valuenow={complete} aria-valuemin={0} aria-valuemax={operation.targets.length}><div style={{ width: `${operation.targets.length ? complete / operation.targets.length * 100 : 0}%` }} /></div>
    {detailed && <><div className="notice mt-5"><p>“已接收”表示任务已保存。节点配置、实际验证和订阅吊销分别确认；关闭页面不取消任务。</p></div><div className="target-list">{operation.targets.map(target => { const Icon = target.state === 'verified' ? Check : target.state === 'unreachable' ? Unplug : LoaderCircle; return <div key={target.node_id}><Icon size={17} className={target.state === 'verified' ? 'text-primary' : 'text-muted-foreground'} /><div className="min-w-0 flex-1"><span className="font-medium">{target.name}</span><p className="text-xs text-muted-foreground mt-1">{target.message}</p></div><StatusBadge status={target.state} /></div> })}</div><div className="flex justify-between items-center border-t pt-5 mt-5"><span className="text-sm">订阅状态</span><StatusBadge status={operation.subscription_state} /></div></>}
  </div>
}
