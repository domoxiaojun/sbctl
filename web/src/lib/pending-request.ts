import { useMemo, useSyncExternalStore } from 'react'
import { api, ApiError } from '@/api/client'

const storageKey = 'sbctl-pending-create'
const changeEvent = 'sbctl-pending-request-changed'
type PendingRequest = { key: string; kind: 'create_user' | 'disable_user' | 'enable_user' | 'update_grants'; target_id?: string }

function snapshot() { try { return sessionStorage.getItem(storageKey) } catch { return null } }
function subscribe(onChange: () => void) {
  window.addEventListener(changeEvent, onChange)
  return () => window.removeEventListener(changeEvent, onChange)
}
export function usePendingRequest() {
  const raw = useSyncExternalStore(subscribe, snapshot, () => null)
  return useMemo(() => {
    try { return raw ? JSON.parse(raw) as PendingRequest : null } catch { return null }
  }, [raw])
}
export function clearPendingRequest() {
  sessionStorage.removeItem(storageKey)
  window.dispatchEvent(new Event(changeEvent))
}
export async function submitUserRequest(kind: PendingRequest['kind'], send: (key: string) => ReturnType<typeof api.createUser>, targetId?: string) {
  if (snapshot()) throw new ApiError('PENDING_REQUEST', '请先找回上次提交的操作，再开始新的变更。')
  const pending: PendingRequest = { key: crypto.randomUUID(), kind, ...(targetId ? { target_id: targetId } : {}) }
  // Persist before sending; storage failure must not send an untraceable request.
  sessionStorage.setItem(storageKey, JSON.stringify(pending))
  window.dispatchEvent(new Event(changeEvent))
  try {
    const result = await send(pending.key)
    clearPendingRequest()
    return result
  } catch (error) {
    // Network/server failures may have committed. Keep the original key for lookup.
    if (error instanceof ApiError && (
      [400, 401, 403, 404, 422].includes(error.status) ||
      ['STALE_REVISION', 'NOT_DISABLED', 'USER_DISABLED', 'GRANT_PREVIEW_CHANGED', 'GRANT_UNCHANGED', 'USER_LIMIT', 'NODE_BUSY', 'SUBSCRIPTION_REVOKE_PENDING', 'SUBSCRIPTION_MISSING'].includes(error.code)
    )) clearPendingRequest()
    throw error
  }
}
