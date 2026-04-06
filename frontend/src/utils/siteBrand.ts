export const DEFAULT_SITE_NAME = 'SubAPIs'

export function normalizeSiteName(siteName?: string | null): string {
  const trimmed = typeof siteName === 'string' ? siteName.trim() : ''
  if (!trimmed) {
    return DEFAULT_SITE_NAME
  }
  const normalized = trimmed.toLowerCase()
  if (normalized === 'sub2api' || normalized === 'subapis') {
    return DEFAULT_SITE_NAME
  }
  return trimmed
}
