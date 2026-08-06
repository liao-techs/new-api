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
import type { TFunction } from 'i18next'
import { z } from 'zod'

import { parseQuotaFromDollars, quotaUnitsToDollars } from '@/lib/format'

import { DEFAULT_GROUP } from '../constants'
import type { ApiKey, ApiKeyFormData } from '../types'

// ============================================================================
// Form Schema
// ============================================================================

export function getApiKeyFormSchema(t: TFunction, maxAutoGroups = 5) {
  const autoGroupLimit =
    Number.isInteger(maxAutoGroups) && maxAutoGroups > 0 ? maxAutoGroups : 5

  return z
    .object({
      name: z.string().min(1, t('Please enter a name')),
      remain_quota_dollars: z.number().optional(),
      expired_time: z.date().optional(),
      unlimited_quota: z.boolean(),
      model_limits: z.array(z.string()),
      allow_ips: z.string().optional(),
      group: z.string().optional(),
      auto_groups_mode: z.enum(['inherit', 'custom']),
      auto_groups: z.array(z.string()),
      cross_group_retry: z.boolean().optional(),
      max_group_ratio_enabled: z.boolean(),
      max_group_ratio: z.number().optional(),
      tokenCount: z.number().min(1).optional(),
    })
    .superRefine((data, ctx) => {
      if (data.group === 'auto') {
        if (
          data.auto_groups_mode === 'custom' &&
          data.auto_groups.length === 0
        ) {
          ctx.addIssue({
            code: 'custom',
            path: ['auto_groups'],
            message: t(
              'Select at least one Auto group or restore global Auto.'
            ),
          })
        }

        if (data.auto_groups.length > autoGroupLimit) {
          ctx.addIssue({
            code: 'custom',
            path: ['auto_groups'],
            message: t('Select at most {{max}} Auto groups', {
              max: autoGroupLimit,
            }),
          })
        }

        if (new Set(data.auto_groups).size !== data.auto_groups.length) {
          ctx.addIssue({
            code: 'custom',
            path: ['auto_groups'],
            message: t('Auto groups must not contain duplicates'),
          })
        }
      }

      if (
        !data.unlimited_quota &&
        (data.remain_quota_dollars === undefined ||
          data.remain_quota_dollars < 0)
      ) {
        ctx.addIssue({
          code: 'custom',
          path: ['remain_quota_dollars'],
          message: t('Quota must be zero or greater'),
        })
      }
      if (
        data.max_group_ratio_enabled &&
        (data.max_group_ratio === undefined ||
          !Number.isFinite(data.max_group_ratio) ||
          data.max_group_ratio < 0)
      ) {
        ctx.addIssue({
          code: 'custom',
          path: ['max_group_ratio'],
          message: t('Maximum allowed ratio must be zero or greater'),
        })
      }
    })
}

export type ApiKeyFormValues = z.infer<ReturnType<typeof getApiKeyFormSchema>>

// ============================================================================
// Form Defaults
// ============================================================================

export const API_KEY_FORM_DEFAULT_VALUES: ApiKeyFormValues = {
  name: '',
  remain_quota_dollars: 10,
  expired_time: undefined,
  unlimited_quota: true,
  model_limits: [],
  allow_ips: '',
  group: DEFAULT_GROUP,
  auto_groups_mode: 'inherit',
  auto_groups: [],
  cross_group_retry: true,
  max_group_ratio_enabled: false,
  max_group_ratio: undefined,
  tokenCount: 1,
}

export type GroupRatioOption = {
  value: string
  ratio?: number | string
  maxRatio?: number
}

function parseGroupRatio(ratio: number | string | undefined) {
  if (typeof ratio === 'number' && Number.isFinite(ratio)) return ratio
  if (typeof ratio !== 'string' || ratio.trim() === '') return undefined
  const parsed = Number(ratio)
  return Number.isFinite(parsed) ? parsed : undefined
}

export function getDefaultMaxGroupRatio(
  group: string,
  groups: GroupRatioOption[],
  inheritedGroup: string = DEFAULT_GROUP
) {
  const effectiveGroup = group || inheritedGroup
  if (effectiveGroup === 'auto') {
    const configuredAutoMax = groups.find(
      (item) => item.value === 'auto'
    )?.maxRatio
    if (configuredAutoMax !== undefined && Number.isFinite(configuredAutoMax)) {
      return configuredAutoMax
    }
  }
  if (effectiveGroup !== 'auto') {
    return parseGroupRatio(
      groups.find((item) => item.value === effectiveGroup)?.ratio
    )
  }
  const ratios = groups
    .filter((item) => item.value !== 'auto')
    .map((item) => parseGroupRatio(item.ratio))
    .filter((ratio): ratio is number => ratio !== undefined)
  return ratios.length > 0 ? Math.max(...ratios) : undefined
}

export type ApiKeyRatioProtectionState = {
  currentRatio?: number
  protected: boolean
  exceeded: boolean
}

export function getApiKeyRatioProtectionState(
  apiKey: Pick<ApiKey, 'group' | 'max_group_ratio'>,
  groups: GroupRatioOption[],
  inheritedGroup: string = DEFAULT_GROUP
): ApiKeyRatioProtectionState {
  const currentRatio = getDefaultMaxGroupRatio(
    apiKey.group || '',
    groups,
    inheritedGroup
  )
  const maxGroupRatio = apiKey.max_group_ratio
  const isProtected = maxGroupRatio !== null && maxGroupRatio !== undefined

  return {
    currentRatio,
    protected: isProtected,
    exceeded:
      isProtected && currentRatio !== undefined && currentRatio > maxGroupRatio,
  }
}

export function getApiKeyFormDefaultValues(
  defaultUseAutoGroup: boolean,
  groups: GroupRatioOption[] = [],
  inheritedGroup: string = DEFAULT_GROUP
): ApiKeyFormValues {
  const group = defaultUseAutoGroup ? 'auto' : DEFAULT_GROUP
  const maxGroupRatio = getDefaultMaxGroupRatio(group, groups, inheritedGroup)
  return {
    ...API_KEY_FORM_DEFAULT_VALUES,
    group,
    auto_groups_mode: 'inherit',
    auto_groups: [],
    cross_group_retry: defaultUseAutoGroup,
    max_group_ratio_enabled: maxGroupRatio !== undefined,
    max_group_ratio: maxGroupRatio,
  }
}

// ============================================================================
// Form Data Transformation
// ============================================================================

/**
 * Transform form data to API payload
 */
export function transformFormDataToPayload(
  data: ApiKeyFormValues
): ApiKeyFormData {
  return {
    name: data.name,
    remain_quota: data.unlimited_quota
      ? 0
      : parseQuotaFromDollars(data.remain_quota_dollars || 0),
    expired_time: data.expired_time
      ? Math.floor(data.expired_time.getTime() / 1000)
      : -1,
    unlimited_quota: data.unlimited_quota,
    model_limits_enabled: data.model_limits.length > 0,
    model_limits: data.model_limits.join(','),
    allow_ips: data.allow_ips || '',
    group: data.group || '',
    auto_groups:
      data.group === 'auto' && data.auto_groups_mode === 'custom'
        ? data.auto_groups
        : [],
    cross_group_retry: data.group === 'auto' ? !!data.cross_group_retry : false,
    max_group_ratio: data.max_group_ratio_enabled
      ? (data.max_group_ratio ?? null)
      : null,
  }
}

/**
 * Transform API key data to form defaults
 */
export function transformApiKeyToFormDefaults(
  apiKey: ApiKey,
  availableAutoGroups: string[] = [],
  maxAutoGroups = 5
): ApiKeyFormValues {
  const availableSet = new Set(availableAutoGroups)
  const storedAutoGroups = apiKey.auto_groups ?? []
  const autoGroups = storedAutoGroups
    .filter((group) => availableSet.has(group))
    .slice(0, Math.max(0, maxAutoGroups))
  const autoGroupsMode = storedAutoGroups.length > 0 ? 'custom' : 'inherit'

  return {
    name: apiKey.name,
    remain_quota_dollars: apiKey.unlimited_quota
      ? 0
      : quotaUnitsToDollars(apiKey.remain_quota),
    expired_time:
      apiKey.expired_time > 0
        ? new Date(apiKey.expired_time * 1000)
        : undefined,
    unlimited_quota: apiKey.unlimited_quota,
    model_limits: apiKey.model_limits
      ? apiKey.model_limits.split(',').filter(Boolean)
      : [],
    allow_ips: apiKey.allow_ips || '',
    group: apiKey.group || DEFAULT_GROUP,
    auto_groups_mode: autoGroupsMode,
    auto_groups: autoGroups,
    cross_group_retry: !!apiKey.cross_group_retry,
    max_group_ratio_enabled: apiKey.max_group_ratio !== null,
    max_group_ratio: apiKey.max_group_ratio ?? undefined,
    tokenCount: 1,
  }
}
