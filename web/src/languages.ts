export const languages = ['ru', 'en', 'zh', 'es', 'de', 'fr', 'pt', 'ja'] as const
export type Language = typeof languages[number]
export const languageNames: Record<Language, string> = {
  ru: 'Русский', en: 'English', zh: '简体中文', es: 'Español',
  de: 'Deutsch', fr: 'Français', pt: 'Português (Brasil)', ja: '日本語',
}

export function matchLanguage(tag: string): Language | null {
  const parts = tag.toLowerCase().replaceAll('_', '-').split(/[.@]/)[0].split('-')
  const code = parts[0]
  if (code === 'zh' && (parts.includes('hant') || (!parts.includes('hans') && parts.some(p => ['tw', 'hk', 'mo'].includes(p))))) return null
  return languages.find(language => language === code) ?? null
}

export function detectLanguage(tags: readonly string[]): Language {
  for (const tag of tags) {
    const language = matchLanguage(tag)
    if (language) return language
  }
  return 'en'
}

export function initialLanguage(info: { language: Language; mode: string; languagePreference?: Language | ''; systemLanguage?: Language | '' }, browserLanguages: readonly string[]): Language {
  if (info.languagePreference && languages.includes(info.languagePreference)) return info.languagePreference
  if (info.mode === 'desktop' && info.systemLanguage) return info.systemLanguage
  // Old API/test fixtures supplied only language. Preserve their explicit choice.
  if (info.languagePreference === undefined) return info.language
  return detectLanguage(browserLanguages)
}
