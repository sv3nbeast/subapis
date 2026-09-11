/** 网页对话展示用的紧凑数字格式化。 */
export function formatTokens(value: number): string {
  return new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(value || 0)
}
