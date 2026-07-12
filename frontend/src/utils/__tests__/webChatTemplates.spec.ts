import { describe, expect, it } from 'vitest'
import { missingRequiredTemplateVariables, renderWebChatTemplate, selectLocalizedWebChatTemplates } from '../webChatTemplates'

const variables = [
  { name: 'tone', label: 'Tone', required: true, default_value: 'professional', type: 'singleline' as const },
  { name: 'content', label: 'Content', required: true, default_value: '', type: 'multiline' as const },
]

describe('web chat template rendering', () => {
  it('performs plain repeated text replacement without evaluating HTML or expressions', () => {
    const result = renderWebChatTemplate({ body: '{{ tone }}: {{content}} / {{ content }}', variables }, { tone: '<b>direct</b>', content: '${alert(1)}' })
    expect(result).toBe('<b>direct</b>: ${alert(1)} / ${alert(1)}')
  })
  it('uses defaults and identifies missing required values', () => {
    expect(renderWebChatTemplate({ body: '{{tone}} {{content}}', variables }, { content: 'memo' })).toBe('professional memo')
    expect(missingRequiredTemplateVariables(variables, { tone: '' })).toEqual(['tone', 'content'])
  })
  it('prefers the active language and falls back when no localized system template exists', () => {
    const base = { scope: 'system' as const, user_id: null, source_template_id: null, category: '', description: '', body: 'x', variables: [], enabled: true, sort_order: 0, created_at: '', updated_at: '' }
    const zh = { ...base, id: 1, name: '中文', language: 'zh-CN' }
    const en = { ...base, id: 2, name: 'English', language: 'en' }
    expect(selectLocalizedWebChatTemplates([zh, en], 'zh-CN').map(item => item.id)).toEqual([1])
    expect(selectLocalizedWebChatTemplates([zh], 'fr').map(item => item.id)).toEqual([1])
  })
})
