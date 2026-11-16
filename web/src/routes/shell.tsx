import { useEffect, useState, type CSSProperties } from 'react'
import { Link, Outlet, useRouterState } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowDownUp, Bell, Blocks, ChevronRight, FlaskConical, Globe2, LayoutDashboard, ListChecks, LogOut, Moon, Network, Settings2, ShieldCheck, Sun } from 'lucide-react'
import { api, ApiError, isDemo } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { Sidebar, SidebarContent, SidebarFooter, SidebarGroup, SidebarGroupLabel, SidebarHeader, SidebarInset, SidebarMenu, SidebarMenuButton, SidebarMenuItem, SidebarProvider, SidebarSeparator, SidebarTrigger, useSidebar } from '@/components/ui/sidebar'
import { DataState } from '@/components/business/common'
import { PendingRequestNotice } from '@/components/business/user-actions'
import { useTheme } from '@/lib/theme'
import { clearPendingRequest } from '@/lib/pending-request'

const nav = [
  { to: '/', label: '总览', icon: LayoutDashboard },
  { to: '/nodes', label: '节点管理', icon: Globe2 },
  { to: '/users', label: '用户与授权', icon: Network },
  { to: '/usage', label: '流量统计', icon: ArrowDownUp },
  { to: '/operations', label: '操作记录', icon: ListChecks },
  { to: '/alerts', label: '告警中心', icon: Bell },
] as const

export function Brand() {
  return <div className="brand"><div className="brand-mark"><Network size={21} strokeWidth={2} /></div><div className="brand-wordmark"><strong>sbctl<span>.</span></strong><small>节点控制台</small></div></div>
}

function Login() {
  const [name, setName] = useState(''), [password, setPassword] = useState(''), client = useQueryClient()
  const login = useMutation({ mutationFn: () => api.login({ login_name: name, password }), onSuccess: session => { setPassword(''); client.setQueryData(['session'], session) } })
  return <div className="login-page"><div className="login-panel"><Brand /><div className="login-heading"><h1>登录控制台</h1><p>管理节点，让每一次连接都有序可循。</p></div><form onSubmit={e => { e.preventDefault(); login.mutate() }} className="space-y-5"><div className="field"><Label htmlFor="login-name">管理员账号</Label><Input required autoComplete="username" id="login-name" placeholder="输入管理员账号" value={name} onChange={e => setName(e.target.value)} /></div><div className="field"><Label htmlFor="password">密码</Label><Input required id="password" type="password" autoComplete="current-password" placeholder="输入密码" value={password} onChange={e => setPassword(e.target.value)} /></div>{login.error && <p role="alert" className="field-error">{login.error.message}</p>}<Button type="submit" className="w-full" disabled={login.isPending}>{login.isPending ? '正在登录…' : '登录'}</Button></form>{isDemo && <p className="login-note">演示环境：任意填写即可体验，请勿输入真实密码。</p>}</div></div>
}

function ConsoleLayout({ displayName, onLogout, loggingOut, logoutError }: { displayName: string; onLogout: () => void; loggingOut: boolean; logoutError: Error | null }) {
  const pathname = useRouterState({ select: state => state.location.pathname })
  const { isMobile, setOpenMobile, state } = useSidebar()
  const { resolvedTheme, setTheme } = useTheme()
  const active = nav.find(item => item.to === '/' ? pathname === '/' : pathname.startsWith(item.to))
  const title = active?.label ?? (pathname === '/components' ? '组件验收' : '设置与诊断')
  const themeLabel = resolvedTheme === 'dark' ? '切换浅色主题' : '切换深色主题'
  useEffect(() => { document.getElementById('main-content')?.focus({ preventScroll: true }); window.scrollTo({ top: 0 }) }, [pathname])

  return <>
    <a className="skip-link" href="#main-content">跳到主要内容</a>
    <Sidebar collapsible="icon" className="app-sidebar">
      <SidebarHeader><Brand /></SidebarHeader>
      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupLabel>工作空间</SidebarGroupLabel>
          <nav aria-label={isMobile ? '移动导航' : '主导航'}><SidebarMenu>
            {nav.map(item => <SidebarMenuItem key={item.to}>
              <SidebarMenuButton isActive={active?.to === item.to} tooltip={item.label} render={<Link to={item.to} search={{}} aria-current={active?.to === item.to ? 'page' : undefined} onClick={() => setOpenMobile(false)} />}>
                <item.icon /><span>{item.label}</span>{active?.to === item.to && <ChevronRight className="nav-active-arrow" />}
              </SidebarMenuButton>
            </SidebarMenuItem>)}
          </SidebarMenu></nav>
        </SidebarGroup>
        <SidebarGroup className="mt-auto">
          <SidebarGroupLabel>偏好与帮助</SidebarGroupLabel>
          <SidebarMenu>
            <SidebarMenuItem><SidebarMenuButton isActive={pathname === '/settings'} tooltip="设置与诊断" render={<Link to="/settings" onClick={() => setOpenMobile(false)} />}><Settings2 /><span>设置与诊断</span></SidebarMenuButton></SidebarMenuItem>
            {isDemo && <SidebarMenuItem><SidebarMenuButton isActive={pathname === '/components'} tooltip="组件验收" render={<Link to="/components" onClick={() => setOpenMobile(false)} />}><Blocks /><span>组件验收</span></SidebarMenuButton></SidebarMenuItem>}
          </SidebarMenu>
        </SidebarGroup>
        <div className="workspace-card"><div className="workspace-card-icon"><ShieldCheck size={19} /></div><div><strong>你的私有工作空间</strong><p>统一管理，清晰掌控。</p></div></div>
      </SidebarContent>
      <SidebarSeparator />
      <SidebarFooter>
        <div className="profile"><Avatar className="size-9"><AvatarFallback>管</AvatarFallback></Avatar><div className="profile-text"><strong>{displayName}</strong><small>工作空间管理员</small></div><Tooltip><TooltipTrigger render={<Button aria-label="退出登录" variant="ghost" size="icon" disabled={loggingOut} onClick={onLogout} />}><LogOut /></TooltipTrigger><TooltipContent>退出登录</TooltipContent></Tooltip></div>
        {logoutError && <p role="alert" className="field-error">{logoutError.message}</p>}
      </SidebarFooter>
    </Sidebar>
    <SidebarInset className="main-column">
      <header className="topbar"><div className="topbar-location"><SidebarTrigger aria-label={isMobile ? '打开导航' : state === 'collapsed' ? '展开侧栏' : '收起侧栏'} /><span className="header-divider" /><div className="breadcrumb"><span>工作空间</span><ChevronRight size={14} /><strong>{title}</strong></div></div><div className="topbar-actions"><span className="topbar-environment"><span className="online-dot" />{isDemo ? '演示环境' : '管理控制台'}</span><Tooltip><TooltipTrigger render={<Button variant="ghost" size="icon" aria-label={themeLabel} onClick={() => setTheme(resolvedTheme === 'dark' ? 'light' : 'dark')} />}>{resolvedTheme === 'dark' ? <Sun /> : <Moon />}</TooltipTrigger><TooltipContent>{themeLabel}</TooltipContent></Tooltip><Tooltip><TooltipTrigger render={<Link className="icon-link" to="/alerts" aria-label="查看告警" />}><Bell size={18} /></TooltipTrigger><TooltipContent>查看告警</TooltipContent></Tooltip><Avatar className="header-avatar size-8"><AvatarFallback>管</AvatarFallback></Avatar></div></header>
      {isDemo && <div className="demo-banner"><FlaskConical size={14} /><span><strong>演示模式</strong><span className="demo-separator">/</span>数据与操作仅用于体验，不会连接服务器。</span><Link to="/components">探索组件 <ChevronRight size={14} /></Link></div>}
      <div id="main-content" tabIndex={-1} className="main-content"><PendingRequestNotice /><Outlet /></div>
      <footer className="app-footer"><span>sbctl<span className="footer-divider">/</span>节点与访问管理</span><span>时间显示：Asia/Singapore</span></footer>
    </SidebarInset>
  </>
}

export default function Shell() {
  const client = useQueryClient()
  const session = useQuery({ queryKey: ['session'], queryFn: ({ signal }) => api.session(signal), retry: false, staleTime: 60_000 })
  const logout = useMutation({ mutationFn: () => api.logout(), onSuccess: async () => { await client.cancelQueries(); client.removeQueries({ predicate: query => query.queryKey[0] !== 'session' }); client.getMutationCache().clear(); clearPendingRequest(); client.setQueryData(['session'], null) } })
  useEffect(() => {
    const expired = () => { void client.cancelQueries().then(() => { client.removeQueries({ predicate: query => query.queryKey[0] !== 'session' }); client.getMutationCache().clear(); client.setQueryData(['session'], null) }) }
    window.addEventListener('sbctl-session-expired', expired)
    return () => window.removeEventListener('sbctl-session-expired', expired)
  }, [client])
  if (session.isPending) return <div className="login-page"><DataState loading /></div>
  if (session.error instanceof ApiError && session.error.status === 401) return <Login />
  if (session.error) return <div className="login-page"><div className="login-panel"><Brand /><DataState error={session.error} retry={() => session.refetch()} /></div></div>
  if (!session.data) return <Login />
  return <SidebarProvider style={{ '--sidebar-width': '15.5rem', '--sidebar-width-icon': '4.5rem' } as CSSProperties}><ConsoleLayout displayName={session.data.display_name} onLogout={() => logout.mutate()} loggingOut={logout.isPending} logoutError={logout.error} /></SidebarProvider>
}
