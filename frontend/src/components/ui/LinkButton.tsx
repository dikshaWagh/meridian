import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Icon } from './Icon'

interface LinkButtonProps {
  to: string
  children: ReactNode
  variant?: 'primary' | 'secondary'
  className?: string
  arrow?: boolean
}

const variants = {
  primary: 'border-ink bg-ink text-[#f8f8f8] hover:border-black hover:bg-black',
  secondary: 'border-ink bg-transparent text-ink hover:bg-black/[0.05]',
}

export function LinkButton({
  to,
  children,
  variant = 'primary',
  className = '',
  arrow = false,
}: LinkButtonProps) {
  return (
    <Link
      to={to}
      className={`btn-chamfer inline-flex items-center justify-center gap-2 rounded-none border px-4 py-2.5 text-sm font-medium transition-colors duration-150 ${variants[variant]} ${className}`}
    >
      {children}
      {arrow ? <Icon name="arrow-right" className="h-4 w-4" /> : null}
    </Link>
  )
}
