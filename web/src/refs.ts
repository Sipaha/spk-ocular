import type { Ref } from './api/types'

/** How an object is shown: its title when the provider gives one (a
 * container's name), else its name (the key). Display only. */
export const refTitle = (r: Pick<Ref, 'name' | 'title'>) => r.title || r.name
