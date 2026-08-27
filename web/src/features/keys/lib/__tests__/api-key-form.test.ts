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
import { describe, expect, it } from 'vitest'

import {
  API_KEY_FORM_DEFAULT_VALUES,
  getApiKeyFormDefaultValues,
  getApiKeyRatioProtectionState,
  getDefaultMaxGroupRatio,
  transformApiKeyToFormDefaults,
  transformFormDataToPayload,
} from '../api-key-form'

const groups = [
  { value: 'default', ratio: 0.8 },
  { value: 'premium', ratio: '0.2' },
  { value: 'auto', ratio: 'automatic', maxRatio: 0.15 },
]

describe('API key maximum group ratio form', () => {
  it('defaults a fixed group to twice its current effective ratio', () => {
    const values = getApiKeyFormDefaultValues(false, groups, 'default')
    expect(values.group).toBe('')
    expect(values.max_group_ratio_enabled).toBe(true)
    expect(values.max_group_ratio).toBe(1.6)
  })

  it('defaults auto group to twice the highest current candidate ratio', () => {
    const values = getApiKeyFormDefaultValues(true, groups)
    expect(values.group).toBe('auto')
    expect(values.max_group_ratio_enabled).toBe(true)
    expect(values.max_group_ratio).toBe(0.3)
    expect(getDefaultMaxGroupRatio('auto', groups)).toBe(0.3)
  })

  it('preserves zero and sends null only when protection is disabled', () => {
    const zeroPayload = transformFormDataToPayload({
      ...API_KEY_FORM_DEFAULT_VALUES,
      max_group_ratio_enabled: true,
      max_group_ratio: 0,
    })
    expect(zeroPayload.max_group_ratio).toBe(0)

    const unlimitedPayload = transformFormDataToPayload({
      ...API_KEY_FORM_DEFAULT_VALUES,
      max_group_ratio_enabled: false,
      max_group_ratio: 0.12,
    })
    expect(unlimitedPayload.max_group_ratio).toBeNull()
  })

  it('maps existing unrestricted and guarded keys without changing meaning', () => {
    const baseKey = {
      id: 1,
      name: 'key',
      key: 'masked',
      status: 1,
      remain_quota: 0,
      used_quota: 0,
      unlimited_quota: true,
      expired_time: -1,
      created_time: 1,
      accessed_time: 1,
      group: 'default',
      auto_groups: null,
      cross_group_retry: false,
      model_limits_enabled: false,
      model_limits: '',
      allow_ips: '',
    }

    const unrestricted = transformApiKeyToFormDefaults({
      ...baseKey,
      max_group_ratio: null,
    })
    expect(unrestricted.max_group_ratio_enabled).toBe(false)
    expect(unrestricted.max_group_ratio).toBeUndefined()

    const guarded = transformApiKeyToFormDefaults({
      ...baseKey,
      max_group_ratio: 0,
    })
    expect(guarded.max_group_ratio_enabled).toBe(true)
    expect(guarded.max_group_ratio).toBe(0)
  })

  it('classifies protected, exceeded, and unrestricted keys', () => {
    const protectedKey = getApiKeyRatioProtectionState(
      { group: 'default', max_group_ratio: 1.6 },
      groups
    )
    expect(protectedKey).toEqual({
      currentRatio: 0.8,
      protected: true,
      exceeded: false,
    })

    const exceededKey = getApiKeyRatioProtectionState(
      { group: 'premium', max_group_ratio: 0.15 },
      groups
    )
    expect(exceededKey.exceeded).toBe(true)

    const unrestrictedKey = getApiKeyRatioProtectionState(
      { group: 'premium', max_group_ratio: null },
      groups
    )
    expect(unrestrictedKey.protected).toBe(false)
    expect(unrestrictedKey.exceeded).toBe(false)
  })
})
