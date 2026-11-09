import type { ButtonHTMLAttributes } from 'react'

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'success'
type Size = 'sm' | 'md'

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant
  size?: Size
}

const base =
  'btn-chamfer inline-flex items-center justify-center gap-2 rounded-none border font-medium transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-45'

const variants: Record<Variant, string> = {
  primary: 'border-ink bg-ink text-[#f8f8f8] hover:border-black hover:bg-black',
  secondary: 'border-ink bg-transparent text-ink hover:bg-black/[0.05]',
  ghost: 'border-transparent text-dim hover:bg-black/[0.05] hover:text-ink',
  danger: 'border-bad bg-transparent text-bad hover:bg-bad-soft',
  success: 'border-ok bg-transparent text-ok hover:bg-ok-soft',
}

const sizes: Record<Size, string> = {
  sm: 'px-2.5 py-1.5 text-xs',
  md: 'px-3.5 py-2 text-sm',
}

export function Button({
  variant = 'secondary',
  size = 'md',
  className = '',
  type = 'button',
  ...rest
}: ButtonProps) {
  return <button type={type} className={`${base} ${variants[variant]} ${sizes[size]} ${className}`} {...rest} />
}
