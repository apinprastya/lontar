/** Small inline icons (original artwork, 16×16, currentColor). */

interface IconProps {
  className?: string
  title?: string
}

function Svg({ className, title, children }: IconProps & { children: React.ReactNode }) {
  return (
    <svg className={className ?? 'icon'} viewBox="0 0 16 16" width="14" height="14" aria-hidden={title ? undefined : true} role={title ? 'img' : undefined}>
      {title && <title>{title}</title>}
      {children}
    </svg>
  )
}

export function TableIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <rect x="1.5" y="2.5" width="13" height="11" rx="1.5" fill="none" stroke="currentColor" />
      <path d="M1.5 6h13M6 6v7.5" stroke="currentColor" fill="none" />
    </Svg>
  )
}

export function ViewIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M1 8s2.5-4.5 7-4.5S15 8 15 8s-2.5 4.5-7 4.5S1 8 1 8z" fill="none" stroke="currentColor" />
      <circle cx="8" cy="8" r="2" fill="currentColor" />
    </Svg>
  )
}

export function MatViewIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M1 8s2.5-4.5 7-4.5S15 8 15 8s-2.5 4.5-7 4.5S1 8 1 8z" fill="none" stroke="currentColor" />
      <rect x="6" y="6" width="4" height="4" fill="currentColor" />
    </Svg>
  )
}

export function RefreshIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M13 8a5 5 0 1 1-1.5-3.6M13 2.5V5h-2.5" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
    </Svg>
  )
}

export function KeyIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <circle cx="5" cy="8" r="3" fill="none" stroke="currentColor" strokeWidth="1.4" />
      <path d="M8 8h7m-2 0v2.5M11 8v1.5" stroke="currentColor" strokeWidth="1.4" fill="none" />
    </Svg>
  )
}
