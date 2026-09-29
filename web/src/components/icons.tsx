// Inline SVG icons (currentColor): no icon font, no emoji glyphs.

type P = { className?: string }

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
