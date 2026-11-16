import { expect, it } from 'vitest'
import type { Schema } from '../src/api/types'
import { fixtures } from '../src/demo/fixtures'
import { createNodeManagementDemo } from '../src/demo/node-management'

it('演示后端的配置片段读取与修改遵循版本和摘要保护', async () => {
 const state = fixtures(Date.now()), handle = createNodeManagementDemo(() => state), id = state.nodes[0].id
 const call = async <T>(path: string, body?: unknown, key?: string) => {
  const response = (await handle(new Request(`http://demo.invalid/api/v1${path}`, { method: body ? 'POST' : 'GET', headers: key ? { 'Idempotency-Key': key } : {}, ...(body ? { body: JSON.stringify(body) } : {}) }), path))!
  return { status: response.status, body: await response.json() as T }
 }
 const node = async () => (await call<Schema['NodeConfiguration']>(`/nodes/${id}/config`)).body
 const job = async (input: Partial<Schema['NodeJobInput']>, key: string) => {
  const current = await node()
  const accepted = await call<Schema['NodeJobAccepted'] & { message?: string }>('/node-jobs', { draft: current.draft, expected_version: current.version, inspection_id: '', ...input }, key)
  if (accepted.status !== 202) return accepted
  return call<Schema['NodeJob']>(`/node-jobs/${accepted.body.job_id}`)
 }
 const read = async (key: string) => (await job({ kind: 'config' }, key)).body as Schema['NodeJob']
 const first = (await read('config-read-key-00001')).result as Schema['NodeConfigView']
 expect(Object.keys(first.sections).sort()).toEqual(['dns', 'log', 'ntp', 'route'])
 expect(first.inbound_tags.length).toBeGreaterThan(0)
 const version = (await node()).version
 const route = { final: 'block', rules: [] }
 const changed = await job({ kind: 'config_change', config_change: { base_hash: first.hash, section: 'route', content: route } }, 'config-change-key-0001')
 expect((changed.body as Schema['NodeJob']).state).toBe('completed')
 expect((await node()).version).toBe(version + 1)
 const second = (await read('config-read-key-00002')).result as Schema['NodeConfigView']
 expect(second.sections.route).toEqual(route)
 expect(second.sections.dns).toEqual(first.sections.dns)
 expect(second.hash).not.toBe(first.hash)
 const stale = await job({ kind: 'config_change', config_change: { base_hash: first.hash, section: 'log', content: {} } }, 'config-change-key-0002')
 expect(stale.status).toBe(409)
 const outdated = await call('/node-jobs', { kind: 'config_change', draft: (await node()).draft, expected_version: version, inspection_id: '', config_change: { base_hash: second.hash, section: 'log', content: {} } }, 'config-change-key-0003')
 expect(outdated.status).toBe(409)
 expect(((await read('config-read-key-00003')).result as Schema['NodeConfigView']).sections.log).toEqual(first.sections.log)
})
