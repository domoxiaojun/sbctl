import fs from 'node:fs/promises'
import openapiTS, { astToString, COMMENT_HEADER } from 'openapi-typescript'
const generated = COMMENT_HEADER + astToString(await openapiTS(new URL('../../contracts/openapi.json', import.meta.url)))
const existing = await fs.readFile(new URL('../src/api/generated/schema.d.ts', import.meta.url), 'utf8')
if (generated !== existing) throw new Error('API types have drifted. Run pnpm api:generate and review the result.')
console.log('OpenAPI generated types match the committed contract.')
