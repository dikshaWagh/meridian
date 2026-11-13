import type { SVGProps } from 'react'

// Navy compass rose: the Meridian mark, as vector art.
// The navy is fixed so the mark reads the same in light and dark themes.
export function CompassMark(props: SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 240 240" aria-hidden="true" {...props}>
      <g fill="none" stroke="#0b2a8c">
        <circle cx="120" cy="120" r="110" strokeWidth="3" />
        <circle cx="120" cy="120" r="96" strokeWidth="1.2" />
        <path d="M120.00 24.00L120.00 33.00M128.37 24.37L128.06 27.85M136.67 25.46L135.63 31.37M144.85 27.27L143.94 30.65M152.83 29.79L150.78 35.43M160.57 32.99L159.09 36.17M168.00 36.86L163.50 44.66M175.06 41.36L173.06 44.23M181.71 46.46L177.85 51.06M187.88 52.12L185.41 54.59M193.54 58.29L188.94 62.15M198.64 64.94L195.77 66.94M203.14 72.00L195.34 76.50M207.01 79.43L203.83 80.91M210.21 87.17L204.57 89.22M212.73 95.15L209.35 96.06M214.54 103.33L208.63 104.37M215.63 111.63L212.15 111.94M216.00 120.00L207.00 120.00M215.63 128.37L212.15 128.06M214.54 136.67L208.63 135.63M212.73 144.85L209.35 143.94M210.21 152.83L204.57 150.78M207.01 160.57L203.83 159.09M203.14 168.00L195.34 163.50M198.64 175.06L195.77 173.06M193.54 181.71L188.94 177.85M187.88 187.88L185.41 185.41M181.71 193.54L177.85 188.94M175.06 198.64L173.06 195.77M168.00 203.14L163.50 195.34M160.57 207.01L159.09 203.83M152.83 210.21L150.78 204.57M144.85 212.73L143.94 209.35M136.67 214.54L135.63 208.63M128.37 215.63L128.06 212.15M120.00 216.00L120.00 207.00M111.63 215.63L111.94 212.15M103.33 214.54L104.37 208.63M95.15 212.73L96.06 209.35M87.17 210.21L89.22 204.57M79.43 207.01L80.91 203.83M72.00 203.14L76.50 195.34M64.94 198.64L66.94 195.77M58.29 193.54L62.15 188.94M52.12 187.88L54.59 185.41M46.46 181.71L51.06 177.85M41.36 175.06L44.23 173.06M36.86 168.00L44.66 163.50M32.99 160.57L36.17 159.09M29.79 152.83L35.43 150.78M27.27 144.85L30.65 143.94M25.46 136.67L31.37 135.63M24.37 128.37L27.85 128.06M24.00 120.00L33.00 120.00M24.37 111.63L27.85 111.94M25.46 103.33L31.37 104.37M27.27 95.15L30.65 96.06M29.79 87.17L35.43 89.22M32.99 79.43L36.17 80.91M36.86 72.00L44.66 76.50M41.36 64.94L44.23 66.94M46.46 58.29L51.06 62.15M52.12 52.12L54.59 54.59M58.29 46.46L62.15 51.06M64.94 41.36L66.94 44.23M72.00 36.86L76.50 44.66M79.43 32.99L80.91 36.17M87.17 29.79L89.22 35.43M95.15 27.27L96.06 30.65M103.33 25.46L104.37 31.37M111.63 24.37L111.94 27.85" strokeWidth="1" />
        <circle cx="120" cy="120" r="62" strokeWidth="2.4" />
        <circle cx="120" cy="120" r="54" strokeWidth="1" />
        <circle cx="120" cy="120" r="40" strokeWidth="1" />
      </g>
      <g fill="#0b2a8c"><circle cx="135.31" cy="83.04" r="1.6"/><circle cx="156.96" cy="104.69" r="1.6"/><circle cx="156.96" cy="135.31" r="1.6"/><circle cx="135.31" cy="156.96" r="1.6"/><circle cx="104.69" cy="156.96" r="1.6"/><circle cx="83.04" cy="135.31" r="1.6"/><circle cx="83.04" cy="104.69" r="1.6"/><circle cx="104.69" cy="83.04" r="1.6"/></g>
      <g>
        <path d="M120.00 120.00L109.00 120.00L120.00 50.00Z" fill="#0b2a8c"/>
        <path d="M120.00 120.00L120.00 50.00L131.00 120.00Z" fill="#fff" stroke="#0b2a8c" stroke-width="1.2" stroke-linejoin="round"/>
        <path d="M120.00 120.00L120.00 109.00L190.00 120.00Z" fill="#0b2a8c"/>
        <path d="M120.00 120.00L190.00 120.00L120.00 131.00Z" fill="#fff" stroke="#0b2a8c" stroke-width="1.2" stroke-linejoin="round"/>
        <path d="M120.00 120.00L131.00 120.00L120.00 190.00Z" fill="#0b2a8c"/>
        <path d="M120.00 120.00L120.00 190.00L109.00 120.00Z" fill="#fff" stroke="#0b2a8c" stroke-width="1.2" stroke-linejoin="round"/>
        <path d="M120.00 120.00L120.00 131.00L50.00 120.00Z" fill="#0b2a8c"/>
        <path d="M120.00 120.00L50.00 120.00L120.00 109.00Z" fill="#fff" stroke="#0b2a8c" stroke-width="1.2" stroke-linejoin="round"/>
        <path d="M120.00 120.00L114.34 114.34L152.53 87.47Z" fill="#0b2a8c"/>
        <path d="M120.00 120.00L152.53 87.47L125.66 125.66Z" fill="#fff" stroke="#0b2a8c" stroke-width="1.2" stroke-linejoin="round"/>
        <path d="M120.00 120.00L125.66 114.34L152.53 152.53Z" fill="#0b2a8c"/>
        <path d="M120.00 120.00L152.53 152.53L114.34 125.66Z" fill="#fff" stroke="#0b2a8c" stroke-width="1.2" stroke-linejoin="round"/>
        <path d="M120.00 120.00L125.66 125.66L87.47 152.53Z" fill="#0b2a8c"/>
        <path d="M120.00 120.00L87.47 152.53L114.34 114.34Z" fill="#fff" stroke="#0b2a8c" stroke-width="1.2" stroke-linejoin="round"/>
        <path d="M120.00 120.00L114.34 125.66L87.47 87.47Z" fill="#0b2a8c"/>
        <path d="M120.00 120.00L87.47 87.47L125.66 114.34Z" fill="#fff" stroke="#0b2a8c" stroke-width="1.2" stroke-linejoin="round"/>
      </g>
      <circle cx="120" cy="120" r="5" fill="#0b2a8c" />
      <g fill="#0b2a8c" fontFamily="'Century Schoolbook','New Century Schoolbook','C059','TeX Gyre Schola',Georgia,serif" fontWeight="700" fontSize="22" textAnchor="middle">
        <text x="120" y="20">N</text>
        <text x="120" y="234">S</text>
        <text x="232" y="128">E</text>
        <text x="8" y="128">W</text>
      </g>
    </svg>
  )
}
