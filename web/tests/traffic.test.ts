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

import { trafficRankings } from '../src/lib/traffic'
import { demoTraffic } from '../src/demo/traffic'
import { fixtures } from '../src/demo/fixtures'
it('跨节点用户排行按身份聚合，大整数和同名身份均保留', () => {
 const seed = fixtures(Date.now())
 seed.users[0].display_name = '同名'
 seed.users[1].display_name = '同名'
 const report = demoTraffic(seed, new URLSearchParams('scope=managed'), Date.now())
 const ranking = trafficRankings(report, 'managed')
 expect(ranking.identities.filter(u => u.name === '同名')).toHaveLength(2)
 const first = ranking.identities.find(u => u.id === seed.users[0].id)!
 expect(first.upload_bytes).toBe(seed.users[0].upload_bytes)
 expect(first.filter.user).toBe(seed.users[0].id)
 expect(trafficTotals(ranking.nodes)).toEqual(trafficTotals(ranking.identities))
 const filtered = demoTraffic(seed, new URLSearchParams(`scope=managed&user=${first.id}`), Date.now())
 expect(new Set(filtered.users.map(u => u.user_id))).toEqual(new Set([first.id]))
})
it('日趋势与所选时段汇总保持同一统计范围', () => {
 const seed = fixtures(Date.now()), from=calendarDate(Date.now()-6*86400000), to=calendarDate()
 const report = demoTraffic(seed, new URLSearchParams({scope:'managed',from,to}), Date.now())
 expect(report.trend).toHaveLength(7)
 expect(trafficTotals(report.trend.map(p => ({upload_bytes:p.upload_bytes!,download_bytes:p.download_bytes!})))).toEqual(trafficTotals(report.users))
})
