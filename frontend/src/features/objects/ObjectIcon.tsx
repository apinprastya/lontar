import { EventIcon, FunctionIcon, SequenceIcon, TriggerIcon, TypeIcon } from '../../components/icons'
import type { ObjectKind } from './types'

/** The icon of an object kind. */
export default function ObjectIcon({ kind, className }: { kind: ObjectKind; className?: string }) {
  const cls = className ?? `icon object-icon object-icon-${kind}`
  switch (kind) {
    case 'sequence':
      return <SequenceIcon className={cls} />
    case 'enum':
    case 'composite':
    case 'domain':
      return <TypeIcon className={cls} />
    case 'trigger':
      return <TriggerIcon className={cls} />
    case 'event':
      return <EventIcon className={cls} />
    default:
      return <FunctionIcon className={cls} />
  }
}
