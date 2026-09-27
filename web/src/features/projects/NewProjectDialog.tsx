import { zodResolver } from '@hookform/resolvers/zod'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router'
import { toast } from 'sonner'
import { z } from 'zod'
import { FormField } from '@/components/FormField'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { applyServerErrors } from '@/features/auth/serverErrors'
import { useCreateProject } from './api'

type Values = { name: string; description: string }

export function NewProjectDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const create = useCreateProject()
  const [formError, setFormError] = useState<string | null>(null)
  const schema = z.object({ name: z.string().trim().min(1, t('auth.required')).max(100), description: z.string().max(500) })
  const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues: { name: '', description: '' } })
  const { errors } = form.formState

  const onSubmit = form.handleSubmit(async (values) => {
    setFormError(null)
    try {
      const p = await create.mutateAsync(values)
      toast.success(t('projects.created'))
      onOpenChange(false)
      form.reset()
      navigate(`/projects/${p.id}/source`)
    } catch (e) {
      setFormError(applyServerErrors(e, form.setError, ['name', 'description']))
    }
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <form onSubmit={onSubmit} noValidate>
          <DialogHeader>
            <DialogTitle>{t('projects.newTitle')}</DialogTitle>
            <DialogDescription>{t('projects.newBody')}</DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-4">
            {formError ? (
              <div role="alert" className="rounded-md bg-danger-soft px-3 py-2 text-[13px] text-danger">
                {formError}
              </div>
            ) : null}
            <FormField id="project-name" label={t('projects.name')} error={errors.name?.message}>
              <Input id="project-name" autoFocus invalid={!!errors.name} {...form.register('name')} />
            </FormField>
            <FormField id="project-description" label={t('projects.descriptionLabel')} error={errors.description?.message}>
              <Input id="project-description" {...form.register('description')} />
            </FormField>
          </div>
          <DialogFooter>
            <Button variant="secondary" onClick={() => onOpenChange(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" loading={create.isPending}>
              {t('projects.create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
