// UI strings, Russian and English. The language comes from the system
// locale via AppInfo.language (Go reads the gettext environment).

const en = {
  'app.loading': 'Loading…',
  'app.loadFailed': 'Could not load: {error}',
  'app.retry': 'Retry',
  'sidebar.filter': 'Filter',
  'sidebar.filterHint': 'Filter targets (/)',
  'sidebar.empty': 'Nothing found',
  'sidebar.noTargets.kubernetes': 'No contexts. Ocular reads KUBECONFIG, else ~/.kube/config, plus other kubeconfig files in ~/.kube.',
  'sidebar.problems': 'Could not read {count} file(s)',
  'sidebar.providerError': 'Discovery failed: {error}',
  'target.current': 'current',
  'target.currentHint': 'current-context in kubeconfig',
  'main.pick': 'Pick a context on the left',
  'main.pickHint': '↑ ↓ to move, Enter to open, / to filter',
  'detail.context': 'Context',
  'detail.cluster': 'Cluster',
  'detail.server': 'Server',
  'detail.user': 'User',
  'detail.auth': 'Authentication',
  'detail.namespace': 'Default namespace',
  'detail.file': 'Kubeconfig',
  'status.desktop': 'desktop',
  'status.browser': 'browser',
  'error.selectFailed': 'Could not select: {error}',
} as const

export type MessageKey = keyof typeof en

const ru: Record<MessageKey, string> = {
  'app.loading': 'Загрузка…',
  'app.loadFailed': 'Не удалось загрузить: {error}',
  'app.retry': 'Повторить',
  'sidebar.filter': 'Фильтр',
  'sidebar.filterHint': 'Фильтр (/)',
  'sidebar.empty': 'Ничего не найдено',
  'sidebar.noTargets.kubernetes': 'Нет contexts. Ocular читает KUBECONFIG, иначе ~/.kube/config, и другие kubeconfig-файлы в ~/.kube.',
  'sidebar.problems': 'Не удалось прочитать файлов: {count}',
  'sidebar.providerError': 'Ошибка обнаружения: {error}',
  'target.current': 'текущий',
  'target.currentHint': 'current-context в kubeconfig',
  'main.pick': 'Выберите context слева',
  'main.pickHint': '↑ ↓ — перемещение, Enter — открыть, / — фильтр',
  'detail.context': 'Context',
  'detail.cluster': 'Кластер',
  'detail.server': 'Сервер',
  'detail.user': 'Пользователь',
  'detail.auth': 'Аутентификация',
  'detail.namespace': 'Namespace по умолчанию',
  'detail.file': 'Kubeconfig',
  'status.desktop': 'desktop',
  'status.browser': 'браузер',
  'error.selectFailed': 'Не удалось выбрать: {error}',
}

const dicts = { en, ru }
export type Language = keyof typeof dicts

let current: Language = 'en'

export function setLanguage(lang: Language) {
  current = dicts[lang] ? lang : 'en'
  document.documentElement.lang = current
}

export function t(key: MessageKey, vars?: Record<string, string | number>): string {
  let s: string = dicts[current][key] ?? en[key]
  if (vars) for (const [k, v] of Object.entries(vars)) s = s.replaceAll(`{${k}}`, String(v))
  return s
}

/** Detail rows use stable keys from Go; unknown keys show as-is. */
export function detailLabel(key: string): string {
  const k = `detail.${key}` as MessageKey
  return k in en ? t(k) : key
}

export const _dicts = dicts // tests
