import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Logo } from '@/components/Logo'

/** Split layout: product story on the left, form card on the right (approved design). */
export function AuthLayout({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  const steps = [
    ['auth.steps.one', 'auth.steps.oneBody'],
    ['auth.steps.two', 'auth.steps.twoBody'],
    ['auth.steps.three', 'auth.steps.threeBody'],
  ] as const
  return (
    <div className="flex min-h-screen bg-background">
      <section className="hidden w-[640px] shrink-0 flex-col justify-between bg-[#09090b] px-16 py-14 text-[#fafafa] lg:flex">
        <div className="flex items-center gap-3">
          <Logo size={36} />
          <span className="text-lg font-semibold tracking-tight">{t('app.name')}</span>
        </div>
        <div className="flex flex-col gap-8">
          <h2 className="text-[40px] font-semibold leading-[48px] tracking-tight">{t('app.tagline')}</h2>
          <ol className="flex flex-col gap-5">
            {steps.map(([title, body], i) => (
              <li key={title} className="flex items-start gap-4">
                <span className="flex size-7 shrink-0 items-center justify-center rounded-full border border-[#3f3f46] font-mono text-xs text-[#a5b4fc]">
                  {i + 1}
                </span>
                <span className="flex flex-col gap-0.5">
                  <span className="text-[15px] font-medium">{t(title)}</span>
                  <span className="text-sm text-[#a1a1aa]">{t(body)}</span>
                </span>
              </li>
            ))}
          </ol>
        </div>
        <span className="text-[13px] text-[#a1a1aa]">{t('auth.release')}</span>
      </section>
      <main className="flex flex-1 items-center justify-center px-4 py-10">{children}</main>
    </div>
  )
}
