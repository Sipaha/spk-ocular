export const MAX_BYTES=1<<20
export type AuditCheck={id:string;scope:'resource'|'pod'|'container';container?:string;success:boolean;severity:'warning'|'danger'|'ignore';category:string}
export type AuditSubject={kind:string;namespace:string;name:string;checks:AuditCheck[]}
export type AuditReport={auditTime?:string;sourceType:'Cluster'|'Path';subjects:AuditSubject[];checks:number}
export type AuditSummary={id:string;version:string;target:string;configRev:string;createdAt:number;expiresAt:number;objects:number;checks:number}
export type AuditStored={summary:AuditSummary;report:AuditReport}
export type AuditRequest={command:'status'|'import'|'get'|'forget';id?:string;version?:string;target?:string;configRev?:string;data?:string}
export type AuditResponse={reports:AuditSummary[];selected?:AuditStored}
