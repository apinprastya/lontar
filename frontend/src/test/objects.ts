import type { DbObject } from '../features/objects/types'

/** A DbObject for tests (a function public.f() unless given). */
export function makeObject(over: Partial<DbObject> = {}): DbObject {
  return {
    schema: 'public',
    kind: 'function',
    name: 'f',
    signature: '',
    detail: '',
    table: '',
    id: '1',
    properties: [],
    ...over,
  }
}
