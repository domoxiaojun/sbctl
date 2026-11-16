import { createRootRoute, createRoute, createRouter, lazyRouteComponent } from '@tanstack/react-router'
import Shell from './routes/shell'
import { DataState } from './components/business/common'
import type { Filters } from './api/types'
import { trafficSearch, type TrafficFilters } from './lib/traffic'
const root = createRootRoute({ component: Shell, validateSearch: (raw: Record<string, unknown>): Omit<Filters, 'page_size'> & TrafficFilters => ({ ...trafficSearch(raw), q: typeof raw.q === 'string' ? raw.q.slice(0, 100) : undefined, page: Number.isSafeInteger(Number(raw.page)) && Number(raw.page) > 0 ? Number(raw.page) : undefined, status: typeof raw.status === 'string' ? raw.status : undefined, region: typeof raw.region === 'string' ? raw.region : undefined, sort: ['name', 'bytes', 'created'].includes(String(raw.sort)) ? raw.sort as Filters['sort'] : undefined }), notFoundComponent: () => <DataState title="没有找到这个页面" />, errorComponent: ({ error, reset }) => <DataState error={error instanceof Error ? error : new Error("页面暂时无法显示")} retry={reset} /> })
const overview = createRoute({ getParentRoute: () => root, path: '/', component: lazyRouteComponent(() => import('./routes/overview')) })
const nodes = createRoute({ getParentRoute: () => root, path: '/nodes', component: lazyRouteComponent(() => import('./routes/nodes'), 'NodesPage') })
const node = createRoute({ getParentRoute: () => root, path: '/nodes/$nodeId', component: lazyRouteComponent(() => import('./routes/nodes'), 'NodeDetail') })
const newNode = createRoute({ getParentRoute: () => root, path: '/nodes/new', component: lazyRouteComponent(() => import('./components/business/node-management'), 'NodeSetupPage') })
const setupNode = createRoute({ getParentRoute: () => root, path: '/nodes/$nodeId/setup', component: lazyRouteComponent(() => import('./components/business/node-management'), 'NodeSetupPage') })
const users = createRoute({ getParentRoute: () => root, path: '/users', component: lazyRouteComponent(() => import('./routes/users'), 'UsersPage') })
const user = createRoute({ getParentRoute: () => root, path: '/users/$userId', component: lazyRouteComponent(() => import('./routes/users'), 'UserDetail') })
const usage = createRoute({ getParentRoute: () => root, path: '/usage', component: lazyRouteComponent(() => import('./routes/usage')) })
const operations = createRoute({ getParentRoute: () => root, path: '/operations', component: lazyRouteComponent(() => import('./routes/operations'), 'OperationsPage') })
const operation = createRoute({ getParentRoute: () => root, path: '/operations/$operationId', component: lazyRouteComponent(() => import('./routes/operations'), 'OperationDetail') })
const alerts = createRoute({ getParentRoute: () => root, path: '/alerts', component: lazyRouteComponent(() => import('./routes/secondary'), 'AlertsPage') })
const settings = createRoute({ getParentRoute: () => root, path: '/settings', component: lazyRouteComponent(() => import('./routes/secondary'), 'SettingsPage') })
const components = createRoute({ getParentRoute: () => root, path: '/components', component: import.meta.env.DEV ? lazyRouteComponent(() => import('./demo/components-page')) : () => <DataState title="页面不可用" /> })
export const router = createRouter({ routeTree: root.addChildren([overview, nodes, node, newNode, setupNode, users, user, usage, operations, operation, alerts, settings, components]), defaultPreload: 'intent', defaultPendingComponent: () => <DataState loading /> })
declare module '@tanstack/react-router' { interface Register { router: typeof router } }
