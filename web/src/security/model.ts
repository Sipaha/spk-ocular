import type { SecurityFinding } from '../api/types'
import { t, type MessageKey } from '../i18n'
export const rules = ['privileged','host-access','host-path','escalation','root','writable-root','capabilities','seccomp','token-mount','default-account','network-ingress','network-egress','rbac-wildcard','rbac-secrets','rbac-escalation','rbac-exec'] as const
export const severities = ['critical','high','medium','low','unknown'] as const
export function findingTitle(f: SecurityFinding) {
 return (f.category === 'cve' || f.audit) ? f.title || f.rule : (rules as readonly string[]).includes(f.rule) ? t(`security.rule.${f.rule}` as MessageKey) : f.rule
}
export function recommendation(f: SecurityFinding) {
 return (rules as readonly string[]).includes(f.rule) && f.category !== 'cve' && !f.audit ? t(`security.fix.${f.rule}` as MessageKey) : ''
}
export function filterFindings(findings: SecurityFinding[], query: string, category: string, severity: string, result = '') {
 const words=query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean)
 return findings.filter(f=>(!category || f.category===category) && (!severity || f.severity===severity) && (!result || f.audit?.result===result) && words.every(word=>[f.ref.name,f.ref.title,f.ref.scope,f.kindTitle,f.container,f.rule,f.evidence,f.title,f.image,f.package,f.workload,f.scanner,f.audit?.result,f.audit?.nativeSeverity,f.audit?.category,f.audit?.scopeType,f.audit?.scopeValue,f.audit?.description,f.audit?.remediation,...(f.audit?.messages??[]),findingTitle(f)].join(' ').toLocaleLowerCase().includes(word)))
}
