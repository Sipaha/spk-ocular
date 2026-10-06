export interface ConfigRequest { command: string; target?: string; targetRevision?: string; contentRevision?: string; paths?: string[]; name?: string; yaml?: string; password?: string; id?: string; expect?: string }
export interface ConfigEntry { id: string; name: string; kind: 'linked' | 'stored'; path?: string; revision: string }
export interface ConfigState {
 resetRevision?: string
 targetName?: string
 targetRevision?: string
 entryId?: string
 contentRevision?: string
 yaml?: string
 initialized: boolean; encrypted: boolean; locked: boolean
 candidates: { path: string; contexts: string[]; selected: boolean; problem?: string }[]
 entries: ConfigEntry[]
}
