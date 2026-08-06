/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { sideDrawerSwitchItemClassName } from '@/components/drawer-layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'

import {
  getDefaultMaxGroupRatio,
  type ApiKeyFormValues,
  type GroupRatioOption,
} from '../lib'

type ApiKeyRatioProtectionFieldsProps = {
  form: UseFormReturn<ApiKeyFormValues>
  groups: GroupRatioOption[]
  currentUserGroup: string
}

export function ApiKeyRatioProtectionFields({
  form,
  groups,
  currentUserGroup,
}: ApiKeyRatioProtectionFieldsProps) {
  const { t } = useTranslation()
  const selectedGroup = form.watch('group')
  const enabled = form.watch('max_group_ratio_enabled')
  const maximum = form.watch('max_group_ratio')
  const currentRatio = getDefaultMaxGroupRatio(
    selectedGroup || '',
    groups,
    currentUserGroup
  )
  const wouldBlock =
    enabled &&
    maximum !== undefined &&
    currentRatio !== undefined &&
    maximum < currentRatio

  return (
    <div className='border-border/70 bg-muted/20 space-y-3 rounded-lg border p-3'>
      <FormField
        control={form.control}
        name='max_group_ratio_enabled'
        render={({ field }) => (
          <FormItem className={sideDrawerSwitchItemClassName()}>
            <div className='flex flex-col gap-0.5'>
              <FormLabel className='text-sm'>
                {t('Maximum allowed ratio')}
              </FormLabel>
              <FormDescription className='text-xs'>
                {selectedGroup === 'auto'
                  ? t(
                      'Auto selects only groups at or below this cap. If none qualify, the request is rejected before billing.'
                    )
                  : t(
                      'Reject requests before billing when the effective group ratio exceeds this value.'
                    )}
              </FormDescription>
            </div>
            <FormControl>
              <Switch
                checked={field.value}
                onCheckedChange={(checked) => {
                  field.onChange(checked)
                  if (
                    checked &&
                    form.getValues('max_group_ratio') === undefined
                  ) {
                    form.setValue('max_group_ratio', currentRatio)
                  }
                }}
              />
            </FormControl>
          </FormItem>
        )}
      />
      {enabled && (
        <FormField
          control={form.control}
          name='max_group_ratio'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Ratio limit')}</FormLabel>
              <FormControl>
                <Input
                  type='number'
                  min='0'
                  step='any'
                  value={field.value ?? ''}
                  placeholder={t('Enter the highest allowed ratio')}
                  onBlur={field.onBlur}
                  name={field.name}
                  ref={field.ref}
                  onChange={(event) => {
                    const value = event.target.value
                    field.onChange(value === '' ? undefined : Number(value))
                  }}
                />
              </FormControl>
              <FormDescription>
                {t(
                  'The request is allowed when the effective ratio equals this limit.'
                )}
              </FormDescription>
              {wouldBlock && (
                <Alert className='border-amber-200 bg-amber-50 text-amber-900 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-50'>
                  <AlertDescription>
                    {t(
                      'This limit is below the current effective ratio ({{ratio}}). Requests using this key will be blocked.',
                      { ratio: currentRatio }
                    )}
                  </AlertDescription>
                </Alert>
              )}
              <FormMessage />
            </FormItem>
          )}
        />
      )}
    </div>
  )
}
