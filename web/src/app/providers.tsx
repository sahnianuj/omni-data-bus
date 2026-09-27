import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { Toaster } from 'sonner'
import { TooltipProvider } from '@/components/ui/tooltip'
import { ThemeProvider, useTheme } from './ThemeProvider'

export function createQueryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false } } })
}

function ThemedToaster() {
  const { resolved } = useTheme()
  return <Toaster theme={resolved} position="bottom-right" richColors closeButton />
}

export function Providers({ children, queryClient }: { children: ReactNode; queryClient: QueryClient }) {
  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <TooltipProvider>
          {children}
          <ThemedToaster />
        </TooltipProvider>
      </ThemeProvider>
    </QueryClientProvider>
  )
}
