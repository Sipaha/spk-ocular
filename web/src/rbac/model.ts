import type {AccessAttributes,RBACGrant,RBACRule} from '../api/types'

// Declarations explain potential RBAC grants only. Other authorizers, stale
// snapshots and missing sources require the separate server decision.
export function ruleMatches(grant:RBACGrant,rule:RBACRule,a:AccessAttributes):boolean {
 if(!grant.clusterWide && (!a.namespace || grant.namespace!==a.namespace))return false
 if(!rule.verbs.includes('*')&&!rule.verbs.includes(a.verb))return false
 if(!rule.apiGroups.includes('*')&&!rule.apiGroups.includes(a.group))return false
 const resource=a.resource+(a.subresource?'/'+a.subresource:'')
 if(!rule.resources.some(r=>r==='*'||r===resource||(a.subresource&&r==='*/'+a.subresource)))return false
 if(rule.resourceNames.length){
  if(a.verb==='deletecollection'||(a.verb==='create'&&!a.subresource))return false
  if(!a.name || !rule.resourceNames.includes(a.name))return false
 }
 return true
}
export function matchingGrants(grants:RBACGrant[],a:AccessAttributes){return grants.filter(g=>g.rules.some(r=>ruleMatches(g,r,a)))}
