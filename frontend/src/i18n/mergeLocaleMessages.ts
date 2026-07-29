type LocaleMessageTree = Record<string, unknown>

type UnionToIntersection<T> = (
  T extends unknown ? (value: T) => void : never
) extends (value: infer I) => void
  ? I
  : never

function isMessageTree(value: unknown): value is LocaleMessageTree {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

export function mergeLocaleMessages<const T extends readonly LocaleMessageTree[]>(
  ...sources: T
): UnionToIntersection<T[number]> {
  const result: LocaleMessageTree = {}

  for (const source of sources) {
    for (const [key, value] of Object.entries(source)) {
      const current = result[key]
      result[key] = isMessageTree(current) && isMessageTree(value)
        ? mergeLocaleMessages(current, value)
        : value
    }
  }

  return result as UnionToIntersection<T[number]>
}
