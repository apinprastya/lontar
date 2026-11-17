/** Schema objects besides tables and views (Go driver.Object). */

export type ObjectKind = 'function' | 'procedure' | 'aggregate' | 'sequence' | 'enum' | 'composite' | 'domain' | 'trigger' | 'event'

export interface ObjectProperty {
  key: string
  value: string
}

/** A function, procedure, aggregate, sequence, type, trigger or event. */
export interface DbObject {
  schema: string
  kind: ObjectKind
  name: string
  /** Argument list of a routine ("" otherwise). */
  signature: string
  /** One-line summary. */
  detail: string
  /** The table of a trigger ("" otherwise). */
  table: string
  /** Engine ID (PostgreSQL OID, MySQL specific name); may be "". */
  id: string
  properties: ObjectProperty[]
}

/** What the backend needs to find an object again (it re-checks it against a fresh listing). */
export interface ObjectRef {
  kind: ObjectKind
  name: string
  signature: string
  table: string
  id: string
}

export function objectRef(o: DbObject): ObjectRef {
  return { kind: o.kind, name: o.name, signature: o.signature, table: o.table, id: o.id }
}

/** Identity of an object within a schema, ignoring the ID (stable across DROP/CREATE). */
export function objectKey(o: Pick<DbObject, 'schema' | 'kind' | 'name' | 'signature' | 'table'>): string {
  return JSON.stringify([o.schema, o.kind, o.name, o.signature, o.table])
}

const ROUTINES: ObjectKind[] = ['function', 'procedure', 'aggregate']

/** Display name: routines with their arguments, add_one(x integer). */
export function objectLabel(o: Pick<DbObject, 'kind' | 'name' | 'signature'>): string {
  return ROUTINES.includes(o.kind) ? `${o.name}(${o.signature})` : o.name
}

export const OBJECT_KIND_LABELS: Record<ObjectKind, string> = {
  function: 'function',
  procedure: 'procedure',
  aggregate: 'aggregate',
  sequence: 'sequence',
  enum: 'enum type',
  composite: 'composite type',
  domain: 'domain',
  trigger: 'trigger',
  event: 'event',
}

/** Normalizes a binding object (nil slices arrive as null). */
export function normalizeObject(o: DbObject): DbObject {
  return { ...o, properties: o.properties ?? [] }
}
