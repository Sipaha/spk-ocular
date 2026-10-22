import type {SecurityFinding,SecurityPolicySubject} from '../api/types'
import {isoDateTime,localDateTime} from '../dates'
import {t} from '../i18n'

export function PolicyEvidence({finding:f}:{finding:SecurityFinding}) {
 const p=f.policy!
 const unknown=t('security.unknown')
 const subject=(s:SecurityPolicySubject)=>[s.apiVersion,s.kind,s.namespace||'—',s.name||unknown,'UID '+(s.uid||unknown)].join(' · ')
 return <>
  <p className="mt-2">{t('security.auditResult')}: {t(`security.result.${p.result}`)} · {p.nativeResult||unknown} · {t('security.severity')}: {p.nativeSeverity||unknown}</p>
  <p className="mt-1 break-words">{t('security.policyProducer')}: {p.source||unknown}</p>
  <p className="mt-1 break-words">{t('security.policyRule')}: {p.policy} · {p.rule||unknown} · {p.category}</p>
  <p className="mt-1">{t('security.policyScored')}: {p.scored==null?unknown:p.scored?t('security.policyTrue'):t('security.policyFalse')}</p>
  <p className="mt-1 whitespace-pre-wrap break-words">{p.message}</p>
  <p className="mt-1 whitespace-pre-wrap break-words">{t('security.policyProcess')}: {p.process||unknown}</p>
  <p className="mt-1 whitespace-pre-wrap break-words">{t('security.policyExceptions')}: {p.exceptions||unknown}</p>
  <p className="mt-1 break-words">{t('security.policyScope')}: {p.scope?subject(p.scope):unknown}</p>
  <p className="mt-1 break-words">{t('security.policySelectors')}: {p.scopeSelector||'—'} · {p.subjectSelector||'—'}</p>
  <p className="mt-1 text-fg-muted">{t('security.policySubjects')}</p>
  {p.subjects.map((s,i)=><p key={i} className="mt-1 break-words">{subject(s)}</p>)}
  <p className="mt-1">{t('security.policyLimits')}: {p.maxResults??unknown} · {p.statusFilter.length?p.statusFilter.join(', '):unknown}</p>
  <p className="mt-1">{t('security.policySummary')}: {p.summary?(['pass','fail','warn','error','skip'] as const).map(result=>t(`security.result.${result}`)+' '+(p.summary?.[result]??unknown)).join(' · '):unknown}</p>
  <p className="mt-1 break-words">{t('security.auditIdentity')}: {p.apiVersion} · UID {f.ref.uid} · RV {p.resourceVersion}</p>
  <p className="mt-1" title={isoDateTime(f.scannedAt??0)}>{t('security.policyTime')}: {f.scannedAt?localDateTime(f.scannedAt):unknown}</p>
  <p className="mt-1 text-fg-muted">{t('security.policyFreshness')}</p>
 </>
}
