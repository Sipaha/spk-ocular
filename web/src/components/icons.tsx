// Inline SVG icons (currentColor): no icon font, no emoji glyphs.

type P = { className?: string }

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
  provider === 'kubernetes' ? <HelmWheelIcon className={className} /> : <LayersIcon className={className} />

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
