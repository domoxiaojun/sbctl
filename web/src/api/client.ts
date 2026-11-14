import type { Filters, Schema } from './types'

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
function query(filters: Filters = {}) {
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
  users: (f?: Filters, signal?: AbortSignal) => request<Schema['UserPage']>(`/users${query(f)}`, 'GET', undefined, undefined, signal),
  user: (id: string, signal?: AbortSignal) => request<Schema['User']>(`/users/${encodeURIComponent(id)}`, 'GET', undefined, undefined, signal),
  groups: (signal?: AbortSignal) => request<Schema['Group'][]>('/groups', 'GET', undefined, undefined, signal),
  usage: (f?: Filters, signal?: AbortSignal) => request<Schema['UsagePage']>(`/usage${query(f)}`, 'GET', undefined, undefined, signal),
  operations: (f?: Filters, signal?: AbortSignal) => request<Schema['OperationPage']>(`/operations${query(f)}`, 'GET', undefined, undefined, signal),
  operation: (id: string, signal?: AbortSignal) => request<Schema['Operation']>(`/operations/${encodeURIComponent(id)}`, 'GET', undefined, undefined, signal),
  createUser: (body: Schema['CreateUser'], key: string) => request<Schema['Accepted']>('/users', 'POST', body, key),
  disableUser: (id: string, body: Schema['DisableUser'], key: string) => request<Schema['Accepted']>(`/users/${encodeURIComponent(id)}/disable`, 'POST', body, key),
  findRequest: (key: string) => request<Schema['Accepted']>(`/requests/${encodeURIComponent(key)}`),
  alerts: (f?: Filters, signal?: AbortSignal) => request<Schema['AlertPage']>(`/alerts${query(f)}`, 'GET', undefined, undefined, signal),
  acknowledge: (id: string, key: string) => request<Schema['Alert']>(`/alerts/${encodeURIComponent(id)}/ack`, 'POST', undefined, key),
  subscription: (id: string, signal?: AbortSignal) => request<Schema['Subscription']>(`/users/${encodeURIComponent(id)}/subscription`, 'GET', undefined, undefined, signal),
  subscriptionLink: (id: string, key: string) => request<Schema['SubscriptionLink']>(`/users/${encodeURIComponent(id)}/subscription-link`, 'POST', undefined, key),
}
