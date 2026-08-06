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

import { getIsolatedIframeSandbox } from '../html-content-sandbox'

const homePageUrl = 'https://ooioo.work/'

describe('HtmlContent isolated iframe sandbox', () => {
  test('allows Channel Pulse to refresh without granting same-origin access', () => {
    const sandbox = getIsolatedIframeSandbox(
      'https://channel-pulse.bothyouandme.com/embed.html',
      homePageUrl
    ).split(/\s+/)

    assert.ok(sandbox.includes('allow-scripts'))
    assert.ok(!sandbox.includes('allow-same-origin'))
  })

  test('keeps script execution blocked for other iframe origins', () => {
    const sandbox = getIsolatedIframeSandbox(
      'https://example.com/embed.html',
      homePageUrl
    ).split(/\s+/)

    assert.ok(!sandbox.includes('allow-scripts'))
    assert.ok(!sandbox.includes('allow-same-origin'))
  })

  test('does not trust an origin prefix lookalike', () => {
    const sandbox = getIsolatedIframeSandbox(
      'https://channel-pulse.bothyouandme.com.attacker.example/embed.html',
      homePageUrl
    ).split(/\s+/)

    assert.ok(!sandbox.includes('allow-scripts'))
  })
})
