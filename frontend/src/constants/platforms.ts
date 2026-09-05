import type { AccountPlatform, GroupPlatform } from '@/types'

export interface PlatformOption<T extends string = string> {
  value: T
  label: string
}

/**
 * Concrete upstream platforms supported by accounts and request routing.
 * Keep platform selectors derived from this catalog so newly added providers
 * do not silently disappear from list filters.
 */
export const CONCRETE_PLATFORM_OPTIONS = [
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'gemini', label: 'Gemini' },
  { value: 'antigravity', label: 'Antigravity' },
  { value: 'kiro', label: 'Kiro' },
  { value: 'droid', label: 'Droid' },
  { value: 'grok', label: 'Grok' },
  { value: 'kimi', label: 'Kimi' },
  { value: 'zhipu', label: 'Zhipu GLM' },
  { value: 'deepseek', label: 'DeepSeek' }
] as const satisfies readonly PlatformOption<AccountPlatform>[]

/** Stable platform order shared by quota, channel, and dashboard surfaces. */
export const CONCRETE_PLATFORM_VALUES = CONCRETE_PLATFORM_OPTIONS.map(
  (option) => option.value
)

/** Platforms that can own a group. */
export const GROUP_PLATFORM_OPTIONS = [
  ...CONCRETE_PLATFORM_OPTIONS,
  { value: 'composite', label: 'Composite' }
] as const satisfies readonly PlatformOption<GroupPlatform>[]

/**
 * Concrete platforms accepted by CompositeModelRoute.target_platform.
 * Kiro and Droid can own direct groups, but are not composite route targets.
 */
const COMPOSITE_ROUTE_EXCLUDED_PLATFORMS = new Set<AccountPlatform>(['kiro', 'droid'])

export const COMPOSITE_ROUTE_PLATFORM_OPTIONS = CONCRETE_PLATFORM_OPTIONS.filter(
  (option) => !COMPOSITE_ROUTE_EXCLUDED_PLATFORMS.has(option.value)
)
