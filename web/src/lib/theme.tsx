import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
type Theme = 'light' | 'dark' | 'system'
const ThemeContext = createContext<{ theme: Theme; setTheme: (theme: Theme) => void }>({ theme: 'system', setTheme: () => {} })
export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setTheme] = useState<Theme>(() => { try { const v = localStorage.getItem('sbctl-theme'); return v === 'light' || v === 'dark' ? v : 'system' } catch { return 'system' } })
  useEffect(() => {
    const media = matchMedia('(prefers-color-scheme: dark)')
    const apply = () => { const dark = theme === 'dark' || (theme === 'system' && media.matches); document.documentElement.classList.toggle('dark', dark); document.documentElement.style.colorScheme = dark ? 'dark' : 'light' }
    apply(); media.addEventListener('change', apply)
    try { localStorage.setItem('sbctl-theme', theme) } catch { /* Theme still works without storage. */ }
    return () => media.removeEventListener('change', apply)
  }, [theme])
  return <ThemeContext value={{ theme, setTheme }}>{children}</ThemeContext>
}
// eslint-disable-next-line react-refresh/only-export-components
export function useTheme() { return useContext(ThemeContext) }
