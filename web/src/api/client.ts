import type { Filters, Schema } from './types'
import type { TrafficFilters } from '@/lib/traffic'

export class ApiError extends Error {
  constructor(public code: string, message: string, public status = 0) { super(message) }
}
export const isDemo = import.meta.env.DEV && import.meta.env.VITE_DEMO_MODE === 'true'
let csrfToken = ''
async function transport(request: Request): Promise<Response> {
  if (import.meta.env.DEV && import.meta.env.VITE_DEMO_MODE === 'true') {
    const { demoFetch } = await import('../demo/transport')
    return demoFetch(request)
  }
  return fetch(request)
}
async function request<T>(path: string, method = 'GET', body?: unknown, key?: string, signal?: AbortSignal): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (method !== 'GET') headers['X-CSRF-Token'] = csrfToken
  if (key) headers['Idempotency-Key'] = key
  let response: Response
  try {
    response = await transport(new Request(new URL(`/api/v1${path}`, location.origin), {
      method, headers, credentials: 'same-origin', cache: 'no-store', signal,
      ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
    }))
  } catch (error) {
    if (error instanceof Error && error.name === 'AbortError') throw error
    throw new ApiError('API_UNAVAILABLE', '无法连接管理服务，请稍后重试。')
  }
  if (response.status === 204) return undefined as T
  if (!response.headers.get('content-type')?.includes('application/json')) {
    throw new ApiError('API_UNAVAILABLE', '管理 API 尚未接入或暂不可用，请检查服务连接。', response.status)
  }
  const result = await response.json()
  if (!response.ok) {
    if (response.status === 401 && path !== '/session') window.dispatchEvent(new Event('sbctl-session-expired'))
    throw new ApiError(result.code ?? 'REQUEST_FAILED', result.message ?? '请求失败，请重试。', response.status)
  }
  return result as T
}
function query(filters: Filters | Record<string, string | number | undefined> = {}) {
  const params = new URLSearchParams()
  Object.entries(filters).forEach(([key, value]) => { if (value !== undefined && value !== '') params.set(key, String(value)) })
  return params.size ? `?${params}` : ''
}
export const api = {
  session: async (signal?: AbortSignal) => {
    const session = await request<Schema['Session']>('/session', 'GET', undefined, undefined, signal)
    csrfToken = session.csrf_token
    return session
  },
  login: async (body: Schema['Login']) => {
    const session = await request<Schema['Session']>('/session', 'POST', body)
    csrfToken = session.csrf_token
    return session
  },
  logout: async () => { await request<void>('/session', 'DELETE'); csrfToken = '' },
  overview: (signal?: AbortSignal) => request<Schema['Overview']>('/overview', 'GET', undefined, undefined, signal),
  nodes: (f?: Filters, signal?: AbortSignal) => request<Schema['NodePage']>(`/nodes${query(f)}`, 'GET', undefined, undefined, signal),
  node: (id: string, signal?: AbortSignal) => request<Schema['Node']>(`/nodes/${encodeURIComponent(id)}`, 'GET', undefined, undefined, signal),
  nodeDiscovery: (signal?: AbortSignal) => request<Schema['NodeDiscovery']>('/node-discovery', 'GET', undefined, undefined, signal),
  nodeOnboarding: (signal?: AbortSignal) => request<Schema['NodeOnboarding']>('/node-onboarding', 'GET', undefined, undefined, signal),
  nodeConfig: (id: string, signal?: AbortSignal) => request<Schema['NodeConfiguration']>(`/nodes/${encodeURIComponent(id)}/config`, 'GET', undefined, undefined, signal),
  submitNodeJob: (body: Schema['NodeJobInput'], key: string) => request<Schema['NodeJobAccepted']>('/node-jobs', 'POST', body, key),
  nodeJobs: (id: string, signal?: AbortSignal) => request<Schema['NodeJobs']>(`/nodes/${encodeURIComponent(id)}/jobs`, 'GET', undefined, undefined, signal),
  nodeJob: (id: string, signal?: AbortSignal) => request<Schema['NodeJob']>(`/node-jobs/${encodeURIComponent(id)}`, 'GET', undefined, undefined, signal),
  retryNodeJob: (id: string) => request<Schema['NodeJobAccepted']>(`/node-jobs/${encodeURIComponent(id)}/retry`, 'POST'),
  updateNodeMetadata: (id: string, body: Schema['NodeMetadata'], key: string) => request<Schema['NodeSaved']>(`/nodes/${encodeURIComponent(id)}/metadata`, 'POST', body, key),
  users: (f?: Filters, signal?: AbortSignal) => request<Schema['UserPage']>(`/users${query(f)}`, 'GET', undefined, undefined, signal),
  user: (id: string, signal?: AbortSignal) => request<Schema['User']>(`/users/${encodeURIComponent(id)}`, 'GET', undefined, undefined, signal),
  editUser: (id: string, body: Schema['UserMetadata'], key: string) => request<Schema['NodeSaved']>(`/users/${encodeURIComponent(id)}/metadata`, 'POST', body, key),
  archiveUsers: (body: Schema['ArchiveUsers'], key: string) => request<Schema['ArchiveResult']>('/users/archive', 'POST', body, key),
  deleteUsers: (body: Schema['DeleteUsers'], key: string) => request<Schema['ArchiveResult']>('/users/delete', 'POST', body, key),
  resetSubscription: (id: string, body: Schema['ResetSubscription'], key: string) => request<Schema['SubscriptionResetResult']>(`/users/${encodeURIComponent(id)}/subscription-reset`, 'POST', body, key),
  userAudit: (id: string, signal?: AbortSignal) => request<Schema['UserAudit']>(`/users/${encodeURIComponent(id)}/audit`, 'GET', undefined, undefined, signal),
  groups: (signal?: AbortSignal) => request<Schema['Group'][]>('/groups', 'GET', undefined, undefined, signal),
  traffic: (f: TrafficFilters = {}, signal?: AbortSignal) => request<Schema['TrafficReport']>(`/traffic${query(f)}`, 'GET', undefined, undefined, signal),
  trafficConnections: (f: Record<string, string | number | undefined> = {}, signal?: AbortSignal) => request<Schema['TrafficConnectionReport']>(`/traffic/connections${query(f)}`, 'GET', undefined, undefined, signal),
  operations: (f?: Filters, signal?: AbortSignal) => request<Schema['OperationPage']>(`/operations${query(f)}`, 'GET', undefined, undefined, signal),
  operation: (id: string, signal?: AbortSignal) => request<Schema['Operation']>(`/operations/${encodeURIComponent(id)}`, 'GET', undefined, undefined, signal),
  createUser: (body: Schema['CreateUser'], key: string) => request<Schema['Accepted']>('/users', 'POST', body, key),
  disableUser: (id: string, body: Schema['DisableUser'], key: string) => request<Schema['Accepted']>(`/users/${encodeURIComponent(id)}/disable`, 'POST', body, key),
  enableUser: (id: string, body: Schema['EnableUser'], key: string) => request<Schema['Accepted']>(`/users/${encodeURIComponent(id)}/enable`, 'POST', body, key),
  previewGrants: (id: string, body: Schema['GrantSelection']) => request<Schema['GrantPreview']>(`/users/${encodeURIComponent(id)}/grants/preview`, 'POST', body),
  updateGrants: (id: string, body: Schema['UpdateGrants'], key: string) => request<Schema['Accepted']>(`/users/${encodeURIComponent(id)}/grants`, 'POST', body, key),
  findRequest: (key: string) => request<Schema['Accepted']>(`/requests/${encodeURIComponent(key)}`),
  alerts: (f?: Filters, signal?: AbortSignal) => request<Schema['AlertPage']>(`/alerts${query(f)}`, 'GET', undefined, undefined, signal),
  acknowledge: (id: string, key: string) => request<Schema['Alert']>(`/alerts/${encodeURIComponent(id)}/ack`, 'POST', undefined, key),
  subscription: (id: string, signal?: AbortSignal) => request<Schema['Subscription']>(`/users/${encodeURIComponent(id)}/subscription`, 'GET', undefined, undefined, signal),
  subscriptionLink: (id: string, key: string) => request<Schema['SubscriptionLink']>(`/users/${encodeURIComponent(id)}/subscription-link`, 'POST', undefined, key),
}
