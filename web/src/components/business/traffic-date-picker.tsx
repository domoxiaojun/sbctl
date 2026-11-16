import { useState } from 'react'
import { Popover } from '@base-ui/react/popover'
import { CalendarDays, ChevronDown } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { calendarDate, dateError, presetDates } from '@/lib/traffic'

const presets = [['all', '全部累计'], ['today', '今天'], ['yesterday', '昨天'], ['week', '近 7 天'], ['month', '本月']] as const
export function TrafficDatePicker({ from = '', to = '', onChange }: { from?: string; to?: string; onChange: (dates: { from: string; to: string }) => void }) {
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState({ from, to })
  const selected = presets.find(([key]) => { const dates = presetDates(key); return dates.from === from && dates.to === to })
  const label = selected?.[1] ?? (from && to ? `${from} 至 ${to}` : '选择日期')
  const error = dateError(draft.from, draft.to)
  const apply = (dates: { from: string; to: string }) => { onChange(dates); setOpen(false) }
  return <Popover.Root open={open} onOpenChange={next => { if (next) setDraft({ from, to }); setOpen(next) }}>
    <Popover.Trigger render={<Button variant="outline" className="min-h-11 max-w-full justify-between" aria-label={`选择统计日期：${label}`} />}><CalendarDays size={16} /><span className="truncate">{label}</span><ChevronDown size={14} /></Popover.Trigger>
    <Popover.Portal><Popover.Positioner sideOffset={8} align="end" className="z-50"><Popover.Popup className="w-[min(24rem,calc(100vw-2rem))] max-h-[80dvh] overflow-y-auto rounded-xl border bg-popover p-5 text-popover-foreground shadow-xl outline-none">
      <Popover.Title className="font-semibold">选择统计日期</Popover.Title>
      <Popover.Description className="mt-1 text-xs text-muted-foreground">UTC+8 · 包含结束日期全天</Popover.Description>
      <div className="my-4 flex flex-wrap gap-2" role="group" aria-label="日期快捷选择">{presets.map(([key, text]) => <Button key={key} variant={selected?.[0] === key ? 'secondary' : 'ghost'} className="min-h-11" aria-pressed={selected?.[0] === key} onClick={() => apply(presetDates(key))}>{text}</Button>)}</div>
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="text-sm space-y-1.5">开始日期<Input type="date" aria-label="开始日期" className="min-h-11 min-w-0" value={draft.from} max={draft.to || calendarDate()} onChange={e => setDraft({ ...draft, from: e.target.value })} /></label>
        <label className="text-sm space-y-1.5">结束日期<Input type="date" aria-label="结束日期" className="min-h-11 min-w-0" value={draft.to} min={draft.from} max={calendarDate()} onChange={e => setDraft({ ...draft, to: e.target.value })} /></label>
      </div>
      {error && <p role="alert" className="mt-3 text-sm text-destructive">{error}</p>}
      <div className="mt-4 flex justify-end gap-2"><Popover.Close render={<Button variant="ghost" className="min-h-11" />}>取消</Popover.Close><Button className="min-h-11" disabled={!!error || !draft.from || !draft.to} onClick={() => apply(draft)}>应用日期</Button></div>
    </Popover.Popup></Popover.Positioner></Popover.Portal>
  </Popover.Root>
}
