// Minimal, dependency-free i18n. t() reads the reactive `i18n.lang`, so any
// template that calls it re-renders when the language changes.
import en from './en'
import zhCN from './zh-CN'
import zhTW from './zh-TW'
import es from './es'
import ja from './ja'
import ko from './ko'

export type Key = keyof typeof en
/** Every translation must define every key (checked by svelte-check). */
export type Dict = Record<Key, string>

export const LANGUAGES = [
  { code: 'en', name: 'English' },
  { code: 'zh-CN', name: '简体中文' },
  { code: 'zh-TW', name: '繁體中文' },
  { code: 'es', name: 'Español' },
  { code: 'ja', name: '日本語' },
  { code: 'ko', name: '한국어' },
] as const

export type Lang = (typeof LANGUAGES)[number]['code']

const dicts: Record<Lang, Dict> = { en, 'zh-CN': zhCN, 'zh-TW': zhTW, es, ja, ko }

/** Maps a BCP 47 tag to a supported language (mirrors internal/app/lang.go). */
export function resolveLang(tag: string | undefined | null): Lang {
  const t = (tag ?? '').trim().toLowerCase().replace('_', '-')
  if (t.startsWith('zh')) return /hant|-tw$|-hk$|-mo$/.test(t) ? 'zh-TW' : 'zh-CN'
  if (t.startsWith('es')) return 'es'
  if (t.startsWith('ja')) return 'ja'
  if (t.startsWith('ko')) return 'ko'
  return 'en'
}

class I18n {
  lang = $state<Lang>(resolveLang(typeof navigator !== 'undefined' ? navigator.language : 'en'))

  set(lang: string) {
    this.lang = resolveLang(lang)
    document.documentElement.lang = this.lang
  }
}

export const i18n = new I18n()

export function t(key: Key, params?: Record<string, string | number>): string {
  const s = dicts[i18n.lang]?.[key] ?? en[key]
  if (!params) return s
  return s.replace(/\{(\w+)\}/g, (m, k) => (k in params ? String(params[k]) : m))
}

/** Translates a device state, falling back to the raw value. */
export function tState(state: string): string {
  const k = `state.${state}` as Key
  return k in en ? t(k) : state
}
