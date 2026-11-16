import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'
import { describe, expect, it } from 'vitest'

type Merge = (target: object, source: object) => Record<string, unknown>

const require = createRequire(import.meta.url)
const shadcnRequire = createRequire(require.resolve('shadcn'))
const entry = shadcnRequire.resolve('deepmerge')
const root = dirname(dirname(entry))
const registryRequire = createRequire(shadcnRequire.resolve('@shadcn/registry'))

it('shadcn 和 registry 使用同一个已修补的 deepmerge', () => {
  expect(registryRequire.resolve('deepmerge')).toBe(entry)
})

for (const [name, path] of [
  ['CommonJS', entry],
  ['UMD', join(root, 'dist/umd.js')],
] as const) {
  const merge = require(path) as Merge
  describe(`deepmerge ${name}`, () => {
    it.each(['target', 'source'])('拒绝 %s 中顶层和嵌套的原型键', (side) => {
      const payload = JSON.parse('{"__proto__":{"review_polluted":true},"constructor":{"prototype":{"review_polluted":true}},"prototype":{"review_polluted":true},"nested":{"__proto__":{"review_polluted":true}},"items":[{"__proto__":{"review_polluted":true}}]}') as object
      const result = side === 'target' ? merge(payload, {}) : merge({}, payload)
      const objects = [result, result.nested, ...(result.items as object[])]
      for (const object of objects) {
        expect(Object.getPrototypeOf(object)).toBe(Object.prototype)
        expect(object).not.toHaveProperty('review_polluted')
        for (const key of ['__proto__', 'constructor', 'prototype']) {
          expect(Object.hasOwn(object as object, key)).toBe(false)
        }
      }
      expect(Object.prototype).not.toHaveProperty('review_polluted')
    })

    it('保留普通配置的嵌套合并与数组合并', () => {
      expect(merge({ theme: { color: 'blue' }, items: [1] }, { theme: { size: 2 }, items: [2] }))
        .toEqual({ theme: { color: 'blue', size: 2 }, items: [1, 2] })
    })
  })
}
