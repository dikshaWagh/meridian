// BrandMark is the compass in a circle used by the header, the landing page
// and the chat empty state. Presentation only.
export function BrandMark({ size = 22 }: { size?: number }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true" focusable="false">
      <circle cx="12" cy="12" r="10.5" fill="none" stroke="currentColor" strokeWidth="1.5" />
      <path d="M12 3 L13.6 12 L12 21 L10.4 12 Z" fill="currentColor" />
      <path d="M3 12 L12 10.4 L21 12 L12 13.6 Z" fill="currentColor" opacity="0.45" />
    </svg>
  )
}
