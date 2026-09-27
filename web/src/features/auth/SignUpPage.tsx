import { zodResolver } from '@hookform/resolvers/zod'
import { useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { Link, Navigate, useNavigate, useSearchParams } from 'react-router'
import { z } from 'zod'
import { FormField } from '@/components/FormField'
import { SecretInput } from '@/components/SecretInput'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { safeNext, useSession, useSignUp } from './api'
import { AuthLayout } from './AuthLayout'
import { PasswordStrength } from './PasswordStrength'
import { applyServerErrors } from './serverErrors'

type Values = { displayName: string; email: string; password: string }

export function SignUpPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const next = safeNext(params.get('next'))
  const session = useSession()
  const signUp = useSignUp()
  const [formError, setFormError] = useState<string | null>(null)

  const schema = z.object({
    displayName: z.string().trim().min(1, t('auth.required')).max(80),
    email: z.string().trim().email(t('auth.invalidEmail')),
    password: z.string().min(12, t('auth.passwordMin')).max(256),
  })
  const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues: { displayName: '', email: '', password: '' } })
  const { errors } = form.formState
  const password = useWatch({ control: form.control, name: 'password' })

  if (session.data) return <Navigate to={next} replace />

  const onSubmit = form.handleSubmit(async (values) => {
    setFormError(null)
    try {
      await signUp.mutateAsync(values)
      navigate(next, { replace: true })
    } catch (e) {
      setFormError(applyServerErrors(e, form.setError, ['displayName', 'email', 'password']))
    }
  })

  return (
    <AuthLayout>
      <form onSubmit={onSubmit} noValidate className="flex w-full max-w-[400px] flex-col gap-5 rounded-[var(--radius-dialog)] border border-border bg-surface p-9 shadow-sm">
        <div className="flex flex-col gap-1.5">
          <h1 className="text-[22px] font-semibold tracking-tight">{t('auth.signUpTitle')}</h1>
          <p className="text-sm text-muted">{t('auth.signUpSubtitle')}</p>
        </div>
        {formError ? (
          <div role="alert" className="rounded-md bg-danger-soft px-3 py-2 text-[13px] text-danger">
            {formError}
          </div>
        ) : null}
        <FormField id="displayName" label={t('auth.displayName')} error={errors.displayName?.message}>
          <Input id="displayName" autoComplete="name" invalid={!!errors.displayName} {...form.register('displayName')} />
        </FormField>
        <FormField id="email" label={t('auth.email')} error={errors.email?.message}>
          <Input id="email" type="email" autoComplete="email" invalid={!!errors.email} {...form.register('email')} />
        </FormField>
        <FormField id="password" label={t('auth.password')} error={errors.password?.message} hint={t('auth.passwordHint')}>
          <SecretInput
            id="password"
            stored={false}
            value={password}
            invalid={!!errors.password}
            onChange={(v) => form.setValue('password', v ?? '', { shouldValidate: form.formState.isSubmitted })}
          />
          <PasswordStrength password={password} />
        </FormField>
        <Button type="submit" loading={signUp.isPending} className="h-10">
          {t('auth.signUp')}
        </Button>
        <p className="text-center text-[13px] text-muted">
          {t('auth.haveAccount')}{' '}
          <Link to="/signin" className="font-medium text-primary-soft-foreground hover:underline">
            {t('auth.goSignIn')}
          </Link>
        </p>
      </form>
    </AuthLayout>
  )
}
