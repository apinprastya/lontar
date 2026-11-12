import type { ButtonHTMLAttributes, ReactNode } from 'react'

export interface ToolButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'title'> {
  /** Accessible name (screen readers, tests); also the tooltip unless title is given. */
  label: string
  /** Tooltip, e.g. with the keyboard shortcut. */
  title?: string
  icon: ReactNode
  /** Optional visible text next to the icon (main actions). */
  text?: string
}

/** A toolbar button showing an icon, with its name in the tooltip. */
export default function ToolButton({ label, title, icon, text, className, ...rest }: ToolButtonProps) {
  return (
    <button type="button" {...rest} className={`tool-button${text ? ' with-text' : ''}${className ? ` ${className}` : ''}`} aria-label={label} title={title ?? label}>
      {icon}
      {text && <span className="tool-button-text">{text}</span>}
    </button>
  )
}
