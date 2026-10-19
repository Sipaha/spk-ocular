// Source SVG icons; the Docker provider uses the unmodified official white mark.
import dockerMark from '../assets/docker-mark-white.svg'

type P = { className?: string }

export const GlobeIcon = ({ className }: P) => (
  <svg viewBox="0 0 24 24" className={className} aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.8">
    <circle cx="12" cy="12" r="9" />
    <ellipse cx="12" cy="12" rx="4" ry="9" />
    <path d="M3 12h18" />
  </svg>
)

export const InfoIcon = ({ className }: P) => (
  <svg viewBox="0 0 24 24" className={className} aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round">
    <circle cx="12" cy="12" r="9" />
    <path d="M12 11v6M12 7h.01" />
  </svg>
)

export const StarIcon = ({ className, filled = false }: P & { filled?: boolean }) => (
  <svg viewBox="0 0 24 24" className={className} aria-hidden="true" fill={filled ? 'currentColor' : 'none'} stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round">
    <path d="m12 3 2.8 5.7 6.3.9-4.55 4.4 1.07 6.25L12 17.3l-5.62 2.95 1.07-6.25L2.9 9.6l6.3-.9Z" />
  </svg>
)

export const CloseIcon = ({ className }: P) => (
  <svg viewBox="0 0 24 24" className={className} aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
    <path d="m5 5 14 14M19 5 5 19" />
  </svg>
)

export const EyeIcon = ({ className }: P) => (
  <svg viewBox="0 0 24 24" className={className} aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2">
    <path d="M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7S2 12 2 12Z" />
    <circle cx="12" cy="12" r="3" fill="currentColor" />
  </svg>
)

export const HelmWheelIcon = ({ className }: P) => (
  <svg viewBox="0 0 24 24" className={className} aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.8">
    <path d="M12 2.5 20.2 6.5 22 15.3l-5.6 7H7.6L2 15.3 3.8 6.5Z" />
    <circle cx="12" cy="12.5" r="2.6" />
    <path d="M12 6v3.9M12 15.1V19M6.2 9.8l3.4 1.6M14.4 13.6l3.4 1.6M6.2 15.2l3.4-1.6M14.4 11.4l3.4-1.6" />
  </svg>
)

/** A provider without an icon of its own: stacked layers. */
export const LayersIcon = ({ className }: P) => (
  <svg viewBox="0 0 24 24" className={className} aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round">
    <path d="M12 3 2.5 8 12 13l9.5-5L12 3Z" />
    <path d="m2.5 12.5 9.5 5 9.5-5M2.5 16.5l9.5 5 9.5-5" />
  </svg>
)

/** The icon of a provider's group (by provider id; presentation only). */
export const ProviderIcon = ({ provider, className }: P & { provider: string }) =>
  provider === 'kubernetes' ? <HelmWheelIcon className={className} /> : provider==='docker'||provider==='compose' ? <img src={dockerMark} alt="" aria-hidden="true" draggable={false} className={className} style={{minWidth:24,minHeight:24}}/> : <LayersIcon className={className} />

export const WarningIcon = ({ className }: P) => (
  <svg viewBox="0 0 24 24" className={className} aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2">
    <path d="M12 3 2 20h20L12 3Z" strokeLinejoin="round" />
    <path d="M12 10v4M12 17h.01" strokeLinecap="round" />
  </svg>
)

export const SearchIcon = ({ className }: P) => (
  <svg viewBox="0 0 24 24" className={className} aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2">
    <circle cx="11" cy="11" r="7" />
    <path d="m20 20-3.5-3.5" strokeLinecap="round" />
  </svg>
)

export const FolderIcon = ({ className, open = false }: P & { open?: boolean }) => (
  <svg viewBox="0 0 24 24" className={className} aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round">
    <path d="M3 7V5.5A1.5 1.5 0 0 1 4.5 4H9l2 3h8.5A1.5 1.5 0 0 1 21 8.5V10" />
    {open ? <path d="M3 7v12h15l3-9H7l-4 9" /> : <path d="M3 7v12h18V10H3" />}
  </svg>
)

export const FileIcon = ({ className }: P) => (
  <svg viewBox="0 0 24 24" className={className} aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round">
    <path d="M5 3h9l5 5v13H5Z M14 3v5h5 M8 12h8 M8 16h6" />
  </svg>
)

export const LinkArrowIcon = ({ className }: P) => (
  <svg viewBox="0 0 16 16" className={className} aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
    <path d="M4 12 12 4M5 4h7v7" />
  </svg>
)

/** A secondary window, with the direction indicating detach or return. */
export const WindowActionIcon = ({className, returning=false}: P & {returning?:boolean}) => (
  <svg viewBox="0 0 24 24" className={className} aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
    <path d="M10 4H4v16h16v-6M14 4h6v6"/>
    {returning ? <path d="m20 4-9 9m0-5v5h5"/> : <path d="m11 13 9-9"/>}
  </svg>
)

const actionPaths = {
  pin: 'M9 3h6l-1 6 4 4v2H6v-2l4-4-1-6ZM12 15v6',
  baseline: 'M4 3h10l4 4v3M14 3v5h4M4 3v18h6M13 12h7v9l-3.5-2-3.5 2Z',
  compare: 'M3 4h7v16H3ZM14 4h7v16h-7ZM5 8h3m-3 4h3m8-4h3m-1.5-1.5v3M16 15h3',
  permissions: 'M12 3 4 6v6c0 5 8 9 8 9s8-4 8-9V6ZM9 12l2 2 4-4',
  logs: 'M4 4h16v16H4ZM8 8h8M8 12h8M8 16h5',
  terminal: 'M3 4h18v16H3ZM7 9l3 3-3 3m6 0h4',
  files: 'M3 7V5h6l2 3h10v12H3ZM3 8h8',
  edit: 'm4 16 12-12 4 4L8 20H4ZM13 7l4 4',
  actions: 'M5 12h.01M12 12h.01M19 12h.01',
  scale: 'M3 14h6v6H3ZM15 14h6v6h-6ZM6 14v-4h12v4M12 10V3m-3 3 3-3 3 3',
  reconnect: 'M20 11a8 8 0 0 0-14-5L3 9M3 4v5h5M4 13a8 8 0 0 0 14 5l3-3m0 5v-5h-5',
  previous: 'M4 9a8 8 0 1 1-1 7M4 4v5h5m3-2v5l3 2',
  time: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18ZM12 7v5l3 2',
  source: 'M4 4h6v6H4ZM14 4h6v6h-6ZM9 20h6v-6H9ZM7 10v2h10v-2m-5 2v2',
  wrap: 'M4 6h16M4 11h12a4 4 0 0 1 0 8h-4m3-3-3 3 3 3M4 16h4',
  copy: 'M8 8h12v13H8ZM16 8V3H3v13h5',
  download: 'M12 3v12m-5-5 5 5 5-5M4 16v5h16v-5',
  clear: 'M4 6h16M9 6V3h6v3M6 6l1 15h10l1-15M10 10v7m4-7v7',
  regex: 'M5 16v.01M15 5v10m-4-8 8 6m-8 0 8-6',
  up: 'M12 20V4m-6 6 6-6 6 6',
  down: 'M12 4v16m-6-6 6 6 6-6',
} as const
export type ActionIconName = keyof typeof actionPaths

/** Shared toolbar glyphs with the same stroke and size as the application icon set. */
export const ActionIcon = ({name,className}:P & {name:ActionIconName}) => (
  <svg viewBox="0 0 24 24" className={className??'h-4 w-4'} aria-hidden="true" fill="none" stroke="currentColor" strokeWidth={name==='actions'?3:1.8} strokeLinecap="round" strokeLinejoin="round"><path d={actionPaths[name]}/></svg>
)

export const ImportConfigIcon = ({className}:P) => <svg viewBox="0 0 24 24" className={className} aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><path d="M8 7V3h8l5 5v13H8v-3M16 3v5h5M2 13h12m-4-4 4 4-4 4"/></svg>
export const NewConfigIcon = ({className}:P) => <svg viewBox="0 0 24 24" className={className} aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><path d="M4 21V3h11l5 5v13H4ZM15 3v5h5M8 14h8m-4-4v8"/></svg>
