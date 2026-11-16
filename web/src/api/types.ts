import type { components, operations } from './generated/schema'
export type Schema = components['schemas']
export type Node = Schema['Node']
export type User = Schema['User']
export type Operation = Schema['Operation']
export type Quality = Schema['Quality']
export type Filters = NonNullable<operations['get__nodes']['parameters']['query']> & { archived?: 'false' | 'true' | 'all' }
