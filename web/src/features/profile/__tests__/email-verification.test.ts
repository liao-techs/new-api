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

import { buildEmailVerificationPath } from '../api'

describe('profile email verification request', () => {
  it('includes the Turnstile token when requesting an email bind code', () => {
    const path = buildEmailVerificationPath(
      'user+alias@example.com',
      'turnstile-token'
    )
    const url = new URL(path, 'https://example.test')

    expect(url.pathname).toBe('/api/verification')
    expect(url.searchParams.get('email')).toBe('user+alias@example.com')
    expect(url.searchParams.get('turnstile')).toBe('turnstile-token')
  })
})
