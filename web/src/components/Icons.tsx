import type { ReactNode } from 'react'

interface IconProps {
  size?: number
  className?: string
  strokeWidth?: number
}

function Svg({ size = 20, className, strokeWidth = 1.8, children }: IconProps & { children: ReactNode }) {
  return (
    <svg
      className={className}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={strokeWidth}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {children}
    </svg>
  )
}

export const Sparkle = (p: IconProps) => (
  <Svg {...p}>
    <path d="M12 3.5l1.7 4.6 4.6 1.7-4.6 1.7L12 16.1l-1.7-4.6-4.6-1.7 4.6-1.7z" />
    <path d="M18.8 15.2l.7 1.9 1.9.7-1.9.7-.7 1.9-.7-1.9-1.9-.7 1.9-.7z" />
  </Svg>
)

export const Send = (p: IconProps) => (
  <Svg {...p}>
    <path d="M21 3L10.5 13.5" />
    <path d="M21 3l-6.6 18-3.9-7.5L3 9.6z" />
  </Svg>
)

export const Stop = (p: IconProps) => (
  <Svg {...p}>
    <rect x="7" y="7" width="10" height="10" rx="2.5" fill="currentColor" stroke="none" />
  </Svg>
)

export const Plus = (p: IconProps) => (
  <Svg {...p}>
    <path d="M12 5v14M5 12h14" />
  </Svg>
)

export const Trash = (p: IconProps) => (
  <Svg {...p}>
    <path d="M4 7h16M9 7V5h6v2M6.5 7l.8 12.2a1.8 1.8 0 001.8 1.8h5.8a1.8 1.8 0 001.8-1.8L18.5 7" />
    <path d="M10 11v6M14 11v6" />
  </Svg>
)

export const Sun = (p: IconProps) => (
  <Svg {...p}>
    <circle cx="12" cy="12" r="4" />
    <path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
  </Svg>
)

export const Moon = (p: IconProps) => (
  <Svg {...p}>
    <path d="M20.5 14.2A8.5 8.5 0 019.8 3.5 8.5 8.5 0 1020.5 14.2z" />
  </Svg>
)

export const ChevronDown = (p: IconProps) => (
  <Svg {...p}>
    <path d="M6 9l6 6 6-6" />
  </Svg>
)

export const ScanText = (p: IconProps) => (
  <Svg {...p}>
    <path d="M4 8V6a2 2 0 012-2h2M16 4h2a2 2 0 012 2v2M20 16v2a2 2 0 01-2 2h-2M8 20H6a2 2 0 01-2-2v-2" />
    <path d="M8 9h8M8 12.5h8M8 16h5" />
  </Svg>
)

export const ImageIcon = (p: IconProps) => (
  <Svg {...p}>
    <rect x="3" y="4" width="18" height="16" rx="3" />
    <circle cx="8.5" cy="9.5" r="1.6" />
    <path d="M21 15.5l-4.3-4.3a2 2 0 00-2.8 0L5 20" />
  </Svg>
)

export const Download = (p: IconProps) => (
  <Svg {...p}>
    <path d="M12 3v12M7.5 10.5L12 15l4.5-4.5M5 20h14" />
  </Svg>
)

export const Copy = (p: IconProps) => (
  <Svg {...p}>
    <rect x="9" y="9" width="11" height="11" rx="2.5" />
    <path d="M15 9V6.5A2.5 2.5 0 0012.5 4h-6A2.5 2.5 0 004 6.5v6A2.5 2.5 0 006.5 15H9" />
  </Svg>
)

export const Check = (p: IconProps) => (
  <Svg {...p}>
    <path d="M5 12.5l4.5 4.5L19 7" />
  </Svg>
)

export const ArrowRight = (p: IconProps) => (
  <Svg {...p}>
    <path d="M4 12h16M14 6l6 6-6 6" />
  </Svg>
)

export const Cpu = (p: IconProps) => (
  <Svg {...p}>
    <rect x="6" y="6" width="12" height="12" rx="2.5" />
    <rect x="9.5" y="9.5" width="5" height="5" rx="1" />
    <path d="M9 2.5v3M15 2.5v3M9 18.5v3M15 18.5v3M2.5 9h3M2.5 15h3M18.5 9h3M18.5 15h3" />
  </Svg>
)

export const Bolt = (p: IconProps) => (
  <Svg {...p}>
    <path d="M13 2L4.5 13.5H11L10 22l8.5-11.5H12z" />
  </Svg>
)

export const X = (p: IconProps) => (
  <Svg {...p}>
    <path d="M6 6l12 12M18 6L6 18" />
  </Svg>
)

export const Menu = (p: IconProps) => (
  <Svg {...p}>
    <path d="M4 7h16M4 12h16M4 17h16" />
  </Svg>
)

export const Sliders = (p: IconProps) => (
  <Svg {...p}>
    <path d="M4 7h9M17 7h3M4 17h3M11 17h9" />
    <circle cx="15" cy="7" r="2" />
    <circle cx="9" cy="17" r="2" />
  </Svg>
)
