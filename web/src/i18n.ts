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
  'detail.defaultNamespace': 'Default namespace',
  'detail.namespace': 'Namespace',
  'detail.file': 'Kubeconfig',
  'status.desktop': 'desktop',
  'status.browser': 'browser',
  'error.selectFailed': 'Could not select: {error}',
  'nav.overview': 'Overview',
  'table.filter': 'Filter (/)',
  'table.filterLabel': 'Filter rows',
  'table.empty': 'No objects',
  'scope.all': 'All namespaces',
  'scope.label': 'Namespace',
  'scope.type': 'Type a namespace',
  'scope.cannotList': 'Cannot list namespaces: {error}',
  'status.error': 'Cannot show',
  'status.stale': 'Connection lost, showing last known state',
  'class.forbidden': 'access denied',
  'class.unauthorized': 'not authenticated',
  'class.unavailable': 'cluster unavailable',
  'class.not_found': 'not found',
  'class.unsupported': 'not supported',
  'class.gone': 'gone',
  'class.internal': 'internal error',
  'drawer.back': 'Back',
  'drawer.close': 'Close',
  'drawer.details': 'Details',
  'drawer.yaml': 'YAML',
  'drawer.health': 'Health',
  'drawer.related': 'Related',
  'drawer.relationsPartial': 'Some relations could not be loaded: {error}',
  'drawer.relationsTruncated': 'Only the first 200 related objects are shown.',
  'drawer.events': 'Events',
  'drawer.noEvents': 'No events',
  'rel.owner': 'Owned by',
  'rel.owns': 'Owns',
  'rel.selects': 'Selects',
  'rel.routes-to': 'Routes to',
  'rel.runs-on': 'Runs on',
  'detail.kind': 'Kind',
  'detail.created': 'Created',
  'detail.labels': 'Labels',
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
  'detail.defaultNamespace': 'Namespace по умолчанию',
  'detail.namespace': 'Namespace',
  'detail.file': 'Kubeconfig',
  'status.desktop': 'desktop',
  'status.browser': 'браузер',
  'error.selectFailed': 'Не удалось выбрать: {error}',
  'nav.overview': 'Обзор',
  'table.filter': 'Фильтр (/)',
  'table.filterLabel': 'Фильтр строк',
  'table.empty': 'Объектов нет',
  'scope.all': 'Все namespaces',
  'scope.label': 'Namespace',
  'scope.type': 'Введите namespace',
  'scope.cannotList': 'Нельзя получить список namespaces: {error}',
  'status.error': 'Не удаётся показать',
  'status.stale': 'Связь потеряна, показано последнее известное',
  'class.forbidden': 'нет доступа',
  'class.unauthorized': 'не аутентифицирован',
  'class.unavailable': 'кластер недоступен',
  'class.not_found': 'не найдено',
  'class.unsupported': 'не поддерживается',
  'class.gone': 'больше не существует',
  'class.internal': 'внутренняя ошибка',
  'drawer.back': 'Назад',
  'drawer.close': 'Закрыть',
  'drawer.details': 'Детали',
  'drawer.yaml': 'YAML',
  'drawer.health': 'Состояние',
  'drawer.related': 'Связи',
  'drawer.relationsPartial': 'Часть связей не загрузилась: {error}',
  'drawer.relationsTruncated': 'Показаны первые 200 связанных объектов.',
  'drawer.events': 'События',
  'drawer.noEvents': 'Событий нет',
  'rel.owner': 'Владелец',
  'rel.owns': 'Владеет',
  'rel.selects': 'Выбирает',
  'rel.routes-to': 'Направляет на',
  'rel.runs-on': 'Запущен на',
  'detail.kind': 'Kind',
  'detail.created': 'Создан',
  'detail.labels': 'Labels',
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

/** Error class (API code) label; unknown classes show as-is. */
export function classLabel(cls: string): string {
  const k = `class.${cls}` as MessageKey
  return k in en ? t(k) : cls
}

/** Relation type label; unknown types show as-is. */
export function relationLabel(type: string): string {
  const k = `rel.${type}` as MessageKey
  return k in en ? t(k) : type
}
