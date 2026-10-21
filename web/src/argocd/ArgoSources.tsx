import {useState,type MouseEvent} from 'react'
import type {ArgoSource} from './types'
import {repositoryLink,revisionLink} from './model'
import {isDesktop} from '../api/client'
import {openURL} from '../tunnels/open'
import {errorDetail} from '../errors'
import {t} from '../i18n'
export function ArgoSources({sources,application}:{sources:ArgoSource[];application:boolean}){
 const [error,setError]=useState('')
 const external=(e:MouseEvent<HTMLAnchorElement>)=>{if(!isDesktop())return;e.preventDefault();void openURL(e.currentTarget.href,'desktop').catch(err=>setError(errorDetail(err)))}
 return <>{sources.map((s,i)=>{const repo=repositoryLink(s.repository),revision=revisionLink(s);return <div key={i} className="mt-2 border-l-2 border-line pl-2 break-words"><p>{repo?<a href={repo} target="_blank" rel="noopener noreferrer" className="text-accent hover:underline" onClick={external}>{s.repository}</a>:s.repository}{s.chart||s.path?' · '+(s.chart||s.path):''}</p>{application&&<><p>{t('argo.targetRevision')}: <code>{s.targetRevision||'—'}</code></p><p>{t('argo.observedRevision')}: {revision?<a href={revision} target="_blank" rel="noopener noreferrer" className="font-mono text-accent hover:underline" onClick={external}>{s.observedRevision}</a>:<code>{s.observedRevision||t('argo.unknown')}</code>}</p></>}</div>})}{error&&<p role="alert" className="mt-2 text-xs text-danger">{error}</p>}</>
}
