import {useState,type MouseEvent} from 'react'
import {repositoryLink} from '../argocd/model'
import {isDesktop} from '../api/client'
import {openURL} from '../tunnels/open'
import {errorDetail} from '../errors'
import {t} from '../i18n'
import {revisionURL} from './model'
import type {FluxObject} from './types'
export function FluxSources({object:o}:{object:FluxObject}){
 const[error,setError]=useState('')
 const url=repositoryLink(o.url),commit=revisionURL(o)
 const external=(e:MouseEvent<HTMLAnchorElement>)=>{if(!isDesktop())return;e.preventDefault();void openURL(e.currentTarget.href,'desktop').catch(err=>setError(errorDetail(err)))}
 return <div className="mt-3 break-words">{o.url&&<p>{url?<a className="text-accent hover:underline" href={url} target="_blank" rel="noopener noreferrer" onClick={external}>{o.url}</a>:o.url}</p>}{o.path&&<p>{t('flux.path')}: {o.path}</p>}<p>{t('flux.revision')}: {commit?<a className="font-mono text-accent hover:underline" href={commit} target="_blank" rel="noopener noreferrer" onClick={external}>{o.revision}</a>:<code>{o.revision||t('argo.unknown')}</code>}</p>{error&&<p role="alert" className="text-danger">{error}</p>}</div>
}
