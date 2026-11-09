interface PixelGlyphProps {
  /** Index of the accent square in the 2x2 block. */
  accentIndex?: number
  className?: string
}

/** A 2x2 pixel glyph: three ink squares and one orange square. */
export function PixelGlyph({ accentIndex = 0, className = '' }: PixelGlyphProps) {
  return (
    <span
      aria-hidden="true"
      className={`pointer-events-none inline-grid h-[9px] w-[9px] grid-cols-2 gap-[2px] ${className}`}
    >
      {Array.from({ length: 4 }, (_, index) => (
        <span
          key={index}
          className={`block ${index === accentIndex ? 'bg-accent' : 'bg-ink opacity-80'}`}
        />
      ))}
    </span>
  )
}
