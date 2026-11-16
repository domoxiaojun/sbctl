import { describe, expect, it } from 'vitest'
import { calendarDate, dateError, presetDates, sortTraffic, trafficTotals } from '../src/lib/traffic'
describe('流量查询', () => {
 it('按 UTC+8 提供包含结束日的快捷日期', () => {
  const now=Date.parse('2026-01-31T16:15:00Z')
  expect(calendarDate(now)).toBe('2026-02-01')
  expect(presetDates('yesterday',now)).toEqual({from:'2026-01-31',to:'2026-01-31'})
  expect(presetDates('week',now)).toEqual({from:'2026-01-26',to:'2026-02-01'})
  expect(presetDates('month',now)).toEqual({from:'2026-02-01',to:'2026-02-01'})
 })
 it('拒绝无效及颠倒日期',()=>{
  expect(dateError('2026-01-01','')).toBeTruthy()
  expect(dateError('2026-02-30','2026-03-01')).toBeTruthy()
  expect(dateError('2026-02-02','2026-02-01')).toBeTruthy()
  expect(dateError('2026-01-01','2026-01-01')).toBe('')
 })
 it('筛选结果汇总和排序保留大整数精度',()=>{
  const rows=[{name:'b',upload_bytes:'9007199254740993',download_bytes:'1'},{name:'a',upload_bytes:'9007199254740994',download_bytes:'1'}]
  expect(sortTraffic(rows,'bytes',r=>r.name)[0].name).toBe('a')
  expect(trafficTotals(rows).upload_bytes).toBe('18014398509481987')
 })
})
