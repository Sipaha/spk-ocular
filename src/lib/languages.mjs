export const languages = [
  { code: 'ru', name: 'Русский', short: 'RU', og: 'ru_RU' },
  { code: 'en', name: 'English', short: 'EN', og: 'en_US' },
  { code: 'zh', name: '简体中文', short: '中文', og: 'zh_CN' },
  { code: 'es', name: 'Español', short: 'ES', og: 'es_ES' },
  { code: 'de', name: 'Deutsch', short: 'DE', og: 'de_DE' },
  { code: 'fr', name: 'Français', short: 'FR', og: 'fr_FR' },
  { code: 'pt', name: 'Português', short: 'PT', og: 'pt_BR' },
  { code: 'ja', name: '日本語', short: '日本語', og: 'ja_JP' },
];
export const pagePath = (code, base) => base + (code === 'ru' ? '' : `${code}/`);

// Self-contained: also serialized into the static head, before page rendering.
export function languageTarget(input) {
  const supported = input.codes;
  const normalize = (value) => {
    if (typeof value !== 'string') return null;
    const lower = value.toLowerCase();
    if (/^zh-(hant|tw|hk|mo)(-|$)/.test(lower)) return null;
    const code = lower.split(/[-_]/)[0];
    return supported.includes(code) ? code : null;
  };
  const path = input.path.endsWith('/') ? input.path : input.path + '/';
  if (path !== input.base || input.webdriver || /bot|crawl|spider|lighthouse/i.test(input.userAgent)) return null;
  if (new URLSearchParams(input.search).get("lang") === "ru") return null;
  const stored = supported.includes(input.stored) ? input.stored : null;
  const code = stored || input.languages.map(normalize).find(Boolean) || 'en';
  return code === 'ru' ? null : input.base + code + '/' + input.search + input.hash;
}
export function languageBootstrap({ base, storageKey, codes }) {
  return `(()=>{let stored=null;try{stored=localStorage.getItem(${JSON.stringify(storageKey)})}catch{}const to=(${languageTarget.toString()})({base:${JSON.stringify(base)},codes:${JSON.stringify(codes)},stored,path:location.pathname,search:location.search,hash:location.hash,languages:navigator.languages||[navigator.language],userAgent:navigator.userAgent,webdriver:navigator.webdriver});if(to)location.replace(to)})();`;
}
