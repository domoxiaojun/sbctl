import React from 'react'
import ReactDOM from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'
import { router } from './router'
import { ThemeProvider } from './lib/theme'
import { TooltipProvider } from './components/ui/tooltip'
import './styles/globals.css'
const client = new QueryClient({ defaultOptions: { queries: { staleTime: 15_000, retry: false }, mutations: { retry: false } } })
ReactDOM.createRoot(document.getElementById('root')!).render(<React.StrictMode><QueryClientProvider client={client}><ThemeProvider><TooltipProvider delay={300}><RouterProvider router={router} /></TooltipProvider></ThemeProvider></QueryClientProvider></React.StrictMode>)
