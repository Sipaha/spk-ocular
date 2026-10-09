import type { FilesResponse } from '../api/types'

export type FileEntry = NonNullable<FilesResponse['entries']>[number]
export interface DirectoryState {
  entries?: FileEntry[]
  loading?: boolean
  refreshing?: boolean
  error?: string
}
export interface FileTreeRow extends FileEntry {
  path: string
  parent: string | null
  depth: number
  expanded: boolean
  loading: boolean
  error?: string
  placeholder?: boolean
  empty?: boolean
  count?: number
}
export type Directories = Record<string, DirectoryState>

export const childPath = (parent: string, name: string) => `${parent === '/' ? '' : parent}/${name}`
export const ancestorsOf = (path: string): string[] => {
  const ancestors = ['/']
  const parts = path.split('/').filter(Boolean)
  for (let index = 0; index < parts.length; index++) ancestors.push('/' + parts.slice(0, index + 1).join('/'))
  return ancestors
}

export function cleanDirectoryPath(value: string): string | null {
  if (!value.startsWith('/') || value.includes('\0')) return null
  const parts: string[] = []
  for (const part of value.split('/')) {
    if (!part || part === '.') continue
    if (part === '..') parts.pop()
    else parts.push(part)
  }
  return '/' + parts.join('/')
}

/** Flatten only expanded branches. Stable full paths own virtual row identity. */
export function fileTreeRows(directories: Directories, expanded: ReadonlySet<string>): FileTreeRow[] {
  const rows: FileTreeRow[] = []
  const append = (entry: FileEntry, path: string, parent: string | null, depth: number) => {
    const state = directories[path]
    const open = entry.directory && expanded.has(path)
    rows.push({ ...entry, path, parent, depth, expanded: open, loading: !!state?.loading, error: state?.error, count: entry.directory ? state?.entries?.length : undefined })
    if (!open) return
    if (state?.loading && !state.refreshing && state.entries === undefined) {
      rows.push({name:'', path:path+'\0placeholder',parent:path,depth:depth+1,directory:false,symlink:false,expanded:false,loading:true,placeholder:true})
      return
    }
    if (!state?.entries) return
    if (state.entries.length === 0) {
      rows.push({name:'',path:path+'\0placeholder',parent:path,depth:depth+1,directory:false,symlink:false,expanded:false,loading:false,placeholder:true,empty:true})
      return
    }
    const children = [...state.entries].sort((a, b) => Number(b.directory) - Number(a.directory) || a.name.localeCompare(b.name))
    for (const child of children) append(child, childPath(path, child.name), path, depth + 1)
  }
  append({ name: '/', directory: true, symlink: false }, '/', null, 0)
  return rows
}
