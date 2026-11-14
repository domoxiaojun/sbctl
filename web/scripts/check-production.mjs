import fs from 'node:fs/promises'
import path from 'node:path'
async function inspect(dir) {
  for (const entry of await fs.readdir(dir, { withFileTypes: true })) {
    const name = path.join(dir, entry.name)
    if (entry.isDirectory()) await inspect(name)
    else if (/\.(js|html)$/.test(name)) {
      const content = await fs.readFile(name, 'utf8')
      for (const marker of ['SBCTL_DEMO_FIXTURE_ONLY', 'sbctl-f0-synthetic-state-v1', 'DEMO-NOT-A-SECRET', 'synthetic-csrf', '组件与状态验收']) if (content.includes(marker)) throw new Error(`Demo content in production bundle: ${name}`)
    }
  }
}
await inspect(new URL('../dist/', import.meta.url).pathname)
console.log('Production bundle contains no demo transport, fixtures, or component lab.')
