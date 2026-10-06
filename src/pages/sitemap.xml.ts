import { languages, pagePath } from '../lib/languages.mjs';
export function GET() {
  const urls = languages.map(l => `https://sipaha.github.io${pagePath(l.code, '/spk-ocular/')}`);
  return new Response(`<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">${urls.map(url => `<url><loc>${url}</loc></url>`).join('')}</urlset>`, {headers: {'Content-Type': 'application/xml'}});
}
