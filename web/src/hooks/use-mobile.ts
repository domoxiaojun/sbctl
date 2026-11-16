import { useSyncExternalStore } from 'react'
const query = '(max-width: 767px)'
function subscribe(change: () => void) {
  const media = window.matchMedia(query)
  media.addEventListener('change', change)
  return () => media.removeEventListener('change', change)
}
const snapshot = () => window.matchMedia(query).matches
export function useIsMobile() {
  return useSyncExternalStore(subscribe, snapshot, () => false)
}
