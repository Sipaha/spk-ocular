import { describe, expect, it } from 'vitest'
import { ancestorsOf, cleanDirectoryPath, fileTreeRows, type Directories } from './treeModel'

const directories: Directories = {
  '/': { entries: [{ name: 'README.md', directory: false, symlink: false }, { name: 'etc', directory: true, symlink: false }, { name: 'app', directory: true, symlink: false }] },
  '/etc': { entries: [{ name: 'config.yaml', directory: false, symlink: false }, { name: 'conf.d', directory: true, symlink: false }] },
  '/etc/conf.d': { entries: [{ name: 'server.conf', directory: false, symlink: false }] },
  '/app': { entries: [{ name: 'current', directory: true, symlink: true }] },
}

describe('filesystem tree', () => {
  it('keeps sibling branches visible with nested paths and stable depth', () => {
    const rows = fileTreeRows(directories, new Set(['/', '/app', '/etc', '/etc/conf.d']))
    expect(rows.map(row => [row.path, row.depth])).toEqual([
      ['/', 0], ['/app', 1], ['/app/current', 2], ['/etc', 1], ['/etc/conf.d', 2], ['/etc/conf.d/server.conf', 3], ['/etc/config.yaml', 2], ['/README.md', 1],
    ])
    expect(rows.find(row => row.path === '/app/current')?.symlink).toBe(true)
  })
  it('hides collapsed descendants without removing siblings or cached documents', () => {
    const rows = fileTreeRows(directories, new Set(['/', '/app']))
    expect(rows.map(row => row.path)).toEqual(['/', '/app', '/app/current', '/etc', '/README.md'])
  })
  it('distinguishes failed/loading directory reads from an empty folder', () => {
    const rows = fileTreeRows({ '/': { loading: true, error: 'permission denied' } }, new Set(['/']))
    expect(rows[0]).toMatchObject({ path: '/', loading: true, error: 'permission denied', expanded: true })
  })
  it('normalizes absolute navigation and reveals the complete ancestor chain', () => {
    expect(cleanDirectoryPath('/etc/../app/./config/')).toBe('/app/config')
    expect(cleanDirectoryPath('etc')).toBeNull()
    expect(ancestorsOf('/etc/conf.d')).toEqual(['/', '/etc', '/etc/conf.d'])
  })
})

it('adds exactly one loading child on the first load, and retains cached children during Refresh', () => {
 const first=fileTreeRows({'/':{loading:true}},new Set(['/']))
 expect(first.filter(row=>row.placeholder)).toHaveLength(1)
 expect(first[1]).toMatchObject({parent:'/',depth:1,loading:true,placeholder:true})
 const refreshed=fileTreeRows({'/':{...directories['/'],loading:true,refreshing:true}},new Set(['/']))
 expect(refreshed.some(row=>row.placeholder)).toBe(false)
 expect(refreshed.map(row=>row.path)).toEqual(['/','/app','/etc','/README.md'])
})

it('explicit Refresh without a cached list uses the parent indicator instead of a first-open child', () => {
 const rows=fileTreeRows({'/':{loading:true,refreshing:true}},new Set(['/']))
 expect(rows).toHaveLength(1)
 expect(rows[0].loading).toBe(true)
})

it('replaces loading with an empty placeholder at the same position and reports cached counts', () => {
 const pending=fileTreeRows({'/':{loading:true}},new Set(['/']))
 const empty=fileTreeRows({'/':{entries:[]}},new Set(['/']))
 expect(empty).toHaveLength(pending.length)
 expect(empty[1]).toMatchObject({path:pending[1].path,depth:pending[1].depth,placeholder:true,empty:true,loading:false})
 expect(empty[0].count).toBe(0)
 const collapsed=fileTreeRows(directories,new Set(['/']))
 expect(collapsed.find(row=>row.path==='/etc')?.count).toBe(2)
})
