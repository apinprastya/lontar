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

export function LockIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <rect x="3" y="7" width="10" height="7.5" rx="1.5" fill="currentColor" />
      <path d="M5.5 7V5a2.5 2.5 0 0 1 5 0v2" fill="none" stroke="currentColor" strokeWidth="1.5" />
    </Svg>
  )
}

export function ShieldIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M8 1.5l5.5 2v4c0 3.5-2.4 6-5.5 7-3.1-1-5.5-3.5-5.5-7v-4z" fill="none" stroke="currentColor" strokeWidth="1.3" />
      <path d="M5.5 8l1.8 1.8L10.8 6" fill="none" stroke="currentColor" strokeWidth="1.3" />
    </Svg>
  )
}

export function LinkIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <g fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round">
        <path d="M6.5 9.5l3-3" />
        <path d="M8 4.5l1.2-1.2a2.5 2.5 0 0 1 3.5 3.5L11.5 8" />
        <path d="M8 11.5l-1.2 1.2a2.5 2.5 0 0 1-3.5-3.5L4.5 8" />
      </g>
    </Svg>
  )
}

/** Up/down chevrons: a control that opens a list to choose from. */
export function ChevronIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M5 6l3-3 3 3M5 10l3 3 3-3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" fill="none" />
    </Svg>
  )
}

/* ---- Toolbar icons (1.4px strokes, round caps, like the rest) ------------- */

const stroke = { fill: 'none', stroke: 'currentColor', strokeWidth: 1.4, strokeLinecap: 'round', strokeLinejoin: 'round' } as const

/** A new SQL query: a page with a plus. */
export function NewQueryIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M9 1.5H4a1.5 1.5 0 0 0-1.5 1.5v10A1.5 1.5 0 0 0 4 14.5h4M9 1.5l3.5 3.5v3M9 1.5V5h3.5" {...stroke} />
      <path d="M12 10.5v4M10 12.5h4" {...stroke} />
    </Svg>
  )
}

/** Quick open: a magnifier. */
export function SearchIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <circle cx="7" cy="7" r="4.5" {...stroke} />
      <path d="M10.5 10.5l3.5 3.5" {...stroke} />
    </Svg>
  )
}

export function FilterIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M2 3h12l-4.5 5.5v4.5l-3 1.5v-6z" {...stroke} />
    </Svg>
  )
}

/** Row detail: a window with its right side panel. */
export function PanelRightIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <rect x="1.5" y="2.5" width="13" height="11" rx="1.5" {...stroke} />
      <path d="M10 2.5v11M11.8 5.5h1M11.8 7.5h1" {...stroke} />
    </Svg>
  )
}

/** Insert row: a row with a plus. */
export function RowPlusIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <rect x="1.5" y="2.5" width="13" height="5" rx="1" {...stroke} />
      <path d="M8 10v5M5.5 12.5h5" {...stroke} />
    </Svg>
  )
}

/** Export: an arrow leaving a tray. */
export function ExportIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M8 10V1.5M5 4.5l3-3 3 3" {...stroke} />
      <path d="M2.5 9.5v3a1.5 1.5 0 0 0 1.5 1.5h8a1.5 1.5 0 0 0 1.5-1.5v-3" {...stroke} />
    </Svg>
  )
}

/** Import: an arrow entering a tray. */
export function ImportIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M8 1.5V10M5 7l3 3 3-3" {...stroke} />
      <path d="M2.5 9.5v3a1.5 1.5 0 0 0 1.5 1.5h8a1.5 1.5 0 0 0 1.5-1.5v-3" {...stroke} />
    </Svg>
  )
}

/** Disconnect: an unplugged cable. */
export function UnplugIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M2 14l2.5-2.5M14 2l-2.5 2.5" {...stroke} />
      <path d="M4.5 7.5l4 4-1 1a2.8 2.8 0 0 1-4-4z" {...stroke} />
      <path d="M11.5 8.5l-4-4 1-1a2.8 2.8 0 0 1 4 4z" {...stroke} />
    </Svg>
  )
}

export function PlayIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M4.5 2.5v11l9-5.5z" fill="currentColor" stroke="currentColor" strokeWidth="1" strokeLinejoin="round" />
    </Svg>
  )
}

/** Run all: two play marks. */
export function PlayAllIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M2 3v10l6-5z" fill="currentColor" stroke="currentColor" strokeWidth="1" strokeLinejoin="round" />
      <path d="M8.5 3v10l6-5z" fill="currentColor" stroke="currentColor" strokeWidth="1" strokeLinejoin="round" />
    </Svg>
  )
}

export function StopIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <rect x="3.5" y="3.5" width="9" height="9" rx="1.5" fill="currentColor" />
    </Svg>
  )
}

/** History: a clock with a back arrow. */
export function HistoryIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M2.5 8a5.5 5.5 0 1 0 1.6-3.9M2.5 2.5v2.5H5" {...stroke} />
      <path d="M8 5v3l2 1.5" {...stroke} />
    </Svg>
  )
}
