/** Provider group names stay technical; labels are localized by their callers. */
const paths: Record<string, string> = {
  Favorites: 'm12 3 2.8 5.7 6.3.9-4.55 4.4 1.07 6.25L12 17.3l-5.62 2.95 1.07-6.25L2.9 9.6l6.3-.9Z',
  Workloads: 'm12 3 8 4.5v9L12 21l-8-4.5v-9ZM4 7.5l8 4.5 8-4.5M12 12v9M8 5.3l8 4.5v4',
  Network: 'M9 5a3 3 0 1 0 6 0 3 3 0 1 0-6 0ZM2 19a3 3 0 1 0 6 0 3 3 0 1 0-6 0ZM16 19a3 3 0 1 0 6 0 3 3 0 1 0-6 0ZM12 8v4M5 16v-4h14v4',
  Config: 'M4 6h7m5 0h4M4 12h2m5 0h9M4 18h10m5 0h1M11 3h5v6h-5ZM6 9h5v6H6ZM14 15h5v6h-5Z',
  Storage: 'M4 6c0-4 16-4 16 0s-16 4-16 0Zm0 0v12c0 4 16 4 16 0V6M4 12c0 4 16 4 16 0',
  Cluster: 'M3 3h18v7H3ZM3 14h18v7H3ZM7 6.5h.01M7 17.5h.01M11 6.5h6m-6 11h6',
  'Access Control': 'm12 3 8 3v6c0 5-8 9-8 9s-8-4-8-9V6ZM9 12l2 2 4-4',
  'API groups': 'M3 3h7v7H3ZM14 3h7v7h-7ZM3 14h7v7H3ZM14 14h7v7h-7Z',
  Health: 'M3 12h4l3-8 4 16 3-8h4',
  Helm: 'M12 3v5m0 8v5M3 12h5m8 0h5M5.6 5.6l3.5 3.5m5.8 5.8 3.5 3.5M5.6 18.4l3.5-3.5m5.8-5.8 3.5-3.5M5 12a7 7 0 1 0 14 0 7 7 0 1 0-14 0ZM9 12a3 3 0 1 0 6 0 3 3 0 1 0-6 0Z',
  Compose: 'm12 3 9 5-9 5-9-5ZM3 12l9 5 9-5M3 16l9 5 9-5',
  Engine: 'M6 6h12v12H6ZM9 9h6v6H9ZM9 2v4m6-4v4M9 18v4m6-4v4M2 9h4m-4 6h4m12-6h4m-4 6h4',
  Projects: 'M3 7V4h6l3 3h9v13H3ZM3 10h18',
}
const aliases: Record<string, string> = { Networks: 'Network', Volumes: 'Storage', Images: 'Compose' }
const fallback = 'm12 3 9 5-9 5-9-5ZM3 12l9 5 9-5M3 16l9 5 9-5'

/** Small stroke glyphs for known sections, with one neutral unknown-group glyph. */
export function ResourceGroupIcon({ group, className = 'resource-group-icon' }: { group: string; className?: string }) {
  const key = Object.hasOwn(aliases, group) ? aliases[group] : group
  const known = Object.hasOwn(paths, key)
  return <svg data-resource-group-icon={known ? key : 'default'} aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" className={className}><path d={known ? paths[key] : fallback} /></svg>
}
