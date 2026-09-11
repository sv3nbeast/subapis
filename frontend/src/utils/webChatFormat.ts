/** 网页对话工作台共用的展示格式化函数。 */

export function formatTokens(value: number): string {
  return new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(value || 0)
}

export function shortRequestID(id: string): string {
  return id.length > 16 ? `${id.slice(0, 8)}…${id.slice(-6)}` : id
}

export function formatMessageTime(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '' : new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' }).format(date)
}

export function formatBytes(value: number): string {
  if (value >= 1024 ** 2) return `${(value / 1024 ** 2).toFixed(1)} MB`
  return `${Math.max(1, Math.ceil(value / 1024))} KB`
}

/** 归一化品牌名，供头像与模型标记按品牌着色。 */
export function brandKey(brand?: string | null): string {
  const value = (brand || '').trim().toLowerCase()
  if (!value) return ''
  if (value.includes('claude') || value.includes('anthropic')) return 'claude'
  if (value.includes('openai') || value.includes('gpt') || value.includes('codex')) return 'openai'
  if (value.includes('google') || value.includes('gemini')) return 'google'
  return value.replace(/[^a-z0-9]+/g, '-')
}

export function fileExtension(nameOrExt: string): string {
  const raw = nameOrExt.includes('.') ? nameOrExt.slice(nameOrExt.lastIndexOf('.') + 1) : nameOrExt
  return raw.trim().toLowerCase()
}
