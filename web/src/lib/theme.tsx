import { createContext, useContext, useEffect, useState, useSyncExternalStore, type ReactNode } from 'react'
type Theme = 'light' | 'dark' | 'system'
const ThemeContext = createContext<{ theme: Theme; resolvedTheme: 'light' | 'dark'; setTheme: (theme: Theme) => void }>({ theme: 'system', resolvedTheme: 'light', setTheme: () => {} })
const readSystemTheme = () => matchMedia('(prefers-color-scheme: dark)').matches
function subscribeSystemTheme(change: () => void) {
  const media = matchMedia('(prefers-color-scheme: dark)')
  media.addEventListener('change', change)
  return () => media.removeEventListener('change', change)
}
export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setTheme] = useState<Theme>(() => { try { const value = localStorage.getItem('sbctl-theme'); return value === 'light' || value === 'dark' ? value : 'system' } catch { return 'system' } })
  const systemDark = useSyncExternalStore(subscribeSystemTheme, readSystemTheme, () => false)
  const resolvedTheme = theme === 'system' ? (systemDark ? 'dark' : 'light') : theme
  useEffect(() => {
    document.documentElement.classList.toggle('dark', resolvedTheme === 'dark')
    document.documentElement.style.colorScheme = resolvedTheme
    try { localStorage.setItem('sbctl-theme', theme) } catch { /* Theme remains available without storage. */ }
  }, [theme, resolvedTheme])
  return <ThemeContext value={{ theme, resolvedTheme, setTheme }}>{children}</ThemeContext>
}
// eslint-disable-next-line react-refresh/only-export-components
export function useTheme() { return useContext(ThemeContext) }
