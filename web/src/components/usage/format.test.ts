import { describe, expect, it, vi } from 'vite-plus/test'
import { displayModelName, showSeparateToolCalls } from './format'

vi.mock('@/i18n', () => ({}))

const prefix = '\u6297\u622a\u65ad-'

describe('usage model display names', () => {
  it.each([
    [`${prefix}gemini-2.5-pro`, 'gemini-2.5-pro'],
    [`models/${prefix}gemini-2.5-pro`, 'models/gemini-2.5-pro'],
    [`${prefix}mapped-model-search(high)`, 'mapped-model-search(high)'],
    [`models/${prefix}mapped-model(high)-search`, 'models/mapped-model(high)-search'],
    [`${prefix}${prefix}gemini-2.5-pro`, `${prefix}gemini-2.5-pro`],
  ])('removes only the reserved leading prefix from %s', (value, expected) => {
    expect(displayModelName(value)).toBe(expected)
  })

  it.each([
    '',
    'gemini-2.5-pro',
    'models/gemini-2.5-pro',
    'mapped-model-search(high)',
    'models/mapped-model(high)-search',
    `custom-${prefix}gemini-2.5-pro`,
    `providers/google/models/${prefix}gemini-2.5-pro`,
  ])('preserves ordinary names and embedded prefix text: %s', (value) => {
    expect(displayModelName(value)).toBe(value)
  })
})

describe('usage tool-call counts', () => {
  it.each(['completed', 'failed', 'limited', 'blocked', 'cancelled'])(
    'shows actual calls independently of the %s state',
    (state) => {
      expect(
        showSeparateToolCalls({ state, tool_calls: 2, requested_model: 'gemini-2.5-pro' }),
      ).toBe(true)
    },
  )

  it.each([`${prefix}gemini-2.5-pro`, `models/${prefix}mapped-model-search(high)`])(
    'shows the recorded zero for anti-truncation requests: %s',
    (requested_model) => {
      const record = Object.freeze({ state: 'completed', tool_calls: 0, requested_model })
      expect(showSeparateToolCalls(record)).toBe(true)
      expect(record.tool_calls).toBe(0)
      expect(record.requested_model).toBe(requested_model)
    },
  )

  it.each([0, 1, 2])('does not duplicate the tool_calls badge for count %s', (tool_calls) => {
    expect(
      showSeparateToolCalls({
        state: 'tool_calls',
        tool_calls,
        requested_model: `${prefix}gemini-2.5-pro`,
      }),
    ).toBe(false)
  })

  it.each(['', 'gemini-2.5-pro', 'models/gemini-2.5-pro', `custom-${prefix}gemini-2.5-pro`])(
    'keeps zero-count ordinary requests unchanged: %s',
    (requested_model) => {
      expect(showSeparateToolCalls({ state: 'completed', tool_calls: 0, requested_model })).toBe(
        false,
      )
    },
  )

  it('uses only the original requested model to identify anti-truncation', () => {
    const record = {
      state: 'failed',
      tool_calls: 0,
      requested_model: 'gemini-2.5-pro',
      model: `${prefix}gemini-2.5-pro`,
      response_model: `${prefix}gemini-2.5-pro`,
    }
    expect(showSeparateToolCalls(record)).toBe(false)
  })
})
