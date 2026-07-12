import type { WebChatTemplate, WebChatTemplateVariable } from '@/api/webChat'

export function renderWebChatTemplate(template: Pick<WebChatTemplate, 'body' | 'variables'>, values: Record<string, string>): string {
  let output = template.body
  for (const variable of template.variables) {
    const pattern = new RegExp(`\\{\\{\\s*${escapeRegExp(variable.name)}\\s*\\}\\}`, 'g')
    output = output.replace(pattern, values[variable.name] ?? variable.default_value ?? '')
  }
  return output
}

export function missingRequiredTemplateVariables(variables: WebChatTemplateVariable[], values: Record<string, string>): string[] {
  return variables.filter(variable => variable.required && !(values[variable.name] ?? variable.default_value ?? '').trim()).map(variable => variable.name)
}

export function selectLocalizedWebChatTemplates(templates: WebChatTemplate[], locale: string): WebChatTemplate[] {
  const personal = templates.filter(template => template.scope === 'personal')
  const system = templates.filter(template => template.scope === 'system')
  const language = locale.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en'
  const localized = system.filter(template => template.language.toLowerCase() === language.toLowerCase())
  return [...(localized.length ? localized : system), ...personal]
}

function escapeRegExp(value: string): string { return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') }
