import { createI18n } from 'vue-i18n'

type LocaleCode = 'en' | 'zh'

type LocaleMessages = Record<string, any>

const LOCALE_KEY = 'sub2api_locale'
const DEFAULT_LOCALE: LocaleCode = 'en'

const localeLoaders: Record<LocaleCode, () => Promise<{ default: LocaleMessages }>> = {
  en: () => import('./locales/en'),
  zh: () => import('./locales/zh')
}

function isLocaleCode(value: string): value is LocaleCode {
  return value === 'en' || value === 'zh'
}

function safeStorageGet(key: string): string {
  try {
    if (typeof window === 'undefined' || !window.localStorage) {
      return ''
    }
    return window.localStorage.getItem(key) ?? ''
  } catch {
    return ''
  }
}

function safeStorageSet(key: string, value: string): void {
  try {
    if (typeof window === 'undefined' || !window.localStorage) {
      return
    }
    window.localStorage.setItem(key, value)
  } catch {
    // Ignore storage failures in restricted/private contexts.
  }
}

function getBrowserLanguage(): string {
  try {
    if (typeof navigator === 'undefined') {
      return ''
    }
    return String(navigator.language || '').toLowerCase()
  } catch {
    return ''
  }
}

function isChineseSiteHost(): boolean {
  try {
    if (typeof window === 'undefined') {
      return false
    }
    const host = String(window.location?.hostname || '').toLowerCase()
    return host === 'subapis.com' || host.endsWith('.subapis.com')
  } catch {
    return false
  }
}

function getPreferredLocale(): LocaleCode {
  const browserLang = getBrowserLanguage()
  if (browserLang.startsWith('zh') || isChineseSiteHost()) {
    return 'zh'
  }
  return DEFAULT_LOCALE
}

function getDefaultLocale(): LocaleCode {
  const saved = safeStorageGet(LOCALE_KEY)
  if (saved && isLocaleCode(saved)) {
    if (saved === 'en' && getBrowserLanguage().startsWith('zh')) {
      return 'zh'
    }
    return saved
  }

  return getPreferredLocale()
}

export const i18n = createI18n({
  legacy: false,
  locale: getDefaultLocale(),
  fallbackLocale: DEFAULT_LOCALE,
  messages: {},
  // 禁用 HTML 消息警告 - 引导步骤使用富文本内容（driver.js 支持 HTML）
  // 这些内容是内部定义的，不存在 XSS 风险
  warnHtmlMessage: false
})

const loadedLocales = new Set<LocaleCode>()

export async function loadLocaleMessages(locale: LocaleCode): Promise<void> {
  if (loadedLocales.has(locale)) {
    return
  }

  const loader = localeLoaders[locale]
  const module = await loader()
  i18n.global.setLocaleMessage(locale, module.default)
  loadedLocales.add(locale)
}

export async function initI18n(): Promise<void> {
  const current = getLocale()
  await loadLocaleMessages(current)
  document.documentElement.setAttribute('lang', current)
}

export async function setLocale(locale: string): Promise<void> {
  if (!isLocaleCode(locale)) {
    return
  }

  await loadLocaleMessages(locale)
  i18n.global.locale.value = locale
  safeStorageSet(LOCALE_KEY, locale)
  document.documentElement.setAttribute('lang', locale)

  // 同步更新浏览器页签标题，使其跟随语言切换
  const { resolveDocumentTitle } = await import('@/router/title')
  const { default: router } = await import('@/router')
  const { useAppStore } = await import('@/stores/app')
  const route = router.currentRoute.value
  const appStore = useAppStore()
  document.title = resolveDocumentTitle(route.meta.title, appStore.siteName, route.meta.titleKey as string)
}

export function getLocale(): LocaleCode {
  const current = i18n.global.locale.value
  return isLocaleCode(current) ? current : DEFAULT_LOCALE
}

export const availableLocales = [
  { code: 'en', name: 'English', flag: '🇺🇸' },
  { code: 'zh', name: '中文', flag: '🇨🇳' }
] as const

export default i18n
