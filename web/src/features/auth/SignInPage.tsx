import { zodResolver } from '@hookform/resolvers/zod'
import { Eye, EyeOff } from 'lucide-react'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { Link, Navigate, useNavigate, useSearchParams } from 'react-router'
import { z } from 'zod'
import { FormField } from '@/components/FormField'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ApiError } from '@/lib/api/client'
import { safeNext, useSession, useSignIn } from './api'
import { AuthLayout } from './AuthLayout'
import { applyServerErrors } from './serverErrors'

type Values = { email: string; password: string }

export function SignInPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const next = safeNext(params.get('next'))
  const session = useSession()
  const signIn = useSignIn()
  const [showPw, setShowPw] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)

  const schema = z.object({
    email: z.string().min(1, t('auth.required')),
    password: z.string().min(1, t('auth.required')),
  })
  const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues: { email: '', password: '' } })
  const { errors } = form.formState

  if (session.data) return <Navigate to={next} replace />

  const onSubmit = form.handleSubmit(async (values) => {
    setFormError(null)
    try {
      await signIn.mutateAsync(values)
      navigate(next, { replace: true })
    } catch (e) {
      if (e instanceof ApiError && e.status === 429) {
        const seconds = e.detail?.match(/(\d+) seconds/)?.[1] ?? '60'
        setFormError(t('auth.rateLimited', { seconds }))
        return
      }
      setFormError(applyServerErrors(e, form.setError, ['email', 'password']))
    }
  })

  return (
    <AuthLayout>
      <form onSubmit={onSubmit} noValidate className="flex w-full max-w-[400px] flex-col gap-5 rounded-[var(--radius-dialog)] border border-border bg-surface p-9 shadow-sm">
        <div className="flex flex-col gap-1.5">
          <h1 className="text-[22px] font-semibold tracking-tight">{t('auth.signInTitle')}</h1>
          <p className="text-sm text-muted">{t('auth.signInSubtitle')}</p>
        </div>
        {formError ? (
          <div role="alert" className="rounded-md bg-danger-soft px-3 py-2 text-[13px] text-danger">
            {formError}
          </div>
        ) : null}
        <FormField id="email" label={t('auth.email')} error={errors.email?.message}>
          <Input id="email" type="email" autoComplete="email" invalid={!!errors.email} {...form.register('email')} />
        </FormField>
        <FormField id="password" label={t('auth.password')} error={errors.password?.message}>
          <div className="relative">
            <Input id="password" type={showPw ? 'text' : 'password'} autoComplete="current-password" className="pr-9" invalid={!!errors.password} {...form.register('password')} />
            <button
              type="button"
              onClick={() => setShowPw((v) => !v)}
              aria-label={showPw ? t('secret.hide') : t('secret.show')}
              className="absolute right-1 top-1/2 flex size-7 -translate-y-1/2 items-center justify-center rounded text-subtle hover:text-foreground"
            >
              {showPw ? <EyeOff className="size-4" aria-hidden /> : <Eye className="size-4" aria-hidden />}
            </button>
          </div>
        </FormField>
        <Button type="submit" loading={signIn.isPending} className="h-10">
          {t('auth.signIn')}
        </Button>
        <p className="text-center text-[13px] text-muted">
          {t('auth.noAccount')}{' '}
          <Link to={`/signup${params.get('next') ? `?next=${encodeURIComponent(next)}` : ''}`} className="font-medium text-primary-soft-foreground hover:underline">
            {t('auth.createAccount')}
          </Link>
        </p>
      </form>
    </AuthLayout>
  )
}
