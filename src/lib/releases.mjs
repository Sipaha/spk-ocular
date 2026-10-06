export const REPO = 'https://github.com/Sipaha/spk-ocular';
export const RELEASES = `${REPO}/releases`;
export const API = 'https://api.github.com/repos/Sipaha/spk-ocular/releases/latest';
const packageName = /^spk-ocular(_|\-browser_)(\d+\.\d+\.\d+)_(linux|windows|darwin)_(amd64|arm64)\.(deb|rpm|msi|dmg|zip|tar\.gz)$/;
function validURL(value) {
  if (typeof value !== 'string') return false;
  try { const u = new URL(value); return u.origin === 'https://github.com' && u.pathname.startsWith('/Sipaha/spk-ocular/releases/download/') && !u.username && !u.password; } catch { return false; }
}
/** Accept only this repository's real stable-release assets and matching versions. */
export function parseRelease(value) {
  if (!value || typeof value !== 'object' || value.draft || value.prerelease || !/^v?\d+\.\d+\.\d+$/.test(value.tag_name) || !Array.isArray(value.assets)) return null;
  const version = value.tag_name.replace(/^v/, '');
  const assets = value.assets.filter(a => a && typeof a.name === 'string' && validURL(a.browser_download_url));
  const files = [];
  const seen = new Set();
  for (const asset of assets) {
    const m = packageName.exec(asset.name);
    if (!m || m[2] !== version || seen.has(asset.name)) continue;
    const [os, arch, format] = m.slice(3);
    const browser = m[1] === '-browser_';
    if (!(os === 'linux' ? ['deb', 'rpm', 'tar.gz'] : os === 'windows' ? ['msi', 'zip'] : ['dmg', 'tar.gz']).includes(format)) continue;
    if (browser && !['zip', 'tar.gz'].includes(format)) continue;
    seen.add(asset.name);
    const checksum = assets.find(a => a.name === asset.name + '.sha256');
    files.push({ name: asset.name, url: asset.browser_download_url, os, arch, format, browser, checksum: checksum?.browser_download_url, size: Number.isFinite(asset.size) && asset.size > 0 ? asset.size : 0 });
  }
  // GitHub asset order is not a product recommendation: installers come first.
  const rank = file => file.browser ? 2 : ['deb', 'rpm', 'msi', 'dmg'].includes(file.format) ? 0 : 1;
  files.sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
  return { version, files };
}
/** OS hints never silently choose a processor architecture. */
export function detectOS(ua, touchPoints = 0) {
  if (/Android|iPhone|iPad|iPod|Mobile/i.test(ua) || (/Macintosh/.test(ua) && touchPoints > 1)) return '';
  if (/Windows/.test(ua)) return 'windows';
  if (/Macintosh|Mac OS X/.test(ua)) return 'darwin';
  if (/Linux|X11/.test(ua) && !/CrOS/.test(ua)) return 'linux';
  return '';
}
