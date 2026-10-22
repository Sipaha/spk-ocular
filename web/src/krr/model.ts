/** UI projection of backend-owned KRR evidence; no raw report parsing here. */
export const MAX_BYTES = 1024 * 1024
export interface Allocation {state: 'number' | 'unset' | 'unavailable' | 'unknown'; value?: number}
export interface KrrRow {
 cluster?: string; kind: string; namespace: string; name: string; container: string
 current: Allocation[]; proposed: Allocation[]; severities: string[]; severity: string
 reasons: string[]; warnings: string[]; hpa: 'present' | 'absent' | 'unknown'
}
export interface KrrReport {rows: KrrRow[]; minimums?: Allocation[]; settings: {key: string; value: number | boolean}[]; errors: number}
export interface KrrSummary {id: string; origin: string; version: string; createdAt: number; expiresAt: number; rows: number}
export interface KrrStored {summary: KrrSummary; report: KrrReport}
export interface KrrRequest {command: 'import' | 'status' | 'get' | 'forget' | 'review' | 'start' | 'cancel'; provider?: string; target?: string; options?: KrrRunOptions; review?: KrrReview; id?: string; version?: string; data?: string}
export interface KrrResponse {reports: KrrSummary[]; selected?: KrrStored; runs?: KrrRun[]; review?: KrrReview; recovery?: KrrRecovery[]; recoveryUnknown?: boolean}

export interface KrrRunOptions {
 namespace: string; serviceAccount: string; source: string; label: string; cluster: string
 singleCluster: boolean; reviewedAccount: boolean; noSourceAuth: boolean
}
export interface KrrReview {
 options: KrrRunOptions; expect: string; configRev: string; image: string
 namespaceUID: string; serviceAccountUID: string; name: string; deadlineSeconds: number
}
export interface KrrRun {
 id: string; state: 'running' | 'cancelling' | 'succeeded' | 'failed' | 'cancelled'
 reportID?: string; cleanupPending: boolean; createdAt: number; expiresAt: number
 provider: string; target: string; namespace: string; name: string
}
export interface KrrRecovery {provider: string; target: string; namespace: string; name: string}
