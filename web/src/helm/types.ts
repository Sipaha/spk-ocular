import type { Diagnosis } from '../diagnosis/types'
import type { Ref, ScopeSel } from '../api/types'
export interface HelmRepository { name: string; url: string; username?: string; password?: string; hasPassword?: boolean; caFile?: string; certFile?: string; keyFile?: string; insecure?: boolean }
export interface HelmSettings { repositories: HelmRepository[]; storage: { driver: string; sqlConnection?: string }; hasSQLConnection?: boolean }
export interface HelmChartRef { repository: string; name: string; version: string }
export interface HelmChart extends HelmChartRef { description: string; appVersion: string; deprecated: boolean; values?: string; readme?: string }
export interface HelmRelease { storageNamespace?: string; rollbackRevision?: number; name: string; namespace: string; revision: number; chart: string; chartVersion: string; appVersion: string; status: string; updated: string }
export interface HelmDetail extends HelmRelease { resources: { ref?: Ref; apiVersion: string; kind: string; namespace: string; name: string }[]; values: string; computedValues: string; manifest: string; hooks: string; notes: string; history: HelmRelease[] }
export interface HelmOperation { action: 'install' | 'upgrade' | 'rollback' | 'uninstall'; namespace: string; name: string; chart: HelmChartRef; values: string; revision: number; timeoutSeconds: number; wait: boolean; disableHooks: boolean; createNamespace: boolean; keepHistory: boolean }
export interface HelmPlan { previewMode?: string; id: string; operation: HelmOperation; currentRevision: number; manifest: string; previousManifest: string; notes: string; warnings: string[]; expires: string }
export interface HelmResult { outcome: string; message: string; release?: HelmRelease }
export interface HelmRequest { readOwner?: string; otherRevision?: number; hookIndex?: number; controllerNamespace?: string; command: 'diagnose' | 'compare' | 'observe-hook' | 'flux-context' | 'settings' | 'save-settings' | 'catalog' | 'chart' | 'list' | 'detail' | 'prepare' | 'run' | 'forget'; provider?: string; target?: string; configRev?: string; scope?: ScopeSel; namespace?: string; name?: string; revision?: number; repository?: string; chart?: HelmChartRef; settings?: HelmSettings; operation?: HelmOperation; planId?: string }
export interface HelmResponse { readOwner?: string; diagnosis?: HelmDiagnosis; comparison?: { from:number; to:number; left:string; right:string }; currentHook?: Diagnosis; flux?: HelmFlux; problems?: string[]; configRev?: string; settings?: HelmSettings; charts?: HelmChart[]; chart?: HelmChart; releases?: HelmRelease[]; detail?: HelmDetail; plan?: HelmPlan; result?: HelmResult }

export interface HelmHook {index:number;name:string;kind:string;apiVersion:string;namespace:string;phase:string;startedAt:string;completedAt:string;events:string[];deletePolicies:string[];coverage:string}
export interface HelmDiagnosis {selected:HelmRelease;history:HelmRelease[];hooks:HelmHook[];truncated:boolean;compareFrom:number;compareTo:number;capturedAt:string}
export interface HelmFlux {candidates:{ref:Ref;state:string;source:string;conditions:Diagnosis['evidence']}[];coverage:string;capturedAt:number}
