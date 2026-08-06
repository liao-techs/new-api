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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  API_KEY_FORM_DEFAULT_VALUES,
  getApiKeyFormDefaultValues,
  getApiKeyRatioProtectionState,
  getDefaultMaxGroupRatio,
  transformApiKeyToFormDefaults,
  transformFormDataToPayload,
} from '../api-key-form'

const groups = [
  { value: 'default', ratio: 0.1 },
  { value: 'premium', ratio: '0.2' },
  { value: 'auto', ratio: 'automatic', maxRatio: 0.15 },
]

describe('API key maximum group ratio form', () => {
  test('defaults a fixed group to its current effective ratio', () => {
    const values = getApiKeyFormDefaultValues(false, groups, 'default')
    assert.equal(values.group, '')
    assert.equal(values.max_group_ratio_enabled, true)
    assert.equal(values.max_group_ratio, 0.1)
  })

  test('defaults auto group to the highest current candidate ratio', () => {
    const values = getApiKeyFormDefaultValues(true, groups)
    assert.equal(values.group, 'auto')
    assert.equal(values.max_group_ratio_enabled, true)
    assert.equal(values.max_group_ratio, 0.15)
    assert.equal(getDefaultMaxGroupRatio('auto', groups), 0.15)
  })

  test('preserves zero and sends null only when protection is disabled', () => {
    const zeroPayload = transformFormDataToPayload({
      ...API_KEY_FORM_DEFAULT_VALUES,
      max_group_ratio_enabled: true,
      max_group_ratio: 0,
    })
    assert.equal(zeroPayload.max_group_ratio, 0)

    const unlimitedPayload = transformFormDataToPayload({
      ...API_KEY_FORM_DEFAULT_VALUES,
      max_group_ratio_enabled: false,
      max_group_ratio: 0.12,
    })
    assert.equal(unlimitedPayload.max_group_ratio, null)
  })

  test('maps existing unrestricted and guarded keys without changing meaning', () => {
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
    assert.equal(unrestricted.max_group_ratio_enabled, false)
    assert.equal(unrestricted.max_group_ratio, undefined)

    const guarded = transformApiKeyToFormDefaults({
      ...baseKey,
      max_group_ratio: 0,
    })
    assert.equal(guarded.max_group_ratio_enabled, true)
    assert.equal(guarded.max_group_ratio, 0)
  })

  test('classifies protected, exceeded, and unrestricted keys', () => {
    const protectedKey = getApiKeyRatioProtectionState(
      { group: 'default', max_group_ratio: 0.1 },
      groups
    )
    assert.deepEqual(protectedKey, {
      currentRatio: 0.1,
      protected: true,
      exceeded: false,
    })

    const exceededKey = getApiKeyRatioProtectionState(
      { group: 'premium', max_group_ratio: 0.15 },
      groups
    )
    assert.equal(exceededKey.exceeded, true)

    const unrestrictedKey = getApiKeyRatioProtectionState(
      { group: 'premium', max_group_ratio: null },
      groups
    )
    assert.equal(unrestrictedKey.protected, false)
    assert.equal(unrestrictedKey.exceeded, false)
  })
})
